package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

// ReplaceSessionTodos stores the complete todo list for sessionID.
func (s *Store) ReplaceSessionTodos(ctx context.Context, sessionID string, items []session.TodoItem) (session.TodoSummary, error) {
	if sessionID == "" {
		return session.TodoSummary{}, fmt.Errorf("store: ReplaceSessionTodos: session_id required")
	}
	copyItems := append([]session.TodoItem(nil), items...)
	payload, err := json.Marshal(copyItems)
	if err != nil {
		return session.TodoSummary{}, fmt.Errorf("store: ReplaceSessionTodos marshal: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO session_todos (session_id, items, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET items = excluded.items, updated_at = excluded.updated_at`,
		sessionID, string(payload), time.Now().UTC().UnixMilli())
	if err != nil {
		return session.TodoSummary{}, fmt.Errorf("store: ReplaceSessionTodos: %w", err)
	}
	return summarizeTodoItems(copyItems), nil
}

// GetSessionTodos returns the persisted todo list for sessionID.
func (s *Store) GetSessionTodos(ctx context.Context, sessionID string) ([]session.TodoItem, session.TodoSummary, error) {
	if sessionID == "" {
		return nil, session.TodoSummary{}, fmt.Errorf("store: GetSessionTodos: session_id required")
	}
	var payload string
	err := s.db.QueryRowContext(ctx, "SELECT items FROM session_todos WHERE session_id = ?", sessionID).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, session.TodoSummary{}, nil
	}
	if err != nil {
		return nil, session.TodoSummary{}, fmt.Errorf("store: GetSessionTodos: %w", err)
	}
	var items []session.TodoItem
	if err := json.Unmarshal([]byte(payload), &items); err != nil {
		return nil, session.TodoSummary{}, fmt.Errorf("store: GetSessionTodos decode: %w", err)
	}
	return items, summarizeTodoItems(items), nil
}

func summarizeTodoItems(items []session.TodoItem) session.TodoSummary {
	var out session.TodoSummary
	out.Total = len(items)
	for _, item := range items {
		switch item.Status {
		case session.TodoCompleted:
			out.Completed++
		case session.TodoCancelled:
			out.Cancelled++
		case session.TodoInProgress:
			out.InProgress++
			out.Incomplete++
		default:
			out.Incomplete++
		}
	}
	return out
}
