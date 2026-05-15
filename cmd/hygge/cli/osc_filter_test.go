package cli

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestDropOSCResponses_DropsLeakedBackgroundQuery verifies that the canonical
// OSC 11 background-color response leaked by bubbletea v2.0.6 is suppressed.
func TestDropOSCResponses_DropsLeakedBackgroundQuery(t *testing.T) {
	t.Parallel()
	msg := dropOSCResponses(nil, tea.KeyPressMsg{Text: "11;rgb:1818/0808/1010"})
	if msg != nil {
		t.Errorf("expected nil for OSC 11 background query, got %T(%v)", msg, msg)
	}
}

// TestDropOSCResponses_DropsOSC10ForegroundQuery verifies that an OSC 10
// foreground response is also suppressed.
func TestDropOSCResponses_DropsOSC10ForegroundQuery(t *testing.T) {
	t.Parallel()
	msg := dropOSCResponses(nil, tea.KeyPressMsg{Text: "10;rgb:ffff/ffff/ffff"})
	if msg != nil {
		t.Errorf("expected nil for OSC 10 foreground query, got %T(%v)", msg, msg)
	}
}

// TestDropOSCResponses_DropsShortHexForm verifies that the 2-digit hex variant
// of an OSC response is also suppressed.
func TestDropOSCResponses_DropsShortHexForm(t *testing.T) {
	t.Parallel()
	msg := dropOSCResponses(nil, tea.KeyPressMsg{Text: "11;rgb:18/08/10"})
	if msg != nil {
		t.Errorf("expected nil for short-hex OSC response, got %T(%v)", msg, msg)
	}
}

// TestDropOSCResponses_PassesRegularText verifies that ordinary user text is
// not filtered.
func TestDropOSCResponses_PassesRegularText(t *testing.T) {
	t.Parallel()
	in := tea.KeyPressMsg{Text: "regular text"}
	msg := dropOSCResponses(nil, in)
	if msg != in {
		t.Errorf("expected original msg to pass through, got %T(%v)", msg, msg)
	}
}

// TestDropOSCResponses_PassesRGBSubstring ensures that a string containing
// "rgb:" but NOT matching the full "^<digits>;rgb:<hex>/..." pattern is not
// filtered (no false positives on partial matches).
func TestDropOSCResponses_PassesRGBSubstring(t *testing.T) {
	t.Parallel()
	// Not anchored at start with digits — should NOT be dropped.
	in := tea.KeyPressMsg{Text: "color is rgb:ff/ff/ff here"}
	msg := dropOSCResponses(nil, in)
	if msg != in {
		t.Errorf("expected partial-match text to pass through, got %T(%v)", msg, msg)
	}
}

// TestDropOSCResponses_PassesWindowSizeMsg verifies that non-KeyPressMsg
// messages (e.g. tea.WindowSizeMsg) are passed through unchanged.
func TestDropOSCResponses_PassesWindowSizeMsg(t *testing.T) {
	t.Parallel()
	in := tea.WindowSizeMsg{Width: 80, Height: 24}
	msg := dropOSCResponses(nil, in)
	if msg != in {
		t.Errorf("expected WindowSizeMsg to pass through, got %T(%v)", msg, msg)
	}
}

func TestInputEventFilter_DropsOSCResponse(t *testing.T) {
	t.Parallel()
	filter := inputEventFilter(func() time.Time { return time.Unix(0, 0) })
	msg := filter(nil, tea.KeyPressMsg{Text: "11;rgb:1818/0808/1010"})
	if msg != nil {
		t.Errorf("expected nil for OSC response, got %T(%v)", msg, msg)
	}
}

func TestInputEventFilter_ThrottlesMouseWheelSpam(t *testing.T) {
	t.Parallel()
	now := time.Unix(100, 0)
	filter := inputEventFilter(func() time.Time { return now })
	first := tea.MouseWheelMsg(tea.Mouse{X: 1, Y: 2, Button: tea.MouseWheelDown})
	if got := filter(nil, first); got != first {
		t.Fatalf("first mouse wheel msg should pass, got %T(%v)", got, got)
	}
	now = now.Add(10 * time.Millisecond)
	second := tea.MouseWheelMsg(tea.Mouse{X: 1, Y: 3, Button: tea.MouseWheelDown})
	if got := filter(nil, second); got != nil {
		t.Fatalf("second mouse wheel msg inside throttle should drop, got %T(%v)", got, got)
	}
	now = now.Add(15 * time.Millisecond)
	third := tea.MouseMotionMsg(tea.Mouse{X: 1, Y: 4})
	if got := filter(nil, third); got != third {
		t.Fatalf("mouse motion after throttle window should pass, got %T(%v)", got, got)
	}
}
