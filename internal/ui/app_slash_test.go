package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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

// typeInto sends each rune in s through Update as a Runes key.
func typeInto(app *App, s string) {
	for _, r := range s {
		if r == '/' {
			app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			continue
		}
		app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
}

func TestSlashCommandPaletteShowsForSlashBuffer(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	view := app.View()
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
	view := app.View()
	if strings.Contains(view, "▶") {
		t.Errorf("palette should not render outside slash mode:\n%s", view)
	}
}

func TestSlashCommandEnterRunsHelp(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/help")
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(app.notice, "unknown command") {
		t.Errorf("notice should say unknown: %q", app.notice)
	}
}

func TestSlashCommandModelUpdatesOpts(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/model openrouter/gpt-5")
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.opts.Reasoning.Effort != "high" {
		t.Errorf("Reasoning.Effort = %q, want high", app.opts.Reasoning.Effort)
	}
}

func TestSlashCommandClearWipesMessages(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.appendAssistantDelta("hello")
	app.flushAssistantStream("assistant")
	if len(app.messages) == 0 {
		t.Fatal("setup: expected a message before /clear")
	}
	typeInto(app, "/clear")
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if len(app.messages) != 0 {
		t.Errorf("expected /clear to wipe messages, got %d", len(app.messages))
	}
}

func TestSlashCommandSessionsOpensModal(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/sessions")
	app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if app.activeModal != command.ModalSessions {
		t.Errorf("activeModal = %q, want %q", app.activeModal, command.ModalSessions)
	}
}

func TestSlashCommandPaletteTabCompletes(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	// Tab completes the highlighted (first) match → /compact.
	app.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := app.input.Value(); !strings.HasPrefix(got, "/compact") {
		t.Errorf("expected Tab to complete to /compact, got %q", got)
	}
}

func TestSlashCommandPaletteEscDismisses(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	app.Update(tea.KeyMsg{Type: tea.KeyEsc})
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
	app.Update(tea.KeyMsg{Type: tea.KeyDown})
	if app.paletteHighlight != 1 {
		t.Errorf("after Down, highlight = %d, want 1", app.paletteHighlight)
	}
	// Up should move back to 0.
	app.Update(tea.KeyMsg{Type: tea.KeyUp})
	if app.paletteHighlight != 0 {
		t.Errorf("after Up, highlight = %d, want 0", app.paletteHighlight)
	}
}

func TestNonSlashInputStillRoutesToSend(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "hello")
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
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
