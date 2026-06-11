package session

import (
	"context"
	"errors"
	"testing"
)

// stubStore is a minimal session.Store for testing ResolveRootSessionID.
// Only GetSession is implemented; all other methods panic.
type stubStore struct {
	sessions map[string]*Session
	getErr   map[string]error
}

func (s *stubStore) GetSession(_ context.Context, id string) (*Session, error) {
	if err, ok := s.getErr[id]; ok {
		return nil, err
	}
	if sess, ok := s.sessions[id]; ok {
		return sess, nil
	}
	return nil, errors.New("session not found: " + id)
}

// Unimplemented Store methods — tests that only call GetSession won't hit these.

func (s *stubStore) CreateSession(_ context.Context, _ NewSession) (*Session, error) {
	panic("not implemented")
}
func (s *stubStore) ListSessions(_ context.Context, _ ListOpts) ([]*Session, error) {
	panic("not implemented")
}
func (s *stubStore) UpdateSessionTotals(_ context.Context, _ string, _ Totals) error {
	panic("not implemented")
}
func (s *stubStore) PropagateTotals(_ context.Context, _ string, _ Totals) ([]SessionTotalsUpdate, error) {
	panic("not implemented")
}
func (s *stubStore) SoftDeleteSession(_ context.Context, _ string) error { panic("not implemented") }
func (s *stubStore) RenameSession(_ context.Context, _, _ string) error  { panic("not implemented") }
func (s *stubStore) LatestUserMessageID(_ context.Context, _ string) (string, error) {
	panic("not implemented")
}
func (s *stubStore) ForkSession(_ context.Context, _, _ string, _ ModelRef, _ string) (*Session, error) {
	panic("not implemented")
}
func (s *stubStore) AppendMessage(_ context.Context, _ string, _ NewMessage) (*Message, error) {
	panic("not implemented")
}
func (s *stubStore) GetMessage(_ context.Context, _ string) (*Message, error) {
	panic("not implemented")
}
func (s *stubStore) MessagesForSession(_ context.Context, _ string) ([]*Message, error) {
	panic("not implemented")
}
func (s *stubStore) MessagesDirectForSession(_ context.Context, _ string) ([]*Message, error) {
	panic("not implemented")
}
func (s *stubStore) MessagesSinceLatestMarker(_ context.Context, _ string) ([]*Message, *Marker, error) {
	panic("not implemented")
}
func (s *stubStore) AddCompactionMarker(_ context.Context, _, _, _ string, _ int64) (*Marker, error) {
	panic("not implemented")
}
func (s *stubStore) LatestMarker(_ context.Context, _ string) (*Marker, error) {
	panic("not implemented")
}
func (s *stubStore) ListMarkersForSession(_ context.Context, _ string) ([]*Marker, error) {
	panic("not implemented")
}
func (s *stubStore) RememberSessionMemory(_ context.Context, _ string, _ NewMemory) (*Memory, error) {
	panic("not implemented")
}
func (s *stubStore) ListSessionMemories(_ context.Context, _ string) ([]*Memory, error) {
	panic("not implemented")
}
func (s *stubStore) ForgetSessionMemory(_ context.Context, _, _ string) (*Memory, error) {
	panic("not implemented")
}
func (s *stubStore) ReplaceSessionTodos(_ context.Context, _ string, _ []TodoItem) (TodoSummary, error) {
	panic("not implemented")
}
func (s *stubStore) GetSessionTodos(_ context.Context, _ string) ([]TodoItem, TodoSummary, error) {
	panic("not implemented")
}
func (s *stubStore) Close() error { return nil }

// TestResolveRootSessionID_AlreadyRoot verifies that a session with no parent
// returns its own ID.
func TestResolveRootSessionID_AlreadyRoot(t *testing.T) {
	store := &stubStore{
		sessions: map[string]*Session{
			"root": {ID: "root", ParentID: ""},
		},
	}

	got, err := ResolveRootSessionID(context.Background(), store, "root")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "root" {
		t.Errorf("got %q, want %q", got, "root")
	}
}

// TestResolveRootSessionID_OneLevel verifies that a child session returns its
// parent's ID.
func TestResolveRootSessionID_OneLevel(t *testing.T) {
	store := &stubStore{
		sessions: map[string]*Session{
			"root":  {ID: "root", ParentID: ""},
			"child": {ID: "child", ParentID: "root"},
		},
	}

	got, err := ResolveRootSessionID(context.Background(), store, "child")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "root" {
		t.Errorf("got %q, want %q", got, "root")
	}
}

// TestResolveRootSessionID_TwoLevels verifies traversal through a two-level
// subagent chain: grandchild → child → root.
func TestResolveRootSessionID_TwoLevels(t *testing.T) {
	store := &stubStore{
		sessions: map[string]*Session{
			"root":       {ID: "root", ParentID: ""},
			"child":      {ID: "child", ParentID: "root"},
			"grandchild": {ID: "grandchild", ParentID: "child"},
		},
	}

	got, err := ResolveRootSessionID(context.Background(), store, "grandchild")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "root" {
		t.Errorf("got %q, want %q", got, "root")
	}
}

// TestResolveRootSessionID_EmptyID rejects an empty session ID.
func TestResolveRootSessionID_EmptyID(t *testing.T) {
	store := &stubStore{}
	_, err := ResolveRootSessionID(context.Background(), store, "")
	if err == nil {
		t.Fatal("expected error for empty session ID")
	}
}

// TestResolveRootSessionID_StoreError propagates store errors and returns the
// last successfully-walked ID as a fallback.
func TestResolveRootSessionID_StoreError(t *testing.T) {
	sentinelErr := errors.New("db unavailable")
	store := &stubStore{
		sessions: map[string]*Session{
			"child": {ID: "child", ParentID: "root"},
		},
		getErr: map[string]error{
			"root": sentinelErr,
		},
	}

	// child's parent is "root" which errors.  We should get "child" back
	// (the last position before the error) plus a wrapped sentinel error.
	got, err := ResolveRootSessionID(context.Background(), store, "child")
	if err == nil {
		t.Fatal("expected error when store fails")
	}
	if !errors.Is(err, sentinelErr) {
		t.Errorf("error should wrap sentinelErr; got %v", err)
	}
	// Fallback: should return "root" (the ID that caused the error) or "child"
	// depending on which hop failed.  The implementation returns currentID at
	// the point of the error — which is "root" because we successfully read
	// child (finding parentID="root") and then tried to read root.
	if got != "root" {
		t.Errorf("fallback id = %q, want %q", got, "root")
	}
}
