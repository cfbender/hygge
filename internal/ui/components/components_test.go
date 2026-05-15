package components

import (
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestStatusBarRendersIdentity(t *testing.T) {
	t.Parallel()
	sb := StatusBar{
		Profile:  "work",
		Provider: "anthropic",
		Model:    "claude-sonnet-4-5",
		Pwd:      "~/proj",
		Width:    80,
		Theme:    theme.ShellTheme(),
	}
	out := sb.View()
	for _, want := range []string{"[profile:work]", "anthropic/claude-sonnet-4-5", "~/proj"} {
		if !strings.Contains(out, want) {
			t.Errorf("statusbar missing %q in:\n%s", want, out)
		}
	}
}

func TestStatusBarSpinnerWhenBusy(t *testing.T) {
	t.Parallel()
	sb := StatusBar{Provider: "anthropic", Model: "claude", Width: 60, Busy: true, SpinnerTick: 0, Theme: theme.ShellTheme()}
	out := sb.View()
	if !strings.Contains(out, spinnerFrames[0]) {
		t.Errorf("expected spinner glyph %q in:\n%s", spinnerFrames[0], out)
	}

	sb.Busy = false
	out = sb.View()
	for _, f := range spinnerFrames {
		if strings.Contains(out, f) {
			t.Errorf("expected NO spinner glyph when not busy, found %q", f)
		}
	}
}

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
	for _, want := range []string{"hello", "General", "hi back", "read", "/tmp/x"} {
		if !strings.Contains(out, want) {
			t.Errorf("messagelist missing %q in:\n%s", want, out)
		}
	}
	// The old gutter format must not appear for non-task tools.
	if strings.Contains(out, "▌tool: read") {
		t.Errorf("messagelist should not render '▌tool: read' gutter for non-task tool; got:\n%s", out)
	}
}

func TestMessageListCollapsesLongToolOutput(t *testing.T) {
	t.Parallel()
	var lines []string
	for i := 0; i < 20; i++ {
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
	if !strings.Contains(out, "read") {
		t.Errorf("expected tool name 'read' in output:\n%s", out)
	}
	if !strings.Contains(out, "somefile.go") {
		t.Errorf("expected target 'somefile.go' in output:\n%s", out)
	}
	// The raw body lines must NOT appear (tool-group bubble omits body content).
	if strings.Contains(out, "line0") {
		t.Errorf("expected raw body lines to be absent from tool-group bubble, found in:\n%s", out)
	}
}

func TestMessageListEmpty(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	out := ml.View()
	// New empty state: centered welcome with hygge glyph and hints.
	for _, want := range []string{"hygge", "Type a message", "ctrl+p", "ctrl+g"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty state missing %q in:\n%s", want, out)
		}
	}
	// Old "no messages" placeholder must no longer appear.
	if strings.Contains(out, "no messages") {
		t.Errorf("empty state should not contain old placeholder text; got:\n%s", out)
	}
}

// TestMessageListEmptyState_NoMessagesShowsWelcome verifies the empty state
// is shown only when Messages is nil/empty.
func TestMessageListEmptyState_NoMessagesShowsWelcome(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	out := ml.View()
	if !strings.Contains(out, "·hygge·") {
		t.Errorf("empty state must contain '·hygge·' glyph; got:\n%s", out)
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
	if strings.Contains(out, "·hygge·") {
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
	for i := 0; i < 12; i++ {
		thinkLines = append(thinkLines, "thought line "+itoa(i))
	}
	ml := MessageList{Width: 80, Theme: theme.ShellTheme()}
	result := ml.truncateThinking(strings.Join(thinkLines, "\n"))
	plain := stripANSI(result)

	// First 7 lines (thinkingMaxLines-1=7) must appear.
	for i := 0; i < 7; i++ {
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
	for i := 0; i < thinkingMaxLines; i++ {
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

// TestInputBorder_FocusedUsesAccent verifies that the bordered input view
// renders and contains the typed text.
func TestInputBorder_FocusedUsesAccent(t *testing.T) {
	t.Parallel()
	in := NewInput(theme.ShellTheme())
	in.Focused = true
	in.SetWidth(60)
	in.Textarea.SetValue("test input")
	view := in.View()
	if !strings.Contains(view, "test input") {
		t.Errorf("focused input view should contain typed text; got:\n%s", view)
	}
	// The view should contain rounded border characters.
	plain := stripANSI(view)
	if !strings.ContainsAny(plain, "╭╰╮╯") {
		t.Errorf("input view should contain rounded border characters; got:\n%s", plain)
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

// TestToolGroupBubble_ThreeCallGroup verifies three consecutive non-task tool
// calls render as a single bordered bubble with one row each.
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
	for _, want := range []string{"read", "/tmp/a.go", "grep", "func Main", "bash", "go build ."} {
		if !strings.Contains(out, want) {
			t.Errorf("tool group missing %q in:\n%s", want, out)
		}
	}
	// The middle dot prefix must appear.
	if !strings.Contains(out, "·") {
		t.Errorf("tool group missing '·' dot prefix in:\n%s", out)
	}
	// Old gutter format must be absent.
	if strings.Contains(out, "▌tool:") {
		t.Errorf("tool group must not contain '▌tool:' gutter; got:\n%s", out)
	}
	// All three tools in a single group → output has exactly one bubble
	// (one pair of \n\n-separated blocks, not three separate ones).
	blocks := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(blocks) != 1 {
		t.Errorf("expected 1 bubble block for 3 grouped tool calls, got %d blocks", len(blocks))
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
	if !strings.Contains(out, "read") {
		t.Errorf("single-call group missing 'read' in:\n%s", out)
	}
	if !strings.Contains(out, "/etc/hosts") {
		t.Errorf("single-call group missing target in:\n%s", out)
	}
	if strings.Contains(out, "▌tool:") {
		t.Errorf("single-call group must not use old gutter format; got:\n%s", out)
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
	if !strings.Contains(out, "bash") {
		t.Errorf("error-tool group missing 'bash' in:\n%s", out)
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
			{Role: RoleTool, ToolName: "task", Target: "deploy", SubagentID: "sub-1"},
			{Role: RoleTool, ToolName: "bash", Target: "go vet ./..."},
		},
		Subagents: map[string]*SubagentState{"sub-1": st},
	}
	out := ml.View()

	// All content must appear.
	for _, want := range []string{"read", "main.go", "grep", "TODO", "General Subagent", "deploy", "bash", "go vet ./..."} {
		if !strings.Contains(out, want) {
			t.Errorf("interleaved output missing %q in:\n%s", want, out)
		}
	}

	// There should be 3 bubble blocks (two tool groups + subagent bubble).
	blocks := strings.Split(strings.TrimSpace(out), "\n\n")
	if len(blocks) != 3 {
		t.Errorf("expected 3 bubble blocks for read+grep / task / bash, got %d:\n%s", len(blocks), out)
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
// renders the compact subagent block content wrapped in a bordered bubble
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
			{Role: RoleTool, ToolName: "task", Target: "find LICENSE", SubagentID: "sub-1"},
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
