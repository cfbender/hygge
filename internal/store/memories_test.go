package store_test

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/session"
)

func TestSessionMemoriesRoundTrip(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	sess, err := s.CreateSession(ctx, session.NewSession{ProjectDir: t.TempDir(), Model: session.ModelRef{Provider: "p", Name: "m"}})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	first, err := s.RememberSessionMemory(ctx, sess.ID, session.NewMemory{Content: "  prefers focused diffs  "})
	if err != nil {
		t.Fatalf("RememberSessionMemory first: %v", err)
	}
	second, err := s.RememberSessionMemory(ctx, sess.ID, session.NewMemory{Content: "verify with focused tests"})
	if err != nil {
		t.Fatalf("RememberSessionMemory second: %v", err)
	}
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("memory IDs not assigned uniquely: first=%q second=%q", first.ID, second.ID)
	}
	if first.Scope != session.MemoryScopeSession || first.SessionID != sess.ID {
		t.Fatalf("first memory scope/session = %q/%q", first.Scope, first.SessionID)
	}
	if first.Content != "prefers focused diffs" {
		t.Fatalf("content was not trimmed: %q", first.Content)
	}

	got, err := s.ListSessionMemories(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListSessionMemories: %v", err)
	}
	if len(got) != 2 || got[0].ID != first.ID || got[1].ID != second.ID {
		t.Fatalf("memories order/content = %+v, want first then second", got)
	}
}

func TestSessionMemoriesRejectInvalidInput(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	if _, err := s.RememberSessionMemory(ctx, "", session.NewMemory{Content: "x"}); err == nil || !strings.Contains(err.Error(), "session_id required") {
		t.Fatalf("expected session_id required, got %v", err)
	}
	sess, err := s.CreateSession(ctx, session.NewSession{ProjectDir: t.TempDir(), Model: session.ModelRef{Provider: "p", Name: "m"}})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.RememberSessionMemory(ctx, sess.ID, session.NewMemory{Content: "   "}); err == nil || !strings.Contains(err.Error(), "content required") {
		t.Fatalf("expected content required, got %v", err)
	}
}
