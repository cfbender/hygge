package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

// nullStr unwraps sql.NullString.  Empty string when NULL.
func nullStr(n sql.NullString) string {
	if !n.Valid {
		return ""
	}
	return n.String
}

// nullableString is the sql.NullString equivalent for write paths.  Empty
// strings become NULL.
func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// CreateSession persists a new session row.  ID, CreatedAt, UpdatedAt are
// assigned by this method.
func (s *Store) CreateSession(ctx context.Context, in session.NewSession) (*session.Session, error) {
	if in.ProjectDir == "" {
		return nil, fmt.Errorf("store: CreateSession: project_dir required")
	}
	if in.Model.Provider == "" || in.Model.Name == "" {
		return nil, fmt.Errorf("store: CreateSession: model provider+name required")
	}
	kind := in.Kind
	if kind == "" {
		kind = session.KindPrimary
	}
	switch kind {
	case session.KindPrimary, session.KindSubagent:
	default:
		return nil, fmt.Errorf("store: CreateSession: unknown kind %q", kind)
	}
	// Forked primary sessions still require fork_message_id.  Subagent
	// sessions branch out of an originating tool_use; they keep
	// parent_id (auditability) but have no inherited prefix.
	if in.ParentID != "" && kind != session.KindSubagent && in.ForkMessageID == "" {
		return nil, fmt.Errorf("store: CreateSession: fork_message_id required when parent_id set")
	}

	id := session.NewSessionID()
	now := time.Now().UTC()
	nowMillis := now.UnixMilli()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, parent_id, fork_message_id, parent_tool_use_id, slug, project_dir,
			model_provider, model_name,
			total_input_tokens, total_output_tokens,
			total_cache_read_tokens, total_cache_write_tokens, total_cost_usd,
			created_at, updated_at, deleted_at, metadata, kind
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0.0, ?, ?, NULL, '{}', ?)`,
		id, nullableString(in.ParentID), nullableString(in.ForkMessageID), nullableString(in.ParentToolUseID), nullableString(in.Slug),
		in.ProjectDir, in.Model.Provider, in.Model.Name, nowMillis, nowMillis, string(kind),
	)
	if err != nil {
		return nil, fmt.Errorf("store: CreateSession: %w", err)
	}

	return &session.Session{
		ID:              id,
		ParentID:        in.ParentID,
		ForkMessageID:   in.ForkMessageID,
		ParentToolUseID: in.ParentToolUseID,
		Slug:            in.Slug,
		ProjectDir:      in.ProjectDir,
		Model:           in.Model,
		Kind:            kind,
		CreatedAt:       now,
		UpdatedAt:       now,
		Metadata:        []byte("{}"),
	}, nil
}

// GetSession returns the session row, or ErrNotFound.
func (s *Store) GetSession(ctx context.Context, id string) (*session.Session, error) {
	row := s.db.QueryRowContext(ctx, sessionSelectColumns+" FROM sessions WHERE id = ?", id)
	out, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: GetSession %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	// Populate own totals from messages directly owned by this session.
	own, err := s.fetchOwnTotals(ctx, []string{id})
	if err != nil {
		return nil, fmt.Errorf("store: GetSession own totals: %w", err)
	}
	if t, ok := own[id]; ok {
		out.OwnTotals = t
	}
	return out, nil
}

// sessionSelectColumns is the canonical column list, used by every read path
// to keep scan order in lockstep with the schema.
const sessionSelectColumns = `SELECT
	id, parent_id, fork_message_id, parent_tool_use_id, slug, project_dir,
	model_provider, model_name,
	total_input_tokens, total_output_tokens,
	total_cache_read_tokens, total_cache_write_tokens, total_cost_usd,
	created_at, updated_at, deleted_at, metadata, kind,
	COALESCE(first_message_preview, '') AS first_message_preview`

// rowScanner is satisfied by both *sql.Row and *sql.Rows so scanSession can
// service single-row and multi-row reads.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(r rowScanner) (*session.Session, error) {
	var (
		s               session.Session
		parentID        sql.NullString
		forkMsg         sql.NullString
		parentToolUseID sql.NullString
		slug            sql.NullString
		createdMs       int64
		updatedMs       int64
		deletedMs       sql.NullInt64
		metadata        string
		kind            string
		firstMsgPreview string
	)
	if err := r.Scan(
		&s.ID, &parentID, &forkMsg, &parentToolUseID, &slug, &s.ProjectDir,
		&s.Model.Provider, &s.Model.Name,
		&s.Totals.InputTokens, &s.Totals.OutputTokens,
		&s.Totals.CacheReadTokens, &s.Totals.CacheWriteTokens, &s.Totals.CostUSD,
		&createdMs, &updatedMs, &deletedMs, &metadata, &kind,
		&firstMsgPreview,
	); err != nil {
		return nil, err
	}
	s.ParentID = nullStr(parentID)
	s.ForkMessageID = nullStr(forkMsg)
	s.ParentToolUseID = nullStr(parentToolUseID)
	s.Slug = nullStr(slug)
	s.CreatedAt = time.UnixMilli(createdMs).UTC()
	s.UpdatedAt = time.UnixMilli(updatedMs).UTC()
	if deletedMs.Valid {
		s.DeletedAt = time.UnixMilli(deletedMs.Int64).UTC()
	}
	s.Metadata = []byte(metadata)
	s.Kind = session.Kind(kind)
	if s.Kind == "" {
		s.Kind = session.KindPrimary
	}
	s.FirstMessagePreview = firstMsgPreview
	return &s, nil
}

// ListSessions returns sessions matching opts, newest first.
func (s *Store) ListSessions(ctx context.Context, opts session.ListOpts) ([]*session.Session, error) {
	limit := opts.Limit
	if limit <= 0 {
		limit = session.DefaultListLimit
	}

	var (
		clauses []string
		args    []any
	)
	if opts.ProjectDir != "" {
		clauses = append(clauses, "project_dir = ?")
		args = append(args, opts.ProjectDir)
	}
	if !opts.IncludeDeleted {
		clauses = append(clauses, "deleted_at IS NULL")
	}
	if opts.Kind != "" {
		clauses = append(clauses, "kind = ?")
		args = append(args, string(opts.Kind))
	}
	if opts.ParentID != "" {
		clauses = append(clauses, "parent_id = ?")
		args = append(args, opts.ParentID)
	}

	query := sessionSelectColumns + " FROM sessions"
	if len(clauses) > 0 {
		// clauses are all constant string literals chosen by Go switches
		// above; no user input is interpolated.
		query += " WHERE " + strings.Join(clauses, " AND ") //nolint:gosec // G202: clauses are hard-coded literals
	}
	query += " ORDER BY created_at DESC, id DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: ListSessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*session.Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("store: ListSessions scan: %w", err)
		}
		out = append(out, sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListSessions iterate: %w", err)
	}

	// Apply the Query substring filter in Go after the SQL scan.
	// This is applied against slug, project_dir, and first_message_preview.
	// Case-insensitive; empty query passes all rows through.
	if opts.Query != "" {
		out = filterSessionsByQuery(out, opts.Query)
	}

	// Batch-fetch own totals and latest message previews for all returned
	// sessions in single queries.
	if len(out) > 0 {
		ids := make([]string, len(out))
		for i, sess := range out {
			ids[i] = sess.ID
		}
		ownMap, err := s.fetchOwnTotals(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("store: ListSessions own totals: %w", err)
		}
		for _, sess := range out {
			if t, ok := ownMap[sess.ID]; ok {
				sess.OwnTotals = t
			}
		}
		previews, err := s.fetchLatestMessagePreviews(ctx, ids)
		if err != nil {
			return nil, fmt.Errorf("store: ListSessions latest message previews: %w", err)
		}
		for _, sess := range out {
			if p, ok := previews[sess.ID]; ok {
				sess.LastUserMessage = p.User
				sess.LastAgentMessage = p.Agent
			}
		}
	}

	return out, nil
}

// UpdateSessionTotals atomically adds delta to the session's running
// totals.  Single-statement, single round-trip.
func (s *Store) UpdateSessionTotals(ctx context.Context, id string, delta session.Totals) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions SET
			total_input_tokens       = total_input_tokens       + ?,
			total_output_tokens      = total_output_tokens      + ?,
			total_cache_read_tokens  = total_cache_read_tokens  + ?,
			total_cache_write_tokens = total_cache_write_tokens + ?,
			total_cost_usd           = total_cost_usd           + ?,
			updated_at               = ?
		WHERE id = ?`,
		delta.InputTokens, delta.OutputTokens,
		delta.CacheReadTokens, delta.CacheWriteTokens,
		delta.CostUSD, time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: UpdateSessionTotals: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: UpdateSessionTotals rows affected: %w", err)
	}
	if affected == 0 {
		return fmt.Errorf("store: UpdateSessionTotals %q: %w", id, ErrNotFound)
	}
	return nil
}

// PropagateTotals adds delta to every session in the parent chain
// starting at id (inclusive) and walking up via parent_id.  The chain
// walk uses a recursive CTE capped at 32 hops to guard against
// accidental parent_id cycles (impossible by schema constraints, but
// the cap is a defensive belt-and-suspenders measure).  All row updates
// happen inside a single transaction so the chain is updated atomically;
// a partial write is an acceptable failure mode — the next delta will
// catch up.
//
// Returns the slice of session ids that were updated, ordered leaf-first
// (id is always index 0; the root ancestor is last).
func (s *Store) PropagateTotals(ctx context.Context, id string, delta session.Totals) ([]string, error) {
	// Step 1: collect the ancestor chain in a single read query.
	// We do this outside the update transaction so we can return the
	// list of ids for event publishing even if the update partially fails.
	const chainSQL = `
		WITH RECURSIVE ancestors(id, parent_id, depth) AS (
			SELECT id, parent_id, 0 FROM sessions WHERE id = ?
			UNION ALL
			SELECT s.id, s.parent_id, a.depth + 1
			  FROM sessions s
			  JOIN ancestors a ON s.id = a.parent_id
			 WHERE a.depth < 32
		)
		SELECT id FROM ancestors ORDER BY depth ASC`

	rows, err := s.db.QueryContext(ctx, chainSQL, id)
	if err != nil {
		return nil, fmt.Errorf("store: PropagateTotals chain walk: %w", err)
	}
	var chain []string
	for rows.Next() {
		var chainID string
		if err := rows.Scan(&chainID); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("store: PropagateTotals scan: %w", err)
		}
		chain = append(chain, chainID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: PropagateTotals iterate: %w", err)
	}
	_ = rows.Close()

	if len(chain) == 0 {
		return nil, fmt.Errorf("store: PropagateTotals %q: %w", id, ErrNotFound)
	}

	// Step 2: apply the delta to each row in a single transaction.
	// The same delta is applied to each ancestor — we are adding the
	// same token increment to every level of the chain.  Triple-counting
	// is avoided because the caller (agent/cost.go) publishes CostUpdated
	// events for ALL ancestors but the TUI footer subscribes to the ROOT
	// id only.
	now := time.Now().UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return chain, fmt.Errorf("store: PropagateTotals begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, chainID := range chain {
		_, err := tx.ExecContext(ctx, `
			UPDATE sessions SET
				total_input_tokens       = total_input_tokens       + ?,
				total_output_tokens      = total_output_tokens      + ?,
				total_cache_read_tokens  = total_cache_read_tokens  + ?,
				total_cache_write_tokens = total_cache_write_tokens + ?,
				total_cost_usd           = total_cost_usd           + ?,
				updated_at               = ?
			WHERE id = ?`,
			delta.InputTokens, delta.OutputTokens,
			delta.CacheReadTokens, delta.CacheWriteTokens,
			delta.CostUSD, now, chainID,
		)
		if err != nil {
			return chain, fmt.Errorf("store: PropagateTotals update %q: %w", chainID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return chain, fmt.Errorf("store: PropagateTotals commit: %w", err)
	}
	return chain, nil
}

// SoftDeleteSession marks deleted_at and bumps updated_at.  No-op if
// already deleted.
func (s *Store) SoftDeleteSession(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	res, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("store: SoftDeleteSession: %w", err)
	}
	// Distinguish "already deleted" (0 rows) from "row never existed".  If
	// the row truly does not exist, surface ErrNotFound.
	if affected, _ := res.RowsAffected(); affected == 0 {
		var exists int
		if err := s.db.QueryRowContext(ctx,
			"SELECT 1 FROM sessions WHERE id = ?", id,
		).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("store: SoftDeleteSession %q: %w", id, ErrNotFound)
			}
			return fmt.Errorf("store: SoftDeleteSession lookup: %w", err)
		}
	}
	return nil
}

// ForkSession creates a new session pointing at fromSessionID, inheriting
// history up to and including fromMessageID.  The parent session need not
// be the message's owner — but the message must be visible to it via
// MessagesForSession.  We perform a cheap existence check on both before
// inserting.
func (s *Store) ForkSession(
	ctx context.Context,
	fromSessionID, fromMessageID string,
	model session.ModelRef,
	slug string,
) (*session.Session, error) {
	if fromSessionID == "" || fromMessageID == "" {
		return nil, fmt.Errorf("store: ForkSession: session and message ids required")
	}

	// Validate the parent session exists and read its project_dir; the
	// child inherits the same project context.
	var projectDir string
	err := s.db.QueryRowContext(ctx,
		"SELECT project_dir FROM sessions WHERE id = ?", fromSessionID,
	).Scan(&projectDir)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: ForkSession parent %q: %w", fromSessionID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: ForkSession lookup parent: %w", err)
	}

	// Validate the fork point exists.
	var msgSession string
	err = s.db.QueryRowContext(ctx,
		"SELECT session_id FROM messages WHERE id = ?", fromMessageID,
	).Scan(&msgSession)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: ForkSession fork message %q: %w", fromMessageID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: ForkSession lookup message: %w", err)
	}

	return s.CreateSession(ctx, session.NewSession{
		ProjectDir:    projectDir,
		Model:         model,
		Slug:          slug,
		ParentID:      fromSessionID,
		ForkMessageID: fromMessageID,
	})
}

// RenameSession sets a new slug on an existing session and bumps UpdatedAt.
// An empty slug clears the slug (sets the column to NULL).  Returns
// ErrNotFound when id is unknown.  A no-op when the new slug equals the
// current slug (UpdatedAt is not bumped in that case).
func (s *Store) RenameSession(ctx context.Context, id, slug string) error {
	if id == "" {
		return fmt.Errorf("store: RenameSession: id required")
	}
	// Read the current slug so we can skip the write on a no-op rename.
	var curSlug sql.NullString
	err := s.db.QueryRowContext(ctx,
		"SELECT slug FROM sessions WHERE id = ?", id,
	).Scan(&curSlug)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: RenameSession %q: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: RenameSession lookup: %w", err)
	}
	// No-op: same slug (both empty, or both the same non-empty string).
	current := nullStr(curSlug)
	if current == slug {
		return nil
	}
	res, err := s.db.ExecContext(ctx,
		"UPDATE sessions SET slug = ?, updated_at = ? WHERE id = ?",
		nullableString(slug), time.Now().UnixMilli(), id,
	)
	if err != nil {
		return fmt.Errorf("store: RenameSession: %w", err)
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return fmt.Errorf("store: RenameSession %q: %w", id, ErrNotFound)
	}
	return nil
}

// filterSessionsByQuery applies a case-insensitive substring filter against
// slug, project_dir, and first_message_preview.  Rows that match any field
// are retained; all others are dropped.  An empty query passes all rows.
func filterSessionsByQuery(sessions []*session.Session, query string) []*session.Session {
	if query == "" {
		return sessions
	}
	lower := strings.ToLower(query)
	out := sessions[:0]
	for _, s := range sessions {
		if strings.Contains(strings.ToLower(s.Slug), lower) ||
			strings.Contains(strings.ToLower(s.ProjectDir), lower) ||
			strings.Contains(strings.ToLower(s.FirstMessagePreview), lower) {
			out = append(out, s)
		}
	}
	return out
}

// fetchOwnTotals returns a map of session_id → Totals computed from the
// messages table for only the sessions in ids (no subagent descendants).
// A single aggregation query with GROUP BY is used to avoid N+1 round-trips.
// Sessions with no messages will not appear in the returned map (OwnTotals
// stays as the zero value on the caller's Session struct).
func (s *Store) fetchOwnTotals(ctx context.Context, ids []string) (map[string]session.Totals, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	// Build the IN-list placeholders.
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1] // trim trailing comma
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	//nolint:gosec // G202: placeholders are "?,?,?" — no user input interpolated
	q := `SELECT session_id,
	             COALESCE(SUM(input_tokens), 0),
	             COALESCE(SUM(output_tokens), 0),
	             COALESCE(SUM(cache_read_tokens), 0),
	             COALESCE(SUM(cache_write_tokens), 0),
	             COALESCE(SUM(cost_usd), 0)
	        FROM messages
	       WHERE session_id IN (` + placeholders + `)
	         AND deleted_at IS NULL
	       GROUP BY session_id`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("fetchOwnTotals: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]session.Totals, len(ids))
	for rows.Next() {
		var id string
		var t session.Totals
		if err := rows.Scan(&id, &t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.CostUSD); err != nil {
			return nil, fmt.Errorf("fetchOwnTotals scan: %w", err)
		}
		out[id] = t
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fetchOwnTotals iterate: %w", err)
	}
	return out, nil
}

type latestMessagePreviews struct {
	User  string
	Agent string
}

// fetchLatestMessagePreviews returns the latest direct user and assistant text
// previews for the requested sessions. Messages are ordered newest-first and
// the first text part from the first message seen for each role wins.
func (s *Store) fetchLatestMessagePreviews(ctx context.Context, ids []string) (map[string]latestMessagePreviews, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := strings.Repeat("?,", len(ids))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}

	//nolint:gosec // G202: placeholders are "?,?,?" — no user input interpolated
	q := `SELECT session_id, role, parts
	        FROM messages
	       WHERE session_id IN (` + placeholders + `)
	         AND role IN ('user', 'assistant')
	         AND deleted_at IS NULL
	       ORDER BY session_id, role, created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("fetchLatestMessagePreviews: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]latestMessagePreviews, len(ids))
	seen := make(map[string]map[session.Role]bool, len(ids))
	for rows.Next() {
		var id, roleStr, partsJSON string
		if err := rows.Scan(&id, &roleStr, &partsJSON); err != nil {
			return nil, fmt.Errorf("fetchLatestMessagePreviews scan: %w", err)
		}
		role := session.Role(roleStr)
		if seen[id] != nil && seen[id][role] {
			continue
		}
		parts, err := session.UnmarshalParts([]byte(partsJSON))
		if err != nil {
			return nil, fmt.Errorf("fetchLatestMessagePreviews decode parts for session %q: %w", id, err)
		}
		preview := extractTextPreview(parts, 160)
		if preview == "" {
			continue
		}
		p := out[id]
		switch role {
		case session.RoleUser:
			p.User = preview
		case session.RoleAssistant:
			p.Agent = preview
		default:
			continue
		}
		out[id] = p
		if seen[id] == nil {
			seen[id] = make(map[session.Role]bool, 2)
		}
		seen[id][role] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fetchLatestMessagePreviews iterate: %w", err)
	}
	return out, nil
}

// latestUserMessageID returns the id of the most recent non-deleted user
// message in sessionID, or ("", nil) when no user message exists.
// Used by the fork-at-latest path.
func (s *Store) latestUserMessageID(ctx context.Context, sessionID string) (string, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM messages
		 WHERE session_id = ? AND role = 'user' AND deleted_at IS NULL
		 ORDER BY created_at DESC, id DESC LIMIT 1`,
		sessionID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("store: latestUserMessageID: %w", err)
	}
	return id, nil
}

// LatestUserMessageID returns the id of the most recent non-deleted user
// message in sessionID, or ("", nil) when no user message exists.
// Part of the extended store surface used by the session modal fork path.
func (s *Store) LatestUserMessageID(ctx context.Context, sessionID string) (string, error) {
	return s.latestUserMessageID(ctx, sessionID)
}
