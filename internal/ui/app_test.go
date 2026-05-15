package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
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
		ModelName:     "claude-sonnet-4-5",
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
	out := app.View().Content
	// Header bar: app name + version, profile, project path.
	// Footer: agent identity.
	for _, want := range []string{"Hygge", "profile: work", "~/proj", "ype a message", "no messages"} {
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
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if got := app.input.Value(); got != "hello" {
		t.Fatalf("input value = %q, want %q", got, "hello")
	}

	// Press Enter.
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
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

	out := app.View().Content
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

	out := app.View().Content
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
	out := app.View().Content
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

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
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
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'A', Text: "A"})
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
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
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
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
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
	_, _ = app.Update(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if app.modalToast == "" {
		t.Errorf("expected toast after 'e' key")
	}
	if len(app.pendingPerms) != 1 {
		t.Errorf("'e' should NOT dismiss the modal; pending=%d", len(app.pendingPerms))
	}
	out := app.View().Content
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
	if !strings.Contains(app.View().Content, "/a") {
		t.Errorf("expected first request /a in view")
	}

	_, cmd := app.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	cmd()
	<-repliedCh.C()

	if len(app.pendingPerms) != 1 {
		t.Fatalf("expected 1 pending after dismiss, got %d", len(app.pendingPerms))
	}
	if !strings.Contains(app.View().Content, "/b") {
		t.Errorf("expected second request /b in view after first dismissed")
	}
}

func TestContextUsageUpdatesHeader(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.ContextUsageUpdated{UsedTokens: 50, MaxTokens: 100, PctUsed: 0.5})
	out := app.View().Content
	// Context usage is now shown in the header bar as "50% ctx".
	if !strings.Contains(out, "50% ctx") {
		t.Errorf("expected '50%% ctx' in header after context update, got:\n%s", out)
	}
}

func TestBusyStateIsTracked(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(sendStarted{})
	if !app.busy {
		t.Errorf("expected app.busy=true after sendStarted")
	}

	app.Update(sendCompleted{})
	if app.busy {
		t.Errorf("expected app.busy=false after sendCompleted")
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

func TestCostUpdatesHeader(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.CostUpdated{DollarsTotal: 0.1234})
	out := app.View().Content
	if !strings.Contains(out, "$0.1234") {
		t.Errorf("expected updated cost in header, got:\n%s", out)
	}
}

func TestIterationLimitAppendsSystemMessage(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.IterationLimitReached{Limit: 25})
	out := app.View().Content
	if !strings.Contains(out, "iteration limit reached") {
		t.Errorf("expected system message in view, got:\n%s", out)
	}
}

func TestModalBlocksInputKeys(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.PermissionAsked{RequestID: "rb", ToolName: "x", Target: "y"})
	app.Update(tea.KeyPressMsg{Code: 'h', Text: "h"})
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
	_, _ = app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !cancelled {
		t.Errorf("expected Ctrl+C to call inflightCancel")
	}
}

func TestCtrlLClearsInput(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.input.Textarea.SetValue("garbage")
	_, _ = app.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if app.input.Value() != "" {
		t.Errorf("Ctrl+L did not clear input, got %q", app.input.Value())
	}
}

func TestEnsureSessionReturnsExisting(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "abc"
	got, err := app.ensureSession(context.Background())
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if got != "abc" {
		t.Errorf("got %q, want abc", got)
	}
}

func TestEnsureSessionLazilyCreates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	b := bus.New()
	startSub := bus.Subscribe[bus.SessionStart](b, bus.SubscribeOptions{})
	defer startSub.Unsubscribe()

	var observed string
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-5",
		Now:           func() time.Time { return time.Unix(0, 0).UTC() },
		OnSessionCreated: func(id string) {
			observed = id
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	id, err := app.ensureSession(ctx)
	if err != nil {
		t.Fatalf("ensureSession: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	if app.opts.SessionID != id {
		t.Errorf("opts.SessionID = %q, want %q", app.opts.SessionID, id)
	}
	if observed != id {
		t.Errorf("OnSessionCreated callback id = %q, want %q", observed, id)
	}

	select {
	case ev := <-startSub.C():
		if ev.SessionID != id {
			t.Errorf("SessionStart id = %q, want %q", ev.SessionID, id)
		}
		if ev.Resumed {
			t.Errorf("expected Resumed=false")
		}
	case <-time.After(time.Second):
		t.Fatal("no SessionStart event received")
	}

	// Subsequent calls should be idempotent.
	id2, err := app.ensureSession(ctx)
	if err != nil {
		t.Fatalf("ensureSession (second): %v", err)
	}
	if id2 != id {
		t.Errorf("second call returned %q, want %q", id2, id)
	}

	if _, err := st.GetSession(ctx, id); err != nil {
		t.Errorf("GetSession: %v", err)
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

// ---------------------------------------------------------------------------
// T2.4 — OpenSessionsModalOnStart tests
// ---------------------------------------------------------------------------

// newTestAppWithPicker builds an App that opens the sessions picker on start,
// with an optional in-memory store for session loading.
func newTestAppWithPicker(t *testing.T, st session.Store) (*App, *bus.Bus) {
	t.Helper()
	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	opts := AppOptions{
		Bus:                      b,
		Theme:                    theme.ShellTheme(),
		ProjectDir:               "~/proj",
		ModelProvider:            "anthropic",
		ModelName:                "claude-sonnet-4-5",
		ProfileName:              "default",
		Now:                      now,
		OpenSessionsModalOnStart: true,
	}
	if st != nil {
		opts.Store = st
	}
	app, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, b
}

// TestOpenSessionsModalOnStart_InitOpensPicker verifies that the sessions
// modal is active immediately after Init.
func TestOpenSessionsModalOnStart_InitOpensPicker(t *testing.T) {
	t.Parallel()
	app, _ := newTestAppWithPicker(t, nil)

	// Init should schedule the modal open.
	cmd := app.Init()
	_ = cmd // execute init commands asynchronously; just verify state

	if app.activeModal != "sessions" {
		t.Errorf("expected activeModal=sessions after Init with OpenSessionsModalOnStart, got %q", app.activeModal)
	}
	if !app.sessionsModal.AllowNew {
		t.Errorf("expected sessionsModal.AllowNew=true when opened on start")
	}
}

// TestOpenSessionsModalOnStart_EscWithNoSessionQuitsApp verifies that
// pressing Esc in the picker with no foreground session causes tea.Quit.
func TestOpenSessionsModalOnStart_EscWithNoSessionQuitsApp(t *testing.T) {
	t.Parallel()
	app, _ := newTestAppWithPicker(t, nil)
	_ = app.Init()

	// Simulate Esc key with no sessions loaded.
	app.sessionsModal.Sessions = nil
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected a cmd from Esc (should be tea.Quit)")
	}
	// Execute the cmd and check for tea.QuitMsg.
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); !ok {
		t.Errorf("expected tea.QuitMsg from Esc with no session, got %T", msg)
	}
}

// TestOpenSessionsModalOnStart_NKeyWithNoSessionsStartsFresh verifies that
// pressing 'n' with AllowNew=true and an empty list starts a fresh session.
func TestOpenSessionsModalOnStart_NKeyWithNoSessionsStartsFresh(t *testing.T) {
	t.Parallel()
	app, _ := newTestAppWithPicker(t, nil)
	_ = app.Init()
	app.sessionsModal.Sessions = nil

	// Press 'n'.
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	// The modal should be closed.
	if app.activeModal != "" {
		t.Errorf("expected modal closed after 'n', got %q", app.activeModal)
	}
	// A cmd should have been returned (notice or batch).
	_ = cmd
}

// TestOpenSessionsModalOnStart_SelectSessionSwitches verifies that selecting
// a session in the picker calls applySwitchSession and sets opts.SessionID.
func TestOpenSessionsModalOnStart_SelectSessionSwitches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Seed a session.
	sess, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	app, _ := newTestAppWithPicker(t, st)
	_ = app.Init()

	// Simulate sessions loaded.
	app.Update(sessionsLoadedMsg{sessions: []*session.Session{sess}})

	// Press Enter to select the first (only) session.
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	// The modal should be closed and SessionID should be set.
	if app.activeModal != "" {
		t.Errorf("expected modal closed after selection, got %q", app.activeModal)
	}
	if app.opts.SessionID != sess.ID {
		t.Errorf("expected SessionID=%q, got %q", sess.ID, app.opts.SessionID)
	}
}
