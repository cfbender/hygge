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

func TestCreateSession_SubagentSkipsForkMessageRequirement(t *testing.T) {
	// Subagent sessions branch out of a tool_use, not a forked
	// message, so the legacy "parent_id requires fork_message_id"
	// rule is relaxed for KindSubagent.
	s := newTestStore(t)
	parent := mustCreateSession(t, s, "/p")
	sub, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p",
		Model:      sampleModel(),
		ParentID:   parent.ID,
		Kind:       session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("subagent CreateSession should not require fork_message_id: %v", err)
	}
	if sub.Kind != session.KindSubagent {
		t.Fatalf("Kind: got %q want %q", sub.Kind, session.KindSubagent)
	}
	if sub.ParentID != parent.ID {
		t.Fatalf("ParentID: got %q want %q", sub.ParentID, parent.ID)
	}
	if sub.ForkMessageID != "" {
		t.Fatalf("subagent should have empty fork_message_id, got %q", sub.ForkMessageID)
	}
}

func TestCreateSession_RejectsUnknownKind(t *testing.T) {
	s := newTestStore(t)
	_, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p",
		Model:      sampleModel(),
		Kind:       "bogus",
	})
	if err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestCreateSession_DefaultKindIsPrimary(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	if sess.Kind != session.KindPrimary {
		t.Fatalf("default Kind: got %q want %q", sess.Kind, session.KindPrimary)
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

// --- New ListOpts filter tests (T1.2) ----------------------------------------

func TestListSessions_KindFilter(t *testing.T) {
	s := newTestStore(t)
	parent := mustCreateSession(t, s, "/p")

	sub, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p",
		Model:      sampleModel(),
		ParentID:   parent.ID,
		Kind:       session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("CreateSession subagent: %v", err)
	}

	primaries, err := s.ListSessions(t.Context(), session.ListOpts{Kind: session.KindPrimary})
	if err != nil {
		t.Fatalf("ListSessions KindPrimary: %v", err)
	}
	for _, sess := range primaries {
		if sess.Kind != session.KindPrimary {
			t.Errorf("KindPrimary filter returned %v session %s", sess.Kind, sess.ID)
		}
	}
	foundParent := false
	for _, sess := range primaries {
		if sess.ID == parent.ID {
			foundParent = true
		}
		if sess.ID == sub.ID {
			t.Errorf("KindPrimary filter should not return subagent %s", sub.ID)
		}
	}
	if !foundParent {
		t.Errorf("KindPrimary filter missing primary session %s", parent.ID)
	}

	subs, err := s.ListSessions(t.Context(), session.ListOpts{Kind: session.KindSubagent})
	if err != nil {
		t.Fatalf("ListSessions KindSubagent: %v", err)
	}
	if len(subs) != 1 || subs[0].ID != sub.ID {
		t.Errorf("KindSubagent filter: got %v want [%s]", ids(subs), sub.ID)
	}

	all, err := s.ListSessions(t.Context(), session.ListOpts{})
	if err != nil {
		t.Fatalf("ListSessions all: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("empty Kind should match all: got %d want 2", len(all))
	}
}

func TestListSessions_ParentIDFilter(t *testing.T) {
	s := newTestStore(t)
	parent := mustCreateSession(t, s, "/p")
	other := mustCreateSession(t, s, "/p")

	sub1, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(),
		ParentID: parent.ID, Kind: session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("CreateSession sub1: %v", err)
	}
	sub2, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(),
		ParentID: parent.ID, Kind: session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("CreateSession sub2: %v", err)
	}
	// Subagent of `other` session — should not appear in parent's filter.
	_, err = s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(),
		ParentID: other.ID, Kind: session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("CreateSession sub3: %v", err)
	}

	got, err := s.ListSessions(t.Context(), session.ListOpts{ParentID: parent.ID})
	if err != nil {
		t.Fatalf("ListSessions ParentID: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("ParentID filter: got %d want 2", len(got))
	}
	gotIDs := ids(got)
	for _, wantID := range []string{sub1.ID, sub2.ID} {
		found := false
		for _, gid := range gotIDs {
			if gid == wantID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ParentID filter missing %s", wantID)
		}
	}
}

func TestListSessions_QueryFilter_Slug(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(),
		Slug: "investigation-bug",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	mustCreateSession(t, s, "/p") // no slug

	got, err := s.ListSessions(t.Context(), session.ListOpts{Query: "invest"})
	if err != nil {
		t.Fatalf("ListSessions query: %v", err)
	}
	if len(got) != 1 || got[0].ID != sess.ID {
		t.Errorf("slug query: got %v want [%s]", ids(got), sess.ID)
	}
}

func TestListSessions_QueryFilter_ProjectDir(t *testing.T) {
	s := newTestStore(t)
	a := mustCreateSession(t, s, "/work/proj/frontend")
	mustCreateSession(t, s, "/work/proj/backend")

	got, err := s.ListSessions(t.Context(), session.ListOpts{Query: "frontend"})
	if err != nil {
		t.Fatalf("ListSessions query: %v", err)
	}
	if len(got) != 1 || got[0].ID != a.ID {
		t.Errorf("project_dir query: got %v want [%s]", ids(got), a.ID)
	}
}

func TestListSessions_QueryFilter_FirstMessage(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	_, err := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "hello world refactor request"}},
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	other := mustCreateSession(t, s, "/p")
	_, err = s.AppendMessage(t.Context(), other.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "completely different topic"}},
	})
	if err != nil {
		t.Fatalf("AppendMessage other: %v", err)
	}

	got, err := s.ListSessions(t.Context(), session.ListOpts{Query: "refactor"})
	if err != nil {
		t.Fatalf("ListSessions query: %v", err)
	}
	if len(got) != 1 || got[0].ID != sess.ID {
		t.Errorf("first-msg query: got %v want [%s]", ids(got), sess.ID)
	}
}

func TestListSessions_QueryFilter_CaseInsensitive(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(), Slug: "MyProject",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	for _, q := range []string{"myproject", "MYPROJECT", "MyPrOjEcT"} {
		got, err := s.ListSessions(t.Context(), session.ListOpts{Query: q})
		if err != nil {
			t.Fatalf("ListSessions query %q: %v", q, err)
		}
		if len(got) != 1 || got[0].ID != sess.ID {
			t.Errorf("case-insensitive query %q: got %v want [%s]", q, ids(got), sess.ID)
		}
	}
}

func TestListSessions_QueryFilter_Empty(t *testing.T) {
	s := newTestStore(t)
	for range 3 {
		mustCreateSession(t, s, "/p")
	}

	got, err := s.ListSessions(t.Context(), session.ListOpts{Query: ""})
	if err != nil {
		t.Fatalf("ListSessions empty query: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("empty query should match all: got %d want 3", len(got))
	}
}

func TestRenameSession_HappyPath(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")

	// Allow UpdatedAt to advance meaningfully.
	time.Sleep(2 * time.Millisecond)

	if err := s.RenameSession(t.Context(), sess.ID, "new-slug"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Slug != "new-slug" {
		t.Errorf("Slug: got %q want %q", got.Slug, "new-slug")
	}
	// UpdatedAt should be >= the original UpdatedAt.
	if got.UpdatedAt.Before(sess.UpdatedAt) {
		t.Errorf("UpdatedAt should not decrease; got %v original %v", got.UpdatedAt, sess.UpdatedAt)
	}
}

func TestRenameSession_ClearsSlugOnEmpty(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(), Slug: "orig",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	if err := s.RenameSession(t.Context(), sess.ID, ""); err != nil {
		t.Fatalf("RenameSession empty: %v", err)
	}
	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Slug != "" {
		t.Errorf("Slug should be empty after clear, got %q", got.Slug)
	}
}

func TestRenameSession_NotFound(t *testing.T) {
	s := newTestStore(t)
	err := s.RenameSession(t.Context(), "01ZZZZZZZZZZZZZZZZZZZZZZZZ", "slug")
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestRenameSession_NoOpSameName(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p", Model: sampleModel(), Slug: "existing",
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Allow a tiny sleep to ensure UpdatedAt would differ if we bumped it.
	time.Sleep(2 * time.Millisecond)
	if err := s.RenameSession(t.Context(), sess.ID, "existing"); err != nil {
		t.Fatalf("RenameSession same slug: %v", err)
	}

	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	// UpdatedAt should NOT have been bumped.
	if got.UpdatedAt.After(sess.UpdatedAt.Add(time.Millisecond)) {
		t.Errorf("UpdatedAt should not be bumped for no-op rename; got %v old %v", got.UpdatedAt, sess.UpdatedAt)
	}
}

func TestAppendMessage_SetsFirstMessagePreview(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")

	_, err := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "what is the capital of France?"}},
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.FirstMessagePreview != "what is the capital of France?" {
		t.Errorf("FirstMessagePreview: got %q want %q", got.FirstMessagePreview, "what is the capital of France?")
	}
}

func TestAppendMessage_PreviewOnlySetOnce(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")

	_, _ = s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "first message"}},
	})
	_, _ = s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "second message should not overwrite preview"}},
	})

	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.FirstMessagePreview != "first message" {
		t.Errorf("FirstMessagePreview should be first message, got %q", got.FirstMessagePreview)
	}
}

func TestAppendMessage_NonUserRoleDoesNotSetPreview(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")

	_, _ = s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartText, Text: "assistant reply"}},
	})

	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.FirstMessagePreview != "" {
		t.Errorf("FirstMessagePreview should be empty for non-user role, got %q", got.FirstMessagePreview)
	}
}

func TestLatestUserMessageID(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")

	// No messages yet.
	id, err := s.LatestUserMessageID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LatestUserMessageID (empty): %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id with no messages, got %q", id)
	}

	// Append an assistant message (should not count).
	_, _ = s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartText, Text: "hi"}},
	})
	id, err = s.LatestUserMessageID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LatestUserMessageID (assistant only): %v", err)
	}
	if id != "" {
		t.Errorf("expected empty id with no user messages, got %q", id)
	}

	// Append two user messages; latest should win.
	msg1, _ := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "first"}},
	})
	time.Sleep(2 * time.Millisecond)
	msg2, _ := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "second"}},
	})

	id, err = s.LatestUserMessageID(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("LatestUserMessageID (two msgs): %v", err)
	}
	if id != msg2.ID {
		t.Errorf("expected latest user msg %s, got %s (first was %s)", msg2.ID, id, msg1.ID)
	}
}
