package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

// AddCompactionMarker records a new compaction cut-off for the session.
// The marker is never a deletion: the original messages remain in place,
// and the marker simply defines where the live context window starts.
func (s *Store) AddCompactionMarker(
	ctx context.Context,
	sessionID, beforeMessageID, summary string,
	tokensSaved int64,
) (*session.Marker, error) {
	if sessionID == "" || beforeMessageID == "" {
		return nil, fmt.Errorf("store: AddCompactionMarker: session and before-message ids required")
	}
	id := session.NewMarkerID()
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO compaction_markers
			(id, session_id, before_message_id, summary, input_tokens_saved, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		id, sessionID, beforeMessageID, summary, tokensSaved, now.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("store: AddCompactionMarker: %w", err)
	}
	return &session.Marker{
		ID:               id,
		SessionID:        sessionID,
		BeforeMessageID:  beforeMessageID,
		Summary:          summary,
		InputTokensSaved: tokensSaved,
		CreatedAt:        now,
	}, nil
}

// LatestMarker returns the most recent compaction marker for the session,
// breaking ties by id (ULIDs are time-ordered so id-desc is the natural
// secondary sort).  Returns (nil, nil) when no marker exists for the session.
func (s *Store) LatestMarker(ctx context.Context, sessionID string) (*session.Marker, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("store: LatestMarker: session_id required")
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, before_message_id, summary, input_tokens_saved, created_at
		FROM compaction_markers
		WHERE session_id = ?
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, sessionID)

	var (
		m         session.Marker
		createdMs int64
	)
	if err := row.Scan(
		&m.ID, &m.SessionID, &m.BeforeMessageID, &m.Summary,
		&m.InputTokensSaved, &createdMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil //nolint:nilnil // documented: (nil, nil) means "no marker"
		}
		return nil, fmt.Errorf("store: LatestMarker: %w", err)
	}
	m.CreatedAt = time.UnixMilli(createdMs).UTC()
	return &m, nil
}
