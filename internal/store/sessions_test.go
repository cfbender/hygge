package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
)

func sampleModel() session.ModelRef {
	return session.ModelRef{Provider: "anthropic", Name: "claude-3-5-sonnet"}
}

func mustCreateSession(t *testing.T, s *store.Store, projectDir string) *session.Session {
	t.Helper()
	sess, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: projectDir,
		Model:      sampleModel(),
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

func TestCreateSession_Root(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/projects/foo")

	if sess.ID == "" || len(sess.ID) != 26 {
		t.Errorf("expected 26-char ULID id, got %q", sess.ID)
	}
	if sess.ParentID != "" {
		t.Errorf("expected empty parent_id, got %q", sess.ParentID)
	}
	if sess.ForkMessageID != "" {
		t.Errorf("expected empty fork_message_id, got %q", sess.ForkMessageID)
	}
	if sess.ProjectDir != "/projects/foo" {
		t.Errorf("project_dir: got %q", sess.ProjectDir)
	}
	if !sess.DeletedAt.IsZero() {
		t.Errorf("expected zero DeletedAt, got %v", sess.DeletedAt)
	}
}

func TestCreateSession_RequiresProjectDir(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateSession(t.Context(), session.NewSession{Model: sampleModel()})
	if err == nil {
		t.Fatal("expected error when project_dir empty")
	}
}

func TestCreateSession_ParentRequiresForkMessage(t *testing.T) {
	s := newTestStore(t)
	parent := mustCreateSession(t, s, "/p")
	_, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p",
		Model:      sampleModel(),
		ParentID:   parent.ID,
	})
	if err == nil {
		t.Fatal("expected error when parent set but fork_message_id missing")
	}
}

func TestGetSession_ReturnsZeroDeletedAt(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !got.DeletedAt.IsZero() {
		t.Fatalf("expected zero DeletedAt, got %v", got.DeletedAt)
	}
}

func TestGetSession_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.GetSession(t.Context(), "01HXXXXXXXXXXXXXXXXXXXXXXX")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestListSessions_FilterAndLimit(t *testing.T) {
	s := newTestStore(t)
	for range 3 {
		mustCreateSession(t, s, "/a")
	}
	for range 2 {
		mustCreateSession(t, s, "/b")
	}

	all, err := s.ListSessions(t.Context(), session.ListOpts{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(all) != 5 {
		t.Errorf("expected 5 sessions, got %d", len(all))
	}

	onlyA, err := s.ListSessions(t.Context(), session.ListOpts{ProjectDir: "/a"})
	if err != nil {
		t.Fatalf("ListSessions /a: %v", err)
	}
	if len(onlyA) != 3 {
		t.Errorf("expected 3 sessions in /a, got %d", len(onlyA))
	}

	capped, err := s.ListSessions(t.Context(), session.ListOpts{Limit: 2})
	if err != nil {
		t.Fatalf("ListSessions limit: %v", err)
	}
	if len(capped) != 2 {
		t.Errorf("expected limit 2, got %d", len(capped))
	}
}

func TestListSessions_ExcludesDeletedByDefault(t *testing.T) {
	s := newTestStore(t)
	keep := mustCreateSession(t, s, "/p")
	drop := mustCreateSession(t, s, "/p")
	if err := s.SoftDeleteSession(t.Context(), drop.ID); err != nil {
		t.Fatalf("SoftDeleteSession: %v", err)
	}

	visible, err := s.ListSessions(t.Context(), session.ListOpts{ProjectDir: "/p"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(visible) != 1 || visible[0].ID != keep.ID {
		t.Errorf("expected only %q, got %+v", keep.ID, ids(visible))
	}

	withDel, err := s.ListSessions(t.Context(), session.ListOpts{ProjectDir: "/p", IncludeDeleted: true})
	if err != nil {
		t.Fatalf("ListSessions IncludeDeleted: %v", err)
	}
	if len(withDel) != 2 {
		t.Errorf("expected 2 with deleted, got %d", len(withDel))
	}
}

func ids(sess []*session.Session) []string {
	out := make([]string, len(sess))
	for i, s := range sess {
		out[i] = s.ID
	}
	return out
}

func TestSoftDeleteSession_SetsTimestamp(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	if err := s.SoftDeleteSession(t.Context(), sess.ID); err != nil {
		t.Fatalf("SoftDeleteSession: %v", err)
	}
	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.DeletedAt.IsZero() {
		t.Fatalf("expected DeletedAt set, was zero")
	}
}

func TestSoftDeleteSession_AlreadyDeletedNoError(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	if err := s.SoftDeleteSession(t.Context(), sess.ID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := s.SoftDeleteSession(t.Context(), sess.ID); err != nil {
		t.Fatalf("second delete should be no-op, got %v", err)
	}
}

func TestSoftDeleteSession_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.SoftDeleteSession(t.Context(), "01ZZZZZZZZZZZZZZZZZZZZZZZZ")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpdateSessionTotals_Additive(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	delta := session.Totals{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 0, CacheWriteTokens: 0, CostUSD: 0.01,
	}
	for range 2 {
		if err := s.UpdateSessionTotals(t.Context(), sess.ID, delta); err != nil {
			t.Fatalf("UpdateSessionTotals: %v", err)
		}
	}
	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Totals.InputTokens != 2 || got.Totals.OutputTokens != 4 {
		t.Errorf("tokens: got %+v want {2,4,...}", got.Totals)
	}
	if got.Totals.CostUSD < 0.019 || got.Totals.CostUSD > 0.021 {
		t.Errorf("cost: got %v want ~0.02", got.Totals.CostUSD)
	}
}

func TestUpdateSessionTotals_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.UpdateSessionTotals(t.Context(), "01ZZZZZZZZZZZZZZZZZZZZZZZZ", session.Totals{InputTokens: 1})
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestForeignKey_ParentDeleteSetsNullOnChild(t *testing.T) {
	s := newTestStore(t)
	parent := mustCreateSession(t, s, "/p")
	// Append a message we can fork from.
	msg, err := s.AppendMessage(t.Context(), parent.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "hi"}},
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	child, err := s.ForkSession(t.Context(), parent.ID, msg.ID, sampleModel(), "branch")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Fatalf("child parent: got %q want %q", child.ParentID, parent.ID)
	}

	// Hard-delete the parent via raw SQL.  FK ON DELETE SET NULL should
	// clear the child's parent_id.
	if _, err := s.DB().ExecContext(context.Background(),
		"DELETE FROM sessions WHERE id = ?", parent.ID,
	); err != nil {
		t.Fatalf("hard delete parent: %v", err)
	}

	got, err := s.GetSession(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("GetSession child: %v", err)
	}
	if got.ParentID != "" {
		t.Fatalf("expected parent_id cleared, got %q", got.ParentID)
	}
}

func TestCreateSession_RequiresModel(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateSession(t.Context(), session.NewSession{ProjectDir: "/p"})
	if err == nil {
		t.Fatal("expected error when model empty")
	}
}

func TestForkSession_RequiresIDs(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.ForkSession(t.Context(), "", "x", sampleModel(), ""); err == nil {
		t.Error("expected error for empty session id")
	}
	if _, err := s.ForkSession(t.Context(), "x", "", sampleModel(), ""); err == nil {
		t.Error("expected error for empty message id")
	}
}

func TestForkSession_NotFound(t *testing.T) {
	s := newTestStore(t)
	_, err := s.ForkSession(t.Context(), "01ZZZZZZZZZZZZZZZZZZZZZZZZ", "01ZZZZZZZZZZZZZZZZZZZZZZZZ", sampleModel(), "")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing parent, got %v", err)
	}

	parent := mustCreateSession(t, s, "/p")
	_, err = s.ForkSession(t.Context(), parent.ID, "01ZZZZZZZZZZZZZZZZZZZZZZZZ", sampleModel(), "")
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing message, got %v", err)
	}
}

func TestListSessions_OrderedNewestFirst(t *testing.T) {
	s := newTestStore(t)
	first := mustCreateSession(t, s, "/p")
	time.Sleep(2 * time.Millisecond)
	second := mustCreateSession(t, s, "/p")

	got, err := s.ListSessions(t.Context(), session.ListOpts{ProjectDir: "/p"})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(got) != 2 || got[0].ID != second.ID || got[1].ID != first.ID {
		t.Errorf("expected newest first: %v", ids(got))
	}
}
