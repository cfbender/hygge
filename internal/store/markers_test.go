package store_test

import (
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

func TestAddCompactionMarker_PersistsRow(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m := appendUserText(t, s, sess.ID, "hi")

	marker, err := s.AddCompactionMarker(t.Context(), sess.ID, m.ID, "summary", 999)
	if err != nil {
		t.Fatalf("AddCompactionMarker: %v", err)
	}
	if marker.ID == "" || len(marker.ID) != 26 {
		t.Errorf("expected 26-char ULID id, got %q", marker.ID)
	}
	if marker.SessionID != sess.ID {
		t.Errorf("session id mismatch: got %q want %q", marker.SessionID, sess.ID)
	}
	if marker.BeforeMessageID != m.ID {
		t.Errorf("before_message_id: got %q want %q", marker.BeforeMessageID, m.ID)
	}
	if marker.InputTokensSaved != 999 {
		t.Errorf("tokens_saved: got %d", marker.InputTokensSaved)
	}
}

func TestLatestMarker_NoneReturnsNilNil(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m, err := s.LatestMarker(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LatestMarker: %v", err)
	}
	if m != nil {
		t.Fatalf("expected nil marker, got %+v", m)
	}
}

// TestLatestMarker_NewestWins inserts two markers close in time and verifies
// the most recent wins.  Because ULIDs are time-ordered, even if SQLite's
// stored created_at timestamps collide at millisecond resolution, the
// (created_at DESC, id DESC) ordering keeps the result deterministic.
func TestLatestMarker_NewestWins(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m1 := appendUserText(t, s, sess.ID, "a")
	m2 := appendUserText(t, s, sess.ID, "b")

	first, err := s.AddCompactionMarker(t.Context(), sess.ID, m1.ID, "s1", 10)
	if err != nil {
		t.Fatalf("AddCompactionMarker 1: %v", err)
	}
	// Tiny pause so created_at is at least one ms apart.  Not strictly
	// required given the id tie-break, but makes the assertion robust.
	time.Sleep(2 * time.Millisecond)
	second, err := s.AddCompactionMarker(t.Context(), sess.ID, m2.ID, "s2", 20)
	if err != nil {
		t.Fatalf("AddCompactionMarker 2: %v", err)
	}

	got, err := s.LatestMarker(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LatestMarker: %v", err)
	}
	if got == nil || got.ID != second.ID {
		t.Fatalf("expected newest %q, got %v (first was %q)", second.ID, got, first.ID)
	}
}

func TestAddCompactionMarker_RequiresIDs(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AddCompactionMarker(t.Context(), "", "x", "s", 0); err == nil {
		t.Error("expected error for empty session id")
	}
	if _, err := s.AddCompactionMarker(t.Context(), "x", "", "s", 0); err == nil {
		t.Error("expected error for empty before-message id")
	}
}

func TestLatestMarker_RequiresSessionID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.LatestMarker(t.Context(), ""); err == nil {
		t.Error("expected error for empty session id")
	}
}

// Compile-time use check: keep session imported here even if all tests below
// fall back to helper-only references later.
var _ = session.RoleUser
