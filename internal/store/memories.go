package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

// RememberSessionMemory stores a new active memory scoped to sessionID.
func (s *Store) RememberSessionMemory(ctx context.Context, sessionID string, in session.NewMemory) (*session.Memory, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("store: RememberSessionMemory: session_id required")
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return nil, fmt.Errorf("store: RememberSessionMemory: content required")
	}
	id := session.NewMemoryID()
	now := time.Now().UTC()
	nowMillis := now.UnixMilli()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO session_memories (id, session_id, content, created_at, updated_at, deleted_at)
		VALUES (?, ?, ?, ?, ?, NULL)`, id, sessionID, content, nowMillis, nowMillis)
	if err != nil {
		return nil, fmt.Errorf("store: RememberSessionMemory: %w", err)
	}
	return &session.Memory{
		ID:        id,
		Scope:     session.MemoryScopeSession,
		SessionID: sessionID,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}, nil
}

// ListSessionMemories returns active session memories in creation order.
func (s *Store) ListSessionMemories(ctx context.Context, sessionID string) ([]*session.Memory, error) {
	if sessionID == "" {
		return nil, fmt.Errorf("store: ListSessionMemories: session_id required")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, content, created_at, updated_at, deleted_at
		FROM session_memories
		WHERE session_id = ? AND deleted_at IS NULL
		ORDER BY created_at, id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: ListSessionMemories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []*session.Memory
	for rows.Next() {
		m, err := scanMemory(rows)
		if err != nil {
			return nil, fmt.Errorf("store: ListSessionMemories scan: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: ListSessionMemories iterate: %w", err)
	}
	return out, nil
}

func scanMemory(r rowScanner) (*session.Memory, error) {
	var (
		m         session.Memory
		createdMs int64
		updatedMs int64
		deletedMs sql.NullInt64
	)
	if err := r.Scan(&m.ID, &m.SessionID, &m.Content, &createdMs, &updatedMs, &deletedMs); err != nil {
		return nil, err
	}
	m.Scope = session.MemoryScopeSession
	m.CreatedAt = time.UnixMilli(createdMs).UTC()
	m.UpdatedAt = time.UnixMilli(updatedMs).UTC()
	if deletedMs.Valid {
		m.DeletedAt = time.UnixMilli(deletedMs.Int64).UTC()
	}
	return &m, nil
}
