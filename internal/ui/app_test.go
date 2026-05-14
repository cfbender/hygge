package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// newTestApp builds an App with a real bus but no agent/store.  Tests drive
// the model directly via Update and Handle; no goroutine on the bus channel
// is needed because the bridge goroutines started by New also work for tests
// (they subscribe to a real bus).  Close is called in t.Cleanup.
func newTestApp(t *testing.T) (*App, *bus.Bus) {
	t.Helper()
	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "~/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4.5",
		ProfileName:   "work",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Set a known window size so layout is deterministic.
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, b
}

func TestNewValidatesBus(t *testing.T) {
	t.Parallel()
	if _, err := New(AppOptions{}); err == nil {
		t.Fatal("expected error when Bus is nil")
	}
}

func TestColdStartEmptyState(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	out := app.View()
	for _, want := range []string{"[profile:work]", "anthropic/claude-sonnet-4.5", "Type a message", "$0.0000", "no messages"} {
		if !strings.Contains(out, want) {
			t.Errorf("cold-start view missing %q in:\n%s", want, out)
		}
	}
}

func TestUserSubmitClearsInputAndStartsSend(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	// Type "hello".
	for _, r := range "hello" {
		app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if got := app.input.Value(); got != "hello" {
		t.Fatalf("input value = %q, want %q", got, "hello")
	}

	// Press Enter.
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from Enter")
	}
	if got := app.input.Value(); got != "" {
		t.Errorf("input not cleared after submit, got %q", got)
	}
	// Drive the returned cmd; it should produce sendStarted.
	msg := cmd()
	if started, ok := msg.(sendStarted); !ok {
		t.Errorf("expected sendStarted, got %T (%v)", msg, msg)
	} else if started.UserInput != "hello" {
		t.Errorf("sendStarted.UserInput = %q, want %q", started.UserInput, "hello")
	}

	// Apply sendStarted → busy flag should flip.
	app.Update(sendStarted{UserInput: "hello"})
	if !app.busy {
		t.Errorf("expected busy=true after sendStarted")
	}
}

func TestStreamingAssistantText(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.AssistantTextDelta{Text: "hello "})
	app.Handle(bus.AssistantTextDelta{Text: "world"})

	out := app.View()
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected streamed text in view, got:\n%s", out)
	}
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 assistant message, got %d", got)
	}
	if !app.messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=true mid-stream")
	}
}

func TestFinalCommitRendersMarkdown(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.AssistantTextDelta{Text: "# header\n\nbody"})
	app.Handle(bus.MessageAppended{Role: "assistant", MessageID: "m1"})

	if app.messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=false after MessageAppended")
	}
	if app.messages[0].FinalMarkdown == "" {
		t.Errorf("expected FinalMarkdown populated, got empty")
	}
	// glamour should at least have transformed the content somehow.
	if app.messages[0].FinalMarkdown == app.messages[0].Raw {
		t.Errorf("expected glamour to transform content; final == raw == %q", app.messages[0].Raw)
	}
}

func TestToolCallDisplay(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{
		ToolName: "read",
		Args:     []byte(`{"path":"/etc/passwd","limit":50}`),
	})
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].Target != "/etc/passwd" {
		t.Errorf("target = %q, want /etc/passwd", app.messages[0].Target)
	}
	if !app.messages[0].IsStreaming {
		t.Errorf("expected tool message to be streaming until completed")
	}

	app.Handle(bus.ToolCallCompleted{
		ToolName: "read",
		Result:   []byte("line1\nline2\nline3"),
	})
	if app.messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=false after completion")
	}
	if !strings.Contains(app.messages[0].Raw, "line1") {
		t.Errorf("expected result in Raw, got %q", app.messages[0].Raw)
	}

	out := app.View()
	for _, want := range []string{"▌tool: read", "/etc/passwd", "line1"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool view missing %q in:\n%s", want, out)
		}
	}
}

func TestPermissionModalAppears(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.PermissionAsked{
		RequestID: "req-1",
		Category:  "file.read",
		Target:    "/Users/cfb/.aws/credentials",
		ToolName:  "read",
	})
	out := app.View()
	for _, want := range []string{"permission request", "Tool:", "read", "/Users/cfb/.aws/credentials", "[y]"} {
		if !strings.Contains(out, want) {
			t.Errorf("modal view missing %q in:\n%s", want, out)
		}
	}
}

func TestPermissionModalYAllowsOnce(t *testing.T) {
	t.Parallel()
	app, b := newTestApp(t)

	repliedCh := bus.Subscribe[bus.PermissionReplied](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.PermissionAsked{RequestID: "r1", ToolName: "read", Target: "/x"})

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("expected reply cmd")
	}
	cmd() // execute the publish

	select {
	case reply := <-repliedCh.C():
		if reply.Decision != "allow" || reply.Scope != "once" {
			t.Errorf("got decision=%q scope=%q, want allow/once", reply.Decision, reply.Scope)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PermissionReplied")
	}

	if len(app.pendingPerms) != 0 {
		t.Errorf("expected modal closed, %d pending", len(app.pendingPerms))
	}
}

func TestPermissionModalAllowsAlways(t *testing.T) {
	t.Parallel()
	app, b := newTestApp(t)
	repliedCh := bus.Subscribe[bus.PermissionReplied](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.PermissionAsked{RequestID: "r2", ToolName: "bash", Target: "ls"})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'A'}})
	cmd()
	select {
	case reply := <-repliedCh.C():
		if reply.Scope != "always" {
			t.Errorf("scope = %q, want always", reply.Scope)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
}

func TestPermissionModalDeny(t *testing.T) {
	t.Parallel()
	app, b := newTestApp(t)
	repliedCh := bus.Subscribe[bus.PermissionReplied](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.PermissionAsked{RequestID: "r3", ToolName: "write", Target: "/etc/passwd"})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	cmd()

	select {
	case reply := <-repliedCh.C():
		if reply.Decision != "deny" {
			t.Errorf("decision = %q, want deny", reply.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for reply")
	}
}

func TestPermissionModalEscDenies(t *testing.T) {
	t.Parallel()
	app, b := newTestApp(t)
	repliedCh := bus.Subscribe[bus.PermissionReplied](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.PermissionAsked{RequestID: "r4", ToolName: "x", Target: "y"})
	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyEsc})
	cmd()
	select {
	case reply := <-repliedCh.C():
		if reply.Decision != "deny" {
			t.Errorf("Esc decision = %q, want deny", reply.Decision)
		}
	case <-time.After(time.Second):
		t.Fatal("no reply on Esc")
	}
}

func TestPermissionModalEditShowsToast(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.PermissionAsked{RequestID: "r5", ToolName: "x", Target: "y"})
	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	if app.modalToast == "" {
		t.Errorf("expected toast after 'e' key")
	}
	if len(app.pendingPerms) != 1 {
		t.Errorf("'e' should NOT dismiss the modal; pending=%d", len(app.pendingPerms))
	}
	out := app.View()
	if !strings.Contains(out, "edit not yet implemented") {
		t.Errorf("expected toast in view, got:\n%s", out)
	}
}

func TestPermissionModalStacks(t *testing.T) {
	t.Parallel()
	app, b := newTestApp(t)
	repliedCh := bus.Subscribe[bus.PermissionReplied](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.PermissionAsked{RequestID: "first", ToolName: "read", Target: "/a"})
	app.Handle(bus.PermissionAsked{RequestID: "second", ToolName: "read", Target: "/b"})

	if len(app.pendingPerms) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(app.pendingPerms))
	}

	// First View shows the first request.
	if !strings.Contains(app.View(), "/a") {
		t.Errorf("expected first request /a in view")
	}

	_, cmd := app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	cmd()
	<-repliedCh.C()

	if len(app.pendingPerms) != 1 {
		t.Fatalf("expected 1 pending after dismiss, got %d", len(app.pendingPerms))
	}
	if !strings.Contains(app.View(), "/b") {
		t.Errorf("expected second request /b in view after first dismissed")
	}
}

func TestFooterContextColoring(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.ContextUsageUpdated{UsedTokens: 50, MaxTokens: 100, PctUsed: 0.5})
	f := components.Footer{Theme: app.opts.Theme, MaxTok: app.maxTok, UsedTok: app.usedTok, PctUsed: app.pctUsed}
	if got := f.SeverityAtom(); got != theme.AtomSuccess {
		t.Errorf("0.5 → %q, want success", got)
	}

	app.Handle(bus.ContextUsageUpdated{UsedTokens: 85, MaxTokens: 100, PctUsed: 0.85})
	f = components.Footer{Theme: app.opts.Theme, MaxTok: app.maxTok, UsedTok: app.usedTok, PctUsed: app.pctUsed}
	if got := f.SeverityAtom(); got != theme.AtomWarn {
		t.Errorf("0.85 → %q, want warn", got)
	}

	app.Handle(bus.ContextUsageUpdated{UsedTokens: 95, MaxTokens: 100, PctUsed: 0.95})
	f = components.Footer{Theme: app.opts.Theme, MaxTok: app.maxTok, UsedTok: app.usedTok, PctUsed: app.pctUsed}
	if got := f.SeverityAtom(); got != theme.AtomError {
		t.Errorf("0.95 → %q, want error", got)
	}
}

func TestStatusBarSpinnerDuringSend(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(sendStarted{})
	out := app.View()
	if !strings.Contains(out, "●") {
		t.Errorf("expected spinner glyph during send, got:\n%s", out)
	}

	app.Update(sendCompleted{})
	out = app.View()
	if strings.Contains(out, "●") {
		t.Errorf("did not expect spinner glyph after send completed, got:\n%s", out)
	}
}

func TestResizeRebuildsRenderer(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	// Trigger renderer build at width 100.
	app.Handle(bus.AssistantTextDelta{Text: "# h"})
	app.Handle(bus.MessageAppended{Role: "assistant"})
	r1 := app.renderer

	// Resize.
	app.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if app.renderer != nil {
		t.Errorf("expected renderer to be invalidated on resize")
	}

	// Re-render → new renderer built.
	app.messages[0].FinalMarkdown = "" // force rebuild path on next stream completion
	app.messages[0].IsStreaming = true
	app.Handle(bus.MessageAppended{Role: "assistant"})
	if app.renderer == nil {
		t.Fatal("expected renderer rebuilt after stream completion")
	}
	if app.renderer == r1 {
		t.Errorf("expected new renderer instance after resize")
	}
	if app.rendererW != 60 {
		t.Errorf("renderer width = %d, want 60", app.rendererW)
	}
}

func TestCostUpdatesFooter(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.CostUpdated{DollarsTotal: 0.1234})
	out := app.View()
	if !strings.Contains(out, "$0.1234") {
		t.Errorf("expected updated cost in view, got:\n%s", out)
	}
}

func TestIterationLimitAppendsSystemMessage(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.IterationLimitReached{Limit: 25})
	out := app.View()
	if !strings.Contains(out, "iteration limit reached") {
		t.Errorf("expected system message in view, got:\n%s", out)
	}
}

func TestModalBlocksInputKeys(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.PermissionAsked{RequestID: "rb", ToolName: "x", Target: "y"})
	app.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("hello")})
	if app.input.Value() != "" {
		t.Errorf("expected modal to swallow typing, got %q", app.input.Value())
	}
}

func TestCtrlCCancelsInflight(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	cancelled := false
	app.busy = true
	app.inflightCancel = func() { cancelled = true }
	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if !cancelled {
		t.Errorf("expected Ctrl+C to call inflightCancel")
	}
}

func TestCtrlLClearsInput(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.input.Textarea.SetValue("garbage")
	_, _ = app.Update(tea.KeyMsg{Type: tea.KeyCtrlL})
	if app.input.Value() != "" {
		t.Errorf("Ctrl+L did not clear input, got %q", app.input.Value())
	}
}

func TestListenBusReadsAndReissues(t *testing.T) {
	t.Parallel()
	app, b := newTestApp(t)
	// Publish via the real bus; the bridge will forward into busCh.
	bus.Publish(b, bus.CostUpdated{DollarsTotal: 9.99})

	// Spin briefly waiting for the deliver goroutine.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		select {
		case ev := <-app.busCh:
			// Hand it to the App.
			_, cmd := app.Update(busDelivery{Event: ev})
			if cmd == nil {
				t.Fatal("expected cmd batch including listenBus reissue")
			}
			if app.costDollars != 9.99 {
				t.Errorf("cost = %v, want 9.99", app.costDollars)
			}
			return
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatal("never received event off bridge")
}
