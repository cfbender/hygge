package components

import (
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestSubagentBlockCollapsedRunning(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Model:        "anthropic/claude-haiku-4-5",
		Description:  "Find LICENSE files",
		StartedAt:    now.Add(-4200 * time.Millisecond),
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: now}.View()
	for _, want := range []string{
		"▸",                          // collapsed chevron
		"task[general]",              // type label
		"anthropic/claude-haiku-4-5", // model
		"running",                    // state
		"4.2s",                       // elapsed
		"0 tokens",                   // tokens
		"$0.0000",                    // cost
		`"Find LICENSE files"`,       // description quoted
	} {
		if !strings.Contains(out, want) {
			t.Errorf("collapsed-running missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "│") {
		t.Errorf("collapsed view should NOT contain transcript gutter, got:\n%s", out)
	}
}

func TestSubagentBlockExpandedRunningRendersTranscript(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Model:        "anthropic/claude-haiku-4-5",
		Description:  "find license",
		StartedAt:    now.Add(-time.Second),
		Expanded:     true,
		Messages: []UIMessage{
			{Role: RoleAssistant, Raw: "I'll search for LICENSE files.", IsStreaming: false},
			{Role: RoleTool, ToolName: "grep", Target: "LICENSE", Raw: "./LICENSE:1:MIT"},
		},
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: now}.View()
	for _, want := range []string{
		"▾",                       // expanded chevron
		"I'll search for LICENSE", // assistant message body
		"tool grep: LICENSE",      // tool row
		"./LICENSE:1:MIT",         // tool result body
		"│",                       // gutter
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expanded view missing %q in:\n%s", want, out)
		}
	}
}

func TestSubagentBlockExpandedRunningEmptyTranscriptPlaceholder(t *testing.T) {
	t.Parallel()
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Model:        "anthropic/claude-haiku-4-5",
		Description:  "look",
		StartedAt:    time.Now(),
		Expanded:     true,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme()}.View()
	if !strings.Contains(out, "no output yet") {
		t.Errorf("expected placeholder when expanded with no messages, got:\n%s", out)
	}
}

func TestSubagentBlockCollapsedDoneShowsCostAndTokens(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	end := start.Add(12*time.Second + 400*time.Millisecond)
	st := &SubagentState{
		Type:         "general",
		Model:        "anthropic/claude-haiku-4-5",
		Description:  "done mission",
		StartedAt:    start,
		EndedAt:      end,
		Cost:         0.0041,
		InputTokens:  4_800,
		OutputTokens: 1_000,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme(), Now: end}.View()
	for _, want := range []string{"▸", "done", "12.4s", "5.8k tokens", "$0.0041"} {
		if !strings.Contains(out, want) {
			t.Errorf("done view missing %q in:\n%s", want, out)
		}
	}
}

func TestSubagentBlockHitIterLimitShowsFailedBanner(t *testing.T) {
	t.Parallel()
	st := &SubagentState{
		Type:         "general",
		Model:        "anthropic/claude-haiku-4-5",
		Description:  "looped",
		StartedAt:    time.Now().Add(-30 * time.Second),
		EndedAt:      time.Now(),
		HitIterLimit: true,
	}
	out := SubagentBlock{State: st, Theme: theme.ShellTheme()}.View()
	if !strings.Contains(out, "failed (iteration limit)") {
		t.Errorf("expected failed-banner in iter-limit view, got:\n%s", out)
	}
}

func TestSubagentBlockNilStateReturnsEmpty(t *testing.T) {
	t.Parallel()
	if got := (SubagentBlock{}).View(); got != "" {
		t.Errorf("nil state should render empty, got %q", got)
	}
}

func TestMessageListRendersNestedSubagentBlock(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	st := &SubagentState{
		SubSessionID: "sub-1",
		Type:         "general",
		Model:        "anthropic/claude-haiku-4-5",
		Description:  "find LICENSE",
		StartedAt:    now.Add(-time.Second),
	}
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Now:   now,
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "find LICENSE"},
			{
				Role:       RoleTool,
				ToolName:   "task",
				Target:     "find LICENSE",
				Raw:        "(running…)",
				SubagentID: "sub-1",
			},
		},
		Subagents: map[string]*SubagentState{"sub-1": st},
	}
	out := ml.View()
	for _, want := range []string{
		"▌tool: task",
		"task[general]",
		"running",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("messagelist nested view missing %q in:\n%s", want, out)
		}
	}
}

func TestMessageListNoNestedWhenSubagentIDMissing(t *testing.T) {
	t.Parallel()
	ml := MessageList{
		Width: 100,
		Theme: theme.ShellTheme(),
		Messages: []UIMessage{
			{Role: RoleTool, ToolName: "task", Raw: "(running…)"},
		},
		// no Subagents map
	}
	out := ml.View()
	if strings.Contains(out, "task[") {
		t.Errorf("expected no nested block when SubagentID empty, got:\n%s", out)
	}
}

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
