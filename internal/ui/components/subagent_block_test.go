package components

import (
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// TestSubagentBlockDoneState verifies the DONE compact layout.
func TestSubagentBlockDoneState(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := start.Add(4*time.Second + 200*time.Millisecond)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "ping test",
		StartedAt:    start,
		EndedAt:      end,
		Cost:         0.0042,
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read"},
			{Role: RoleTool, ToolName: "bash"},
			{Role: RoleTool, ToolName: "grep"},
		},
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: end}.View()
	for _, want := range []string{
		"General Subagent \u2014 ping test", // heading with em dash
		"3 toolcalls",                       // tool count
		"4.2s",                              // duration
		"$0.0042",                           // cost
		"ctrl+g",                            // hint
		"view subagent",                     // hint label
	} {
		if !strings.Contains(out, want) {
			t.Errorf("DONE view missing %q in:\n%s", want, out)
		}
	}
	// Must NOT contain the │ gutter (the outer bubble border now provides containment).
	if strings.Contains(out, "\u2502") {
		t.Errorf("DONE view must not contain │ gutter (double-border); got:\n%s", out)
	}
	// Must NOT contain the old format strings.
	for _, bad := range []string{
		"subagent[general]",
		"\u25b8", // ▸ old chevron
		"running",
		"tokens",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("DONE view should not contain %q in:\n%s", bad, out)
		}
	}
}

// TestSubagentBlockRunningState verifies the RUNNING compact layout.
func TestSubagentBlockRunningState(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "ping test",
		StartedAt:    now.Add(-2 * time.Second),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "read", Target: "internal/ui/app.go"},
		},
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: now}.View()
	for _, want := range []string{
		"General Subagent \u2014 ping test",
		"read internal/ui/app.go", // latest tool label
		"ctrl+g",
		"view subagent",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RUNNING view missing %q in:\n%s", want, out)
		}
	}
}

// TestSubagentBlockRunningNoToolsPlaceholder verifies "working…" when no tool calls yet.
func TestSubagentBlockRunningNoToolsPlaceholder(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "go",
		StartedAt:    now.Add(-time.Second),
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: now}.View()
	if !strings.Contains(out, "working") {
		t.Errorf("RUNNING with no tools should show working placeholder; got:\n%s", out)
	}
}

// TestSubagentBlockFailedState verifies the FAILED (HitIterLimit) layout.
func TestSubagentBlockFailedState(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := start.Add(4*time.Second + 200*time.Millisecond)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "ping test",
		StartedAt:    start,
		EndedAt:      end,
		HitIterLimit: true,
		Cost:         0.0042,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: end}.View()
	for _, want := range []string{
		"General Subagent \u2014 ping test",
		"failed (iteration limit)",
		"4.2s",
		"$0.0042",
		"ctrl+g",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FAILED view missing %q in:\n%s", want, out)
		}
	}
}

// TestSubagentBlockEmptyDescriptionFallback verifies no em-dash when no description.
func TestSubagentBlockEmptyDescriptionFallback(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		StartedAt:    start,
		EndedAt:      end,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: end}.View()
	if !strings.Contains(out, "General Subagent") {
		t.Errorf("empty-description view should contain 'General Subagent'; got:\n%s", out)
	}
	if strings.Contains(out, "\u2014") { // em dash should be absent
		t.Errorf("empty-description view should NOT contain em dash; got:\n%s", out)
	}
}

// TestSubagentBlockLongDescriptionTruncated verifies truncation with ellipsis.
func TestSubagentBlockLongDescriptionTruncated(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	longDesc := strings.Repeat("x", 200)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  longDesc,
		StartedAt:    start,
		EndedAt:      end,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Width: 60, Now: end}.View()
	if !strings.Contains(out, "\u2026") { // ellipsis
		t.Errorf("long description should be truncated with ellipsis; got:\n%s", out)
	}
}

// TestSubagentBlockNilStateReturnsEmpty verifies nil state renders empty.
func TestSubagentBlockNilStateReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := (SubagentBlock{}).View(); got != "" {
		t.Errorf("nil state should render empty, got %q", got)
	}
}

// TestMessageListSubagentWithSubagentRendersBlockOnly verifies that a subagent tool
// message with SubagentID renders ONLY the subagent block (no "▌tool: subagent" gutter).
func TestMessageListSubagentWithSubagentRendersBlockOnly(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	start := now.Add(-time.Second)
	end := now

	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Description:  "find LICENSE",
		StartedAt:    start,
		EndedAt:      end,
		Cost:         0.001,
		Messages:     []UIMessage{{Role: RoleTool, ToolName: "grep"}},
	}
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Now:   now,
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "find LICENSE"},
			{
				Role:       RoleTool,
				ToolName:   "subagent",
				Target:     "find LICENSE",
				Raw:        "(running…)",
				SubagentID: "sub-1",
			},
		},
		Subagents: map[string]*SubagentState{"sub-1": st},
	}
	out := ml.View()

	// Must contain the new compact block format.
	for _, want := range []string{
		"General Subagent \u2014 find LICENSE",
		"1 toolcalls",
		"ctrl+g",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("messagelist nested view missing %q in:\n%s", want, out)
		}
	}
	// Must NOT contain the old format.
	for _, bad := range []string{
		"\u25cctools: subagent", // ▌tool: subagent
		"subagent[general]",
	} {
		if strings.Contains(out, bad) {
			t.Errorf("messagelist should not render %q for subagent+SubagentID; got:\n%s", bad, out)
		}
	}
	// Must NOT contain "▌tool: subagent" specifically (check the actual gutter prefix).
	if strings.Contains(out, "\u258ctool: subagent") {
		t.Errorf("messagelist should not render '▌tool: subagent' for subagent+SubagentID; got:\n%s", out)
	}
}

// TestMessageListSubagentWithSubagentNoGutter: ensure the "▌tool: subagent" gutter is absent.
func TestMessageListSubagentWithSubagentNoGutter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		StartedAt:    now.Add(-time.Second),
		EndedAt:      now,
	}
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Now:   now,
		Messages: []UIMessage{
			{
				Role:       RoleTool,
				ToolName:   "subagent",
				Raw:        "",
				SubagentID: "sub-1",
			},
		},
		Subagents: map[string]*SubagentState{"sub-1": st},
	}
	out := ml.View()
	// The old "▌tool: subagent" gutter must not appear.
	if strings.Contains(out, "tool: subagent") {
		t.Errorf("subagent+subagent row must not render 'tool: subagent' gutter; got:\n%s", out)
	}
}

// TestMessageListNoNestedWhenSubagentIDMissing verifies non-subagent tools render normally.
func TestMessageListNoNestedWhenSubagentIDMissing(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "subagent", Raw: "(running…)"},
		},
		// no Subagents map, no SubagentID
	}
	out := ml.View()
	// A plain subagent tool message (no SubagentID) renders the regular gutter.
	if strings.Contains(out, "subagent[") {
		t.Errorf("expected no nested block when SubagentID empty, got:\n%s", out)
	}
	// The regular gutter should still appear.
	if !strings.Contains(out, "tool: subagent") {
		t.Errorf("plain subagent tool message should still render gutter; got:\n%s", out)
	}
}

// TestFormatElapsedRanges ensures formatElapsed covers the spec cases.
func TestFormatElapsedRanges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Millisecond, "500ms"},
		{1500 * time.Millisecond, "1.5s"},
		{45 * time.Second, "45.0s"},
		{61 * time.Second, "1m01s"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
	}
	for _, tc := range cases {
		if got := formatElapsed(tc.d); got != tc.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// TestFormatSubagentTokensRanges retains coverage of the helper.
func TestFormatSubagentTokensRanges(t *testing.T) {
	t.Parallel()
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 tokens"},
		{42, "42 tokens"},
		{1234, "1.2k tokens"},
		{5_800, "5.8k tokens"},
		{2_500_000, "2.5M tokens"},
	}
	for _, tc := range cases {
		if got := formatSubagentTokens(tc.n); got != tc.want {
			t.Errorf("formatSubagentTokens(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
