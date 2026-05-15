package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// newSlashApp builds an App with a real command.Registry wired in.
// All slash-routing tests start from this fixture so they exercise
// the registry hand-off the production wiring uses.
func newSlashApp(t *testing.T) (*App, *bus.Bus, *command.Registry) {
	t.Helper()
	b := bus.New()
	reg := command.New()
	command.RegisterBuiltins(reg)
	command.AttachHelpRegistry(reg)
	t.Cleanup(func() { command.AttachHelpRegistry(nil) })

	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "~/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-5",
		ProfileName:   "work",
		Commands:      reg,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, b, reg
}

// typeInto sends each rune in s through Update as a KeyPressMsg.
func typeInto(app *App, s string) {
	for _, r := range s {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestSlashCommandPaletteShowsForSlashBuffer(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	view := app.View().Content
	if !strings.Contains(view, "/compact") {
		t.Errorf("palette should show /compact for /co buffer:\n%s", view)
	}
	if !strings.Contains(view, "/cost") {
		t.Errorf("palette should show /cost for /co buffer:\n%s", view)
	}
}

func TestSlashCommandPaletteHiddenForNonSlash(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "hello")
	view := app.View().Content
	if strings.Contains(view, "▶") {
		t.Errorf("palette should not render outside slash mode:\n%s", view)
	}
}

func TestSlashCommandEnterRunsHelp(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/help")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter on /help")
	}
	// The notice is set SYNCHRONOUSLY by applyOutcome; the
	// returned cmd merely schedules its clearing.  Don't drive
	// the tick — it would block for noticeLifetime.
	_ = cmd
	if app.notice == "" {
		t.Errorf("expected notice after /help; notice empty")
	}
	if !strings.Contains(app.notice, "/help") {
		t.Errorf("notice should include /help in listing:\n%s", app.notice)
	}
	if app.input.Value() != "" {
		t.Errorf("input should be cleared after slash submit, got %q", app.input.Value())
	}
}

func TestSlashCommandUnknownShowsHint(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/nope")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !strings.Contains(app.notice, "unknown command") {
		t.Errorf("notice should say unknown: %q", app.notice)
	}
}

func TestSlashCommandModelUpdatesOpts(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/model openrouter/gpt-5")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.opts.ModelProvider != "openrouter" {
		t.Errorf("ModelProvider = %q, want openrouter", app.opts.ModelProvider)
	}
	if app.opts.ModelName != "gpt-5" {
		t.Errorf("ModelName = %q, want gpt-5", app.opts.ModelName)
	}
}

func TestSlashCommandReasonUpdatesOpts(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/reason high")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.opts.Reasoning.Effort != "high" {
		t.Errorf("Reasoning.Effort = %q, want high", app.opts.Reasoning.Effort)
	}
}

func TestSlashCommandClearWipesMessages(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.appendAssistantDelta("hello")
	app.flushAssistantStream("assistant", "")
	if len(app.messages) == 0 {
		t.Fatal("setup: expected a message before /clear")
	}
	typeInto(app, "/clear")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(app.messages) != 0 {
		t.Errorf("expected /clear to wipe messages, got %d", len(app.messages))
	}
}

func TestSlashCommandSessionsOpensModal(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/sessions")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.activeModal != command.ModalSessions {
		t.Errorf("activeModal = %q, want %q", app.activeModal, command.ModalSessions)
	}
}

func TestSlashCommandPaletteTabCompletes(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	// Tab completes the highlighted (first) match → /compact.
	app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := app.input.Value(); !strings.HasPrefix(got, "/compact") {
		t.Errorf("expected Tab to complete to /compact, got %q", got)
	}
}

func TestSlashCommandPaletteEscDismisses(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.input.Value() != "" {
		t.Errorf("Esc should clear the slash buffer, got %q", app.input.Value())
	}
}

func TestSlashCommandPaletteArrowsNavigate(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	matches := app.paletteMatches()
	if len(matches) < 2 {
		t.Fatalf("setup: want >=2 matches for /co, got %d", len(matches))
	}
	// Highlight starts at 0 (clamped); Down should move to 1.
	app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if app.paletteHighlight != 1 {
		t.Errorf("after Down, highlight = %d, want 1", app.paletteHighlight)
	}
	// Up should move back to 0.
	app.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	if app.paletteHighlight != 0 {
		t.Errorf("after Up, highlight = %d, want 0", app.paletteHighlight)
	}
}

func TestNonSlashInputStillRoutesToSend(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "hello")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from Enter on plain input")
	}
	msg := cmd()
	if _, ok := msg.(sendStarted); !ok {
		t.Errorf("expected sendStarted msg, got %T", msg)
	}
}

func TestClearNoticeMsgKeepsFresherNotice(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.notice = "second notice"
	// A stale clear for the previous notice should NOT wipe the fresh one.
	app.Update(clearNoticeMsg{notice: "first notice"})
	if app.notice != "second notice" {
		t.Errorf("stale clearNotice wiped a fresher notice: %q", app.notice)
	}
	app.Update(clearNoticeMsg{notice: "second notice"})
	if app.notice != "" {
		t.Errorf("matching clearNotice should clear, got %q", app.notice)
	}
}

func TestSplitSlash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in       string
		wantName string
		wantBody string
	}{
		{"/help", "help", ""},
		{"/help model", "help", "model"},
		{"/model openrouter/gpt-5", "model", "openrouter/gpt-5"},
		{"/review def foo(): pass", "review", "def foo(): pass"},
		{"/cmd   leading   trailing  ", "cmd", "leading   trailing  "},
	}
	for _, c := range cases {
		c := c
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			n, b := splitSlash(c.in)
			if n != c.wantName {
				t.Errorf("name = %q, want %q", n, c.wantName)
			}
			if b != c.wantBody {
				t.Errorf("body = %q, want %q", b, c.wantBody)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// T2.3 Compaction UI tests
// ---------------------------------------------------------------------------

// TestCompact_OpensModal verifies that /compact opens the confirmation modal
// instead of immediately running compaction.
func TestCompact_OpensModal(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	// Type /compact and press Enter.
	typeInto(app, "/compact")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if app.activeModal != command.ModalCompactConfirm {
		t.Errorf("activeModal = %q, want %q", app.activeModal, command.ModalCompactConfirm)
	}
	view := app.View().Content
	if !strings.Contains(view, "Compact session?") {
		t.Errorf("modal should show 'Compact session?', got:\n%s", view)
	}
}

// TestCompact_ModalCancel_Esc verifies that Esc closes the modal without
// triggering compaction.
func TestCompact_ModalCancel_Esc(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	typeInto(app, "/compact")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	// Modal should be open.
	if app.activeModal != command.ModalCompactConfirm {
		t.Fatalf("modal not open after /compact")
	}

	// Press Esc → modal closes.
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.activeModal != "" {
		t.Errorf("modal still open after Esc: %q", app.activeModal)
	}
}

// TestCompact_ModalCancel_N verifies that pressing 'n' closes the modal.
func TestCompact_ModalCancel_N(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	typeInto(app, "/compact")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	if app.activeModal != "" {
		t.Errorf("modal still open after 'n': %q", app.activeModal)
	}
}

// TestCompact_ForceFlag_SkipsModal verifies that /compact --force bypasses
// the modal (the legacy path for power users).
func TestCompact_ForceFlag_SkipsModal(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	// /compact --force should apply Outcome.Compact=true and not open modal.
	typeInto(app, "/compact --force")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if app.activeModal == command.ModalCompactConfirm {
		t.Errorf("--force should bypass the confirmation modal")
	}
}

// TestCompactionBanner_ShowsWhenThresholdEventFires verifies that a
// CompactionRequested{Source:"threshold"} bus event makes the banner visible.
func TestCompactionBanner_ShowsWhenThresholdEventFires(t *testing.T) {
	t.Parallel()
	app, b, _ := newSlashApp(t)
	app.opts.SessionID = "sess-1"
	app.foregroundStack = []string{"sess-1"}

	bus.Publish(b, bus.CompactionRequested{
		SessionID: "sess-1",
		Source:    "threshold",
		UsagePct:  84,
		At:        time.Now(),
	})

	// Let the bridge goroutine deliver the event.
	time.Sleep(50 * time.Millisecond)

	// Drain the busCh via Handle.
	select {
	case ev := <-app.busCh:
		app.Handle(ev)
	default:
	}

	if !app.bannerVisible {
		t.Error("banner should be visible after threshold event")
	}
	if app.bannerPct != 84 {
		t.Errorf("bannerPct = %v, want 84", app.bannerPct)
	}
}

// TestCompactionBanner_DismissedByCtrlX verifies that Ctrl+X hides the banner.
func TestCompactionBanner_DismissedByCtrlX(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.bannerVisible = true
	app.bannerPct = 84

	app.Update(tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if !app.bannerDismissed {
		t.Error("banner should be dismissed after Ctrl+X")
	}

	view := app.View().Content
	if strings.Contains(view, "Context usage at") {
		t.Error("dismissed banner should not appear in view")
	}
}

// TestCompactionInFlight_ShowsNotice verifies that setting compactionInFlight
// causes the "Compacting N messages…" notice to render.
func TestCompactionInFlight_ShowsNotice(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.compactionInFlight = true
	app.compactionInFlightCount = 42

	view := app.View().Content
	if !strings.Contains(view, "Compacting 42 messages") {
		t.Errorf("in-flight notice missing from view:\n%s", view)
	}
}

// TestCompactionToast_ShowsOnComplete verifies that compactionCompleteMsg
// populates the toast line.
func TestCompactionToast_ShowsOnComplete(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	app.Update(compactionCompleteMsg{
		MarkerID:          "mkr_01ABCDEFGHIJK",
		MessagesCompacted: 12,
		SummaryTokens:     512,
	})

	if app.compactionToast == "" {
		t.Fatal("compactionToast should be set after compactionCompleteMsg")
	}
	view := app.View().Content
	if !strings.Contains(view, "Compacted 12 messages") {
		t.Errorf("toast missing compacted count:\n%s", view)
	}
	if !strings.Contains(view, "512 tokens") {
		t.Errorf("toast missing token count:\n%s", view)
	}
}

// TestCompactionToast_FailureShown verifies that a failed compaction surfaces
// the error in the toast.
func TestCompactionToast_FailureShown(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)

	app.Update(compactionCompleteMsg{
		Err: errForTest("provider exploded"),
	})

	view := app.View().Content
	if !strings.Contains(view, "Compaction failed") {
		t.Errorf("failure toast not shown:\n%s", view)
	}
	if !strings.Contains(view, "provider exploded") {
		t.Errorf("failure reason not in toast:\n%s", view)
	}
}

// errForTest is a convenience error for test fixtures.
type testErr struct{ msg string }

func (e testErr) Error() string { return e.msg }

func errForTest(msg string) error { return testErr{msg: msg} }
