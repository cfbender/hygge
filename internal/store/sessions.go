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
	if in.ParentID != "" && in.ForkMessageID == "" {
		return nil, fmt.Errorf("store: CreateSession: fork_message_id required when parent_id set")
	}

	id := session.NewSessionID()
	now := time.Now().UTC()
	nowMillis := now.UnixMilli()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (
			id, parent_id, fork_message_id, slug, project_dir,
			model_provider, model_name,
			total_input_tokens, total_output_tokens,
			total_cache_read_tokens, total_cache_write_tokens, total_cost_usd,
			created_at, updated_at, deleted_at, metadata
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, 0, 0, 0.0, ?, ?, NULL, '{}')`,
		id, nullableString(in.ParentID), nullableString(in.ForkMessageID), nullableString(in.Slug),
		in.ProjectDir, in.Model.Provider, in.Model.Name, nowMillis, nowMillis,
	)
	if err != nil {
		return nil, fmt.Errorf("store: CreateSession: %w", err)
	}

	return &session.Session{
		ID:            id,
		ParentID:      in.ParentID,
		ForkMessageID: in.ForkMessageID,
		Slug:          in.Slug,
		ProjectDir:    in.ProjectDir,
		Model:         in.Model,
		CreatedAt:     now,
		UpdatedAt:     now,
		Metadata:      []byte("{}"),
	}, nil
}

// GetSession returns the session row, or ErrNotFound.
func (s *Store) GetSession(ctx context.Context, id string) (*session.Session, error) {
	row := s.db.QueryRowContext(ctx, sessionSelectColumns+" FROM sessions WHERE id = ?", id)
	out, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: GetSession %q: %w", id, ErrNotFound)
	}
	return out, err
}

// sessionSelectColumns is the canonical column list, used by every read path
// to keep scan order in lockstep with the schema.
const sessionSelectColumns = `SELECT
	id, parent_id, fork_message_id, slug, project_dir,
	model_provider, model_name,
	total_input_tokens, total_output_tokens,
	total_cache_read_tokens, total_cache_write_tokens, total_cost_usd,
	created_at, updated_at, deleted_at, metadata`

// rowScanner is satisfied by both *sql.Row and *sql.Rows so scanSession can
// service single-row and multi-row reads.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSession(r rowScanner) (*session.Session, error) {
	var (
		s         session.Session
		parentID  sql.NullString
		forkMsg   sql.NullString
		slug      sql.NullString
		createdMs int64
		updatedMs int64
		deletedMs sql.NullInt64
		metadata  string
	)
	if err := r.Scan(
		&s.ID, &parentID, &forkMsg, &slug, &s.ProjectDir,
		&s.Model.Provider, &s.Model.Name,
		&s.Totals.InputTokens, &s.Totals.OutputTokens,
		&s.Totals.CacheReadTokens, &s.Totals.CacheWriteTokens, &s.Totals.CostUSD,
		&createdMs, &updatedMs, &deletedMs, &metadata,
	); err != nil {
		return nil, err
	}
	s.ParentID = nullStr(parentID)
	s.ForkMessageID = nullStr(forkMsg)
	s.Slug = nullStr(slug)
	s.CreatedAt = time.UnixMilli(createdMs).UTC()
	s.UpdatedAt = time.UnixMilli(updatedMs).UTC()
	if deletedMs.Valid {
		s.DeletedAt = time.UnixMilli(deletedMs.Int64).UTC()
	}
	s.Metadata = []byte(metadata)
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
