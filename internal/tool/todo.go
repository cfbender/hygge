package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cfbender/hygge/internal/bus"
)

type todoStatus string

const (
	todoPending    todoStatus = "pending"
	todoInProgress todoStatus = "in_progress"
	todoCompleted  todoStatus = "completed"
	todoCancelled  todoStatus = "cancelled"
)

type todoItem struct {
	Content  string     `json:"content"`
	Status   todoStatus `json:"status"`
	Priority string     `json:"priority,omitempty"`
}

type todoStore struct {
	mu    sync.RWMutex
	items map[string][]todoItem
}

func newTodoStore() *todoStore { return &todoStore{items: make(map[string][]todoItem)} }

func (s *todoStore) set(sessionID string, items []todoItem) todoSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	copyItems := append([]todoItem(nil), items...)
	s.items[sessionID] = copyItems
	return summarizeTodos(copyItems)
}

type todoSummary struct {
	Total      int `json:"total"`
	Incomplete int `json:"incomplete"`
	InProgress int `json:"in_progress"`
	Completed  int `json:"completed"`
	Cancelled  int `json:"cancelled"`
}

func summarizeTodos(items []todoItem) todoSummary {
	var out todoSummary
	out.Total = len(items)
	for _, item := range items {
		switch item.Status {
		case todoCompleted:
			out.Completed++
		case todoCancelled:
			out.Cancelled++
		case todoInProgress:
			out.InProgress++
			out.Incomplete++
		default:
			out.Incomplete++
		}
	}
	return out
}

type todoTool struct{ store *todoStore }

func newTodoTool(store *todoStore) *todoTool { return &todoTool{store: store} }

func (t *todoTool) Name() string { return "todo" }

func (t *todoTool) Description() string {
	return "Set the current session todo list. Use this to track lightweight work items and their statuses during a turn."
}

func (t *todoTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"content":  map[string]any{"type": "string"},
						"status":   map[string]any{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
						"priority": map[string]any{"type": "string"},
					},
					"required":             []string{"content", "status"},
					"additionalProperties": false,
				},
			},
		},
		"required":             []string{"items"},
		"additionalProperties": false,
	}
}

func (t *todoTool) Execute(_ context.Context, args json.RawMessage, ec ExecContext) (Result, error) {
	var in struct {
		Items []todoItem `json:"items"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return Result{}, err
	}
	for i := range in.Items {
		in.Items[i].Content = strings.TrimSpace(in.Items[i].Content)
		if in.Items[i].Content == "" {
			return Result{}, newInvalidArgs(fmt.Sprintf("items[%d].content is required", i), nil)
		}
		switch in.Items[i].Status {
		case todoPending, todoInProgress, todoCompleted, todoCancelled:
		default:
			return Result{}, newInvalidArgs(fmt.Sprintf("items[%d].status must be pending, in_progress, completed, or cancelled", i), nil)
		}
	}
	summary := t.store.set(ec.SessionID, in.Items)
	bus.Publish(ec.Bus, bus.TodoChanged{SessionID: ec.SessionID, Total: summary.Total, Incomplete: summary.Incomplete, InProgress: summary.InProgress, Completed: summary.Completed, Cancelled: summary.Cancelled, At: ec.nowFn()()})
	content := fmt.Sprintf("todo list updated: %d incomplete", summary.Incomplete)
	return Result{Content: content, Metadata: map[string]any{"total": summary.Total, "incomplete": summary.Incomplete, "in_progress": summary.InProgress, "completed": summary.Completed, "cancelled": summary.Cancelled}}, nil
}

func (t *todoTool) Parallelizable() bool { return false }
