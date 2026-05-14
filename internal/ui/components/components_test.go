package components

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestStatusBarRendersIdentity(t *testing.T) {
	t.Parallel()
	sb := StatusBar{
		Profile:  "work",
		Provider: "anthropic",
		Model:    "claude-sonnet-4.5",
		Pwd:      "~/proj",
		Width:    80,
		Theme:    theme.ShellTheme(),
	}
	out := sb.View()
	for _, want := range []string{"[profile:work]", "anthropic/claude-sonnet-4.5", "~/proj"} {
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
			{Role: RoleAssistant, Raw: "hi back"},
			{Role: RoleTool, ToolName: "read", Target: "/tmp/x", Raw: "line1\nline2"},
		},
	}
	out := ml.View()
	for _, want := range []string{"▌user", "hello", "▌assistant", "hi back", "▌tool: read", "/tmp/x", "line1", "line2"} {
		if !strings.Contains(out, want) {
			t.Errorf("messagelist missing %q in:\n%s", want, out)
		}
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
			{Role: RoleTool, ToolName: "read", Raw: strings.Join(lines, "\n")},
		},
	}
	out := ml.View()
	if !strings.Contains(out, "line4") {
		t.Errorf("expected line4 (within first 5 lines) in output:\n%s", out)
	}
	if strings.Contains(out, "line5\n") || strings.Contains(out, "line6") {
		t.Errorf("expected lines beyond limit to be hidden, found in:\n%s", out)
	}
	if !strings.Contains(out, "more lines") {
		t.Errorf("expected expansion hint in:\n%s", out)
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

func TestFooterFormatsCostAndContext(t *testing.T) {
	t.Parallel()
	f := Footer{
		Width:   80,
		Theme:   theme.ShellTheme(),
		Cost:    0.0123,
		UsedTok: 24800,
		MaxTok:  200000,
		PctUsed: 24800.0 / 200000.0,
	}
	out := f.View()
	for _, want := range []string{"$0.0123", "ctx ", "24.8k", "200.0k", "enter send"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q in:\n%s", want, out)
		}
	}
}

func TestFooterSeverityColoring(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pct      float64
		wantAtom theme.Atom
	}{
		{0.5, theme.AtomSuccess},
		{0.75, theme.AtomWarn},
		{0.95, theme.AtomError},
	}
	for _, tc := range cases {
		f := Footer{Width: 80, Theme: theme.ShellTheme(), MaxTok: 100000, UsedTok: int64(100000 * tc.pct), PctUsed: tc.pct}
		if got := f.SeverityAtom(); got != tc.wantAtom {
			t.Errorf("pct=%.2f: got severity atom %q, want %q", tc.pct, got, tc.wantAtom)
		}
	}
}

func TestFooterBusyChangesHints(t *testing.T) {
	t.Parallel()
	f := Footer{Width: 80, Theme: theme.ShellTheme(), MaxTok: 1000, PctUsed: 0.1, Busy: true}
	out := f.View()
	if !strings.Contains(out, "enter blocked") {
		t.Errorf("busy footer should show 'enter blocked', got:\n%s", out)
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
