package anim

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestAnimRenderWidth(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 8, Theme: theme.ShellTheme()})
	got := a.Render()
	// Strip ANSI escapes by counting visible rune-count is non-trivial, but
	// we know Render() returns exactly Width styled characters, so the raw
	// string must be non-empty and shorter than some generous upper bound.
	if len(got) == 0 {
		t.Error("Render() returned empty string")
	}
	// Rendered width must cover at least Width runes (styled, so longer in bytes).
	if len([]rune(stripANSI(got))) != 8 {
		t.Errorf("stripped Render() rune count = %d, want 8; raw=%q", len([]rune(stripANSI(got))), got)
	}
}

func TestAnimFramesCycle(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 6, Theme: theme.ShellTheme()})
	first := a.Render()
	// Advance by advancing frame directly.
	_, cmd := a.Update(StepMsg{ID: a.id})
	if cmd == nil {
		t.Error("Update(StepMsg) should return a next-tick Cmd")
	}
	second := a.Render()
	if first == second {
		t.Errorf("Render() did not change after advancing frame; both = %q", first)
	}
}

func TestAnimNilThemeFallback(t *testing.T) {
	t.Parallel()
	// Should not panic; uses built-in fallback colors.
	a := New(Settings{Width: 6, Theme: nil})
	got := a.Render()
	if len(got) == 0 {
		t.Error("expected non-empty Render() with nil theme")
	}
}

func TestAnimWrongIDIgnored(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 4, Theme: nil})
	before := a.frame
	_, cmd := a.Update(StepMsg{ID: "not-my-id"})
	if cmd != nil {
		t.Error("Update with wrong ID must return nil Cmd")
	}
	if a.frame != before {
		t.Error("Update with wrong ID must not advance frame")
	}
}

func TestAnimNonStepMsgIgnored(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 4, Theme: nil})
	before := a.frame
	_, cmd := a.Update(tea.KeyPressMsg{Code: 'x'})
	if cmd != nil {
		t.Error("Update with non-StepMsg must return nil Cmd")
	}
	if a.frame != before {
		t.Error("Update with non-StepMsg must not advance frame")
	}
}

func TestAnimStartReturnsCmd(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 4, Theme: nil})
	cmd := a.Start()
	if cmd == nil {
		t.Error("Start() must return a non-nil tea.Cmd")
	}
}

func TestAnimPreRenderedFrameCount(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 4, Theme: theme.ShellTheme()})
	if len(a.frames) != defaultFrameCount {
		t.Errorf("expected %d pre-rendered frames, got %d", defaultFrameCount, len(a.frames))
	}
}

// stripANSI is a minimal ANSI escape stripper for testing.
// It removes CSI sequences of the form ESC[...m.
func stripANSI(s string) string {
	var out strings.Builder
	runes := []rune(s)
	i := 0
	for i < len(runes) {
		if runes[i] == '\x1b' && i+1 < len(runes) && runes[i+1] == '[' {
			// Skip until 'm' (SGR terminator).
			i += 2
			for i < len(runes) && runes[i] != 'm' {
				i++
			}
			i++ // skip 'm'
			continue
		}
		out.WriteRune(runes[i])
		i++
	}
	return out.String()
}
