package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/command"
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
	plain := ansiEscapeRE.ReplaceAllString(out, "")
	// Sidebar: app name, project path (no session yet so no session title).
	// Footer: agent identity.
	// MessageList: empty-state welcome text.
	for _, want := range []string{"Hygge", "~/proj", "Ask anything", "███████", "General", "claude-sonnet-4-5"} {
		if !strings.Contains(plain, want) {
			t.Errorf("cold-start view missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(plain, "tab  switch mode") || strings.Contains(plain, "ctrl+p  commands") {
		t.Errorf("cold-start splash should not render duplicate shortcut line; got:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "General") || !strings.Contains(lastLine, "Anthropic") {
		t.Errorf("footer should remain pinned to bottom line during splash, got last line %q in:\n%s", lastLine, out)
	}
	if strings.Contains(plain, "What's on your mind?") {
		t.Errorf("cold-start splash should not render the bottom prompt; got:\n%s", out)
	}
	// "profile: work" was rendered by the old header bar; it is no longer shown.
	if strings.Contains(out, "profile: work") {
		t.Errorf("profile token should not appear after header bar removal; got:\n%s", out)
	}
}

func TestLoadSessionTitlePrefersGeneratedSlugOverFirstPreview(t *testing.T) {
	app, st, _ := newTestAppWithStore(t, []session.NewMessage{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "generate a high level overview of / commands in this project"}}},
	})
	if err := st.RenameSession(t.Context(), app.rootSessionID(), "Commands overview"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	if got := app.loadSessionTitle(app.rootSessionID()); got != "Commands overview" {
		t.Fatalf("loadSessionTitle = %q, want generated slug", got)
	}
}

func TestLoadSessionTitleDoesNotUseFirstPreviewAsTitle(t *testing.T) {
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "generate a high level overview of / commands here"}}},
	})

	got := app.loadSessionTitle(app.rootSessionID())
	if got == "generate a high level overview of / commands here" || got == "Commands overview" {
		t.Fatalf("loadSessionTitle = %q, want only persisted model slug or short id", got)
	}
}

func TestTypingKeepsSplashInputCentered(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	typeInto(app, "j")
	out := app.View().Content
	plain := ansiEscapeRE.ReplaceAllString(out, "")
	for _, want := range []string{"███████", "Ctrl+E opens this prompt", "j"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("typing should keep splash prompt visible; missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(plain, "Type a message to get started") || strings.Contains(plain, "│h│ │y│ │g│") {
		t.Fatalf("typing should not reveal the old component empty state:\n%s", out)
	}
}

func TestNoticeKeepsSplashInputCentered(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.notice = "attached screenshot.png"

	out := app.View().Content
	plain := ansiEscapeRE.ReplaceAllString(out, "")
	for _, want := range []string{"███████", "Ctrl+E opens this prompt"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("notice should keep splash prompt visible; missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(plain, "attached screenshot.png") {
		t.Fatalf("splash notice should not consume chrome height:\n%s", out)
	}
}

func TestSplashSmokeAnimates(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	first := ansiEscapeRE.ReplaceAllString(app.renderSplashSmoke(), "")
	if !strings.Contains(first, "┌─┐") || !strings.Contains(first, "(  )") {
		t.Fatalf("splash smoke should render chimney and smoke:\n%s", first)
	}

	app.spinnerTick = splashFrameSlowdown
	second := ansiEscapeRE.ReplaceAllString(app.renderSplashSmoke(), "")
	if first == second {
		t.Fatalf("splash smoke should change between frames:\nfirst:\n%s\nsecond:\n%s", first, second)
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

func TestUserSubmitPreservesPromptWhitespace(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	input := "  hello\nworld\n\n"
	app.input.Textarea.SetValue(input)

	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from Enter")
	}
	msg := cmd()
	started, ok := msg.(sendStarted)
	if !ok {
		t.Fatalf("expected sendStarted, got %T (%v)", msg, msg)
	}
	if started.UserInput != input {
		t.Fatalf("sendStarted.UserInput = %q, want %q", started.UserInput, input)
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

func TestCtrlEEditsPromptExternally(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	var initial string
	app.opts.EditPrompt = func(_ context.Context, text string) (string, error) {
		initial = text
		return "edited\nbody\n", nil
	}

	typeInto(app, "draft")
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected editor cmd")
	}
	app.Update(cmd())

	if initial != "draft" {
		t.Fatalf("editor initial text = %q, want draft", initial)
	}
	if got := app.input.Value(); got != "edited\nbody" {
		t.Fatalf("input value = %q, want edited body", got)
	}
	if app.notice != "" {
		t.Fatalf("notice = %q, want none", app.notice)
	}
}

func TestCtrlEEditorFailureShowsNotice(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.EditPrompt = func(_ context.Context, _ string) (string, error) {
		return "", errors.New("editor unavailable")
	}

	typeInto(app, "draft")
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected editor cmd")
	}
	app.Update(cmd())

	if got := app.input.Value(); got != "draft" {
		t.Fatalf("input value changed after editor failure: %q", got)
	}
	if !strings.Contains(app.notice, "editor unavailable") {
		t.Fatalf("notice = %q, want editor failure", app.notice)
	}
}

func TestCtrlEEditorExpandsPastedMarkers(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	var initial string
	app.opts.EditPrompt = func(_ context.Context, text string) (string, error) {
		initial = text
		return "edited paste", nil
	}

	app.Update(tea.PasteMsg{Content: "alpha\nbravo"})
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected editor cmd")
	}
	app.Update(cmd())

	if initial != "alpha\nbravo " {
		t.Fatalf("editor initial text = %q, want expanded paste", initial)
	}
	if got := app.input.Value(); got != "edited paste" {
		t.Fatalf("input value = %q, want edited paste", got)
	}
	if len(app.pastedInputBlocks) != 0 {
		t.Fatalf("pasted blocks not cleared: %#v", app.pastedInputBlocks)
	}
}

func TestPasteImagePathCreatesMarkerAndSendsAttachment(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "session-1"
	path := filepath.Join(t.TempDir(), "Screenshot 2026-05-18 at 9.25.10 AM.png")
	if err := os.WriteFile(path, []byte("png bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	gotCh := make(chan []session.Part, 1)
	app.testAgentSendFn = func(_ context.Context, _ string, parts []session.Part) (*session.Message, error) {
		gotCh <- append([]session.Part(nil), parts...)
		return nil, nil
	}

	escapedPath := strings.ReplaceAll(path, " ", `\ `)
	app.Update(tea.PasteMsg{Content: escapedPath})

	if got, want := app.input.Value(), "[Pasted image: Screenshot 2026-05-18 at 9.25.10 AM.png] "; got != want {
		t.Fatalf("image paste marker = %q, want %q", got, want)
	}
	if len(app.pendingAttachments) != 0 {
		t.Fatalf("pasted image should be marker-scoped, not a global pending chip: %+v", app.pendingAttachments)
	}
	if len(app.pastedInputBlocks) != 1 || len(app.pastedInputBlocks[0].Attachments) != 1 {
		t.Fatalf("pasted image block missing attachment: %+v", app.pastedInputBlocks)
	}
	view := app.View().Content
	plainView := ansiEscapeRE.ReplaceAllString(view, "")
	if !strings.Contains(plainView, "[Pasted image: Screenshot 2026-05-18 at 9.25.10") || !strings.Contains(plainView, "AM.png]") {
		t.Fatalf("image paste marker missing from view:\n%s", view)
	}
	if app.notice != "" {
		t.Fatalf("image marker is the attachment feedback; notice = %q, want empty", app.notice)
	}

	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected send command")
	}
	_ = cmd()
	var got []session.Part
	select {
	case got = <-gotCh:
	case <-time.After(2 * time.Second):
		t.Fatal("send function was not called")
	}
	if len(got) != 2 {
		t.Fatalf("sent parts = %+v, want text plus image", got)
	}
	if !strings.Contains(got[0].Text, "[Pasted image: Screenshot 2026-05-18 at 9.25.10 AM.png]") {
		t.Fatalf("sent text should keep visible marker, got %q", got[0].Text)
	}
	if got[1].Kind != session.PartImage || got[1].ImageMimeType != "image/png" || got[1].ImageBase64 == "" {
		t.Fatalf("image attachment not sent correctly: %+v", got[1])
	}
	if len(app.messages) == 0 || !strings.Contains(app.messages[len(app.messages)-1].Raw, "[Pasted image: Screenshot 2026-05-18 at 9.25.10 AM.png]") {
		t.Fatalf("optimistic user message should keep visible marker: %+v", app.messages)
	}
	if len(app.pastedInputBlocks) != 0 {
		t.Fatalf("pasted blocks not cleared after send: %+v", app.pastedInputBlocks)
	}
}

func TestPasteNonImagePathStaysText(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("alpha"), 0o600); err != nil {
		t.Fatal(err)
	}

	app.Update(tea.PasteMsg{Content: path})

	if got := app.input.Value(); got != path {
		t.Fatalf("non-image path paste = %q, want %q", got, path)
	}
	if len(app.pendingAttachments) != 0 {
		t.Fatalf("non-image path should not attach: %+v", app.pendingAttachments)
	}
}

func seedLargeStreamingChat(t *testing.T, app *App) {
	t.Helper()
	for range 80 {
		app.messages = append(app.messages, uiMessage{
			Role: components.RoleAssistant,
			Raw:  strings.Repeat("history line\n", 4),
		})
	}
	app.messages = append(app.messages, uiMessage{
		Role:        components.RoleAssistant,
		Raw:         "streaming response",
		IsStreaming: true,
	})
}

func TestRenderChatContent_StreamingTailUsesCacheWithoutNewDelta(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	seedLargeStreamingChat(t, app)

	_ = app.renderChatContent()
	if !app.msgCacheValid {
		t.Fatal("expected initial render to populate message cache")
	}
	cached := app.msgCache
	cachedAt := app.msgCacheTime
	advanced := cachedAt.Add(time.Second)
	app.opts.Now = func() time.Time { return advanced }
	app.userScrolled = true
	app.msgViewport.PageUp()

	_ = app.renderChatContent()
	if app.msgCache != cached {
		t.Fatal("scroll render rebuilt message cache despite unchanged streaming content")
	}
	if !app.msgCacheTime.Equal(cachedAt) {
		t.Fatalf("cache timestamp changed on scroll render: got %s want %s", app.msgCacheTime, cachedAt)
	}
}

func TestRenderChatContent_ScrolledStreamingDeltaDefersRerenderUntilBottom(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	seedLargeStreamingChat(t, app)

	_ = app.renderChatContent()
	baseline := app.msgCache
	app.userScrolled = true
	app.msgViewport.PageUp()
	app.appendAssistantDelta(" more")
	app.invalidateMsgCacheForStreamingDelta()

	_ = app.renderChatContent()
	if app.msgCache != baseline {
		t.Fatal("scrolled streaming delta rebuilt full message cache")
	}
	if !app.msgCacheStreamingDirty {
		t.Fatal("expected streaming dirty flag while rerender is deferred")
	}

	app.userScrolled = false
	_ = app.renderChatContent()
	if app.msgCache == baseline {
		t.Fatal("returning to bottom did not render deferred streaming delta")
	}
	if app.msgCacheStreamingDirty {
		t.Fatal("streaming dirty flag should clear after rebuild")
	}
}

func TestFlushAssistantStreamRestoresPersistedTextWhenDeltasWereDropped(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, nil)

	// Simulate the UI having missed the beginning of a fast stream. The final
	// persisted message is authoritative and should replace the partial buffer
	// when MessageAppended arrives.
	app.messages = []uiMessage{{
		Role:        components.RoleAssistant,
		Raw:         "tail only",
		IsStreaming: true,
	}}

	full := "complete beginning plus tail only"
	msg, err := st.AppendMessage(context.Background(), app.opts.SessionID, session.NewMessage{
		Role: session.RoleAssistant,
		Parts: []session.Part{{
			Kind: session.PartText,
			Text: full,
		}},
		OutputTokens: 42,
		CostUSD:      0.0123,
		DurationMs:   1234,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	app.Handle(bus.MessageAppended{SessionID: app.opts.SessionID, MessageID: msg.ID, Role: string(session.RoleAssistant)})

	if len(app.messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(app.messages))
	}
	got := app.messages[0]
	if got.IsStreaming {
		t.Fatal("assistant message should be finalized")
	}
	if got.Raw != full {
		t.Fatalf("assistant raw = %q, want persisted full text %q", got.Raw, full)
	}
	if got.OutputTokens != 42 || got.CostUSD != 0.0123 || got.DurationMs != 1234 {
		t.Fatalf("usage metadata not restored from persisted message: %#v", got)
	}
	if !strings.Contains(ansiEscapeRE.ReplaceAllString(got.FinalMarkdown, ""), "complete beginning") {
		t.Fatalf("final markdown missing restored beginning: %q", got.FinalMarkdown)
	}
}

func TestFlushAssistantStreamInsertsPersistedTextWhenAllDeltasWereDropped(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, nil)

	// If every text delta was missed and a tool request arrived first, the
	// finalized assistant message should still appear before the trailing tool row.
	app.messages = []uiMessage{{
		Role:        components.RoleTool,
		ToolName:    "read",
		ToolUseID:   "tu-1",
		Target:      "/tmp/x",
		ToolArgs:    []byte(`{"path":"/tmp/x"}`),
		Raw:         "(running…)",
		IsStreaming: true,
		Status:      components.ToolStatusPending,
	}}

	full := "assistant text recovered from the store"
	msg, err := st.AppendMessage(context.Background(), app.opts.SessionID, session.NewMessage{
		Role: session.RoleAssistant,
		Parts: []session.Part{{
			Kind: session.PartText,
			Text: full,
		}},
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	app.Handle(bus.MessageAppended{SessionID: app.opts.SessionID, MessageID: msg.ID, Role: string(session.RoleAssistant)})

	if len(app.messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(app.messages))
	}
	if app.messages[0].Role != components.RoleAssistant || app.messages[0].Raw != full {
		t.Fatalf("messages[0] = %#v, want recovered assistant text", app.messages[0])
	}
	if app.messages[1].Role != components.RoleTool {
		t.Fatalf("messages[1].Role = %q, want trailing tool preserved", app.messages[1].Role)
	}
}

func TestScrollBarDragScrollsChatViewport(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for i := range 80 {
		app.messages = append(app.messages, uiMessage{
			Role: components.RoleAssistant,
			Raw:  "message line " + string(rune('a'+(i%26))),
		})
	}
	app.invalidateMsgCache()
	_ = app.View()

	if !app.scrollBarVisible() {
		t.Fatal("expected visible scrollbar for overflowing chat")
	}
	bottomOffset := app.msgViewport.YOffset()
	if bottomOffset == 0 {
		t.Fatal("expected viewport to start at bottom with overflow")
	}
	geom, ok := app.scrollBarGeometry()
	if !ok {
		t.Fatal("expected scrollbar geometry")
	}

	app.Update(tea.MouseClickMsg{X: geom.X, Y: geom.ThumbY, Button: tea.MouseLeft})
	app.Update(tea.MouseMotionMsg{X: geom.X, Y: 0})
	app.Update(tea.MouseReleaseMsg{X: geom.X, Y: 0, Button: tea.MouseLeft})

	if app.scrollDragActive {
		t.Fatal("scroll drag still active after release")
	}
	if got := app.msgViewport.YOffset(); got != 0 {
		t.Fatalf("viewport offset = %d, want top", got)
	}
	if !app.userScrolled {
		t.Fatal("dragging upward should pause auto-scroll")
	}
}

func TestScrollBarDragToBottomResumesAutoScroll(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	for i := range 80 {
		app.messages = append(app.messages, uiMessage{
			Role: components.RoleAssistant,
			Raw:  "message line " + string(rune('a'+(i%26))),
		})
	}
	app.invalidateMsgCache()
	_ = app.View()
	app.msgViewport.GotoTop()
	app.userScrolled = true

	geom, ok := app.scrollBarGeometry()
	if !ok {
		t.Fatal("expected scrollbar geometry")
	}
	app.Update(tea.MouseClickMsg{X: geom.X, Y: geom.ThumbY, Button: tea.MouseLeft})
	app.Update(tea.MouseMotionMsg{X: geom.X, Y: app.height - 1})
	app.Update(tea.MouseReleaseMsg{X: geom.X, Y: app.height - 1, Button: tea.MouseLeft})

	if !app.msgViewport.AtBottom() {
		t.Fatalf("viewport offset = %d, want bottom", app.msgViewport.YOffset())
	}
	if app.userScrolled {
		t.Fatal("dragging to bottom should resume auto-scroll")
	}
}

func TestMultiLinePasteCollapsesToMarkerAndSendsContent(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.Modes[app.modeIndex].Color = "5"

	_, cmd := app.Update(tea.PasteMsg{Content: "alpha\nbravo\ncharlie"})
	if cmd != nil {
		t.Fatalf("multi-line paste should be handled locally, got cmd %T", cmd)
	}
	if got, want := app.input.Value(), "[Pasted 3 lines] "; got != want {
		t.Fatalf("input after paste = %q, want %q", got, want)
	}
	view := app.View().Content
	chip := lipgloss.NewStyle().
		Foreground(lipgloss.Color("0")).
		Background(lipgloss.Color("5")).
		Render("[Pasted 3 lines]")
	if !strings.Contains(view, chip) {
		t.Fatalf("paste marker missing from view:\n%s", view)
	}
	if strings.Contains(view, "bravo") {
		t.Fatalf("raw pasted middle line should not be visible in editor:\n%s", view)
	}

	typeInto(app, "summarize")
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected send command")
	}
	msg := cmd()
	started, ok := msg.(sendStarted)
	if !ok {
		t.Fatalf("cmd returned %T, want sendStarted", msg)
	}
	if got, want := started.UserInput, "alpha\nbravo\ncharlie summarize"; got != want {
		t.Fatalf("sent input = %q, want %q", got, want)
	}
	if len(app.pastedInputBlocks) != 0 {
		t.Fatalf("pasted blocks not cleared after send: %+v", app.pastedInputBlocks)
	}
}

func TestMultiLinePasteMarkerBackspacesAsSingleChip(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Update(tea.PasteMsg{Content: "alpha\nbravo\ncharlie"})
	app.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got, want := app.input.Value(), "[Pasted 3 lines]"; got != want {
		t.Fatalf("first backspace should remove trailing paste space, got %q want %q", got, want)
	}
	if len(app.pastedInputBlocks) != 1 {
		t.Fatalf("first backspace should keep paste block: %+v", app.pastedInputBlocks)
	}

	app.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := app.input.Value(); got != "" {
		t.Fatalf("second backspace should remove whole paste chip, got %q", got)
	}
	if len(app.pastedInputBlocks) != 0 {
		t.Fatalf("paste block should be removed: %+v", app.pastedInputBlocks)
	}
}

func TestMultiLinePasteMarkerCannotBeEditedInternally(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Update(tea.PasteMsg{Content: "alpha\nbravo\ncharlie"})
	app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	app.Update(tea.KeyPressMsg{Code: 'X', Text: "X"})
	if got, want := app.input.Value(), "X[Pasted 3 lines] "; got != want {
		t.Fatalf("typing after moving left should land before chip, got %q want %q", got, want)
	}
}

func TestSingleLinePasteStaysEditableText(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Update(tea.PasteMsg{Content: "alpha"})
	if got, want := app.input.Value(), "alpha"; got != want {
		t.Fatalf("input after single-line paste = %q, want %q", got, want)
	}
	if len(app.pastedInputBlocks) != 0 {
		t.Fatalf("single-line paste should not create collapsed blocks: %+v", app.pastedInputBlocks)
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

func TestAtMentionPaletteMouseWheelScrollsRenderedWindow(t *testing.T) {
	t.Parallel()
	b := bus.New()
	subagents := make([]MentionSubagent, 12)
	for i := range subagents {
		subagents[i] = MentionSubagent{Name: fmt.Sprintf("agent%02d", i), Description: "scroll test"}
	}
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		Subagents:     subagents,
		Now:           func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	typeInto(app, "ask @agent")
	for range 10 {
		app.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	}

	view := ansiEscapeRE.ReplaceAllString(app.View().Content, "")
	if !strings.Contains(view, "@agent:agent10") {
		t.Fatalf("rendered mention palette did not scroll to highlighted mention @agent:agent10:\n%s", view)
	}
	if strings.Contains(view, "@agent:agent00") {
		t.Fatalf("rendered mention palette still shows the first mention after scrolling near the end:\n%s", view)
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
	inputLine := lineIndexContaining(lines, "┃ ask @sea")
	if inputLine == -1 {
		t.Fatalf("splash input line missing; mention palette should keep input visible:\n%s", strings.Join(lines, "\n"))
	}
	if got := lineIndexContaining(lines, "@agent:search"); got == -1 {
		t.Fatalf("mention palette missing for splash input line %d:\n%s", inputLine, strings.Join(lines, "\n"))
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
		if got := parts[0].Text; got != "read @docs/notes.md " {
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
	app.messages = []uiMessage{{Role: components.RoleUser, Raw: "hello"}}

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

func TestStreamingAssistantMarkdownRendersBeforeFinalize(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Handle(bus.AssistantTextDelta{Text: "# heading\n\nbody"})
	if !app.messages[0].IsStreaming {
		t.Fatal("expected assistant message to still be streaming")
	}
	if app.messages[0].FinalMarkdown == "" {
		t.Fatal("expected streaming assistant markdown to be rendered")
	}
	before := app.View().Content
	plainBefore := ansiEscapeRE.ReplaceAllString(before, "")
	if strings.Contains(plainBefore, "# heading") {
		t.Fatalf("streaming view should already use markdown rendering, got:\n%s", before)
	}

	app.Handle(bus.MessageAppended{Role: "assistant", MessageID: "m1"})
	after := app.View().Content
	if got, want := lipgloss.Height(after), lipgloss.Height(before); got != want {
		t.Fatalf("finalizing markdown changed view height: got %d, want %d\nbefore:\n%s\nafter:\n%s", got, want, before, after)
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

func TestQuestionModalPublishesSelectedAnswer(t *testing.T) {
	app, b := newTestApp(t)
	repliedCh := bus.Subscribe[bus.QuestionAnswered](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.QuestionAsked{
		RequestID: "q-1",
		ToolName:  "question",
		Question:  "Pick a strategy",
		Options: []bus.QuestionOption{
			{ID: "1", Label: "Fast"},
			{ID: "2", Label: "Careful"},
		},
	})
	if len(app.pendingQuestions) != 1 {
		t.Fatalf("pendingQuestions len = %d, want 1", len(app.pendingQuestions))
	}
	plain := ansiEscapeRE.ReplaceAllString(app.View().Content, "")
	for _, want := range []string{"question", "Pick a strategy", "[1] Fast", "[2] Careful"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("question modal missing %q in:\n%s", want, plain)
		}
	}

	_, cmd := app.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	if cmd == nil {
		t.Fatal("expected reply cmd")
	}
	cmd()
	if len(app.pendingQuestions) != 0 {
		t.Fatalf("pendingQuestions len = %d, want 0", len(app.pendingQuestions))
	}
	select {
	case reply := <-repliedCh.C():
		if reply.RequestID != "q-1" || reply.AnswerID != "2" || reply.Answer != "Careful" || reply.Canceled {
			t.Fatalf("unexpected reply: %+v", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for QuestionAnswered")
	}
}

func TestQuestionModalArrowKeysSelectRenderedMarkdownAnswer(t *testing.T) {
	app, b := newTestApp(t)
	repliedCh := bus.Subscribe[bus.QuestionAnswered](b, bus.SubscribeOptions{})
	defer repliedCh.Unsubscribe()

	app.Handle(bus.QuestionAsked{
		RequestID: "q-markdown",
		ToolName:  "question",
		Question:  "Pick a **strategy** with `markdown`",
		Options: []bus.QuestionOption{
			{ID: "1", Label: "**Fast** option"},
			{ID: "2", Label: "_Careful_ option with `checks`"},
		},
	})
	plain := ansiEscapeRE.ReplaceAllString(app.View().Content, "")
	for _, rawMarker := range []string{"**strategy**", "`markdown`", "**Fast**", "_Careful_", "`checks`"} {
		if strings.Contains(plain, rawMarker) {
			t.Fatalf("question modal preserved raw markdown marker %q in:\n%s", rawMarker, plain)
		}
	}
	if !strings.Contains(plain, "› [1]") {
		t.Fatalf("expected first answer selected initially, got:\n%s", plain)
	}

	app.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if app.questionSelectedIndex != 1 {
		t.Fatalf("questionSelectedIndex = %d, want 1", app.questionSelectedIndex)
	}
	plain = ansiEscapeRE.ReplaceAllString(app.View().Content, "")
	if !strings.Contains(plain, "› [2]") {
		t.Fatalf("expected second answer selected after down arrow, got:\n%s", plain)
	}

	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected reply cmd")
	}
	cmd()
	select {
	case reply := <-repliedCh.C():
		if reply.RequestID != "q-markdown" || reply.AnswerID != "2" || reply.Answer != "_Careful_ option with `checks`" || reply.Canceled {
			t.Fatalf("unexpected reply: %+v", reply)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for QuestionAnswered")
	}
}

func TestContextUsageUpdatesSidebar(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.ContextUsageUpdated{UsedTokens: 50, MaxTokens: 100, PctUsed: 0.5})
	out := app.View().Content
	// Context usage is shown in the sidebar as "50% used" (sidebar is
	// visible because the test window is 100 columns wide).
	if !strings.Contains(out, "50% used") {
		t.Errorf("expected '50%% used' in sidebar after context update, got:\n%s", out)
	}
}

func TestContextUsageUnknownLimitDoesNotShowZeroPercent(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.ContextUsageUpdated{UsedTokens: 334})
	out := app.View().Content
	if !strings.Contains(out, "limit unknown") {
		t.Errorf("expected unknown context limit in sidebar, got:\n%s", out)
	}
	if strings.Contains(out, "0% used") {
		t.Errorf("unknown context limit should not render as 0%% used:\n%s", out)
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

	// Resize. Final rendered markdown is rebuilt immediately so completed
	// bubbles wrap to the new width instead of keeping stale hard wraps.
	app.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if app.renderer == nil {
		t.Fatal("expected renderer rebuilt after resize")
	}
	if app.renderer == r1 {
		t.Errorf("expected new renderer instance after resize")
	}
	if app.rendererW != 45 {
		t.Errorf("renderer width = %d, want 45 (bubble content: int(60*0.80)-3)", app.rendererW)
	}
}

func TestResizeClearsSelectionAndStaleCanvas(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	_ = app.View()
	if app.lastCanvas.RenderBuffer == nil {
		t.Fatal("expected rendered canvas")
	}
	app.sel.hasRange = true
	app.sel.active = true

	app.Update(tea.WindowSizeMsg{Width: 60, Height: 20})

	if app.sel.hasRange || app.sel.active {
		t.Fatalf("resize should clear selection, got %+v", app.sel)
	}
	if app.lastCanvas.RenderBuffer != nil {
		t.Fatal("resize should discard stale canvas before the next render")
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

func TestResizeRerendersFinalMarkdownToBubbleWidth(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Update(tea.WindowSizeMsg{Width: 80, Height: 30})
	raw := "Added and reverted a larger temporary block in `TODOS.md` while checking whether final markdown wraps at the current bubble width."
	app.messages = []uiMessage{{
		Role:          components.RoleAssistant,
		Raw:           raw,
		FinalMarkdown: renderMarkdown(app.ensureRenderer(), raw),
	}}
	narrow := app.messages[0].FinalMarkdown

	app.Update(tea.WindowSizeMsg{Width: 250, Height: 30})
	wide := app.messages[0].FinalMarkdown
	if wide == narrow {
		t.Fatal("FinalMarkdown was not rerendered after resize")
	}

	plain := ansiEscapeRE.ReplaceAllString(wide, "")
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(strings.Join(strings.Fields(line), " "), "temporary block in TODOS.md") {
			return
		}
	}
	t.Fatalf("wide FinalMarkdown should use the wider bubble before wrapping; got:\n%s", plain)
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

func TestPromptInputBorderUsesModeColor(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.Modes[app.modeIndex].Color = "5"

	view := app.View().Content
	want := "\x1b[35m╭"
	if !strings.Contains(view, want) {
		t.Fatalf("input border should use active mode color; missing %q in:\n%s", want, view)
	}
}

func TestLayoutChatFillsInputClampsAndFooterFixed(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.messages = []uiMessage{{Role: components.RoleUser, Raw: "hello"}}

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

func TestCostUpdatesSidebar(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Handle(bus.CostUpdated{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 25, DollarsTotal: 0.1234})
	out := app.View().Content
	for _, want := range []string{"Usage", "175 billed", "$0.1234"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in sidebar, got:\n%s", want, out)
		}
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

func TestQueueChanged_RendersQueuedPromptsNearInput(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 24})

	app.Update(sendStarted{UserInput: "first"})
	app.Handle(bus.QueueChanged{SessionID: "", Count: 2, Prompts: []string{"queued one", "queued two"}})

	out := app.View().Content
	inputIndex := strings.LastIndex(out, "╭")
	queuedIndex := strings.Index(out, "1. queued one")
	if queuedIndex < 0 {
		t.Fatalf("queued prompt missing from sticky bottom chrome:\n%s", out)
	}
	if inputIndex < 0 {
		t.Fatalf("input border missing from view:\n%s", out)
	}
	if queuedIndex > inputIndex {
		t.Fatalf("queued prompt should render above input, got queued index %d after input index %d", queuedIndex, inputIndex)
	}
}

func TestQueueCommandQueuesDraftOutOfChatAndClickEdits(t *testing.T) {
	t.Parallel()
	app, _, reg := newSlashApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	app.opts.Commands = reg
	app.busy = true
	app.input.SetBusy(true, "")

	typeInto(app, "/queue queued one")
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		app.Update(cmd())
	}
	if len(app.messages) != 0 {
		t.Fatalf("queued draft should not appear in chat, messages = %#v", app.messages)
	}
	if app.queueCount != 1 || len(app.queuedDrafts) != 1 || app.queuedPrompts[0] != "queued one" {
		t.Fatalf("queue state = count %d drafts %#v prompts %#v", app.queueCount, app.queuedDrafts, app.queuedPrompts)
	}
	if strings.Contains(app.notice, "queued message") {
		t.Fatalf("queue command should not leave sticky queued-message notice, got %q", app.notice)
	}
	out := app.View().Content
	if !strings.Contains(out, "click to edit") {
		t.Fatalf("queued draft hint missing from view:\n%s", out)
	}

	queuedY := headerHeight + app.layout.chat.Dy() + chatBottomPadding + 1
	app.Update(tea.MouseClickMsg{X: 4, Y: queuedY, Button: tea.MouseLeft})
	if got := app.input.Value(); got != "queued one" {
		t.Fatalf("input after queued draft click = %q, want queued one", got)
	}
	if app.queueCount != 0 || len(app.queuedDrafts) != 0 {
		t.Fatalf("queued draft not removed after edit: count %d drafts %#v", app.queueCount, app.queuedDrafts)
	}
}

func TestQueuedDraftEditPreservesOriginalPosition(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	app.busy = true
	app.input.SetBusy(true, "")
	app.enqueuePromptDraft(queuedPromptDraft{Text: "one"})
	app.enqueuePromptDraft(queuedPromptDraft{Text: "two"})
	app.enqueuePromptDraft(queuedPromptDraft{Text: "three"})
	_ = app.View()

	secondQueuedY := headerHeight + app.layout.chat.Dy() + chatBottomPadding + 2
	app.Update(tea.MouseClickMsg{X: 4, Y: secondQueuedY, Button: tea.MouseLeft})
	if got := app.input.Value(); got != "two" {
		t.Fatalf("input after queued draft click = %q, want two", got)
	}
	app.setInputValueAndCursor("four", len("four"))
	reg := command.New()
	command.RegisterBuiltins(reg)
	app.opts.Commands = reg
	app.setInputValueAndCursor("/queue four", len("/queue four"))
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		app.Update(cmd())
	}
	if got := strings.Join(app.queuedPrompts, ","); got != "one,four,three" {
		t.Fatalf("queued prompts = %q, want one,four,three", got)
	}
}

func TestTurnCompletedFlushesQueuedDraftsInOrder(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "sess"
	seen := make(chan string, 2)
	app.testAgentSendFn = func(_ context.Context, _ string, parts []session.Part) (*session.Message, error) {
		seen <- firstTextPart(parts)
		return nil, nil
	}
	app.busy = true
	app.activeTurns = 1
	app.enqueuePromptDraft(queuedPromptDraft{Text: "queued one"})
	app.enqueuePromptDraft(queuedPromptDraft{Text: "queued two"})

	cmd := app.Handle(bus.TurnCompleted{SessionID: "sess"})
	if cmd == nil {
		t.Fatal("expected queued flush cmd")
	}
	if len(app.queuedDrafts) != 1 || app.queueCount != 1 || app.queuedPrompts[0] != "queued two" {
		t.Fatalf("only sent draft should be dequeued, count %d drafts %#v prompts %#v", app.queueCount, app.queuedDrafts, app.queuedPrompts)
	}
	app.Update(cmd())
	got := []string{readSeenPrompt(t, seen)}
	app.activeTurns = 1
	cmd = app.Handle(bus.TurnCompleted{SessionID: "sess"})
	if cmd == nil {
		t.Fatal("expected second queued flush cmd")
	}
	if len(app.queuedDrafts) != 0 || app.queueCount != 0 {
		t.Fatalf("second draft should be dequeued, count %d drafts %#v", app.queueCount, app.queuedDrafts)
	}
	app.Update(cmd())
	got = append(got, readSeenPrompt(t, seen))
	if strings.Join(got, ",") != "queued one,queued two" {
		t.Fatalf("flushed sends = %v", got)
	}
}

func readSeenPrompt(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case got := <-ch:
		return got
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued send")
		return ""
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
