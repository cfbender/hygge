package components

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestMessageListRendersRoles(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "hello"},
			{Role: RoleAssistant, Raw: "hi back", AgentType: "General"},
			{Role: RoleTool, ToolName: "read", Target: "/tmp/x", Raw: "line1\nline2"},
		},
	}
	out := ml.View()
	// User and assistant render as bubbles; content must still appear.
	// Non-task tool calls now render as a tool-group bubble (no "▌tool: read" gutter).
	for _, want := range []string{"hello", "General", "hi back", "Read", "/tmp/x"} {
		if !strings.Contains(out, want) {
			t.Errorf("messagelist missing %q in:\n%s", want, out)
		}
	}
	// The old gutter format must not appear for non-task tools.
	if strings.Contains(out, "▌tool: read") {
		t.Errorf("messagelist should not render '▌tool: read' gutter for non-task tool; got:\n%s", out)
	}
}

func TestMessageListCompactionMarkerStaysWithinWidth(t *testing.T) {
	t.Parallel()
	const width = 44
	ml := MessageList{
		Width: width,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{{
			Role:              RoleMarker,
			MarkerTokensSaved: 1234,
			MarkerSummary:     strings.Repeat("This compacted summary should wrap inside the marker box. ", 5),
		}},
	}
	out := ml.View()
	for line := range strings.SplitSeq(out, "\n") {
		if got := lipgloss.Width(line); got > width {
			t.Fatalf("marker line width = %d, want <= %d\nline: %q\noutput:\n%s", got, width, line, out)
		}
	}
}

func TestMentionTokensHighlightedInInputAndUserBubble(t *testing.T) {
	t.Parallel()
	th := theme.ShellTheme()
	accentMention := th.Style(theme.AtomAccent).Render("@internal/ui/app.go")

	in := NewInput(th)
	in.Textarea.SetValue("read @internal/ui/app.go")
	if out := in.View(); !strings.Contains(out, accentMention) {
		t.Fatalf("input mention was not highlighted; want %q in:\n%s", accentMention, out)
	}

	ml := MessageList{
		Width: 100,
		Theme: th,
		Messages: []UIMessage{{
			Role: RoleUser,
			Raw:  "read @internal/ui/app.go",
		}},
	}
	if out := ml.View(); !strings.Contains(out, accentMention) {
		t.Fatalf("user bubble mention was not highlighted; want %q in:\n%s", accentMention, out)
	}
}

func TestPastedInputMarkerHighlightIgnoresCursorANSI(t *testing.T) {
	t.Parallel()
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("5"))
	in := "\x1b[7m[Pasted \x1b[0m3 lines]"
	want := style.Render("[Pasted 3 lines]")
	if out := HighlightPastedInputMarkers(in, style); !strings.Contains(out, want) {
		t.Fatalf("paste marker was not highlighted across ANSI cursor styling; want %q in %q", want, out)
	}
}

func TestThemeModalFilterNavigateApplyCancel(t *testing.T) {
	t.Parallel()
	m := ThemeModal{Current: "shell", Themes: []string{"shell", "midnight", "solarized"}}
	updated, msg := m.HandleKey(ThemeKey{Name: "m", Runes: []rune("m")})
	if msg != nil {
		t.Fatalf("typing emitted message: %#v", msg)
	}
	if got := updated.Filtered(); len(got) != 1 || got[0] != "midnight" {
		t.Fatalf("filtered = %#v", got)
	}
	_, msg = updated.HandleKey(ThemeKey{Name: "enter"})
	if sel, ok := msg.(SelectThemeAction); !ok || sel.Name != "midnight" {
		t.Fatalf("enter msg = %#v", msg)
	}
	_, msg = m.HandleKey(ThemeKey{Name: "esc"})
	if _, ok := msg.(CloseThemeModal); !ok {
		t.Fatalf("esc msg = %#v", msg)
	}
}

func TestMessageListCollapsesLongToolOutput(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := range 20 {
		lines = append(lines, "line"+itoa(i))
	}
	ml := MessageList{
		Width:         80,
		CollapseLines: 5,
		Theme:         theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "somefile.go", Raw: strings.Join(lines, "\n")},
		},
	}
	out := ml.View()
	// Non-task tool calls render as a tool-group bubble showing name+target only.
	// The raw body is not shown; no collapse hint is emitted.
	if !strings.Contains(out, "Read") {
		t.Errorf("expected tool name 'Read' in output:\n%s", out)
	}
	if !strings.Contains(out, "somefile.go") {
		t.Errorf("expected target 'somefile.go' in output:\n%s", out)
	}
	// Non-bash tools don't show output inline.
	if strings.Contains(out, "line0") {
		t.Errorf("read tool should not show output inline; got:\n%s", out)
	}
}

func TestMessageListEmpty(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	out := ml.View()
	if out != "" {
		t.Errorf("empty message list should render blank; got:\n%s", out)
	}
	// Old placeholders must no longer appear; the App owns the splash screen.
	if strings.Contains(out, "no messages") {
		t.Errorf("empty state should not contain old placeholder text; got:\n%s", out)
	}
}

func TestMessageListEmptyState_NoMessagesStaysBlank(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	out := ml.View()
	for _, unexpected := range []string{"│h│", "Type a message", "ctrl+e", "tab"} {
		if strings.Contains(out, unexpected) {
			t.Errorf("message-list empty state should not contain %q; got:\n%s", unexpected, out)
		}
	}
}

// TestMessageListEmptyState_DisappearsWithMessage verifies the empty state is
// not rendered once at least one message is present.
func TestMessageListEmptyState_DisappearsWithMessage(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "hello"},
		},
	}
	out := ml.View()
	if strings.Contains(out, "│h│ │y│ │g│ │g│ │e│") {
		t.Errorf("welcome glyph must not appear when messages are present; got:\n%s", out)
	}
	if strings.Contains(out, "Type a message to get started") {
		t.Errorf("empty-state hint must not appear when messages are present; got:\n%s", out)
	}
}

// TestThinkingTruncation verifies that thinking text longer than 8 lines is
// capped with a "… +N more lines (thinking)" indicator.
func TestThinkingTruncation(t *testing.T) {
	t.Parallel()
	// Build 12 lines of thinking.
	var thinkLines []string
	for i := range 12 {
		thinkLines = append(thinkLines, "thought line "+itoa(i))
	}
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	result := ml.truncateThinking(strings.Join(thinkLines, "\n"))
	plain := stripANSI(result)

	// First 7 lines (thinkingMaxLines-1=7) must appear.
	for i := range 7 {
		if !strings.Contains(plain, "thought line "+itoa(i)) {
			t.Errorf("expected line %d to be visible in truncated thinking: %q", i, plain)
		}
	}
	// The indicator must appear.
	if !strings.Contains(plain, "more lines (thinking)") {
		t.Errorf("expected truncation indicator in thinking output; got: %q", plain)
	}
	// The overflow count must be correct: 12 total - 7 visible = 5 overflow.
	if !strings.Contains(plain, "+5 more lines (thinking)") {
		t.Errorf("expected '+5 more lines (thinking)' indicator; got: %q", plain)
	}
}

// TestThinkingTruncation_NoIndicatorWhenFits verifies that short thinking is
// rendered in full without a truncation indicator.
func TestThinkingTruncation_NoIndicatorWhenFits(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	thinking := "line1\nline2\nline3"
	result := ml.truncateThinking(thinking)
	plain := stripANSI(result)
	if strings.Contains(plain, "more lines (thinking)") {
		t.Errorf("expected no indicator when thinking fits within max lines; got: %q", plain)
	}
	if !strings.Contains(plain, "line3") {
		t.Errorf("expected all lines visible when thinking fits; got: %q", plain)
	}
}

// TestThinkingTruncation_ExactlyAtMaxNoIndicator verifies that exactly
// thinkingMaxLines lines is NOT truncated.
func TestThinkingTruncation_ExactlyAtMaxNoIndicator(t *testing.T) {
	t.Parallel()
	var lines []string
	for range thinkingMaxLines {
		lines = append(lines, "line")
	}
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	result := ml.truncateThinking(strings.Join(lines, "\n"))
	plain := stripANSI(result)
	if strings.Contains(plain, "more lines (thinking)") {
		t.Errorf("expected no indicator at exactly thinkingMaxLines lines; got: %q", plain)
	}
}

// TestBubbleTail_UserBubble verifies user bubbles do NOT emit tail glyphs.
// Tails were dropped in the Phase 5 fix: they never rendered cleanly.
func TestBubbleTail_UserBubble(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "hello"},
		},
	}
	out := ml.View()
	plain := stripANSI(out)
	if strings.Contains(plain, "◢") || strings.Contains(plain, "◣") {
		t.Errorf("user bubble must NOT contain tail glyphs after Phase 5 fix; got:\n%s", plain)
	}
}

// TestBubbleTail_AssistantBubble verifies assistant bubbles do NOT emit tail glyphs.
// Tails were dropped in the Phase 5 fix: they never rendered cleanly.
func TestBubbleTail_AssistantBubble(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleAssistant, Raw: "hi there", AgentType: "General"},
		},
	}
	out := ml.View()
	plain := stripANSI(out)
	if strings.Contains(plain, "◢") || strings.Contains(plain, "◣") {
		t.Errorf("assistant bubble must NOT contain tail glyphs after Phase 5 fix; got:\n%s", plain)
	}
}

// TestBubbleTail_ToolGroupNoTail verifies tool-group bubbles have no tail glyphs.
func TestBubbleTail_ToolGroupNoTail(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "/tmp/x"},
		},
	}
	out := ml.View()
	plain := stripANSI(out)
	if strings.Contains(plain, "◢") || strings.Contains(plain, "◣") {
		t.Errorf("tool-group bubble must not contain tail glyphs; got:\n%s", plain)
	}
}

// TestInput_FocusedRendersText verifies that the input view renders and
// contains the typed text.
func TestInput_FocusedRendersText(t *testing.T) {
	t.Parallel()
	in := NewInput(theme.ShellTheme())
	in.Focused = true
	in.SetWidth(60)
	in.Textarea.SetValue("test input")
	view := in.View()
	if !strings.Contains(view, "test input") {
		t.Errorf("focused input view should contain typed text; got:\n%s", view)
	}
}

// TestInputBorder_BlurredAccepted verifies blurred input renders without panic.
func TestInputBorder_BlurredAccepted(t *testing.T) {
	t.Parallel()
	in := NewInput(theme.ShellTheme())
	in.Focused = false
	in.SetWidth(60)
	view := in.View()
	if view == "" {
		t.Errorf("blurred input view should not be empty")
	}
}

func TestStatusPillsRendersQueueCount(t *testing.T) {
	t.Parallel()
	out := StatusPills{Width: 60, Theme: theme.ShellTheme(), QueueCount: 2}.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "2 queued") {
		t.Errorf("status pills missing queue count; got:\n%s", plain)
	}
}

func TestStatusPillsRendersQueuedPrompts(t *testing.T) {
	t.Parallel()
	out := StatusPills{
		Width:          60,
		Theme:          theme.ShellTheme(),
		QueueCount:     4,
		QueuedPrompts:  []string{"first prompt", "second\nline", "third prompt", "fourth prompt"},
		QueuedEditable: true,
	}.View()
	plain := stripANSI(out)
	for _, want := range []string{"4 queued · click to edit", "1. first prompt", "2. second ↵ line", "3. third prompt", "… 1 more queued"} {
		if !strings.Contains(plain, want) {
			t.Errorf("queued prompt view missing %q; got:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "fourth prompt") {
		t.Errorf("queued prompt view should collapse overflow; got:\n%s", plain)
	}
}

func TestStatusPillsNoQueueEmpty(t *testing.T) {
	t.Parallel()
	out := StatusPills{Width: 60, Theme: theme.ShellTheme()}.View()
	if out != "" {
		t.Errorf("status pills should be empty without queue/todo state; got %q", out)
	}
}

func TestStatusPillsRendersTodoCount(t *testing.T) {
	t.Parallel()
	out := StatusPills{Width: 60, Theme: theme.ShellTheme(), TodoCount: 3}.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "3 todos") {
		t.Errorf("status pills missing todo count; got:\n%s", plain)
	}
}

func TestStatusPillsRendersRunningTodoSpinner(t *testing.T) {
	t.Parallel()
	out := StatusPills{Width: 60, Theme: theme.ShellTheme(), TodoCount: 1, TodoRunning: true}.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "◌ 1 todo") {
		t.Errorf("status pills missing running todo marker; got:\n%s", plain)
	}
}

// TestSubagentBlock_NoGutter verifies the │ gutter is absent from subagent block output.
func TestSubagentBlock_NoGutter(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Second)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "test task",
		StartedAt:    start,
		EndedAt:      end,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: end}.View()
	if strings.Contains(out, "\u2502") { // │
		t.Errorf("subagent block must not contain │ gutter; got:\n%s", out)
	}
	// Content must still be present.
	if !strings.Contains(out, "General Subagent") {
		t.Errorf("subagent block must still contain heading; got:\n%s", out)
	}
}

func TestFooterRendersIdentity(t *testing.T) {
	t.Parallel()
	f := Footer{
		Width:          80,
		Theme:          theme.ShellTheme(),
		AgentType:      "general",
		ModelName:      "Claude Opus 4.7",
		Provider:       "openrouter",
		ReasoningLevel: "medium",
	}
	out := f.View()
	for _, want := range []string{"General", "Claude Opus 4.7", "Openrouter", "medium"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q in:\n%s", want, out)
		}
	}
	// Should NOT contain old cost/ctx/keybind hints.
	for _, unwanted := range []string{"$", "ctx", "enter send", "ctrl-c"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("footer should not contain %q in:\n%s", unwanted, out)
		}
	}
}

func TestFooterNoSession(t *testing.T) {
	t.Parallel()
	f := Footer{
		Width: 80,
		Theme: theme.ShellTheme(),
	}
	out := f.View()
	if !strings.Contains(out, "(no session)") {
		t.Errorf("expected '(no session)' when AgentType is empty, got:\n%s", out)
	}
}

func TestFooterCapitalizesAgentAndProvider(t *testing.T) {
	t.Parallel()
	f := Footer{
		Width:     80,
		Theme:     theme.ShellTheme(),
		AgentType: "general",
		Provider:  "anthropic",
	}
	out := f.View()
	if !strings.Contains(out, "General") {
		t.Errorf("expected capitalized AgentType 'General', got:\n%s", out)
	}
	if !strings.Contains(out, "Anthropic") {
		t.Errorf("expected capitalized Provider 'Anthropic', got:\n%s", out)
	}
}

func TestPermissionModalRendersRequest(t *testing.T) {
	t.Parallel()
	m := PermissionModal{
		Width:  100,
		Height: 24,
		Theme:  theme.ShellTheme(),
		Request: PermissionRequest{
			RequestID: "req-1",
			ToolName:  "read",
			Category:  "file.read",
			Target:    "/Users/cfb/.aws/credentials",
			Why:       "needs to inspect config",
		},
	}
	out := m.View()
	for _, want := range []string{"permission request", "Tool:", "read", "Category:", "file.read", "Target:", "/Users/cfb/.aws/credentials", "[y]", "[Y]", "[A]", "[n]", "[e]", "needs to inspect config"} {
		if !strings.Contains(out, want) {
			t.Errorf("modal missing %q in:\n%s", want, out)
		}
	}
}

func TestPermissionModalRendersToast(t *testing.T) {
	t.Parallel()
	m := PermissionModal{
		Width: 80, Height: 20, Theme: theme.ShellTheme(),
		Request: PermissionRequest{ToolName: "write", Category: "file.write", Target: "/tmp"},
		Toast:   "edit not yet implemented",
	}
	out := m.View()
	if !strings.Contains(out, "edit not yet implemented") {
		t.Errorf("expected toast in modal, got:\n%s", out)
	}
}

func TestInputBuildsAndReports(t *testing.T) {
	t.Parallel()
	in := NewInput(theme.ShellTheme())
	in.SetWidth(40)
	in.Textarea.SetValue("hello world")
	if in.Value() != "hello world" {
		t.Errorf("Value: got %q, want %q", in.Value(), "hello world")
	}
	view := in.View()
	if !strings.Contains(view, "hello world") {
		t.Errorf("input view should contain text, got:\n%s", view)
	}
	in.Reset()
	if in.Value() != "" {
		t.Errorf("Reset: expected empty value, got %q", in.Value())
	}
}

// ── Phase 3: tool-call grouping and subagent bubble wrap ────────────────────

// TestToolGroupBubble_ThreeCallGroup verifies compact tools still group while
// bash renders as its own side-bar bubble.
func TestToolGroupBubble_ThreeCallGroup(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "/tmp/a.go"},
			{Role: RoleTool, ToolName: "grep", Target: "func Main"},
			{Role: RoleTool, ToolName: "bash", Target: "go build ."},
		},
	}
	out := ml.View()
	// All three tool names and targets must appear.
	for _, want := range []string{"Read", "/tmp/a.go", "Grep", "Bash", "go build ."} {
		if !strings.Contains(out, want) {
			t.Errorf("tool group missing %q in:\n%s", want, out)
		}
	}
	// The star prefix must appear.
	if !strings.Contains(out, "✱") {
		t.Errorf("tool group missing '✱' prefix in:\n%s", out)
	}
	// Old gutter format must be absent.
	if strings.Contains(out, "▌tool:") {
		t.Errorf("tool group must not contain '▌tool:' gutter; got:\n%s", out)
	}
	// read+grep are grouped, while bash renders as a standalone block.
	blocks := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(blocks) != 2 {
		t.Errorf("expected compact read+grep block plus standalone bash block, got %d blocks", len(blocks))
	}
}

func TestToolGroupBubble_BashEditWriteAreStandaloneBlocks(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "main.go"},
			{Role: RoleTool, ToolName: "bash", ToolUseID: "bash-1", Target: "go test ./...", Raw: "ok"},
			{Role: RoleTool, ToolName: "edit", ToolUseID: "edit-1", Target: "main.go"},
			{Role: RoleTool, ToolName: "write", ToolUseID: "write-1", Target: "main.go"},
			{Role: RoleTool, ToolName: "grep", Target: "TODO"},
		},
	}

	chunks := ml.buildChunks()
	if len(chunks) != 5 {
		t.Fatalf("expected read, bash, edit, write, grep to render as 5 blocks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		if chunk.kind != chunkToolGroup || len(chunk.group) != 1 {
			t.Fatalf("chunk %d = kind %v len %d, want single tool-group block", i, chunk.kind, len(chunk.group))
		}
	}
}

func TestToolGroupBubble_StandaloneBashHitZonesDoNotShareCombinedBlock(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "main.go"},
			{Role: RoleTool, ToolName: "bash", ToolUseID: "bash-1", Target: "go test ./...", Raw: "line 1\nline 2\nline 3\nline 4\nline 5"},
			{Role: RoleTool, ToolName: "grep", Target: "TODO"},
			{Role: RoleTool, ToolName: "bash", ToolUseID: "bash-2", Target: "go vet ./...", Raw: "ok"},
		},
	}

	_, _, zones := ml.ViewWithHitZones()
	if len(zones) != 2 {
		t.Fatalf("expected one hit zone per standalone bash block, got %d", len(zones))
	}
	if zones[0].ToolUseID != "bash-1" || zones[1].ToolUseID != "bash-2" {
		t.Fatalf("unexpected bash hit zones: %+v", zones)
	}
	if zones[0].StartLine == zones[1].StartLine || zones[0].EndLine >= zones[1].StartLine {
		t.Fatalf("bash hit zones should be distinct non-overlapping blocks: %+v", zones)
	}
}

// TestToolGroupBubble_SingleCall verifies a lone non-task tool call also
// produces a tool-group bubble (group of one).
func TestToolGroupBubble_SingleCall(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "/etc/hosts"},
		},
	}
	out := ml.View()
	if !strings.Contains(out, "Read") {
		t.Errorf("single-call group missing 'Read' in:\n%s", out)
	}
	if !strings.Contains(out, "/etc/hosts") {
		t.Errorf("single-call group missing target in:\n%s", out)
	}
	if strings.Contains(out, "▌tool:") {
		t.Errorf("single-call group must not use old gutter format; got:\n%s", out)
	}
}

func TestToolGroupBubble_SkillShowsLoadedName(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "skill", ToolArgs: []byte(`{"name":"diagnose"}`)},
		},
	}
	out := stripANSI(ml.View())
	for _, want := range []string{"✱ Skill", "diagnose"} {
		if !strings.Contains(out, want) {
			t.Errorf("skill tool block missing %q in:\n%s", want, out)
		}
	}
}

func TestToolGroupBubble_HasTopPaddingInsideBlock(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "/etc/hosts"},
		},
	}
	out := stripANSI(ml.View())
	lines := strings.Split(out, "\n")
	readLine := -1
	for i, line := range lines {
		if strings.Contains(line, "✱ Read") {
			readLine = i
			break
		}
	}
	if readLine < 0 {
		t.Fatalf("tool block missing Read label in:\n%s", out)
	}
	if readLine == 0 {
		t.Fatalf("tool label rendered on first row without top padding:\n%s", out)
	}
	paddingLine := strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(lines[readLine-1], " "), "▌"))
	if paddingLine != "" {
		t.Errorf("line above tool label should be empty bubble padding, got %q in:\n%s", lines[readLine-1], out)
	}
}

// TestToolGroupBubble_ErrorToolStyling verifies that a tool with IsError set
// renders the "— error" suffix.
func TestToolGroupBubble_ErrorToolStyling(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "bash", Target: "make test", IsError: true},
		},
	}
	out := ml.View()
	if !strings.Contains(out, "Bash") {
		t.Errorf("error-tool group missing 'Bash' in:\n%s", out)
	}
	if !strings.Contains(out, "error") {
		t.Errorf("error-tool group missing 'error' suffix in:\n%s", out)
	}
}

// TestToolGroupBubble_InterleavedWithTask verifies mixed sequences produce
// three separate bubbles in chronological order:
//
//	[tool group: read+grep] [subagent: task] [tool group: bash]
func TestToolGroupBubble_InterleavedWithTask(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "deploy",
		StartedAt:    now.Add(-time.Second),
		EndedAt:      now,
		Messages:     []UIMessage{{Role: RoleTool, ToolName: "bash"}},
	}
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Now:   now,
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "main.go"},
			{Role: RoleTool, ToolName: "grep", Target: "TODO"},
			{Role: RoleTool, ToolName: "subagent", Target: "deploy", SubagentID: "sub-1"},
			{Role: RoleTool, ToolName: "bash", Target: "go vet ./..."},
		},
		Subagents: map[string]*SubagentState{"sub-1": st},
	}
	out := ml.View()

	// All content must appear.
	for _, want := range []string{"Read", "main.go", "Grep", "General Subagent", "deploy", "Bash", "go vet ./..."} {
		if !strings.Contains(out, want) {
			t.Errorf("interleaved output missing %q in:\n%s", want, out)
		}
	}

	// Chronological order: read/grep before task, task before bash.
	readIdx := strings.Index(out, "read")
	taskIdx := strings.Index(out, "General Subagent")
	bashIdx := strings.LastIndex(out, "go vet")
	if readIdx >= taskIdx || taskIdx >= bashIdx {
		t.Errorf("chronological order violated: readIdx=%d taskIdx=%d bashIdx=%d", readIdx, taskIdx, bashIdx)
	}
}

// TestSubagentBubbleWrap verifies that a task tool call with a bound SubagentID
// renders the compact subagent block content wrapped in a side-bar bubble
// (no bare "▌tool: task" gutter line).
func TestSubagentBubbleWrap(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "find LICENSE",
		StartedAt:    now.Add(-time.Second),
		EndedAt:      now,
		Cost:         0.001,
		Messages:     []UIMessage{{Role: RoleTool, ToolName: "grep"}},
	}
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Now:   now,
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "subagent", Target: "find LICENSE", SubagentID: "sub-1"},
		},
		Subagents: map[string]*SubagentState{"sub-1": st},
	}
	out := ml.View()
	// Subagent content must appear (from SubagentBlock.View() inside the bubble).
	for _, want := range []string{"General Subagent", "find LICENSE", "ctrl+g"} {
		if !strings.Contains(out, want) {
			t.Errorf("subagent bubble missing %q in:\n%s", want, out)
		}
	}
	// Old gutter must not appear.
	if strings.Contains(out, "▌tool: task") {
		t.Errorf("subagent bubble must not contain '▌tool: task' gutter; got:\n%s", out)
	}
}

// stripANSI is a naive ANSI escape stripper for test assertions.
// Removes ESC[...m sequences so tests can check plain text.
func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // consume 'm'
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// ── Sidebar tests ────────────────────────────────────────────────────────────

func TestSidebar_AllSectionsPresent(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:        32,
		Height:       30,
		SessionTitle: "fix the auth bug",
		UsedTokens:   97229,
		MaxTokens:    1_000_000,
		PctUsed:      0.10,
		CostUSD:      0.0042,
		BilledTokens: 123456,
		MCPs: []SidebarMCPStatus{
			{Name: "server-a", Ready: true, ToolCount: 3},
			{Name: "server-b", Ready: false},
		},
		ProjectPath: "~/code/hygge",
		GitBranch:   "main",
		AppName:     "Hygge",
		Version:     "v0.1.0-dev",
		Theme:       theme.ShellTheme(),
		NerdFonts:   false,
	}
	out := sb.View()
	plain := stripANSI(out)

	// Session title.
	if !strings.Contains(plain, "fix the auth bug") {
		t.Errorf("sidebar missing session title; got:\n%s", plain)
	}
	// Usage and context sections.
	for _, want := range []string{"Usage", "123,456 billed", "$0.0042", "Context", "97,229 tokens", "10% used"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar missing usage/context %q; got:\n%s", want, plain)
		}
	}
	// MCPs section.
	for _, want := range []string{"MCPs", "server-a", "server-b"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar missing MCP %q; got:\n%s", want, plain)
		}
	}
	// Todos section.
	if !strings.Contains(plain, "Todos") {
		t.Errorf("sidebar missing 'Todos' header; got:\n%s", plain)
	}
	// Bottom block.
	for _, want := range []string{"~/code/hygge", ":main", "Hygge", "v0.1.0-dev"} {
		if !strings.Contains(plain, want) {
			t.Errorf("sidebar missing bottom %q; got:\n%s", want, plain)
		}
	}
}

func TestSidebar_BackgroundFill_ReassertedAfterStyledFragments(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:        32,
		Height:       12,
		SessionTitle: "styled title",
		MCPs:         []SidebarMCPStatus{{Name: "server-a", Ready: true, ToolCount: 3}},
		Theme:        theme.ShellTheme(),
	}
	out := sb.View()
	bgOpen := sidebarBackgroundOpenSequence(lipgloss.Color("235"))
	if !strings.Contains(out, bgOpen) {
		t.Fatalf("sidebar should emit themed background fill.\nOutput: %q", out)
	}
	if !strings.Contains(out, "\x1b[m"+bgOpen) {
		t.Errorf("sidebar background should be reasserted after nested ANSI resets.\nOutput: %q", out)
	}
}

func TestSidebar_NoMCPs_ShowsNone(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  32,
		Height: 20,
		Theme:  theme.ShellTheme(),
	}
	out := stripANSI(sb.View())
	if !strings.Contains(out, "None") {
		t.Errorf("sidebar should show 'None' when MCPs is empty; got:\n%s", out)
	}
}

func TestSidebar_HidesUsageAndContextWhenZero(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  32,
		Height: 20,
		Theme:  theme.ShellTheme(),
	}
	out := stripANSI(sb.View())
	if strings.Contains(out, "Context") || strings.Contains(out, "Usage") {
		t.Errorf("sidebar should not show Usage/Context sections when usage is zero; got:\n%s", out)
	}
}

func TestSidebar_HidesSessionTitleWhenEmpty(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:        32,
		Height:       20,
		SessionTitle: "",
		Theme:        theme.ShellTheme(),
	}
	out := sb.View()
	// Just verify no panic and output is non-empty.
	if out == "" {
		t.Errorf("sidebar should produce non-empty output even with no session title")
	}
}

func TestSidebar_NilTheme_NoCrash(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:   32,
		Height:  20,
		Theme:   nil,
		AppName: "Hygge",
		Version: "v0.1.0-dev",
	}
	out := sb.View()
	if out == "" {
		t.Errorf("sidebar should render with nil theme")
	}
}

func TestSidebar_MCPStatus_Markers(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  32,
		Height: 20,
		MCPs: []SidebarMCPStatus{
			{Name: "ok", Ready: true},
			{Name: "not-ready", Ready: false},
			{Name: "broken", Error: "connection refused"},
		},
		Theme: theme.ShellTheme(),
	}
	out := stripANSI(sb.View())
	for _, name := range []string{"ok", "not-ready", "broken"} {
		if !strings.Contains(out, name) {
			t.Errorf("sidebar missing MCP name %q; got:\n%s", name, out)
		}
	}
}

func TestFormatTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{97229, "97,229"},
		{1000000, "1,000,000"},
	}
	for _, tc := range cases {
		got := formatTokens(tc.n)
		if got != tc.want {
			t.Errorf("formatTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestSidebarTodos_Empty(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  32,
		Height: 30,
		Theme:  theme.ShellTheme(),
	}
	out := stripANSI(sb.View())
	if !strings.Contains(out, "Todos") {
		t.Errorf("sidebar missing 'Todos' section header; got:\n%s", out)
	}
	if !strings.Contains(out, "—") {
		t.Errorf("sidebar should show '—' when no todos; got:\n%s", out)
	}
	// Old Modified Files header must be gone.
	if strings.Contains(out, "Modified Files") {
		t.Errorf("sidebar still shows old 'Modified Files' header; got:\n%s", out)
	}
}

func TestSidebarTodos_WithItems(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  40,
		Height: 35,
		Theme:  theme.ShellTheme(),
		Todos: []SidebarTodo{
			{Title: "Investigate config merge warning", Status: SidebarTodoCompleted},
			{Title: "Replace sidebar with todos", Status: SidebarTodoInProgress},
			{Title: "Write tests", Status: SidebarTodoPending},
			{Title: "Drop modified files plumbing", Status: SidebarTodoCancelled},
		},
	}
	out := stripANSI(sb.View())
	for _, want := range []string{
		"Todos",
		"✓", "→", "○", "✕",
		"Investigate config merge warning",
		"Write tests",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing %q in todos section; got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "—") {
		t.Errorf("sidebar should not show '—' fallback when todos are present; got:\n%s", out)
	}
}

func TestSidebarTodos_WrapsLongTitles(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  22,
		Height: 35,
		Theme:  theme.ShellTheme(),
		Todos: []SidebarTodo{
			{Title: "Implement sidebar todo wrapping without truncating details", Status: SidebarTodoInProgress},
		},
	}
	out := stripANSI(sb.View())
	if strings.Contains(out, "…") {
		t.Errorf("sidebar should wrap long todo title instead of truncating with ellipsis; got:\n%s", out)
	}
	for _, want := range []string{"→ Implement sidebar", "  todo wrapping", "  without", "  truncating", "  details"} {
		if !strings.Contains(out, want) {
			t.Errorf("sidebar missing wrapped todo fragment %q; got:\n%s", want, out)
		}
	}
}

func TestSidebarTodos_TruncatesAt6(t *testing.T) {
	t.Parallel()
	var items []SidebarTodo
	for i := range 9 {
		items = append(items, SidebarTodo{
			Title:  fmt.Sprintf("item %d", i+1),
			Status: SidebarTodoPending,
		})
	}
	sb := Sidebar{
		Width:  40,
		Height: 40,
		Theme:  theme.ShellTheme(),
		Todos:  items,
	}
	out := stripANSI(sb.View())
	if !strings.Contains(out, "+3 more") {
		t.Errorf("sidebar should show '+3 more' overflow; got:\n%s", out)
	}
	if !strings.Contains(out, "item 1") {
		t.Errorf("sidebar should show first item; got:\n%s", out)
	}
	if strings.Contains(out, "item 9") {
		t.Errorf("sidebar should not show overflow item 9; got:\n%s", out)
	}
}

// ── Tool-group inline status rendering ───────────────────────────────────────

// TestToolGroup_RendersAwaitingPermissionText verifies that a tool row with
// Status==ToolStatusAwaitingPermission renders "Requesting permission…" inline.
func TestToolGroup_RendersAwaitingPermissionText(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "write",
				Target:   "/etc/hosts",
				Status:   ToolStatusAwaitingPermission,
			},
		},
	}
	out := stripANSI(ml.View())
	if !strings.Contains(out, "Requesting permission") {
		t.Errorf("expected 'Requesting permission…' in tool row; got:\n%s", out)
	}
}

// TestToolGroup_RendersWaitingForResponseText verifies that a tool row with
// Status==ToolStatusRunning renders "Waiting for tool response…" inline.
func TestToolGroup_RendersWaitingForResponseText(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "bash",
				Target:   "go test ./...",
				Status:   ToolStatusRunning,
			},
		},
	}
	out := stripANSI(ml.View())
	if !strings.Contains(out, "Waiting for tool response") {
		t.Errorf("expected 'Waiting for tool response…' in tool row; got:\n%s", out)
	}
}

// TestToolGroup_NoStatusTextWhenCompleted verifies that completed tool rows
// render without any status text.
func TestToolGroup_NoStatusTextWhenCompleted(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "read",
				Target:   "/tmp/file.txt",
				Status:   ToolStatusCompleted,
			},
		},
	}
	out := stripANSI(ml.View())
	for _, unwanted := range []string{"Requesting permission", "Waiting for tool response", "cancelled", "error"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("completed tool row must not contain %q; got:\n%s", unwanted, out)
		}
	}
	// Tool name and target must still appear.
	if !strings.Contains(out, "Read") {
		t.Errorf("completed tool row must still show tool name; got:\n%s", out)
	}
}

// TestToolGroup_ErrorText verifies that a tool row with Status==ToolStatusError
// renders "error" inline.
func TestToolGroup_ErrorText(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "edit",
				Target:   "foo.go",
				Status:   ToolStatusError,
				IsError:  true,
			},
		},
	}
	out := stripANSI(ml.View())
	if !strings.Contains(out, "error") {
		t.Errorf("error tool row must contain 'error'; got:\n%s", out)
	}
}

// TestToolGroup_CancelledText verifies that a tool row with
// Status==ToolStatusCancelled renders "cancelled" inline.
func TestToolGroup_CancelledText(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "bash",
				Target:   "rm -rf /",
				Status:   ToolStatusCancelled,
			},
		},
	}
	out := stripANSI(ml.View())
	if !strings.Contains(out, "cancelled") {
		t.Errorf("cancelled tool row must contain 'cancelled'; got:\n%s", out)
	}
}

func TestDiffView_StylesUnifiedDiff(t *testing.T) {
	t.Parallel()
	out := DiffView{
		Width: 80,
		Theme: theme.ShellTheme(),
		Raw:   "--- a/main.go\n+++ b/main.go\n@@ -12,1 +12,1 @@\n-old\n+new",
	}.View()
	plain := stripANSI(out)
	for _, want := range []string{"--- a/main.go", "+++ b/main.go", "@@ -12,1 +12,1 @@", "12 │ -old", "12 │ +new"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("diff view missing %q:\n%s", want, plain)
		}
	}
	assertNoDiffConnectorGlyphs(t, plain)
	if out == plain {
		t.Fatalf("diff view should style diff lines, got plain output:\n%s", out)
	}
}

func TestDiffView_SideBySideSeparatesInsertionsAndDeletions(t *testing.T) {
	t.Parallel()
	out := DiffView{
		Width: 96,
		Theme: theme.ShellTheme(),
		Raw:   "@@ -1,3 +1,3 @@\n keep\n-delete\n+insert\n+added",
	}.View()
	plain := stripANSI(out)
	for _, want := range []string{"1 │  keep", "2 │ -delete", "2 │ +insert", "3 │ +added"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("side-by-side diff missing %q:\n%s", want, plain)
		}
	}
	assertNoDiffConnectorGlyphs(t, plain)
	if strings.Contains(plain, "2 │    │ -delete") {
		t.Fatalf("side-by-side diff should not use inline old/new gutters:\n%s", plain)
	}
}

func TestDiffView_SideBySidePairsContiguousReplacementRuns(t *testing.T) {
	t.Parallel()
	plain := stripANSI(DiffView{
		Width: 104,
		Theme: theme.ShellTheme(),
		Raw:   "@@ -10,3 +10,4 @@\n-old one\n-old two\n+new one\n+new two\n+new three",
	}.View())

	assertDiffLineContains(t, plain, "-old one", "+new one")
	assertDiffLineContains(t, plain, "-old two", "+new two")
	if line := diffLineContaining(plain, "+new three"); line == "" || strings.Contains(line, "-old") {
		t.Fatalf("extra addition should render without a paired deletion, got line:\n%s\nfull diff:\n%s", line, plain)
	}
	assertNoDiffConnectorGlyphs(t, plain)
	if line := diffLineContaining(plain, "-old two"); strings.Contains(line, "+new one") {
		t.Fatalf("second deletion should pair with second addition, got line:\n%s\nfull diff:\n%s", line, plain)
	}
}

func TestDiffView_SideBySideUsesNewLineNumbersOnRightContextPane(t *testing.T) {
	t.Parallel()
	plain := stripANSI(DiffView{
		Width: 96,
		Theme: theme.ShellTheme(),
		Raw:   "@@ -1,2 +1,3 @@\n keep\n+inserted\n next",
	}.View())

	assertDiffLineContains(t, plain, "2 │  next", "3 │  next")
	assertNoDiffConnectorGlyphs(t, plain)
	if line := diffLineContaining(plain, " next"); strings.Count(line, "2 │  next") > 1 {
		t.Fatalf("right context pane should use new line number after insertion, got line:\n%s\nfull diff:\n%s", line, plain)
	}
}

func assertNoDiffConnectorGlyphs(t *testing.T, diff string) {
	t.Helper()
	for _, glyph := range []string{"╰", "╮", "──"} {
		if strings.Contains(diff, glyph) {
			t.Fatalf("diff should not contain connector glyph %q:\n%s", glyph, diff)
		}
	}
}

func assertDiffLineContains(t *testing.T, diff, first, second string) {
	t.Helper()
	line := diffLineContaining(diff, first)
	if line == "" || !strings.Contains(line, second) {
		t.Fatalf("diff line containing %q should also contain %q; line=%q\nfull diff:\n%s", first, second, line, diff)
	}
}

func diffLineContaining(diff, text string) string {
	for line := range strings.SplitSeq(diff, "\n") {
		if strings.Contains(line, text) {
			return line
		}
	}
	return ""
}

func TestDiffView_NarrowWidthKeepsInlineFallback(t *testing.T) {
	t.Parallel()
	plain := stripANSI(DiffView{
		Width: 60,
		Theme: theme.ShellTheme(),
		Raw:   "@@ -1,1 +1,1 @@\n-old\n+new",
	}.View())
	if !strings.Contains(plain, "1 │   │ -old") || !strings.Contains(plain, "  │ 1 │ +new") {
		t.Fatalf("narrow diff should keep readable inline fallback:\n%s", plain)
	}
	assertNoDiffConnectorGlyphs(t, plain)
}

func TestToolGroup_RendersEditReturnedDiff(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "edit",
				Target:   "main.go",
				Raw:      "edited main.go: 1 replacement(s)\n--- main.go (before)\n+++ main.go (after)\n@@ -7,1 +7,1 @@\n-fmt.Println(\"old\")\n+fmt.Println(\"new\")",
				Status:   ToolStatusCompleted,
			},
		},
	}
	plain := stripANSI(ml.View())
	for _, want := range []string{"Edit", "main.go", "edited main.go", "--- main.go (before)", "+++ main.go (after)", "7 │", `-fmt.Println("old")`, `+fmt.Println("new")`} {
		if !strings.Contains(plain, want) {
			t.Fatalf("edit diff preview missing %q:\n%s", want, plain)
		}
	}
	assertNoDiffConnectorGlyphs(t, plain)
}

func TestToolGroup_DoesNotRenderSyntheticArgDiff(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleTool,
				ToolName: "edit",
				Target:   "main.go",
				ToolArgs: []byte(`{"path":"main.go","oldString":"old","newString":"new"}`),
				Status:   ToolStatusCompleted,
			},
		},
	}
	plain := stripANSI(ml.View())
	if strings.Contains(plain, "--- old") || strings.Contains(plain, "+++ new") || strings.Contains(plain, "-old") {
		t.Fatalf("tool group should not synthesize old/new arg diff without result content:\n%s", plain)
	}
}

func TestToolGroup_RendersBashDiffOutputAsDiff(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{
				Role:      RoleTool,
				ToolName:  "bash",
				ToolUseID: "tu-diff",
				Target:    "git diff",
				Raw:       "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,1 +1,1 @@\n-old\n+new",
				Status:    ToolStatusCompleted,
			},
		},
	}
	plain := stripANSI(ml.View())
	for _, want := range []string{"Bash", "$ git diff", "diff --git", "-old", "+new"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("bash diff output missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "Click to expand") {
		t.Fatalf("short diff should not render expand hint:\n%s", plain)
	}
}
