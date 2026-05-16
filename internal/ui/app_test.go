package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/notify"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

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
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
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
	// Sidebar: app name, project path (no session yet so no session title).
	// Footer: agent identity.
	// MessageList: empty-state welcome text.
	for _, want := range []string{"Hygge", "~/proj", "ype a message", "hygge"} {
		if !strings.Contains(out, want) {
			t.Errorf("cold-start view missing %q in:\n%s", want, out)
		}
	}
	// "profile: work" was rendered by the old header bar; it is no longer shown.
	if strings.Contains(out, "profile: work") {
		t.Errorf("profile token should not appear after header bar removal; got:\n%s", out)
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

func TestShiftEnterInsertsInputNewline(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	for _, r := range "hello" {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	if cmd != nil {
		t.Errorf("Shift+Enter should edit input without submitting; got cmd %T", cmd)
	}
	for _, r := range "world" {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	if got, want := app.input.Value(), "hello\nworld"; got != want {
		t.Errorf("input value = %q, want %q", got, want)
	}
	if app.busy {
		t.Error("Shift+Enter should not start a send")
	}
}

func TestAtMentionPaletteCompletesFilesAndSubagents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "ui"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "ui", "app.go"), []byte("package ui\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	b := bus.New()
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ProjectDir:    dir,
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		Subagents: []MentionSubagent{{
			Name:        "search",
			Description: "Search the codebase",
		}},
		Now: func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	for _, r := range "ask @sea" {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if view := app.View().Content; !strings.Contains(view, "@agent:search") {
		t.Fatalf("mention palette missing subagent candidate:\n%s", view)
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got, want := app.input.Value(), "ask @agent:search "; got != want {
		t.Fatalf("subagent mention completion = %q, want %q", got, want)
	}

	app.input.Textarea.SetValue("")
	for _, r := range "open @app" {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if view := app.View().Content; !strings.Contains(view, "@internal/ui/app.go") {
		t.Fatalf("mention palette missing file candidate:\n%s", view)
	}
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if got, want := app.input.Value(), "open @internal/ui/app.go "; got != want {
		t.Fatalf("file mention completion = %q, want %q", got, want)
	}
}

func TestAtMentionPaletteOverlaysChatWithoutMovingEditor(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	b := bus.New()
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ProjectDir:    dir,
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		Subagents: []MentionSubagent{{
			Name:        "search",
			Description: "Search the codebase",
		}},
		Now: func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	typeInto(app, "ask @sea")

	lines := plainViewLines(app)
	wantInputLine := editorTextLine(app)
	if got := lineIndexContaining(lines, "┃ ask @sea"); got != wantInputLine {
		t.Fatalf("input line = %d, want %d; mention palette should overlay chat without moving editor:\n%s", got, wantInputLine, strings.Join(lines, "\n"))
	}
	if got := lineIndexContaining(lines, "@agent:search"); got == -1 || got >= wantInputLine {
		t.Fatalf("mention palette line = %d, want it above input line %d:\n%s", got, wantInputLine, strings.Join(lines, "\n"))
	}
}

func TestAtFileMentionAddsFileToPromptContext(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "docs"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "docs", "notes.md")
	if err := os.WriteFile(path, []byte("important context\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := bus.New()
	t.Cleanup(func() { b.Close() })

	partsCh := make(chan []session.Part, 1)
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         theme.ShellTheme(),
		ProjectDir:    dir,
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		Now:           func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	app.testAgentSendFn = func(_ context.Context, _ string, parts []session.Part) (*session.Message, error) {
		partsCh <- parts
		return nil, nil
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

	for _, r := range "read @docs/notes.md " {
		app.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected send command")
	}
	_ = cmd()

	select {
	case parts := <-partsCh:
		if len(parts) != 2 {
			t.Fatalf("sent %d parts, want text plus attachment: %+v", len(parts), parts)
		}
		if got := parts[0].Text; got != "read @docs/notes.md" {
			t.Fatalf("prompt text = %q", got)
		}
		if !strings.Contains(parts[1].Text, "Attached file: "+path) || !strings.Contains(parts[1].Text, "important context") {
			t.Fatalf("attachment part missing file context: %#v", parts[1].Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("agent send did not receive parts")
	}
}

func TestAtFileMentionAllowsLargeTextFileContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.txt")
	largeContent := strings.Repeat("x", maxPromptAttachmentTextBytes+1)
	if err := os.WriteFile(path, []byte(largeContent), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	attachments, err := (&App{opts: AppOptions{ProjectDir: dir}}).promptAttachmentsForMentions("read @large.txt")
	if err != nil {
		t.Fatalf("promptAttachmentsForMentions: %v", err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments len = %d, want 1", len(attachments))
	}
	if !strings.Contains(attachments[0].Parts[0].Text, largeContent) {
		t.Fatal("large mentioned file content was not attached")
	}

	if _, err := loadPromptAttachment(path); err == nil {
		t.Fatal("/attach path should still enforce text size limit")
	}
}

func TestInputHeightGrowsToEightRowsThenCaps(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	for range 10 {
		app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
		app.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift})
	}

	wantInnerRows := editorMaxHeight - 2
	if got := app.input.Textarea.Height(); got != wantInnerRows {
		t.Errorf("textarea height = %d, want %d inner text rows", got, wantInnerRows)
	}
	l := app.generateLayout(120, 30)
	if got := l.editor.Dy(); got != editorMaxHeight {
		t.Errorf("editor box height = %d, want %d rows", got, editorMaxHeight)
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
	// Non-task tool calls now render as tool-group bubbles (no "▌tool: read" gutter,
	// no raw body in view).  Name and target must appear; raw lines must not.
	for _, want := range []string{"Read", "/etc/passwd"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool view missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "▌tool: read") {
		t.Errorf("tool view must not contain old gutter '▌tool: read'; got:\n%s", out)
	}
	// Non-bash tools don't show output inline.
	if strings.Contains(out, "line1") {
		t.Errorf("read tool should not show output inline; got:\n%s", out)
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
	// Context usage is now shown in the sidebar as "50% used" (sidebar is
	// visible because the test window is 100 columns wide).
	if !strings.Contains(out, "50% used") {
		t.Errorf("expected '50%% used' in sidebar after context update, got:\n%s", out)
	}
}

func TestBusyStateIsTracked(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(sendStarted{})
	if !app.busy {
		t.Errorf("expected app.busy=true after sendStarted")
	}

	// sendCompleted with nil error no longer clears busy — that is now the
	// responsibility of bus.TurnCompleted (or sendCompleted with an error).
	app.Update(sendCompleted{})
	if !app.busy {
		t.Errorf("expected app.busy=true after sendCompleted{Err:nil} (turn not yet finished)")
	}

	// Simulate TurnStarted (increments activeTurns) + TurnCompleted (busy→false).
	app.Handle(bus.TurnStarted{})
	app.Handle(bus.TurnCompleted{})
	if app.busy {
		t.Errorf("expected app.busy=false after TurnCompleted with empty queue")
	}
}

func TestBusyStateIsTracked_ErrorClearsBusy(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(sendStarted{})
	if !app.busy {
		t.Errorf("expected app.busy=true after sendStarted")
	}
	// sendCompleted with an error MUST clear busy (no TurnCompleted will fire).
	app.Update(sendCompleted{Err: errors.New("agent exploded")})
	if app.busy {
		t.Errorf("expected app.busy=false after sendCompleted with error")
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
	if app.rendererW != 45 {
		t.Errorf("renderer width = %d, want 45 (bubble content: int(60*0.80)-3)", app.rendererW)
	}
}

// TestRendererWidthRespectsSidebar verifies that the glamour renderer is built
// at the bubble inner width, not the full terminal or left-column width.
// On a 250-column terminal with a 40-column sidebar, leftW = 210, and the
// bubble content width = int(210*0.80)-3 = 165.  This regression test covers
// the bug where ensureRenderer used a.width (or leftW) instead of the bubble
// inner width, causing markdown lines to overflow the bubble and expand it
// to ~97% of the left column.
func TestRendererWidthRespectsSidebar(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	// Wide terminal: sidebar is 40 cols, leftW = 250 - 40 = 210.
	// Bubble content = int(210*0.80)-3 = 165.
	const termW = 250
	const sidebarW = sidebarFixedWidth
	leftW := termW - sidebarW
	wantRendererW := msgContentWidthForLeft(leftW)
	app.Update(tea.WindowSizeMsg{Width: termW, Height: 40})

	// Trigger a renderer build via a stream-complete event.
	app.Handle(bus.AssistantTextDelta{Text: "# heading\n\nbody text"})
	app.Handle(bus.MessageAppended{Role: "assistant"})

	if app.renderer == nil {
		t.Fatal("expected renderer to be built after MessageAppended")
	}
	if app.rendererW != wantRendererW {
		t.Errorf("renderer width = %d, want %d (bubble content: int(%d*0.80)-3); "+
			"ensureRenderer must use bubble inner width, not leftW or a.width",
			app.rendererW, wantRendererW, leftW)
	}
	// Sanity: msgColW must also equal wantRendererW.
	if app.msgColW != wantRendererW {
		t.Errorf("msgColW = %d, want %d", app.msgColW, wantRendererW)
	}
}

func TestPromptInputWidthFillsMainViewport(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	const termW = 250
	leftW := termW - sidebarFixedWidth
	app.Update(tea.WindowSizeMsg{Width: termW, Height: 40})

	gotInputW := lipgloss.Width(app.input.View())
	if gotInputW != leftW {
		t.Errorf("input view width = %d, want %d (full main viewport width)", gotInputW, leftW)
	}
}

func TestLayoutChatFillsInputClampsAndFooterFixed(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	const (
		termW = 120
		termH = 30
	)

	for _, tt := range []struct {
		name        string
		inputRows   int
		wantEditorH int
	}{
		{name: "minimum", inputRows: 1, wantEditorH: editorMinHeight},
		{name: "maximum", inputRows: 99, wantEditorH: editorMaxHeight},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app.input.Textarea.SetHeight(tt.inputRows)
			l := app.generateLayout(termW, termH)

			if got := l.editor.Dy(); got != tt.wantEditorH {
				t.Errorf("editor height = %d, want %d", got, tt.wantEditorH)
			}
			if got := l.footer.Dy(); got != footerHeight {
				t.Errorf("footer height = %d, want %d", got, footerHeight)
			}

			wantChatH := termH - headerHeight - chatBottomPadding - tt.wantEditorH - footerHeight
			if got := l.chat.Dy(); got != wantChatH {
				t.Errorf("chat height = %d, want %d", got, wantChatH)
			}
		})
	}
}

func TestLayoutMainViewportFillsAroundFixedSidebar(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	t.Run("wide terminal", func(t *testing.T) {
		const (
			termW = 140
			termH = 30
		)
		l := app.generateLayout(termW, termH)

		if got := l.sidebar.Dx(); got != sidebarFixedWidth {
			t.Errorf("sidebar width = %d, want %d", got, sidebarFixedWidth)
		}
		wantMainW := termW - sidebarFixedWidth
		if got := l.leftW; got != wantMainW {
			t.Errorf("main width = %d, want %d", got, wantMainW)
		}
		if got := l.chat.Dx(); got != wantMainW {
			t.Errorf("chat width = %d, want %d", got, wantMainW)
		}
		if got := l.sidebar.Min.X; got != wantMainW {
			t.Errorf("sidebar starts at x=%d, want %d", got, wantMainW)
		}
	})

	t.Run("narrow terminal", func(t *testing.T) {
		const (
			termW = 100
			termH = 30
		)
		l := app.generateLayout(termW, termH)

		if !l.compact {
			t.Error("layout should be compact when sidebar is hidden")
		}
		if got := l.sidebar.Dx(); got != 0 {
			t.Errorf("sidebar width = %d, want 0", got)
		}
		if got := l.leftW; got != termW {
			t.Errorf("main width = %d, want %d", got, termW)
		}
		if got := l.chat.Dx(); got != termW {
			t.Errorf("chat width = %d, want %d", got, termW)
		}
	})
}

// TestRendererWidthNarrowTerminalNoSidebar verifies that on a narrow terminal
// where the sidebar is hidden, the renderer is built at the
// bubble content width for the full terminal width (int(termW*0.80)-3).
func TestRendererWidthNarrowTerminalNoSidebar(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	termW := 80
	wantRendererW := msgContentWidthForLeft(termW) // 61
	app.Update(tea.WindowSizeMsg{Width: termW, Height: 24})

	app.Handle(bus.AssistantTextDelta{Text: "hello"})
	app.Handle(bus.MessageAppended{Role: "assistant"})

	if app.rendererW != wantRendererW {
		t.Errorf("renderer width = %d, want %d (bubble content: int(%d*0.80)-3)",
			app.rendererW, wantRendererW, termW)
	}
	if app.msgColW != wantRendererW {
		t.Errorf("msgColW = %d, want %d", app.msgColW, wantRendererW)
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
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
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

// TestToolCallAddsTouchedFiles verifies that write and edit tool calls add
// the target file to the App's touched-files tracker.
func TestToolCallAddsTouchedFiles(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	// "write" tool with filePath key.
	app.Handle(bus.ToolCallRequested{
		ToolName: "write",
		Args:     []byte(`{"filePath":"/Users/cfb/proj/internal/foo.go","content":"package main"}`),
	})

	// "edit" tool with path key.
	app.Handle(bus.ToolCallRequested{
		ToolName: "edit",
		Args:     []byte(`{"path":"/Users/cfb/proj/internal/bar.go","oldString":"x","newString":"y"}`),
	})

	// "read" tool should NOT add a file.
	app.Handle(bus.ToolCallRequested{
		ToolName: "read",
		Args:     []byte(`{"path":"/Users/cfb/proj/README.md"}`),
	})

	got := app.touched.List()
	if len(got) != 2 {
		t.Fatalf("expected 2 touched files, got %d: %v", len(got), got)
	}
	// Both absolute paths should be present.
	want := map[string]bool{
		"/Users/cfb/proj/internal/foo.go": false,
		"/Users/cfb/proj/internal/bar.go": false,
	}
	for _, p := range got {
		if _, ok := want[p]; ok {
			want[p] = true
		} else {
			t.Errorf("unexpected touched path %q", p)
		}
	}
	for p, found := range want {
		if !found {
			t.Errorf("expected touched path %q not found", p)
		}
	}
}

// --- startSend goroutine tests ----------------------------------------------

// newTestAppWithSendFn builds a test App wired with testAgentSendFn and a
// real store so ensureSession can create a session.  The out-of-band message
// sink is collected into msgs (thread-safe).
func newTestAppWithSendFn(
	t *testing.T,
	sendFn func(ctx context.Context, sid string, parts []session.Part) (*session.Message, error),
) (*App, *[]tea.Msg, *sync.Mutex) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	b := bus.New()
	t.Cleanup(func() { b.Close() })

	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-5",
		Now:           func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	app.testAgentSendFn = sendFn

	var mu sync.Mutex
	var collected []tea.Msg
	app.testSendFn = func(msg tea.Msg) {
		mu.Lock()
		defer mu.Unlock()
		collected = append(collected, msg)
	}

	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return app, &collected, &mu
}

// waitForMsg blocks until msgs contains at least one message or deadline.
func waitForMsg(t *testing.T, msgs *[]tea.Msg, mu *sync.Mutex, deadline time.Duration) tea.Msg {
	t.Helper()
	end := time.Now().Add(deadline)
	for time.Now().Before(end) {
		mu.Lock()
		n := len(*msgs)
		mu.Unlock()
		if n > 0 {
			mu.Lock()
			m := (*msgs)[0]
			mu.Unlock()
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
	return nil
}

// TestStartSend_ReturnsImmediately verifies that the tea.Cmd returned by
// startSend yields sendStarted without waiting for Agent.Send to complete.
// The stub send function sleeps 100ms — the cmd must return before that.
// Not marked parallel: shares SQLite migration state with other store tests.
func TestStartSend_ReturnsImmediately(t *testing.T) {
	done := make(chan struct{})
	app, _, _ := newTestAppWithSendFn(t, func(ctx context.Context, _ string, _ []session.Part) (*session.Message, error) {
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
		}
		close(done)
		return nil, nil
	})

	cmd := app.startSend("hello")
	if cmd == nil {
		t.Fatal("startSend returned nil cmd")
	}

	start := time.Now()
	msg := cmd()
	elapsed := time.Since(start)

	if _, ok := msg.(sendStarted); !ok {
		t.Errorf("expected sendStarted, got %T", msg)
	}
	if elapsed > 10*time.Millisecond {
		t.Errorf("cmd() took %v; expected sub-millisecond (agent stub sleeps 100ms)", elapsed)
	}

	// Wait for stub goroutine to finish so test cleanup is clean.
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		// goroutine may have been cancelled; that's fine
	}
}

// TestStartSend_AgentRunsInGoroutine verifies that subsequent Update calls
// return while Agent.Send is still blocked.
// Not marked parallel: shares SQLite migration state with other store tests.
func TestStartSend_AgentRunsInGoroutine(t *testing.T) {
	release := make(chan struct{})
	app, _, _ := newTestAppWithSendFn(t, func(ctx context.Context, _ string, _ []session.Part) (*session.Message, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	})

	_ = app.startSend("test")

	// A subsequent Update call should return immediately without blocking.
	updateDone := make(chan struct{})
	go func() {
		app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
		close(updateDone)
	}()

	select {
	case <-updateDone:
		// Good: Update returned while agent is still blocked.
	case <-time.After(50 * time.Millisecond):
		t.Error("Update blocked while Agent.Send is in flight (not running in goroutine)")
	}

	close(release)
}

// TestStartSend_SendCompletedFires verifies that sendCompleted arrives via
// sendOutOfBand after Agent.Send returns successfully.
// Not marked parallel: shares SQLite migration state with other store tests.
func TestStartSend_SendCompletedFires(t *testing.T) {
	app, msgs, mu := newTestAppWithSendFn(t, func(_ context.Context, _ string, _ []session.Part) (*session.Message, error) {
		return nil, nil
	})

	_ = app.startSend("hi")

	got := waitForMsg(t, msgs, mu, 2*time.Second)
	if got == nil {
		t.Fatal("sendCompleted never arrived")
	}
	sc, ok := got.(sendCompleted)
	if !ok {
		t.Fatalf("expected sendCompleted, got %T", got)
	}
	if sc.Err != nil {
		t.Errorf("unexpected error in sendCompleted: %v", sc.Err)
	}
}

// TestStartSend_SendFailedFires verifies that an error from Agent.Send
// produces a sendCompleted with Err set.
// Not marked parallel: shares SQLite migration state with other store tests.
func TestStartSend_SendFailedFires(t *testing.T) {
	boom := errors.New("agent exploded")
	app, msgs, mu := newTestAppWithSendFn(t, func(_ context.Context, _ string, _ []session.Part) (*session.Message, error) {
		return nil, boom
	})

	_ = app.startSend("hi")

	got := waitForMsg(t, msgs, mu, 2*time.Second)
	if got == nil {
		t.Fatal("sendCompleted never arrived after error")
	}
	sc, ok := got.(sendCompleted)
	if !ok {
		t.Fatalf("expected sendCompleted, got %T", got)
	}
	if !errors.Is(sc.Err, boom) {
		t.Errorf("expected boom error, got %v", sc.Err)
	}
}

// TestStartSend_InflightCancelStopsGoroutine verifies that cancelling
// inflightCancel causes the goroutine to stop (Agent.Send returns ctx error).
// Not marked parallel: shares SQLite migration state with other store tests.
func TestStartSend_InflightCancelStopsGoroutine(t *testing.T) {
	var sendCtx context.Context
	started := make(chan struct{})
	app, msgs, mu := newTestAppWithSendFn(t, func(ctx context.Context, _ string, _ []session.Part) (*session.Message, error) {
		sendCtx = ctx
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})

	_ = app.startSend("hi")

	// Wait for goroutine to enter Agent.Send.
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine never started Agent.Send")
	}

	// Cancel via inflightCancel (simulates Esc / Ctrl+C handler).
	if app.inflightCancel != nil {
		app.inflightCancel()
	}

	// sendCompleted should arrive with a cancellation error.
	got := waitForMsg(t, msgs, mu, 2*time.Second)
	if got == nil {
		t.Fatal("sendCompleted never arrived after cancel")
	}
	sc, ok := got.(sendCompleted)
	if !ok {
		t.Fatalf("expected sendCompleted, got %T", got)
	}
	if sc.Err == nil {
		t.Error("expected non-nil error after cancellation")
	}
	if !errors.Is(sc.Err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", sc.Err)
	}
	_ = sendCtx // suppress unused-variable warning
}

// ── Tool status tests ─────────────────────────────────────────────────────────

// TestToolStatus_PendingOnRequest verifies that after ToolCallRequested the tool
// row has Status == ToolStatusPending.
func TestToolStatus_PendingOnRequest(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{
		ToolName:  "read",
		ToolUseID: "tu-1",
		Args:      []byte(`{"path":"/tmp/x"}`),
	})

	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].Status != components.ToolStatusPending {
		t.Errorf("Status = %v, want ToolStatusPending", app.messages[0].Status)
	}
}

// TestToolStatus_AwaitingOnPermissionAsked verifies that after PermissionAsked
// the tool row has Status == ToolStatusAwaitingPermission AND the modal opens.
func TestToolStatus_AwaitingOnPermissionAsked(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{
		ToolName:  "write",
		ToolUseID: "tu-2",
		Args:      []byte(`{"path":"/etc/hosts"}`),
	})
	app.Handle(bus.PermissionAsked{
		RequestID: "req-1",
		ToolName:  "write",
		Category:  "file.write",
		Target:    "/etc/hosts",
	})

	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].Status != components.ToolStatusAwaitingPermission {
		t.Errorf("Status = %v, want ToolStatusAwaitingPermission", app.messages[0].Status)
	}
	// Modal must also be open.
	if len(app.pendingPerms) == 0 {
		t.Error("expected permission modal to be open after PermissionAsked")
	}
}

// TestToolStatus_RunningOnPermissionGranted verifies that after PermissionReplied
// with Decision=allow the tool row transitions to Status == ToolStatusRunning.
func TestToolStatus_RunningOnPermissionGranted(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{ToolName: "bash", ToolUseID: "tu-3", Args: []byte(`{}`)})
	app.Handle(bus.PermissionAsked{RequestID: "req-2", ToolName: "bash", Category: "shell", Target: "ls"})
	app.Handle(bus.PermissionReplied{RequestID: "req-2", Decision: "allow", Scope: "once"})

	if app.messages[0].Status != components.ToolStatusRunning {
		t.Errorf("Status = %v, want ToolStatusRunning after allow", app.messages[0].Status)
	}
}

// TestToolStatus_CancelledOnPermissionDenied verifies that after PermissionReplied
// with Decision=deny the tool row transitions to Status == ToolStatusCancelled.
func TestToolStatus_CancelledOnPermissionDenied(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{ToolName: "bash", ToolUseID: "tu-4", Args: []byte(`{}`)})
	app.Handle(bus.PermissionAsked{RequestID: "req-3", ToolName: "bash", Category: "shell", Target: "rm -rf /"})
	app.Handle(bus.PermissionReplied{RequestID: "req-3", Decision: "deny", Scope: "once"})

	if app.messages[0].Status != components.ToolStatusCancelled {
		t.Errorf("Status = %v, want ToolStatusCancelled after deny", app.messages[0].Status)
	}
}

// TestToolStatus_CompletedOnSuccess verifies that after ToolCallCompleted with no
// error the tool row transitions to Status == ToolStatusCompleted.
func TestToolStatus_CompletedOnSuccess(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{ToolName: "read", ToolUseID: "tu-5", Args: []byte(`{"path":"/tmp/ok"}`)})
	app.Handle(bus.ToolCallCompleted{ToolName: "read", ToolUseID: "tu-5", Result: []byte("file contents")})

	if app.messages[0].Status != components.ToolStatusCompleted {
		t.Errorf("Status = %v, want ToolStatusCompleted", app.messages[0].Status)
	}
}

// TestToolStatus_ErrorOnFailure verifies that after ToolCallCompleted with Err set
// the tool row transitions to Status == ToolStatusError.
func TestToolStatus_ErrorOnFailure(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.ToolCallRequested{ToolName: "edit", ToolUseID: "tu-6", Args: []byte(`{"path":"/bad"}`)})
	app.Handle(bus.ToolCallCompleted{ToolName: "edit", ToolUseID: "tu-6", Err: "permission denied"})

	if app.messages[0].Status != components.ToolStatusError {
		t.Errorf("Status = %v, want ToolStatusError", app.messages[0].Status)
	}
	if !app.messages[0].IsError {
		t.Error("expected IsError=true on errored tool")
	}
}

// --- Queue + notification tests ---

// newTestAppWithConfig builds an App with a real bus and an injected config
// for testing notifications.
func newTestAppWithConfig(t *testing.T, cfg *config.Config) (*App, *bus.Bus) {
	t.Helper()
	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "~/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-5",
		Now:           now,
		Config:        cfg,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, b
}

// TestQueueChanged_UpdatesPlaceholder verifies that a QueueChanged event
// with Count > 0 updates the placeholder to include the queue count.
func TestQueueChanged_UpdatesPlaceholder(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	// Simulate busy state and then receive a QueueChanged event.
	app.Update(sendStarted{UserInput: "first"})
	app.Handle(bus.QueueChanged{SessionID: "", Count: 2, Prompts: []string{"p1", "p2"}})

	// The working placeholder should now include "(2 queued)".
	ph := app.input.Textarea.Placeholder
	if !strings.Contains(ph, "2 queued") {
		t.Errorf("placeholder %q should contain '2 queued'", ph)
	}
}

// TestQueueChanged_ClearQueue verifies that a QueueChanged event with
// Count=0 clears the queued indicator.
func TestQueueChanged_ClearQueue(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Update(sendStarted{UserInput: "first"})
	app.Handle(bus.QueueChanged{SessionID: "", Count: 2, Prompts: []string{"p1", "p2"}})
	app.Handle(bus.QueueChanged{SessionID: "", Count: 0, Prompts: nil})

	if app.queueCount != 0 {
		t.Errorf("queueCount = %d, want 0 after clear", app.queueCount)
	}
}

func TestTodoChanged_UpdatesStatusPillState(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.TurnStarted{SessionID: ""})
	app.Handle(bus.TodoChanged{SessionID: "", Incomplete: 2, InProgress: 1})
	if app.todoIncomplete != 2 || app.todoInProgress != 1 {
		t.Fatalf("todo state = incomplete %d, in_progress %d; want 2, 1", app.todoIncomplete, app.todoInProgress)
	}
}

// TestEscWhileQueued_CallsClearQueue verifies that pressing Esc while busy
// with items in the queue calls Agent.ClearQueue and does NOT cancel the
// active run.
func TestEscWhileQueued_CallsClearQueue(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	// Wire a mock clear queue function.
	clearCalled := make(chan string, 1)
	app.testAgentClearQueueFn = func(sid string) int {
		clearCalled <- sid
		return 1
	}

	// Set busy + queued state.
	app.busy = true
	app.queueCount = 1

	app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})

	select {
	case <-clearCalled:
		// Good.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ClearQueue not called within timeout")
	}
}

// TestEscWhileNotQueued_CancelsActiveRun verifies that pressing Esc while busy
// but with an empty queue invokes the inflight cancel.
func TestEscWhileNotQueued_CancelsActiveRun(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	cancelled := false
	app.inflightCancel = func() { cancelled = true }
	app.busy = true
	app.queueCount = 0

	// Ctrl+C cancels the active run (existing path).
	app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	if !cancelled {
		t.Error("expected inflightCancel to be called on Ctrl+C while busy")
	}
}

// TestMaybeNotify_NoopWhenFocused verifies that maybeNotify does nothing when
// the terminal is focused.
func TestMaybeNotify_NoopWhenFocused(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Notifications.Enabled = true
	cfg.Notifications.PermissionAsk = true
	app, _ := newTestAppWithConfig(t, cfg)

	var sent []notify.Notification
	app.notifyBackend = &collectingBackend{received: &sent}
	app.focused = true // focused → no notification

	app.maybeNotify(notify.Notification{Title: "t", Message: "m"}, "permission_ask")
	if len(sent) != 0 {
		t.Errorf("expected no notification when focused, got %d", len(sent))
	}
}

// TestMaybeNotify_SendsWhenUnfocused verifies that maybeNotify sends when
// unfocused and enabled.
func TestMaybeNotify_SendsWhenUnfocused(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Notifications.Enabled = true
	cfg.Notifications.PermissionAsk = true
	app, _ := newTestAppWithConfig(t, cfg)

	var sent []notify.Notification
	app.notifyBackend = &collectingBackend{received: &sent}
	app.focused = false

	app.maybeNotify(notify.Notification{Title: "Hygge is waiting…", Message: "perm"}, "permission_ask")
	if len(sent) != 1 {
		t.Fatalf("expected 1 notification, got %d", len(sent))
	}
	if sent[0].Title != "Hygge is waiting…" {
		t.Errorf("title = %q, want 'Hygge is waiting…'", sent[0].Title)
	}
}

// TestMaybeNotify_DisabledConfig verifies that maybeNotify is a no-op when
// config.Notifications.Enabled is false.
func TestMaybeNotify_DisabledConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Notifications.Enabled = false
	app, _ := newTestAppWithConfig(t, cfg)

	var sent []notify.Notification
	app.notifyBackend = &collectingBackend{received: &sent}
	app.focused = false

	app.maybeNotify(notify.Notification{Title: "t", Message: "m"}, "permission_ask")
	if len(sent) != 0 {
		t.Errorf("expected no notification when disabled, got %d", len(sent))
	}
}

// TestMaybeNotify_TurnCompleteGated verifies that maybeNotify skips turn_complete
// when that kind is not enabled in config.
func TestMaybeNotify_TurnCompleteGated(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{}
	cfg.Notifications.Enabled = true
	cfg.Notifications.TurnComplete = false
	app, _ := newTestAppWithConfig(t, cfg)

	var sent []notify.Notification
	app.notifyBackend = &collectingBackend{received: &sent}
	app.focused = false

	app.maybeNotify(notify.Notification{Title: "t", Message: "m"}, "turn_complete")
	if len(sent) != 0 {
		t.Errorf("expected no turn_complete notification, got %d", len(sent))
	}
}

// collectingBackend records sent notifications for assertions.
type collectingBackend struct {
	received *[]notify.Notification
	mu       sync.Mutex
}

func (c *collectingBackend) Send(n notify.Notification) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	*c.received = append(*c.received, n)
	return nil
}
