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
	if !strings.Contains(out, "no messages") {
		t.Errorf("expected empty hint, got:\n%s", out)
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
