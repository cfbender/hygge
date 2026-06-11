package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func newTestQuitOverlay() *quitOverlay {
	st := &overlayStyles{
		quitSelected: lipgloss.NewStyle().Bold(true),
		quitNormal:   lipgloss.NewStyle(),
		quitBgPad:    lipgloss.NewStyle(),
		quitQuestion: lipgloss.NewStyle(),
		quitBox:      lipgloss.NewStyle(),
	}
	return &quitOverlay{selectedNo: true, styles: func() *overlayStyles { return st }}
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
	}
}

func TestQuitOverlayConfirmKeysQuit(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"y", "Y", "ctrl+c"} {
		o := newTestQuitOverlay()
		cmd, done := o.Update(key(k))
		if !done {
			t.Errorf("%q: done = false, want true", k)
		}
		if cmd == nil {
			t.Errorf("%q: cmd = nil, want tea.Quit", k)
		}
	}
}

func TestQuitOverlayDismissKeysClose(t *testing.T) {
	t.Parallel()
	for _, k := range []string{"n", "N", "esc"} {
		o := newTestQuitOverlay()
		cmd, done := o.Update(key(k))
		if !done {
			t.Errorf("%q: done = false, want true", k)
		}
		if cmd != nil {
			t.Errorf("%q: cmd = %v, want nil", k, cmd)
		}
	}
}

func TestQuitOverlayToggleAndEnter(t *testing.T) {
	t.Parallel()
	o := newTestQuitOverlay()

	// Default is "no": enter closes without quitting.
	cmd, done := o.Update(key("enter"))
	if !done || cmd != nil {
		t.Fatalf("enter on no: done=%v cmd=%v, want done=true cmd=nil", done, cmd)
	}

	// Toggle to "yes": enter quits.
	o = newTestQuitOverlay()
	if _, done := o.Update(key("left")); done {
		t.Fatal("toggle should not close the overlay")
	}
	if o.selectedNo {
		t.Fatal("toggle did not move selection to yes")
	}
	cmd, done = o.Update(key("enter"))
	if !done || cmd == nil {
		t.Fatalf("enter on yes: done=%v cmd=%v, want done=true cmd=tea.Quit", done, cmd)
	}
}

func TestQuitOverlayIgnoresUnboundKeys(t *testing.T) {
	t.Parallel()
	o := newTestQuitOverlay()
	cmd, done := o.Update(key("x"))
	if done || cmd != nil {
		t.Fatalf("unbound key: done=%v cmd=%v, want done=false cmd=nil", done, cmd)
	}
	if !o.selectedNo {
		t.Fatal("unbound key changed selection")
	}
}

func TestQuitOverlayViewCentersDialog(t *testing.T) {
	t.Parallel()
	o := newTestQuitOverlay()
	out := o.View(80, 24)
	if !strings.Contains(out, "Are you sure you want to quit?") {
		t.Fatalf("view missing question:\n%s", out)
	}
	if !strings.Contains(out, "yeah") || !strings.Contains(out, "nah") {
		t.Fatalf("view missing buttons:\n%s", out)
	}
}

// App-level integration: ctrl+c opens the overlay with "no" selected, keys
// route to it, and closing restores typing focus.
func TestCtrlCOpensQuitOverlayAndEscCloses(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)

	app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if top, ok := app.overlays.Top(); !ok || top != overlayQuit {
		t.Fatalf("overlay top = %v, %v; want quit overlay", top, ok)
	}
	if !app.quitConfirm.selectedNo {
		t.Fatal("quit overlay should default to no")
	}

	// Toggle selection through the App's key routing.
	app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if app.quitConfirm.selectedNo {
		t.Fatal("left key did not route to the quit overlay")
	}

	app.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if app.overlays.Has(overlayQuit) {
		t.Fatal("esc should close the quit overlay")
	}

	// Second ctrl+c after reopening quits immediately.
	app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+c with quit overlay open should return tea.Quit")
	}
}
