package anim

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

func TestAnimRenderWidth(t *testing.T) {
	t.Parallel()
	a := New(Settings{Width: 8, Theme: styles.DefaultTheme()})
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
	a := New(Settings{Width: 6, Theme: styles.DefaultTheme()})
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
	a := New(Settings{Width: 4, Theme: styles.DefaultTheme()})
	if len(a.frames) != defaultFrameCount {
		t.Errorf("expected %d pre-rendered frames, got %d", defaultFrameCount, len(a.frames))
	}
}

// TestAnimDistinctIDs verifies that successive calls to New() always produce
// Anims with distinct IDs.  This is the regression test for the bug where
// fmt.Sprintf("anim-%p", &opts) could produce the same ID for two consecutive
// calls when the Go runtime reused the same stack slot — causing one Anim to
// steal the other's StepMsg and freeze its animation.
func TestAnimDistinctIDs(t *testing.T) {
	t.Parallel()

	const n = 10
	ids := make(map[string]bool, n)
	for i := range n {
		a := New(Settings{Width: 4, Theme: nil})
		if ids[a.id] {
			t.Fatalf("duplicate Anim ID %q on iteration %d", a.id, i)
		}
		ids[a.id] = true
	}
}

// TestAnimNoIDCrossTrigger verifies that two Anims created in sequence do not
// steal each other's StepMsg.  This is the concrete failure mode: if both anims
// share an ID, the first anim's Update consumes the second anim's StepMsg
// (returning a non-nil Cmd) and the second anim never advances.
func TestAnimNoIDCrossTrigger(t *testing.T) {
	t.Parallel()

	a1 := New(Settings{Width: 4, Theme: nil})
	a2 := New(Settings{Width: 4, Theme: nil})

	if a1.id == a2.id {
		t.Fatalf("a1 and a2 share ID %q; IDs must be unique", a1.id)
	}

	// A StepMsg for a2 must NOT be consumed by a1.
	_, cmd := a1.Update(StepMsg{ID: a2.id})
	if cmd != nil {
		t.Errorf("a1 consumed a2's StepMsg (IDs: a1=%q a2=%q); cross-trigger detected", a1.id, a2.id)
	}

	// A StepMsg for a1 must NOT be consumed by a2.
	_, cmd = a2.Update(StepMsg{ID: a1.id})
	if cmd != nil {
		t.Errorf("a2 consumed a1's StepMsg (IDs: a1=%q a2=%q); cross-trigger detected", a1.id, a2.id)
	}

	// Each anim must respond to its own StepMsg.
	_, cmd = a1.Update(StepMsg{ID: a1.id})
	if cmd == nil {
		t.Error("a1 did not consume its own StepMsg")
	}
	_, cmd = a2.Update(StepMsg{ID: a2.id})
	if cmd == nil {
		t.Error("a2 did not consume its own StepMsg")
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
