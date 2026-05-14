package components

import (
	"strings"
	"testing"
)

// TestCompactionModal_View_NothingToCompact verifies the modal disables [y]
// when MessageCount < 4.
func TestCompactionModal_View_NothingToCompact(t *testing.T) {
	t.Parallel()
	m := CompactionModal{
		Width:        80,
		Height:       24,
		MessageCount: 2,
		ContextPct:   45,
	}
	if !m.NothingToCompact() {
		t.Error("NothingToCompact should be true when MessageCount < 4")
	}
	view := m.View()
	if !strings.Contains(view, "Nothing to compact") {
		t.Errorf("View missing 'Nothing to compact' notice, got:\n%s", view)
	}
	// Should NOT contain the destructive warning when nothing to compact.
	if strings.Contains(view, "[y] compact") {
		t.Errorf("View should NOT show [y] compact when nothing to compact")
	}
}

// TestCompactionModal_View_HasMessages verifies the modal shows message count
// and the confirm/cancel keybind line when there are enough messages.
func TestCompactionModal_View_HasMessages(t *testing.T) {
	t.Parallel()
	m := CompactionModal{
		Width:         80,
		Height:        24,
		MessageCount:  12,
		ContextPct:    84,
		ContextWindow: 200000,
	}
	if m.NothingToCompact() {
		t.Error("NothingToCompact should be false when MessageCount >= 4")
	}
	view := m.View()
	if !strings.Contains(view, "12 messages") {
		t.Errorf("View missing message count, got:\n%s", view)
	}
	if !strings.Contains(view, "[y] compact") {
		t.Errorf("View missing [y] compact keybind, got:\n%s", view)
	}
	if !strings.Contains(view, "84%") {
		t.Errorf("View missing context pct, got:\n%s", view)
	}
}

// TestCompactionModal_View_Toast verifies the optional toast line appears.
func TestCompactionModal_View_Toast(t *testing.T) {
	t.Parallel()
	m := CompactionModal{
		Width:        80,
		Height:       24,
		MessageCount: 5,
		Toast:        "something went wrong",
	}
	view := m.View()
	if !strings.Contains(view, "something went wrong") {
		t.Errorf("View missing toast, got:\n%s", view)
	}
}

// TestCompactionBanner_View_Visible verifies the banner shows when Visible.
func TestCompactionBanner_View_Visible(t *testing.T) {
	t.Parallel()
	b := CompactionBanner{
		Width:   80,
		Visible: true,
		Pct:     84,
	}
	view := b.View()
	if view == "" {
		t.Error("banner View should not be empty when Visible=true")
	}
	if !strings.Contains(view, "84%") {
		t.Errorf("banner View missing usage pct, got: %q", view)
	}
	if !strings.Contains(view, "/compact") {
		t.Errorf("banner View missing /compact hint, got: %q", view)
	}
}

// TestCompactionBanner_View_Hidden verifies the banner returns empty string
// when Visible is false.
func TestCompactionBanner_View_Hidden(t *testing.T) {
	t.Parallel()
	b := CompactionBanner{
		Width:   80,
		Visible: false,
		Pct:     84,
	}
	if got := b.View(); got != "" {
		t.Errorf("banner should return \"\" when hidden, got %q", got)
	}
}

// TestFormatWindowTokens exercises the comma-formatting helper.
func TestFormatWindowTokens(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{200000, "200,000"},
		{1234567, "1,234,567"},
	}
	for _, tc := range cases {
		got := formatWindowTokens(tc.in)
		if got != tc.want {
			t.Errorf("formatWindowTokens(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
