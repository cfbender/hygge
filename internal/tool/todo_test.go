package tool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cfbender/hygge/internal/bus"
)

func TestTodoToolUpdatesStateAndPublishesEvent(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)
	sub := bus.Subscribe[bus.TodoChanged](b, bus.SubscribeOptions{BufferSize: 1})
	t.Cleanup(sub.Unsubscribe)

	tt := newTodoTool(newTodoStore())
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

func TestTodoToolRejectsInvalidStatus(t *testing.T) {
	tt := newTodoTool(newTodoStore())
	b := bus.New()
	t.Cleanup(b.Close)
	_, err := tt.Execute(context.Background(), json.RawMessage(`{"items":[{"content":"x","status":"blocked"}]}`), ExecContext{SessionID: "sess-1", Bus: b})
	if err == nil {
		t.Fatal("Execute succeeded, want validation error")
	}
}
