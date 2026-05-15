package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/session"
)

type fakeTodoPersister struct {
	items []session.TodoItem
}

func (f *fakeTodoPersister) GetSessionTodos(_ context.Context, _ string) ([]session.TodoItem, session.TodoSummary, error) {
	return append([]session.TodoItem(nil), f.items...), summarizeTodos(f.items), nil
}

func (f *fakeTodoPersister) ReplaceSessionTodos(_ context.Context, _ string, items []session.TodoItem) (session.TodoSummary, error) {
	f.items = append([]session.TodoItem(nil), items...)
	return summarizeTodos(f.items), nil
}

func TestTodoToolUpdatesStateAndPublishesEvent(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)
	sub := bus.Subscribe[bus.TodoChanged](b, bus.SubscribeOptions{BufferSize: 1})
	t.Cleanup(sub.Unsubscribe)

	tt := newTodoTool(newTodoStore(), nil)
	res, err := tt.Execute(context.Background(), json.RawMessage(`{"items":[{"content":"write test","status":"in_progress","priority":"high"},{"content":"verify","status":"pending"},{"content":"old","status":"completed"}]}`), ExecContext{SessionID: "sess-1", Bus: b})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("Execute returned IsError: %s", res.Content)
	}
	if got := res.Metadata["incomplete"]; got != 2 {
		t.Fatalf("incomplete metadata = %v, want 2", got)
	}

	select {
	case ev := <-sub.C():
		if ev.SessionID != "sess-1" || ev.Incomplete != 2 || ev.InProgress != 1 || ev.Completed != 1 {
			t.Fatalf("TodoChanged = %+v, want session/incomplete/in_progress/completed counts", ev)
		}
	default:
		t.Fatal("missing TodoChanged event")
	}
}

func TestTodoToolPersistsReplacementAfterLoadingStoredState(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)
	p := &fakeTodoPersister{items: []session.TodoItem{{Content: "old", Status: session.TodoPending}}}
	tt := newTodoTool(newTodoStore(), p)

	_, err := tt.Execute(context.Background(), json.RawMessage(`{"items":[{"content":"old","status":"completed"},{"content":"new","status":"cancelled"}]}`), ExecContext{SessionID: "sess-1", Bus: b})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(p.items) != 2 || p.items[0].Status != session.TodoCompleted || p.items[1].Status != session.TodoCancelled {
		t.Fatalf("persisted items = %+v", p.items)
	}
}

func TestTodoToolRejectsInvalidStatus(t *testing.T) {
	tt := newTodoTool(newTodoStore(), nil)
	b := bus.New()
	t.Cleanup(b.Close)
	_, err := tt.Execute(context.Background(), json.RawMessage(`{"items":[{"content":"x","status":"blocked"}]}`), ExecContext{SessionID: "sess-1", Bus: b})
	if err == nil {
		t.Fatal("Execute succeeded, want validation error")
	}
}
