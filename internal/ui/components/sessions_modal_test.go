package components_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// --- helpers ----------------------------------------------------------------

func makeSession(id, slug, projectDir string, kind session.Kind) *session.Session {
	return &session.Session{
		ID:         id,
		Slug:       slug,
		ProjectDir: projectDir,
		Kind:       kind,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet"},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

func makeDeletedSession(id string) *session.Session {
	s := makeSession(id, "deleted", "/d", session.KindPrimary)
	s.DeletedAt = time.Now()
	return s
}

func makeSubagentSession(id, parentID string) *session.Session {
	s := makeSession(id, "", "/p", session.KindSubagent)
	s.ParentID = parentID
	return s
}

func defaultModal(sessions []*session.Session) components.SessionsModal {
	return components.SessionsModal{
		Sessions:     sessions,
		ForegroundID: "sess-a",
		Theme:        theme.ShellTheme(),
		Width:        100,
		Height:       30,
		Now:          time.Now(),
	}
}

func press(m components.SessionsModal, key string) (components.SessionsModal, components.SessionsModalMsg) {
	k := components.SessionsKey{Name: key}
	if len(key) == 1 {
		k.Runes = []rune(key)
	}
	return m.HandleKey(k)
}

func pressRune(m components.SessionsModal, r rune) (components.SessionsModal, components.SessionsModalMsg) {
	k := components.SessionsKey{Name: string(r), Runes: []rune{r}}
	return m.HandleKey(k)
}

// --- render tests -----------------------------------------------------------

func TestSessionsModal_DefaultView_NoFilter(t *testing.T) {
	t.Parallel()
	sessions := []*session.Session{
		makeSession("sess-a", "main", "/project", session.KindPrimary),
		makeSession("sess-b", "branch", "/project", session.KindPrimary),
	}
	m := defaultModal(sessions)
	v := m.View()
	if !strings.Contains(v, "Sessions") {
		t.Error("view missing 'Sessions' header")
	}
	if !strings.Contains(v, "main") {
		t.Error("view missing session slug 'main'")
	}
	if !strings.Contains(v, "branch") {
		t.Error("view missing session slug 'branch'")
	}
}

func TestSessionsModal_SubagentsHiddenByDefault(t *testing.T) {
	t.Parallel()
	sessions := []*session.Session{
		makeSession("sess-a", "primary", "/p", session.KindPrimary),
		makeSubagentSession("sess-sub", "sess-a"),
	}
	m := defaultModal(sessions)
	_ = m.View()
	// subagent rows should be hidden by default; only the primary row counts.
	if m.FilteredCount() != 1 {
		t.Errorf("FilteredCount() = %d, want 1 (subagents hidden)", m.FilteredCount())
	}
}

func TestSessionsModal_ToggleSubagents(t *testing.T) {
	t.Parallel()
	sessions := []*session.Session{
		makeSession("sess-a", "primary", "/p", session.KindPrimary),
		makeSubagentSession("sess-sub", "sess-a"),
	}
	m := defaultModal(sessions)
	if m.FilteredCount() != 1 {
		t.Fatalf("start: FilteredCount()=%d want 1", m.FilteredCount())
	}
	m2, _ := press(m, "s")
	if m2.FilteredCount() != 2 {
		t.Errorf("after 's': FilteredCount()=%d want 2", m2.FilteredCount())
	}
}

func TestSessionsModal_ToggleDeleted(t *testing.T) {
	t.Parallel()
	sessions := []*session.Session{
		makeSession("sess-a", "live", "/p", session.KindPrimary),
		makeDeletedSession("sess-del"),
	}
	m := defaultModal(sessions)
	if m.FilteredCount() != 1 {
		t.Fatalf("start: FilteredCount()=%d want 1", m.FilteredCount())
	}
	m2, _ := press(m, "d")
	if m2.FilteredCount() != 2 {
		t.Errorf("after 'd': FilteredCount()=%d want 2", m2.FilteredCount())
	}
}

// --- filter tests -----------------------------------------------------------

func TestSessionsModal_FilterNarrowsList(t *testing.T) {
	t.Parallel()
	sessions := []*session.Session{
		makeSession("sess-a", "main-feature", "/project", session.KindPrimary),
		makeSession("sess-b", "hotfix-bug", "/project", session.KindPrimary),
		makeSession("sess-c", "refactor-auth", "/project", session.KindPrimary),
	}
	m := defaultModal(sessions)
	m.FilterFocused = true

	for _, r := range "hot" {
		m, _ = pressRune(m, r)
	}

	if m.FilterValue != "hot" {
		t.Errorf("FilterValue = %q want %q", m.FilterValue, "hot")
	}
	if m.FilteredCount() != 1 {
		t.Errorf("FilteredCount()=%d want 1 for 'hot'", m.FilteredCount())
	}
}

func TestSessionsModal_FilterByFirstMessage(t *testing.T) {
	t.Parallel()
	sess := makeSession("sess-a", "", "/p", session.KindPrimary)
	sess.FirstMessagePreview = "refactor the auth module"
	other := makeSession("sess-b", "", "/p", session.KindPrimary)
	other.FirstMessagePreview = "add a unit test"

	m := defaultModal([]*session.Session{sess, other})
	m.FilterFocused = true
	for _, r := range "refact" {
		m, _ = pressRune(m, r)
	}
	if m.FilteredCount() != 1 {
		t.Errorf("FilteredCount()=%d want 1 for first-msg filter", m.FilteredCount())
	}
}

func TestSessionsModal_FilterEscClears(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.FilterFocused = true
	m.FilterValue = "ma"

	m2, _ := press(m, "esc")
	if m2.FilterValue != "" {
		t.Errorf("FilterValue should be cleared on esc; got %q", m2.FilterValue)
	}
	if !m2.FilterFocused {
		t.Error("FilterFocused should remain true after first esc (only clears value)")
	}
}

func TestSessionsModal_FilterEscTwiceCloses(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.FilterFocused = true
	m.FilterValue = ""

	_, msg := press(m, "esc")
	if _, ok := msg.(components.CloseSessionsModal); !ok {
		t.Errorf("second esc (empty filter) should emit CloseSessionsModal; got %T", msg)
	}
}

// --- keybind dispatch tests -------------------------------------------------

func TestSessionsModal_EnterEmitsSwitchSession(t *testing.T) {
	t.Parallel()
	sess := makeSession("sess-b", "branch", "/p", session.KindPrimary)
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
		sess,
	})
	m.ForegroundID = "sess-a"
	m.Cursor = 1 // select sess-b

	_, msg := press(m, "enter")
	sw, ok := msg.(components.SwitchSessionAction)
	if !ok {
		t.Fatalf("Enter should emit SwitchSessionAction; got %T", msg)
	}
	if sw.ID != "sess-b" {
		t.Errorf("SwitchSessionAction.ID = %q want %q", sw.ID, "sess-b")
	}
}

func TestSessionsModal_EnterOnForegroundEmitsClose(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.ForegroundID = "sess-a"
	m.Cursor = 0

	_, msg := press(m, "enter")
	if _, ok := msg.(components.CloseSessionsModal); !ok {
		t.Errorf("Enter on foreground should emit CloseSessionsModal; got %T", msg)
	}
}

func TestSessionsModal_EnterOnDeletedToast(t *testing.T) {
	t.Parallel()
	del := makeDeletedSession("sess-del")
	m := defaultModal([]*session.Session{del})
	m.ShowDeleted = true
	m.ForegroundID = "sess-a"
	m.Cursor = 0

	m2, msg := press(m, "enter")
	if msg != nil {
		t.Errorf("Enter on deleted should not emit action; got %T", msg)
	}
	if m2.Toast == "" {
		t.Error("should have a toast when trying to switch to deleted session")
	}
}

func TestSessionsModal_FEmitsForkAtLatest(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.Cursor = 0

	_, msg := press(m, "f")
	fork, ok := msg.(components.ForkSessionAction)
	if !ok {
		t.Fatalf("'f' should emit ForkSessionAction; got %T", msg)
	}
	if fork.ID != "sess-a" {
		t.Errorf("ForkSessionAction.ID = %q want %q", fork.ID, "sess-a")
	}
	if fork.MessageID != "" {
		t.Errorf("ForkSessionAction.MessageID should be empty for fork-at-latest; got %q", fork.MessageID)
	}
}

func TestSessionsModal_EscEmitsClose(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{makeSession("sess-a", "main", "/p", session.KindPrimary)})

	_, msg := press(m, "esc")
	if _, ok := msg.(components.CloseSessionsModal); !ok {
		t.Errorf("esc should emit CloseSessionsModal; got %T", msg)
	}
}

// --- rename tests -----------------------------------------------------------

func TestSessionsModal_RenameFlow(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "old-slug", "/p", session.KindPrimary),
	})
	m.Cursor = 0

	// Press 'r' to open rename.
	m2, _ := press(m, "r")
	if !m2.RenameMode {
		t.Fatal("'r' should enter RenameMode")
	}
	if m2.RenameValue != "old-slug" {
		t.Errorf("RenameValue should pre-populate with current slug; got %q", m2.RenameValue)
	}

	// Clear and type new slug.
	for range len("old-slug") {
		m2, _ = press(m2, "backspace")
	}
	for _, r := range "new-slug" {
		m2, _ = pressRune(m2, r)
	}

	// Enter commits.
	m3, msg := press(m2, "enter")
	if m3.RenameMode {
		t.Error("RenameMode should exit after enter")
	}
	rename, ok := msg.(components.RenameSessionAction)
	if !ok {
		t.Fatalf("enter in rename should emit RenameSessionAction; got %T", msg)
	}
	if rename.ID != "sess-a" || rename.Slug != "new-slug" {
		t.Errorf("RenameSessionAction: id=%q slug=%q", rename.ID, rename.Slug)
	}
}

func TestSessionsModal_RenameEscCancels(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "orig", "/p", session.KindPrimary),
	})
	m.Cursor = 0
	m2, _ := press(m, "r")
	m3, msg := press(m2, "esc")
	if m3.RenameMode {
		t.Error("esc should exit RenameMode")
	}
	if msg != nil {
		t.Errorf("esc in rename should not emit action; got %T", msg)
	}
}

// --- delete tests -----------------------------------------------------------

func TestSessionsModal_DeleteConfirmYCommits(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.Cursor = 0

	m2, _ := press(m, "x")
	if !m2.ConfirmDelete {
		t.Fatal("'x' should enter ConfirmDelete state")
	}

	_, msg := pressRune(m2, 'y')
	del, ok := msg.(components.DeleteSessionAction)
	if !ok {
		t.Fatalf("'y' in confirm should emit DeleteSessionAction; got %T", msg)
	}
	if del.ID != "sess-a" {
		t.Errorf("DeleteSessionAction.ID = %q want %q", del.ID, "sess-a")
	}
}

func TestSessionsModal_DeleteConfirmNoCancels(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.Cursor = 0
	m2, _ := press(m, "x")
	m3, msg := pressRune(m2, 'n')
	if m3.ConfirmDelete {
		t.Error("'n' should exit ConfirmDelete")
	}
	if msg != nil {
		t.Errorf("'n' should not emit action; got %T", msg)
	}
}

func TestSessionsModal_DeleteConfirmEscCancels(t *testing.T) {
	t.Parallel()
	m := defaultModal([]*session.Session{
		makeSession("sess-a", "main", "/p", session.KindPrimary),
	})
	m.Cursor = 0
	m2, _ := press(m, "x")
	m3, msg := press(m2, "esc")
	if m3.ConfirmDelete {
		t.Error("esc should exit ConfirmDelete")
	}
	if msg != nil {
		t.Errorf("esc in confirm should not emit action; got %T", msg)
	}
}

// --- navigation tests -------------------------------------------------------

func TestSessionsModal_Navigation(t *testing.T) {
	t.Parallel()
	sessions := []*session.Session{
		makeSession("s0", "a", "/p", session.KindPrimary),
		makeSession("s1", "b", "/p", session.KindPrimary),
		makeSession("s2", "c", "/p", session.KindPrimary),
	}
	m := defaultModal(sessions)
	m.Cursor = 0

	m2, _ := press(m, "down")
	if m2.Cursor != 1 {
		t.Errorf("down: cursor=%d want 1", m2.Cursor)
	}
	m3, _ := press(m2, "j")
	if m3.Cursor != 2 {
		t.Errorf("j: cursor=%d want 2", m3.Cursor)
	}
	m4, _ := press(m3, "down") // at bottom, no-op
	if m4.Cursor != 2 {
		t.Errorf("down at bottom: cursor=%d want 2", m4.Cursor)
	}
	m5, _ := press(m4, "up")
	if m5.Cursor != 1 {
		t.Errorf("up: cursor=%d want 1", m5.Cursor)
	}
	m6, _ := press(m5, "k")
	if m6.Cursor != 0 {
		t.Errorf("k: cursor=%d want 0", m6.Cursor)
	}
	m7, _ := press(m6, "G")
	if m7.Cursor != 2 {
		t.Errorf("G: cursor=%d want 2", m7.Cursor)
	}
	m8, _ := press(m7, "g")
	if m8.Cursor != 0 {
		t.Errorf("g: cursor=%d want 0", m8.Cursor)
	}
}

// --- humanAgo ---------------------------------------------------------------

func TestHumanAgo(t *testing.T) {
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "just now"},
		{90 * time.Second, "1m ago"},
		{3 * time.Hour, "3h ago"},
		{48 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		m := components.SessionsModal{Now: now}
		got := m.HumanAgo(now.Add(-c.d), now)
		if got != c.want {
			t.Errorf("HumanAgo(-%v) = %q want %q", c.d, got, c.want)
		}
	}
}
