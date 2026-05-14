package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

// AppendMessage inserts a new message row.  The parts slice is encoded via
// session.MarshalParts; an invalid parts payload would be rejected by the
// CHECK (json_valid(parts)) constraint on the messages table.
//
// When the message is the first user-role message for the session, the
// session's first_message_preview column is populated with up to 256
// chars of the message's first text part.  This supports fast substring
// filtering in ListSessions without a per-query JOIN.
func (s *Store) AppendMessage(
	ctx context.Context, sessionID string, in session.NewMessage,
) (*session.Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("store: AppendMessage: session_id required")
	}
	if in.Role == "" {
		return nil, fmt.Errorf("store: AppendMessage: role required")
	}

	partsJSON, err := session.MarshalParts(in.Parts)
	if err != nil {
		return nil, fmt.Errorf("store: AppendMessage marshal parts: %w", err)
	}

	id := session.NewMessageID()
	now := time.Now().UTC()
	nowMillis := now.UnixMilli()

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO messages (
			id, session_id, role, parts,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
			cost_usd, duration_ms, created_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		id, sessionID, string(in.Role), string(partsJSON),
		nullableInt(in.InputTokens), nullableInt(in.OutputTokens),
		nullableInt(in.CacheReadTokens), nullableInt(in.CacheWriteTokens),
		nullableFloat(in.CostUSD), nullableInt(in.DurationMs),
		nowMillis,
	)
	if err != nil {
		return nil, fmt.Errorf("store: AppendMessage: %w", err)
	}

	// Populate first_message_preview on the session row when this is the
	// first user-role message (first_message_preview IS NULL).  Best-effort:
	// failure here is logged and swallowed so the caller's append still
	// succeeds.  We extract the text from the first PartText part.
	if in.Role == session.RoleUser {
		preview := extractTextPreview(in.Parts, 256)
		if preview != "" {
			if _, upErr := s.db.ExecContext(ctx,
				`UPDATE sessions SET first_message_preview = ?
				 WHERE id = ? AND first_message_preview IS NULL`,
				preview, sessionID,
			); upErr != nil {
				slog.Warn("store: AppendMessage: failed to set first_message_preview",
					"session_id", sessionID, "err", upErr)
			}
		}
	}

	return &session.Message{
		ID:               id,
		SessionID:        sessionID,
		Role:             in.Role,
		Parts:            append([]session.Part(nil), in.Parts...),
		InputTokens:      in.InputTokens,
		OutputTokens:     in.OutputTokens,
		CacheReadTokens:  in.CacheReadTokens,
		CacheWriteTokens: in.CacheWriteTokens,
		CostUSD:          in.CostUSD,
		DurationMs:       in.DurationMs,
		CreatedAt:        now,
	}, nil
}

// extractTextPreview returns up to maxLen chars from the first PartText
// part in parts.  Returns "" when no text part is found.
func extractTextPreview(parts []session.Part, maxLen int) string {
	for _, p := range parts {
		if p.Kind == session.PartText && p.Text != "" {
			if len(p.Text) <= maxLen {
				return p.Text
			}
			return p.Text[:maxLen]
		}
	}
	return ""
}

// nullableInt maps 0 -> NULL on write paths.  We treat 0 as "unset" because
// the schema declares the usage columns NULLABLE.
func nullableInt(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

// nullableFloat maps 0.0 -> NULL.  Same rationale as nullableInt.
func nullableFloat(v float64) sql.NullFloat64 {
	if v == 0 {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

// messageSelectColumns mirrors the messages table column order; every read
// path uses this so scan order stays in sync with the schema.
const messageSelectColumns = `SELECT
	id, session_id, role, parts,
	input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
	cost_usd, duration_ms, created_at, deleted_at`

func scanMessage(r rowScanner) (*session.Message, error) {
	var (
		m             session.Message
		role          string
		partsJSON     string
		inTok, outTok sql.NullInt64
		crTok, cwTok  sql.NullInt64
		cost          sql.NullFloat64
		dur           sql.NullInt64
		createdMs     int64
		deletedMs     sql.NullInt64
	)
	if err := r.Scan(
		&m.ID, &m.SessionID, &role, &partsJSON,
		&inTok, &outTok, &crTok, &cwTok,
		&cost, &dur, &createdMs, &deletedMs,
	); err != nil {
		return nil, err
	}
	m.Role = session.Role(role)
	parts, err := session.UnmarshalParts([]byte(partsJSON))
	if err != nil {
		return nil, fmt.Errorf("store: decode parts for message %q: %w", m.ID, err)
	}
	m.Parts = parts
	m.InputTokens = inTok.Int64
	m.OutputTokens = outTok.Int64
	m.CacheReadTokens = crTok.Int64
	m.CacheWriteTokens = cwTok.Int64
	m.CostUSD = cost.Float64
	m.DurationMs = dur.Int64
	m.CreatedAt = time.UnixMilli(createdMs).UTC()
	if deletedMs.Valid {
		m.DeletedAt = time.UnixMilli(deletedMs.Int64).UTC()
	}
	return &m, nil
}

// GetMessage returns the message row (deleted or not), or ErrNotFound.
func (s *Store) GetMessage(ctx context.Context, id string) (*session.Message, error) {
	row := s.db.QueryRowContext(ctx, messageSelectColumns+" FROM messages WHERE id = ?", id)
	out, err := scanMessage(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: GetMessage %q: %w", id, ErrNotFound)
	}
	return out, err
}

// forkChainCTE is the recursive CTE that walks ancestry from a leaf session
// up to its root.  Each ancestry row records (session_id, cutoff_message_id,
// parent_id, depth):
//
//   - session_id      — the ancestor whose messages we include
//   - cutoff_message_id — the highest message id from that ancestor we should
//     include.  NULL means "no upper bound" (used for the leaf session itself,
//     since the leaf owns all of its own messages).
//   - parent_id       — the ancestor's parent (for recursion)
//   - depth           — distance from the leaf (0 = leaf)
//
// The key insight: a session's stored fork_message_id is the cut-off into
// its PARENT, not into itself.  So when the CTE descends from child to
// parent, the cut-off we apply to the parent's rows is the child's
// fork_message_id.  The leaf's own messages have no upper bound.
//
// The depth column lets us detect runaway chains: any visited row at
// depth == MaxForkDepth that still has a non-NULL parent_id indicates the
// real chain extends past the cap.  We surface this as ErrForkChainTooDeep
// rather than silently truncating.
const forkChainCTE = `
WITH RECURSIVE
ancestry(session_id, cutoff_message_id, parent_id, fork_message_id, depth) AS (
	SELECT id, NULL, parent_id, fork_message_id, 0
	  FROM sessions WHERE id = ?
	UNION ALL
	SELECT s.id, a.fork_message_id, s.parent_id, s.fork_message_id, a.depth + 1
	  FROM sessions s, ancestry a
	  WHERE s.id = a.parent_id AND a.depth < ?
)`

// MessagesForSession returns the full conversation visible to sessionID,
// walking ancestor sessions and selecting each one's contribution up to its
// fork point.  Soft-deleted messages are excluded.
//
// The depth of the fork chain is capped at MaxForkDepth (=8); deeper chains
// return ErrForkChainTooDeep.
func (s *Store) MessagesForSession(ctx context.Context, sessionID string) ([]*session.Message, error) {
	return s.messagesForSession(ctx, sessionID, "")
}

// messagesForSession is the shared implementation for the public read APIs.
// When afterMessageID is non-empty, only messages with id > afterMessageID
// are returned (ULIDs are time-ordered, so this is equivalent to
// "messages strictly newer than that one").
func (s *Store) messagesForSession(
	ctx context.Context, sessionID, afterMessageID string,
) ([]*session.Message, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("store: MessagesForSession: session_id required")
	}

	// First verify the chain isn't pathologically deep.  We do this with a
	// cheap probe against the CTE before pulling messages so we surface
	// the error early and avoid wasting a big result-set.
	if err := s.checkForkChainDepth(ctx, sessionID); err != nil {
		return nil, err
	}

	// Build the message query.  We project the standard messageSelectColumns
	// onto the join with ancestry, filtering each row by
	// "m.id <= a.cutoff_message_id" so each ancestor only contributes its
	// prefix up to (and including) the fork point taken into that ancestor.
	// The leaf session has cutoff_message_id IS NULL (no upper bound).
	q := forkChainCTE + `
SELECT
	m.id, m.session_id, m.role, m.parts,
	m.input_tokens, m.output_tokens, m.cache_read_tokens, m.cache_write_tokens,
	m.cost_usd, m.duration_ms, m.created_at, m.deleted_at
FROM messages m
JOIN ancestry a ON m.session_id = a.session_id
WHERE m.deleted_at IS NULL
  AND (a.cutoff_message_id IS NULL OR m.id <= a.cutoff_message_id)`

	args := []any{sessionID, MaxForkDepth}
	if afterMessageID != "" {
		q += " AND m.id > ?"
		args = append(args, afterMessageID)
	}
	q += " ORDER BY m.created_at, m.id"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: MessagesForSession: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*session.Message
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: MessagesForSession scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: MessagesForSession iterate: %w", err)
	}
	return out, nil
}

// checkForkChainDepth probes the CTE and returns ErrForkChainTooDeep if the
// chain hits the depth cap while still having a non-NULL parent_id (i.e.
// would have continued recursing if the cap weren't enforced).
func (s *Store) checkForkChainDepth(ctx context.Context, sessionID string) error {
	// The recursion adds rows at depth+1 only while depth < MaxForkDepth,
	// so the deepest row produced has depth = MaxForkDepth.  If that row's
	// parent_id is non-NULL, the real chain extends past the cap.
	var maxDepth int
	var deepestParent sql.NullString
	err := s.db.QueryRowContext(ctx, forkChainCTE+`
SELECT MAX(depth), MAX(CASE WHEN depth = ? THEN parent_id END)
FROM ancestry`,
		sessionID, MaxForkDepth, MaxForkDepth,
	).Scan(&maxDepth, &deepestParent)
	if err != nil {
		return fmt.Errorf("store: check fork chain depth: %w", err)
	}
	if maxDepth >= MaxForkDepth && deepestParent.Valid {
		slog.Warn("fork chain truncated", "session_id", sessionID, "max_depth", MaxForkDepth)
		return fmt.Errorf("%w: session %q chain >= %d levels", ErrForkChainTooDeep, sessionID, MaxForkDepth)
	}
	return nil
}

// MessagesSinceLatestMarker returns the messages newer than the session's
// latest compaction marker, plus the marker itself.  When the session has
// no marker, the full conversation history is returned with a nil marker.
//
// We use the marker's before_message_id as the filter cursor: because every
// id is a ULID, "id > before_message_id" is equivalent to "created strictly
// after the message at the cut-off".  This pushes the filter into the same
// SQL query that loads history, avoiding a round-trip-and-slice in Go.
func (s *Store) MessagesSinceLatestMarker(
	ctx context.Context, sessionID string,
) ([]*session.Message, *session.Marker, error) {
	marker, err := s.LatestMarker(ctx, sessionID)
	if err != nil {
		return nil, nil, err
	}

	cursor := ""
	if marker != nil {
		cursor = marker.BeforeMessageID
	}
	msgs, err := s.messagesForSession(ctx, sessionID, cursor)
	if err != nil {
		return nil, nil, err
	}
	return msgs, marker, nil
}
