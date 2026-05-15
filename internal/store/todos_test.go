package store_test

import (
	"testing"

	"github.com/cfbender/hygge/internal/session"
)

func TestSessionTodosRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	sess, err := s.CreateSession(ctx, session.NewSession{ProjectDir: t.TempDir(), Model: session.ModelRef{Provider: "p", Name: "m"}})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	items := []session.TodoItem{{Content: "write", Status: session.TodoInProgress, Priority: "high"}, {Content: "verify", Status: session.TodoPending}, {Content: "old", Status: session.TodoCompleted}}
	summary, err := s.ReplaceSessionTodos(ctx, sess.ID, items)
	if err != nil {
		t.Fatalf("ReplaceSessionTodos: %v", err)
	}
	if summary.Incomplete != 2 || summary.InProgress != 1 || summary.Completed != 1 {
		t.Fatalf("summary = %+v, want incomplete=2 in_progress=1 completed=1", summary)
	}

	got, gotSummary, err := s.GetSessionTodos(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSessionTodos: %v", err)
	}
	if len(got) != len(items) || got[0].Content != "write" || got[0].Priority != "high" || gotSummary.Incomplete != 2 {
		t.Fatalf("roundtrip items=%+v summary=%+v", got, gotSummary)
	}

	cleared, err := s.ReplaceSessionTodos(ctx, sess.ID, nil)
	if err != nil {
		t.Fatalf("ReplaceSessionTodos clear: %v", err)
	}
	if cleared.Total != 0 || cleared.Incomplete != 0 {
		t.Fatalf("cleared summary = %+v, want zero", cleared)
	}
}
