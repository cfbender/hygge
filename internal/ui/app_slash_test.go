package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
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
		Catalog:       cost.NewCatalog(cost.CatalogOptions{Now: now}),
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
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
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

var ansiEscapeRE = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)

func plainViewLines(app *App) []string {
	return strings.Split(ansiEscapeRE.ReplaceAllString(app.View().Content, ""), "\n")
}

func lineIndexContaining(lines []string, needle string) int {
	for i, line := range lines {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func TestSlashCommandModelOpensDialog(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/model")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if top, ok := app.overlays.Top(); !ok || top != overlayModel {
		t.Fatalf("top overlay = %q, %v; want model", top, ok)
	}
	view := app.View().Content
	if !strings.Contains(view, "Select model") || !strings.Contains(view, "Search:") {
		t.Fatalf("model dialog not rendered:\n%s", view)
	}
}

func TestSlashCommandAPIKeyOpensDialogForCurrentProvider(t *testing.T) {
	app, _, _ := newSlashApp(t)
	typeInto(app, "/apikey")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if top, ok := app.overlays.Top(); !ok || top != overlayAPIKey {
		t.Fatalf("top overlay = %q, %v; want apikey", top, ok)
	}
	view := app.View().Content
	if !strings.Contains(view, "Set API key") || !strings.Contains(view, "Provider: anthropic") {
		t.Fatalf("api key dialog not rendered:\n%s", view)
	}
}

func TestAPIKeyDialogMasksSavesAndRefreshesCurrentProvider(t *testing.T) {
	app, _, _ := newSlashApp(t)
	var saved, switched []string
	app.opts.SaveAPIKey = func(_ context.Context, providerName, apiKey string) error {
		saved = append(saved, providerName+":"+apiKey)
		return nil
	}
	app.opts.SwitchModel = func(_ context.Context, providerName, modelName string) error {
		switched = append(switched, providerName+"/"+modelName)
		return nil
	}
	typeInto(app, "/apikey")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(app, "sk-fake-dialog")
	view := app.View().Content
	if strings.Contains(view, "sk-fake-dialog") || !strings.Contains(view, "••••") {
		t.Fatalf("api key input not masked:\n%s", view)
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save cmd")
	}
	app.Update(cmd())
	if got := strings.Join(saved, ","); got != "anthropic:sk-fake-dialog" {
		t.Fatalf("saved = %q", got)
	}
	if got := strings.Join(switched, ","); got != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("switched = %q", got)
	}
	if strings.Contains(app.notice, "sk-fake-dialog") || app.notice != "" {
		t.Fatalf("notice = %q, want toast notification", app.notice)
	}
	if app.toast == nil || app.toast.title != "API key saved" || app.toast.body != "Provider: anthropic" {
		t.Fatalf("toast = %+v, want API key saved toast", app.toast)
	}
}

func TestAPIKeyDialogCancelAndSaveFailure(t *testing.T) {
	app, _, _ := newSlashApp(t)
	app.opts.SaveAPIKey = func(_ context.Context, _, _ string) error { return errors.New("permission denied") }
	typeInto(app, "/apikey openai")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.apiKeyModal.Provider != "openai" {
		t.Fatalf("provider = %q", app.apiKeyModal.Provider)
	}
	typeInto(app, "sk-fake-dialog")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.overlays.Has(overlayAPIKey) {
		t.Fatal("api key dialog should close on Esc")
	}
	typeInto(app, "/apikey openai")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(app, "sk-fake-dialog")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected save cmd")
	}
	app.Update(cmd())
	if !app.overlays.Has(overlayAPIKey) {
		t.Fatal("api key dialog should reopen after save failure")
	}
	if strings.Contains(app.notice, "sk-fake-dialog") || !strings.Contains(app.notice, "permission denied") {
		t.Fatalf("notice = %q", app.notice)
	}
}

func TestModelDialogFilterNarrowsList(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/model")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	before := len(app.modelModal.Filtered())
	typeInto(app, "sonnet")
	after := len(app.modelModal.Filtered())
	if before == 0 {
		t.Fatal("expected embedded catalog models")
	}
	if after == 0 || after >= before {
		t.Fatalf("filtered count = %d, want >0 and <%d", after, before)
	}
}

func TestModelDialogOnlyShowsConfiguredProviders(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	providers := app.opts.Catalog.Source().Providers()
	hasUnconfiguredProvider := false
	for _, provider := range providers {
		if provider != app.opts.ModelProvider {
			hasUnconfiguredProvider = true
			break
		}
	}
	if !hasUnconfiguredProvider {
		t.Fatalf("test catalog providers = %v, want at least one provider besides %q", providers, app.opts.ModelProvider)
	}

	typeInto(app, "/model")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	filtered := app.modelModal.Filtered()
	if len(filtered) == 0 {
		t.Fatal("expected configured provider models")
	}
	for _, opt := range filtered {
		if opt.Provider != app.opts.ModelProvider {
			t.Fatalf("model dialog provider = %q, want only %q", opt.Provider, app.opts.ModelProvider)
		}
	}
}

func TestModelDialogNavigationSelectionUpdatesState(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	var switched []string
	var saved []string
	app.opts.SwitchModel = func(_ context.Context, providerName, modelName string) error {
		switched = append(switched, providerName+"/"+modelName)
		return nil
	}
	app.opts.SaveModel = func(_ context.Context, providerName, modelName string) error {
		saved = append(saved, providerName+"/"+modelName)
		return nil
	}
	typeInto(app, "/model")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(app, "claude")
	filtered := app.modelModal.Filtered()
	if len(filtered) == 0 {
		t.Fatal("expected anthropic model in embedded catalog")
	}
	app.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	app.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected model switch cmd")
	}
	runSlashTestCmd(app, cmd)
	if app.anyOverlayOpen() {
		t.Fatal("model dialog should close after selection")
	}
	if len(switched) != 1 || switched[0] != filtered[0].Provider+"/"+filtered[0].Entry.ID {
		t.Fatalf("SwitchModel calls = %v, want %s/%s", switched, filtered[0].Provider, filtered[0].Entry.ID)
	}
	if app.opts.ModelProvider != filtered[0].Provider || app.opts.ModelName != filtered[0].Entry.ID {
		t.Fatalf("selected model = %s/%s, want %s/%s", app.opts.ModelProvider, app.opts.ModelName, filtered[0].Provider, filtered[0].Entry.ID)
	}
}

func TestModelDialogEscClosesWithoutChange(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/model")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	typeInto(app, "gpt")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.anyOverlayOpen() {
		t.Fatal("model dialog should close on Esc")
	}
	if app.opts.ModelProvider != "anthropic" || app.opts.ModelName != "claude-sonnet-4-5" {
		t.Fatalf("model changed on Esc to %s/%s", app.opts.ModelProvider, app.opts.ModelName)
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

func TestSlashCommandPaletteOverlaysChatWithoutMovingEditor(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")

	lines := plainViewLines(app)
	inputLine := lineIndexContaining(lines, "┃ /co")
	if inputLine == -1 {
		t.Fatalf("splash input line missing; palette should keep input visible:\n%s", strings.Join(lines, "\n"))
	}
	if got := lineIndexContaining(lines, "/compact"); got == -1 {
		t.Fatalf("palette missing for splash input line %d:\n%s", inputLine, strings.Join(lines, "\n"))
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
	var switched []string
	var saved []string
	app.opts.SwitchModel = func(_ context.Context, providerName, modelName string) error {
		switched = append(switched, providerName+"/"+modelName)
		return nil
	}
	app.opts.SaveModel = func(_ context.Context, providerName, modelName string) error {
		saved = append(saved, providerName+"/"+modelName)
		return nil
	}
	typeInto(app, "/model openrouter/gpt-5")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected model switch cmd")
	}
	runSlashTestCmd(app, cmd)
	if len(switched) != 1 || switched[0] != "openrouter/gpt-5" {
		t.Fatalf("SwitchModel calls = %v, want [openrouter/gpt-5]", switched)
	}
	if len(saved) != 1 || saved[0] != "openrouter/gpt-5" {
		t.Fatalf("SaveModel calls = %v, want [openrouter/gpt-5]", saved)
	}
	if app.opts.ModelProvider != "openrouter" {
		t.Errorf("ModelProvider = %q, want openrouter", app.opts.ModelProvider)
	}
	if app.opts.ModelName != "gpt-5" {
		t.Errorf("ModelName = %q, want gpt-5", app.opts.ModelName)
	}
	if app.notice != "" {
		t.Fatalf("notice = %q, want toast notification", app.notice)
	}
	if app.toast == nil || app.toast.title != "Model switched" || app.toast.body != "Using openrouter/gpt-5" {
		t.Fatalf("toast = %+v, want model switched toast", app.toast)
	}
}

func TestSlashCommandModelSaveFailureKeepsRuntimeSwitchAndReportsNotice(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	var switched []string
	app.opts.SwitchModel = func(_ context.Context, providerName, modelName string) error {
		switched = append(switched, providerName+"/"+modelName)
		return nil
	}
	app.opts.SaveModel = func(_ context.Context, _, _ string) error {
		return errors.New("permission denied")
	}
	typeInto(app, "/model openrouter/gpt-5")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected model switch cmd")
	}
	runSlashTestCmd(app, cmd)
	if len(switched) != 1 || switched[0] != "openrouter/gpt-5" {
		t.Fatalf("SwitchModel calls = %v, want [openrouter/gpt-5]", switched)
	}
	if app.opts.ModelProvider != "openrouter" || app.opts.ModelName != "gpt-5" {
		t.Fatalf("selected model = %s/%s, want openrouter/gpt-5", app.opts.ModelProvider, app.opts.ModelName)
	}
	if !strings.Contains(app.notice, "save failed") || !strings.Contains(app.notice, "permission denied") {
		t.Fatalf("notice = %q, want save failure", app.notice)
	}
}

func TestThemeSwitchResultShowsToast(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.Update(themeSwitchResult{name: "shell", theme: theme.ShellTheme()})
	if app.notice != "" {
		t.Fatalf("notice = %q, want toast notification", app.notice)
	}
	if app.toast == nil || app.toast.title != "Theme switched" || app.toast.body != "Using shell" {
		t.Fatalf("toast = %+v, want theme switched toast", app.toast)
	}
}

func TestSlashCommandYoloTogglesPermissionMode(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	var states []bool
	app.opts.SetYolo = func(_ context.Context, enabled bool) error {
		states = append(states, enabled)
		return nil
	}

	typeInto(app, "/yolo")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected yolo cmd")
	}
	app.Update(cmd())

	if got := strings.Trim(strings.Join(boolStrings(states), ","), ","); got != "true" {
		t.Fatalf("SetYolo states = %q, want true", got)
	}
	if !app.opts.Yolo {
		t.Fatal("app yolo flag not enabled")
	}
	if app.toast == nil || app.toast.title != "Yolo mode" || !strings.Contains(app.toast.body, "Enabled") {
		t.Fatalf("toast = %+v, want yolo enabled toast", app.toast)
	}

	typeInto(app, "/yolo off")
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected yolo off cmd")
	}
	app.Update(cmd())

	if got := strings.Trim(strings.Join(boolStrings(states), ","), ","); got != "true,false" {
		t.Fatalf("SetYolo states = %q, want true,false", got)
	}
	if app.opts.Yolo {
		t.Fatal("app yolo flag still enabled")
	}
}

func boolStrings(values []bool) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v {
			out = append(out, "true")
		} else {
			out = append(out, "false")
		}
	}
	return out
}

func runSlashTestCmd(app *App, cmd tea.Cmd) {
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			if nested != nil {
				app.Update(nested())
			}
		}
		return
	}
	app.Update(msg)
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

func TestSlashCommandNewStartsFreshSessionAndClearAliases(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.opts.SessionID = "01HZSESSION"
	app.resetForeground("01HZSESSION")
	app.todoIncomplete = 2
	app.todoInProgress = 1
	app.todosCache = []components.SidebarTodo{{Title: "old todo", Status: components.SidebarTodoInProgress}}
	app.appendAssistantDelta("hello")
	app.flushAssistantStream("assistant", "")
	if len(app.messages) == 0 || app.opts.SessionID == "" {
		t.Fatal("setup: expected active session with messages")
	}

	typeInto(app, "/new")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.opts.SessionID != "" {
		t.Fatalf("SessionID = %q, want empty for fresh session", app.opts.SessionID)
	}
	if len(app.messages) != 0 {
		t.Fatalf("/new should clear rendered messages, got %d", len(app.messages))
	}
	if app.todoIncomplete != 0 || app.todoInProgress != 0 || len(app.todosCache) != 0 {
		t.Fatalf("/new should clear todos, incomplete=%d in_progress=%d cache=%+v", app.todoIncomplete, app.todoInProgress, app.todosCache)
	}

	app.opts.SessionID = "01HZSESSION2"
	app.resetForeground("01HZSESSION2")
	app.todoIncomplete = 1
	app.todosCache = []components.SidebarTodo{{Title: "stale todo", Status: components.SidebarTodoPending}}
	app.appendAssistantDelta("again")
	app.flushAssistantStream("assistant", "")
	if len(app.messages) == 0 {
		t.Fatal("setup: expected messages before /clear alias")
	}
	typeInto(app, "/clear")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.opts.SessionID != "" {
		t.Fatalf("/clear alias SessionID = %q, want empty", app.opts.SessionID)
	}
	if len(app.messages) != 0 {
		t.Errorf("/clear alias should clear rendered messages, got %d", len(app.messages))
	}
	if app.todoIncomplete != 0 || app.todoInProgress != 0 || len(app.todosCache) != 0 {
		t.Fatalf("/clear alias should clear todos, incomplete=%d in_progress=%d cache=%+v", app.todoIncomplete, app.todoInProgress, app.todosCache)
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
	if app.input.Value() != "/co" {
		t.Errorf("Esc should preserve the slash buffer, got %q", app.input.Value())
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

func TestSlashCommandPaletteCtrlNPNavigate(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	app.Update(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if app.paletteHighlight != 1 {
		t.Errorf("after Ctrl+N, highlight = %d, want 1", app.paletteHighlight)
	}
	app.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if app.paletteHighlight != 0 {
		t.Errorf("after Ctrl+P, highlight = %d, want 0", app.paletteHighlight)
	}
}

func TestSlashCommandPaletteEnterCompletesPartialCommand(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/co")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("Enter on partial slash prefix should complete, not execute")
	}
	if got := app.input.Value(); !strings.HasPrefix(got, "/compact") {
		t.Errorf("expected Enter to complete to /compact, got %q", got)
	}
}

func TestSlashCommandPaletteFuzzyMatchesSubsequence(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	typeInto(app, "/cpct")
	view := app.View().Content
	if !strings.Contains(view, "/compact") {
		t.Errorf("palette should fuzzy-match /compact for /cpct buffer:\n%s", view)
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

func TestAttachTextFileShowsChipAndSendIncludesContentThenClears(t *testing.T) {
	app, _, reg := newSlashApp(t)
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("alpha bravo"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotCh := make(chan []session.Part, 1)
	app.opts.SessionID = "session-1"
	app.testAgentSendFn = func(_ context.Context, _ string, parts []session.Part) (*session.Message, error) {
		gotCh <- append([]session.Part(nil), parts...)
		return nil, nil
	}
	if _, ok := reg.Get("attach"); !ok {
		t.Fatal("/attach not registered")
	}

	typeInto(app, "/attach "+path)
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	view := app.View().Content
	if !strings.Contains(view, "notes.txt") || !strings.Contains(view, "/attachments clear") {
		t.Fatalf("attachment chip missing from view:\n%s", view)
	}

	cmd := app.startSend("use this")
	if cmd == nil {
		t.Fatal("startSend returned nil")
	}
	_ = cmd()
	var got []session.Part
	select {
	case got = <-gotCh:
	case <-time.After(2 * time.Second):
		t.Fatal("send function was not called")
	}
	if len(got) < 2 {
		t.Fatalf("got parts = %+v, want prompt plus attachment", got)
	}
	if got[0].Text != "use this" {
		t.Fatalf("first part text = %q", got[0].Text)
	}
	if !strings.Contains(got[1].Text, "Attached file:") || !strings.Contains(got[1].Text, "alpha bravo") {
		t.Fatalf("attachment text part missing content: %+v", got[1])
	}
	if len(app.pendingAttachments) != 0 {
		t.Fatalf("pending attachments not cleared: %+v", app.pendingAttachments)
	}
}

func TestAttachRejectsTooLargeTextFile(t *testing.T) {
	app, _, _ := newSlashApp(t)
	path := filepath.Join(t.TempDir(), "large.txt")
	data := strings.Repeat("x", maxPromptAttachmentTextBytes+1)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	typeInto(app, "/attach "+path)
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(app.pendingAttachments) != 0 {
		t.Fatalf("large file should not attach")
	}
	if !strings.Contains(app.notice, "too large") {
		t.Fatalf("notice = %q, want too large", app.notice)
	}
}

func TestAttachRejectsBinaryNonImageFile(t *testing.T) {
	app, _, _ := newSlashApp(t)
	path := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(path, []byte{0xff, 0x00, 0x01}, 0o600); err != nil {
		t.Fatal(err)
	}
	typeInto(app, "/attach "+path)
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(app.pendingAttachments) != 0 {
		t.Fatalf("binary file should not attach")
	}
	if !strings.Contains(app.notice, "binary files are not supported") {
		t.Fatalf("notice = %q", app.notice)
	}
}

func TestAttachmentsClearCommand(t *testing.T) {
	app, _, _ := newSlashApp(t)
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}
	typeInto(app, "/attach "+path)
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(app.pendingAttachments) != 1 {
		t.Fatalf("setup attach failed: %q", app.notice)
	}
	typeInto(app, "/attachments clear")
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if len(app.pendingAttachments) != 0 {
		t.Fatalf("pending attachments not cleared")
	}
}

func TestSlashCompletionIncludesAttachmentCommands(t *testing.T) {
	app, _, _ := newSlashApp(t)
	typeInto(app, "/att")
	view := app.View().Content
	if !strings.Contains(view, "/attach") || !strings.Contains(view, "/attachments") {
		t.Fatalf("attachment commands missing from palette:\n%s", view)
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

// TestCompactionInFlight_ShowsChatBlock verifies that active compaction renders
// as a transient message-list block instead of a chrome notice above the input.
func TestCompactionInFlight_ShowsChatBlock(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	app.compactionInFlight = true
	app.compactionInFlightCount = 42

	view := app.View().Content
	if !strings.Contains(view, "Crunching 42 messages") {
		t.Errorf("in-flight compaction block missing from view:\n%s", view)
	}
	if strings.Contains(view, "⌛  Compacting") {
		t.Errorf("in-flight compaction should not render as chrome notice:\n%s", view)
	}
}

func TestCompactionStartedCreatesCrunchingAnimation(t *testing.T) {
	t.Parallel()
	app, _, _ := newSlashApp(t)
	cmd := app.handleBusEvent(bus.CompactionStarted{SessionID: app.foregroundID(), MessagesToCompact: 7})
	if cmd == nil {
		t.Fatal("expected compaction animation command")
	}
	if !app.compactionInFlight || app.compactionInFlightCount != 7 || app.compactionAnim == nil {
		t.Fatalf("compaction state = inFlight:%v count:%d anim:%v", app.compactionInFlight, app.compactionInFlightCount, app.compactionAnim)
	}
	view := app.View().Content
	if !strings.Contains(view, "compaction · crunching") || !strings.Contains(view, "Crunching 7 messages") {
		t.Fatalf("compaction working block missing:\n%s", view)
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
