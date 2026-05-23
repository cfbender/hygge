package components

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/ui/styles"
)

type stubCommand struct {
	name        string
	description string
}

func (c stubCommand) Name() string        { return c.name }
func (c stubCommand) Description() string { return c.description }
func (c stubCommand) Source() string      { return "test" }
func (c stubCommand) Args() []command.ArgSpec {
	return nil
}
func (c stubCommand) Execute(context.Context, command.App, string) (command.Outcome, error) {
	return command.Outcome{}, nil
}

func TestMessageListRendersRoles(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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

func TestCommandPaletteScrollsHighlightedMatchIntoView(t *testing.T) {
	t.Parallel()
	commands := make([]command.Command, 12)
	for i := range commands {
		commands[i] = stubCommand{name: fmt.Sprintf("cmd%02d", i), description: "test command"}
	}

	out := CommandPalette{Width: 80, Matches: commands, Highlight: 10}.View()
	if !strings.Contains(out, "/cmd10") {
		t.Fatalf("highlighted command should be visible after scrolling:\n%s", out)
	}
	if strings.Contains(out, "/cmd00") {
		t.Fatalf("first command should scroll out of view when highlight is near the end:\n%s", out)
	}
	if !strings.Contains(out, "↑") {
		t.Fatalf("expected overflow-above indicator in:\n%s", out)
	}
}

func TestMentionPaletteScrollsHighlightedMatchIntoView(t *testing.T) {
	t.Parallel()
	mentions := make([]MentionItem, 12)
	for i := range mentions {
		mentions[i] = MentionItem{Kind: "file", Label: fmt.Sprintf("file%02d.go", i)}
	}

	out := MentionPalette{Width: 80, Matches: mentions, Highlight: 10}.View()
	if !strings.Contains(out, "@file10.go") {
		t.Fatalf("highlighted mention should be visible after scrolling:\n%s", out)
	}
	if strings.Contains(out, "@file00.go") {
		t.Fatalf("first mention should scroll out of view when highlight is near the end:\n%s", out)
	}
	if !strings.Contains(out, "↑") {
		t.Fatalf("expected overflow-above indicator in:\n%s", out)
	}
}

func TestMentionTokensHighlightedInInputAndUserBubble(t *testing.T) {
	t.Parallel()
	th := styles.DefaultTheme()
	accentMention := th.Style(styles.AtomAccent).Render("@internal/ui/app.go")

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

func TestMentionHighlightExcludesTrailingPunctuation(t *testing.T) {
	t.Parallel()
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("5"))
	out := HighlightMentions("read @internal/ui/app.go.", style)

	if want := style.Render("@internal/ui/app.go") + "."; !strings.Contains(out, want) {
		t.Fatalf("mention punctuation was highlighted; want %q in:\n%s", want, out)
	}
	if bad := style.Render("@internal/ui/app.go."); strings.Contains(out, bad) {
		t.Fatalf("trailing punctuation should not be highlighted; found %q in:\n%s", bad, out)
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

func TestThemeModalViewUsesCandidatePreviewThemes(t *testing.T) {
	t.Parallel()
	calls := map[string]bool{}
	m := ThemeModal{
		Width:   80,
		Height:  20,
		Theme:   styles.DefaultTheme(),
		Current: "claret",
		Themes:  []string{"claret", "dracula"},
		PreviewTheme: func(name string) *styles.Styles {
			calls[name] = true
			return styles.DefaultTheme()
		},
	}
	out := m.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "dracula") || !strings.Contains(plain, "Aa") {
		t.Fatalf("theme preview missing row/sample text:\n%s", plain)
	}
	if !calls["dracula"] {
		t.Fatalf("preview callback was not called for dracula")
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
		Theme:         styles.DefaultTheme(),
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
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}
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
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}
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
		Theme: styles.DefaultTheme(),
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
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}
	result := ml.truncateThinking(strings.Join(thinkLines, "\n"), false)
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
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}
	thinking := "line1\nline2\nline3"
	result := ml.truncateThinking(thinking, false)
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
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}
	result := ml.truncateThinking(strings.Join(lines, "\n"), false)
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
	in := NewInput(styles.DefaultTheme())
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
	in := NewInput(styles.DefaultTheme())
	in.Focused = false
	in.SetWidth(60)
	view := in.View()
	if view == "" {
		t.Errorf("blurred input view should not be empty")
	}
}

func TestStatusPillsRendersQueueCount(t *testing.T) {
	t.Parallel()
	out := StatusPills{Width: 60, Theme: styles.DefaultTheme(), QueueCount: 2}.View()
	plain := stripANSI(out)
	if !strings.Contains(plain, "2 queued") {
		t.Errorf("status pills missing queue count; got:\n%s", plain)
	}
}

func TestStatusPillsRendersQueuedPrompts(t *testing.T) {
	t.Parallel()
	out := StatusPills{
		Width:          60,
		Theme:          styles.DefaultTheme(),
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
	out := StatusPills{Width: 60, Theme: styles.DefaultTheme()}.View()
	if out != "" {
		t.Errorf("status pills should be empty without queue state; got %q", out)
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
	out := SubagentBlock{State: st, Theme: styles.DefaultTheme(), Now: end}.View()
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
		Theme:          styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme:     styles.DefaultTheme(),
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

func TestFooterTruncatesMetadataToWidth(t *testing.T) {
	t.Parallel()
	f := Footer{
		Width:          24,
		Theme:          styles.DefaultTheme(),
		AgentType:      "general",
		ModelName:      "claude-sonnet-with-a-very-long-name",
		Provider:       "openrouter",
		ReasoningLevel: "high",
	}
	out := f.View()
	if got := lipgloss.Width(out); got > f.Width {
		t.Fatalf("footer width = %d, want <= %d; out=%q", got, f.Width, stripANSI(out))
	}
	if !strings.Contains(stripANSI(out), "General") {
		t.Fatalf("footer should keep left identity visible: %q", stripANSI(out))
	}
}

func TestPermissionModalRendersRequest(t *testing.T) {
	t.Parallel()
	m := PermissionModal{
		Width:  100,
		Height: 24,
		Theme:  styles.DefaultTheme(),
		Request: PermissionRequest{
			RequestID: "req-1",
			ToolName:  "read",
			Category:  "file.read",
			Target:    "/Users/cfb/.aws/credentials",
			Why:       "needs to inspect config",
		},
	}
	out := m.View()
	for _, want := range []string{"permission request", "Tool:", "read", "Category:", "file.read", "Target:", "/Users/cfb/.aws/credentials", "[y]", "[Y]", "[A]", "[n]", "needs to inspect config"} {
		if !strings.Contains(out, want) {
			t.Errorf("modal missing %q in:\n%s", want, out)
		}
	}
	// edit option must not appear
	if strings.Contains(out, "[e]") {
		t.Errorf("modal must not contain [e] (edit removed), got:\n%s", out)
	}
}

func TestPermissionModalNoEditOption(t *testing.T) {
	t.Parallel()
	m := PermissionModal{
		Width: 80, Height: 20, Theme: styles.DefaultTheme(),
		Request: PermissionRequest{ToolName: "write", Category: "file.write", Target: "/tmp"},
	}
	out := m.View()
	if strings.Contains(out, "[e]") || strings.Contains(out, "edit not yet implemented") || strings.Contains(out, "edit (v0.2)") {
		t.Errorf("modal must not contain removed edit UI, got:\n%s", out)
	}
}

func TestInputBuildsAndReports(t *testing.T) {
	t.Parallel()
	in := NewInput(styles.DefaultTheme())
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "main.go"},
			{Role: RoleTool, ToolName: "bash", ToolUseID: "bash-1", Target: "go test ./...", Raw: "line 1\nline 2\nline 3\nline 4\nline 5"},
			{Role: RoleTool, ToolName: "grep", Target: "TODO"},
			{Role: RoleTool, ToolName: "bash", ToolUseID: "bash-2", Target: "go vet ./...", Raw: "ok"},
		},
	}

	_, _, zones, _ := ml.ViewWithHitZones()
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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

func containsBackgroundSGR(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '\x1b' || i+1 >= len(s) || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && s[j] != 'm' {
			j++
		}
		if j >= len(s) {
			return false
		}
		seq := s[i+2 : j]
		if strings.Contains(seq, "48;") || strings.Contains(seq, "48:") || strings.Contains(seq, "49") {
			return true
		}
		for part := range strings.SplitSeq(seq, ";") {
			if len(part) == 2 && part[0] == '4' && part[1] >= '0' && part[1] <= '7' {
				return true
			}
			if len(part) == 3 && part[:2] == "10" && part[2] >= '0' && part[2] <= '7' {
				return true
			}
		}
		i = j
	}
	return false
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
		Width:              32,
		Height:             30,
		SessionTitle:       "fix the auth bug",
		UsedTokens:         97229,
		MaxTokens:          1_000_000,
		PctUsed:            0.10,
		CostUSD:            0.0042,
		BilledInputTokens:  500_000,
		BilledOutputTokens: 874_000,
		MCPs: []SidebarMCPStatus{
			{Name: "server-a", Ready: true, ToolCount: 3},
			{Name: "server-b", Ready: false},
		},
		ProjectPath: "~/code/hygge",
		GitBranch:   "main",
		AppName:     "Hygge",
		Version:     "v0.1.0-dev",
		Theme:       styles.DefaultTheme(),
		NerdFonts:   false,
	}
	out := sb.View()
	plain := stripANSI(out)

	// Session title.
	if !strings.Contains(plain, "fix the auth bug") {
		t.Errorf("sidebar missing session title; got:\n%s", plain)
	}
	// Usage and context sections.
	for _, want := range []string{"Usage", "500k ↑ / 874k ↓", "$0.0042", "Context", "97,229 tokens", "10% used"} {
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
	thm := styles.DefaultTheme()
	sb := Sidebar{
		Width:        32,
		Height:       12,
		SessionTitle: "styled title",
		MCPs:         []SidebarMCPStatus{{Name: "server-a", Ready: true, ToolCount: 3}},
		Theme:        thm,
	}
	out := sb.View()
	bgOpen := sidebarBackgroundOpenSequence(thm.Colors[styles.AtomSidebarBg])
	if bgOpen == "" {
		t.Fatalf("sidebarBackgroundOpenSequence returned empty for atom %q; substring assertions below would vacuously pass", styles.AtomSidebarBg)
	}
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
		Theme:  styles.DefaultTheme(),
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
		Theme:  styles.DefaultTheme(),
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
		Theme:        styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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

func TestCompactTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{999, "999"},
		{1000, "1k"},
		{1500, "1.5k"},
		{12788, "12.7k"},
		{12700, "12.7k"},
		{999000, "999k"},
		{999900, "999.9k"},
		{1_000_000, "1.00M"},
		{1_374_982, "1.37M"},
		{500_000, "500k"},
		{874_000, "874k"},
		{-1, "0"},
	}
	for _, tc := range cases {
		got := compactTokens(tc.n)
		if got != tc.want {
			t.Errorf("compactTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestSidebar_UsageSplitInputOutput(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:              40,
		Height:             30,
		BilledInputTokens:  500_000,
		BilledOutputTokens: 874_000,
		CostUSD:            0.0042,
		Theme:              styles.DefaultTheme(),
	}
	plain := stripANSI(sb.View())
	// Must contain split up/down display.
	if !strings.Contains(plain, "500k ↑ / 874k ↓") {
		t.Errorf("sidebar missing split usage display; got:\n%s", plain)
	}
	// Must NOT contain old "billed" format.
	if strings.Contains(plain, "billed") {
		t.Errorf("sidebar must not show old 'billed' text; got:\n%s", plain)
	}
	// Cost must still appear.
	if !strings.Contains(plain, "$0.0042") {
		t.Errorf("sidebar missing cost display; got:\n%s", plain)
	}
}

func TestSidebar_UsageHiddenWhenBothZero(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  40,
		Height: 20,
		Theme:  styles.DefaultTheme(),
	}
	plain := stripANSI(sb.View())
	if strings.Contains(plain, "Usage") {
		t.Errorf("sidebar should hide Usage section when all zero; got:\n%s", plain)
	}
}

func TestSidebar_UsageShownWithCostOnly(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:   40,
		Height:  20,
		CostUSD: 0.001,
		Theme:   styles.DefaultTheme(),
	}
	plain := stripANSI(sb.View())
	if !strings.Contains(plain, "Usage") {
		t.Errorf("sidebar should show Usage section when cost is non-zero; got:\n%s", plain)
	}
}

func TestMessageList_AssistantCompactTokens(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:         RoleAssistant,
				Raw:          "Here is the answer.",
				AgentType:    "General",
				OutputTokens: 12788,
			},
		},
	}
	plain := stripANSI(ml.View())
	// Compact format with down arrow.
	if !strings.Contains(plain, "12.7k ↓") {
		t.Errorf("assistant bubble missing compact token display; got:\n%s", plain)
	}
	// Old plain format must not appear.
	if strings.Contains(plain, "12788 tokens") {
		t.Errorf("assistant bubble must not use old plain token format; got:\n%s", plain)
	}
}

func TestMessageList_AssistantCompactTokensSmall(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:         RoleAssistant,
				Raw:          "short",
				AgentType:    "General",
				OutputTokens: 42,
			},
		},
	}
	plain := stripANSI(ml.View())
	if !strings.Contains(plain, "42 ↓") {
		t.Errorf("assistant bubble missing small compact token display; got:\n%s", plain)
	}
}

func TestMessageList_AssistantCompactTokensMillion(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:         RoleAssistant,
				Raw:          "long response",
				AgentType:    "General",
				OutputTokens: 1_374_982,
			},
		},
	}
	plain := stripANSI(ml.View())
	if !strings.Contains(plain, "1.37M ↓") {
		t.Errorf("assistant bubble missing million compact token display; got:\n%s", plain)
	}
}

func TestSidebarTodos_Empty(t *testing.T) {
	t.Parallel()
	sb := Sidebar{
		Width:  32,
		Height: 30,
		Theme:  styles.DefaultTheme(),
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
		Theme:  styles.DefaultTheme(),
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
		Theme:  styles.DefaultTheme(),
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
		Theme:  styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
	if containsBackgroundSGR(out) {
		t.Fatalf("shell diff view should use foreground colors without background fills:\n%q", out)
	}
}

func TestDiffView_SideBySideSeparatesInsertionsAndDeletions(t *testing.T) {
	t.Parallel()
	out := DiffView{
		Width: 96,
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
		Raw:   "@@ -1,1 +1,1 @@\n-old\n+new",
	}.View())
	if !strings.Contains(plain, "1 │   │ -old") || !strings.Contains(plain, "  │ 1 │ +new") {
		t.Fatalf("narrow diff should keep readable inline fallback:\n%s", plain)
	}
	assertNoDiffConnectorGlyphs(t, plain)
}

func TestDiffView_TruncatedSideBySideKeepsReplacementPairs(t *testing.T) {
	t.Parallel()
	// 1 meta + 8 del + 8 add + 4 body = 21 rows; MaxLines=12 cuts inside the del
	// block. Pair-preservation extends to include all 8 adds (end=20 < 21), so
	// the diff IS genuinely truncated — the trailing body lines are not shown.
	raw := "@@ -1,12 +1,12 @@\n-old 1\n-old 2\n-old 3\n-old 4\n-old 5\n-old 6\n-old 7\n-old 8\n+new 1\n+new 2\n+new 3\n+new 4\n+new 5\n+new 6\n+new 7\n+new 8\n body 1\n body 2\n body 3\n body 4"
	plain := stripANSI(DiffView{
		Width:    96,
		Theme:    styles.DefaultTheme(),
		Raw:      raw,
		MaxLines: defaultDiffPreviewLines,
	}.View())

	for _, want := range []string{"+new 7", "+new 8", "… diff truncated"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("truncated side-by-side replacement diff missing %q:\n%s", want, plain)
		}
	}
	if strings.Contains(plain, "-old 8") && !strings.Contains(diffLineContaining(plain, "-old 8"), "+new 8") {
		t.Fatalf("side-by-side truncation should not render replacement deletion without its addition:\n%s", plain)
	}
}

// TestDiffView_SideBySideLongLinesDoNotOverflow verifies that very long diff
// lines are clipped within their pane and the combined rendered line never
// exceeds the declared width.
func TestDiffView_SideBySideLongLinesDoNotOverflow(t *testing.T) {
	t.Parallel()
	longLine := strings.Repeat("abcdefghij", 20) // 200 visible chars
	raw := "@@ -1,2 +1,2 @@\n-" + longLine + "\n+" + longLine + "Y"

	for _, width := range []int{72, 80, 96, 120} {
		out := DiffView{
			Width: width,
			Theme: styles.DefaultTheme(),
			Raw:   raw,
		}.View()
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("side-by-side width=%d: line %d width=%d exceeds limit; line=%q", width, i, got, line)
			}
		}
	}
}

// TestDiffView_InlineLongLinesDoNotOverflow verifies that long lines in the
// narrow (inline) diff fallback are clipped and never exceed the declared width.
func TestDiffView_InlineLongLinesDoNotOverflow(t *testing.T) {
	t.Parallel()
	longLine := strings.Repeat("abcdefghij", 20) // 200 visible chars
	raw := "@@ -1,2 +1,2 @@\n-" + longLine + "\n+" + longLine + "Y"

	// widths below sideBySideDiffMinWidth (72) use the inline fallback
	for _, width := range []int{40, 50, 60, 71} {
		out := DiffView{
			Width: width,
			Theme: styles.DefaultTheme(),
			Raw:   raw,
		}.View()
		for i, line := range strings.Split(out, "\n") {
			if got := lipgloss.Width(line); got > width {
				t.Errorf("inline width=%d: line %d width=%d exceeds limit; line=%q", width, i, got, line)
			}
		}
	}
}

// TestDiffView_TabsExpandedSoLinesDoNotOverflow verifies that hard tabs in
// diff content are expanded to spaces before clipping. Terminals render tabs
// as multi-cell tab stops, but lipgloss.Width counts them as a single cell, so
// without expansion a "clipped" diff line still overflows the bubble width
// once the terminal expands the tabs at draw time.
func TestDiffView_TabsExpandedSoLinesDoNotOverflow(t *testing.T) {
	t.Parallel()
	raw := "@@ -1,2 +1,2 @@\n" +
		"-\t\tAtomCodeBg: {kind: colorKindDefault}, // no override since terminal background varies\n" +
		"+\t\tAtomCodeBg: {kind: colorKindDefault}, // tweaked override since terminal background varies"

	for _, width := range []int{72, 96, 117, 150} {
		out := DiffView{
			Width: width,
			Theme: styles.DefaultTheme(),
			Raw:   raw,
		}.View()
		for i, line := range strings.Split(out, "\n") {
			if strings.ContainsRune(line, '\t') {
				t.Errorf("width=%d: line %d still contains a hard tab; "+
					"diff content should be expanded so terminal width matches lipgloss width. plain=%q",
					width, i, stripANSI(line))
			}
			if got := lipgloss.Width(line); got > width {
				t.Errorf("width=%d: line %d width=%d exceeds limit; plain=%q",
					width, i, got, stripANSI(line))
			}
		}
	}
}

func TestToolGroup_RendersEditReturnedDiff(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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
		Theme: styles.DefaultTheme(),
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

func TestToolGroup_LongBashGitDiffCanExpand(t *testing.T) {
	t.Parallel()
	// 3 meta + 1 hunk header + 8 del + 8 add + 4 body = 24 rows. MaxLines=12
	// cuts inside the del block; pair-preservation extends to end=20 (end of
	// adds), leaving 4 body rows hidden — the diff is genuinely truncated.
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,12 +1,12 @@\n-old 1\n-old 2\n-old 3\n-old 4\n-old 5\n-old 6\n-old 7\n-old 8\n+new 1\n+new 2\n+new 3\n+new 4\n+new 5\n+new 6\n+new 7\n+new 8\n body 1\n body 2\n body 3\n body 4"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "bash", ToolUseID: "bash-diff", Target: "git diff", Raw: raw, Status: ToolStatusCompleted},
		},
	}

	collapsed, _, zones, _ := ml.ViewWithHitZones()
	plainCollapsed := stripANSI(collapsed)
	if len(zones) != 1 || zones[0].ToolUseID != "bash-diff" {
		t.Fatalf("expected bash diff hit zone, got %+v", zones)
	}
	if !strings.Contains(plainCollapsed, "Click to expand") || !strings.Contains(plainCollapsed, "… diff truncated") {
		t.Fatalf("long git diff should show expand affordance:\n%s", plainCollapsed)
	}

	ml.ExpandedTools = map[string]bool{"bash-diff": true}
	plainExpanded := stripANSI(ml.View())
	if !strings.Contains(plainExpanded, "+new 8") {
		t.Fatalf("expanded git diff should include final line:\n%s", plainExpanded)
	}
	if strings.Contains(plainExpanded, "Click to expand") || strings.Contains(plainExpanded, "… diff truncated") {
		t.Fatalf("expanded git diff should not show collapsed affordances:\n%s", plainExpanded)
	}
}

func TestToolGroup_LongEditDiffCanExpand(t *testing.T) {
	t.Parallel()
	// 3 meta + 1 hunk header + 8 del + 8 add + 4 body = 24 rows. MaxLines=12
	// cuts inside the del block; pair-preservation extends to end=20 (end of
	// adds), leaving 4 body rows hidden — the diff is genuinely truncated.
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1,12 +1,12 @@\n-old 1\n-old 2\n-old 3\n-old 4\n-old 5\n-old 6\n-old 7\n-old 8\n+new 1\n+new 2\n+new 3\n+new 4\n+new 5\n+new 6\n+new 7\n+new 8\n body 1\n body 2\n body 3\n body 4"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "edit", ToolUseID: "edit-diff", Target: "a.go", Raw: raw, Status: ToolStatusCompleted},
		},
	}

	collapsed, _, zones, _ := ml.ViewWithHitZones()
	plainCollapsed := stripANSI(collapsed)
	if len(zones) != 1 || zones[0].ToolUseID != "edit-diff" {
		t.Fatalf("expected edit diff hit zone, got %+v", zones)
	}
	if !strings.Contains(plainCollapsed, "Click to expand") || !strings.Contains(plainCollapsed, "… diff truncated") {
		t.Fatalf("long edit diff should show expand affordance:\n%s", plainCollapsed)
	}
	if !strings.Contains(plainCollapsed, "+new 8") {
		t.Fatalf("collapsed edit diff should keep replacement right side visible:\n%s", plainCollapsed)
	}

	ml.ExpandedTools = map[string]bool{"edit-diff": true}
	plainExpanded := stripANSI(ml.View())
	if !strings.Contains(plainExpanded, "+new 8") {
		t.Fatalf("expanded edit diff should include final line:\n%s", plainExpanded)
	}
	if strings.Contains(plainExpanded, "Click to expand") || strings.Contains(plainExpanded, "… diff truncated") {
		t.Fatalf("expanded edit diff should not show collapsed affordances:\n%s", plainExpanded)
	}
}

// TestDiffView_SideBySidePairPreservationToEndIsNotTruncated verifies that
// when visibleSideBySidePairs extends `end` to keep a delete/add replacement
// pair together and that extension reaches the last row of the diff, neither
// the rendered view nor IsTruncated() reports truncation.
//
// Regression: before the fix, truncated was returned as true even when end >=
// len(rows), causing "… diff truncated" to appear in the rendered output and
// IsTruncated() to return true, which made MessageList show "Click to expand".
func TestDiffView_SideBySidePairPreservationToEndIsNotTruncated(t *testing.T) {
	t.Parallel()

	// 1 hunk header + 6 del + 6 add = 13 rows; MaxLines=12 cuts inside the add
	// block, pair-preservation extends end to 13 == len(rows).
	raw := "@@ -1,6 +1,6 @@\n-old 1\n-old 2\n-old 3\n-old 4\n-old 5\n-old 6\n+new 1\n+new 2\n+new 3\n+new 4\n+new 5\n+new 6"
	diff := DiffView{
		Width:    96,
		Theme:    styles.DefaultTheme(),
		Raw:      raw,
		MaxLines: defaultDiffPreviewLines, // 12
	}

	if diff.IsTruncated() {
		t.Fatal("IsTruncated() should be false when pair-preservation reaches the end of the diff")
	}

	plain := stripANSI(diff.View())
	if strings.Contains(plain, "… diff truncated") {
		t.Fatalf("rendered view should not contain truncation marker when all rows are shown:\n%s", plain)
	}
	// All replacement lines must be visible.
	for _, want := range []string{"-old 6", "+new 6"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("rendered view missing %q:\n%s", want, plain)
		}
	}
}

// TestToolGroup_SideBySidePairPreservationToEndNoExpandHint verifies that when
// the collapsed side-by-side diff renders the entire diff (via pair-preservation
// reaching the end), the tool bubble does NOT show "Click to expand".
func TestToolGroup_SideBySidePairPreservationToEndNoExpandHint(t *testing.T) {
	t.Parallel()

	// Same 13-row diff as the unit test above; at width=100 side-by-side is used.
	raw := "@@ -1,6 +1,6 @@\n-old 1\n-old 2\n-old 3\n-old 4\n-old 5\n-old 6\n+new 1\n+new 2\n+new 3\n+new 4\n+new 5\n+new 6"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:      RoleTool,
				ToolName:  "edit",
				ToolUseID: "edit-no-expand",
				Target:    "a.go",
				Raw:       raw,
				Status:    ToolStatusCompleted,
			},
		},
	}

	collapsed, _, zones, _ := ml.ViewWithHitZones()
	plainCollapsed := stripANSI(collapsed)

	// A hit zone may exist for the tool block (the infrastructure always
	// registers edit/bash/write for click-to-expand), but the rendered output
	// must not invite the user to click when the diff is fully visible.
	_ = zones
	if strings.Contains(plainCollapsed, "Click to expand") {
		t.Fatalf("should not show 'Click to expand' when all diff rows are rendered:\n%s", plainCollapsed)
	}
	if strings.Contains(plainCollapsed, "… diff truncated") {
		t.Fatalf("should not show truncation marker when all diff rows are rendered:\n%s", plainCollapsed)
	}
	// Both ends of the replacement must be visible.
	for _, want := range []string{"-old 6", "+new 6"} {
		if !strings.Contains(plainCollapsed, want) {
			t.Fatalf("collapsed view missing %q:\n%s", want, plainCollapsed)
		}
	}
}

// TestThinkingClickToExpand_AffordancePresent verifies that an assistant
// message whose Thinking field exceeds thinkingMaxLines renders a
// "click to expand" affordance and produces a ThinkingHitZone.
func TestThinkingClickToExpand_AffordancePresent(t *testing.T) {
	t.Parallel()
	// Build thinking longer than thinkingMaxLines.
	var thinkLines []string
	for i := range thinkingMaxLines + 4 {
		thinkLines = append(thinkLines, "thought "+itoa(i))
	}
	thinking := strings.Join(thinkLines, "\n")

	ml := MessageList{
		Width: 80,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleAssistant,
				Raw:      "response",
				Thinking: thinking,
			},
		},
	}

	content, _, _, thinkingZones := ml.ViewWithHitZones()
	plain := stripANSI(content)

	// Affordance must appear.
	if !strings.Contains(plain, "click to expand") {
		t.Fatalf("expected 'click to expand' affordance in truncated thinking; got:\n%s", plain)
	}
	// ThinkingHitZone must be registered.
	if len(thinkingZones) != 1 {
		t.Fatalf("expected 1 ThinkingHitZone, got %d", len(thinkingZones))
	}
	if thinkingZones[0].MsgIndex != 0 {
		t.Errorf("expected ThinkingHitZone.MsgIndex == 0, got %d", thinkingZones[0].MsgIndex)
	}
	if thinkingZones[0].StartLine >= thinkingZones[0].EndLine {
		t.Errorf("ThinkingHitZone has invalid line range: %+v", thinkingZones[0])
	}
}

// TestThinkingClickToExpand_NoZoneWhenShort verifies that assistant messages
// with thinking that fits within thinkingMaxLines do NOT produce a
// ThinkingHitZone (no click-to-expand needed).
func TestThinkingClickToExpand_NoZoneWhenShort(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 80,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleAssistant,
				Raw:      "response",
				Thinking: "short thinking",
			},
		},
	}

	_, _, _, thinkingZones := ml.ViewWithHitZones()
	if len(thinkingZones) != 0 {
		t.Fatalf("expected no ThinkingHitZone for short thinking, got %d", len(thinkingZones))
	}
}

// TestThinkingClickToExpand_ToggleExpanded verifies that setting
// ExpandedThinking for a message index shows the full thinking content,
// a "click to collapse" affordance, and a ThinkingHitZone for collapsing.
func TestThinkingClickToExpand_ToggleExpanded(t *testing.T) {
	t.Parallel()
	var thinkLines []string
	for i := range thinkingMaxLines + 4 {
		thinkLines = append(thinkLines, "thought "+itoa(i))
	}
	thinking := strings.Join(thinkLines, "\n")

	ml := MessageList{
		Width: 80,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{
				Role:     RoleAssistant,
				Raw:      "response",
				Thinking: thinking,
			},
		},
		ExpandedThinking: map[int]bool{0: true},
	}

	content, _, _, thinkingZones := ml.ViewWithHitZones()
	plain := stripANSI(content)

	// All thinking lines should be visible.
	for i := range thinkingMaxLines + 4 {
		if !strings.Contains(plain, "thought "+itoa(i)) {
			t.Errorf("expected thinking line %d visible when expanded; got:\n%s", i, plain)
		}
	}
	// Collapse affordance should appear.
	if !strings.Contains(plain, "click to collapse thinking") {
		t.Errorf("expected 'click to collapse thinking' affordance when expanded; got:\n%s", plain)
	}
	// No expand affordance.
	if strings.Contains(plain, "click to expand") {
		t.Errorf("unexpected 'click to expand' affordance when already expanded; got:\n%s", plain)
	}
	// Expanded messages still produce a ThinkingHitZone so the user can click
	// to collapse.
	if len(thinkingZones) != 1 {
		t.Fatalf("expected 1 ThinkingHitZone for expanded thinking collapse, got %d", len(thinkingZones))
	}
	if thinkingZones[0].MsgIndex != 0 {
		t.Errorf("expected ThinkingHitZone.MsgIndex == 0, got %d", thinkingZones[0].MsgIndex)
	}
}

// TestThinkingClickToExpand_MultipleMessages verifies that per-message
// ThinkingHitZone indices are distinct and point to the correct messages.
func TestThinkingClickToExpand_MultipleMessages(t *testing.T) {
	t.Parallel()
	var longLines []string
	for i := range thinkingMaxLines + 2 {
		longLines = append(longLines, "thought "+itoa(i))
	}
	longThinking := strings.Join(longLines, "\n")

	ml := MessageList{
		Width: 80,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			// msg 0: user (no thinking)
			{Role: RoleUser, Raw: "hi"},
			// msg 1: assistant with short thinking (no zone expected)
			{Role: RoleAssistant, Raw: "a", Thinking: "short"},
			// msg 2: assistant with long thinking (zone expected, MsgIndex==2)
			{Role: RoleAssistant, Raw: "b", Thinking: longThinking},
			// msg 3: assistant with long thinking (zone expected, MsgIndex==3)
			{Role: RoleAssistant, Raw: "c", Thinking: longThinking},
		},
	}

	_, _, _, thinkingZones := ml.ViewWithHitZones()
	if len(thinkingZones) != 2 {
		t.Fatalf("expected 2 ThinkingHitZones for 2 long-thinking messages, got %d: %+v", len(thinkingZones), thinkingZones)
	}
	if thinkingZones[0].MsgIndex != 2 {
		t.Errorf("first ThinkingHitZone should reference msg index 2, got %d", thinkingZones[0].MsgIndex)
	}
	if thinkingZones[1].MsgIndex != 3 {
		t.Errorf("second ThinkingHitZone should reference msg index 3, got %d", thinkingZones[1].MsgIndex)
	}
	// Zones must not overlap.
	if thinkingZones[0].StartLine >= thinkingZones[0].EndLine {
		t.Errorf("zone 0 has invalid range: %+v", thinkingZones[0])
	}
	if thinkingZones[1].StartLine < thinkingZones[0].EndLine {
		t.Errorf("zones overlap: %+v vs %+v", thinkingZones[0], thinkingZones[1])
	}
}

// TestThinkingClickToExpand_MessageIndexOffset verifies that hit zones and
// expansion state can use indices from the full message slice even when the
// rendered message slice has been shifted by a scrollback cap notice.
func TestThinkingClickToExpand_MessageIndexOffset(t *testing.T) {
	t.Parallel()
	var longLines []string
	for i := range thinkingMaxLines + 2 {
		longLines = append(longLines, "thought "+itoa(i))
	}
	longThinking := strings.Join(longLines, "\n")

	ml := MessageList{
		Width:              80,
		Theme:              styles.DefaultTheme(),
		MessageIndexOffset: 49,
		Messages: []UIMessage{
			{Role: RoleSystem, Raw: "cap notice"},
			{Role: RoleAssistant, Raw: "response", Thinking: longThinking},
		},
		ExpandedThinking: map[int]bool{50: true},
	}

	content, _, _, thinkingZones := ml.ViewWithHitZones()
	plain := stripANSI(content)
	if !strings.Contains(plain, "click to collapse thinking") {
		t.Fatalf("expected expanded thinking looked up by offset message index; got:\n%s", plain)
	}
	if len(thinkingZones) != 1 {
		t.Fatalf("expected 1 ThinkingHitZone, got %d", len(thinkingZones))
	}
	if thinkingZones[0].MsgIndex != 50 {
		t.Fatalf("expected ThinkingHitZone.MsgIndex == 50, got %d", thinkingZones[0].MsgIndex)
	}
}

// TestTruncateThinking_ExpandedAffordance verifies that truncateThinking with
// expanded=true always shows a "click to collapse" affordance regardless of
// line count.
func TestTruncateThinking_ExpandedAffordance(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}

	// Long text expanded.
	var longLines []string
	for i := range thinkingMaxLines + 5 {
		longLines = append(longLines, "line "+itoa(i))
	}
	result := stripANSI(ml.truncateThinking(strings.Join(longLines, "\n"), true))
	if !strings.Contains(result, "click to collapse thinking") {
		t.Errorf("expanded long thinking should have collapse affordance; got:\n%s", result)
	}
	if strings.Contains(result, "click to expand") {
		t.Errorf("expanded long thinking should not have expand affordance; got:\n%s", result)
	}
	// All lines must be visible.
	for i := range thinkingMaxLines + 5 {
		if !strings.Contains(result, "line "+itoa(i)) {
			t.Errorf("expected line %d visible in expanded output; got:\n%s", i, result)
		}
	}
}

// TestTruncateThinking_CollapsedAffordance verifies that truncateThinking with
// expanded=false (collapsed) shows "click to expand" for long thinking.
func TestTruncateThinking_CollapsedAffordance(t *testing.T) {
	t.Parallel()
	ml := MessageList{Width: 80, Theme: styles.DefaultTheme()}

	var longLines []string
	for i := range thinkingMaxLines + 5 {
		longLines = append(longLines, "line "+itoa(i))
	}
	result := stripANSI(ml.truncateThinking(strings.Join(longLines, "\n"), false))
	if !strings.Contains(result, "click to expand") {
		t.Errorf("collapsed long thinking should have expand affordance; got:\n%s", result)
	}
	if strings.Contains(result, "click to collapse") {
		t.Errorf("collapsed long thinking should not have collapse affordance; got:\n%s", result)
	}
}
