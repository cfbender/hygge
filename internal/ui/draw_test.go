package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/ui/components"
)

// Every visible cell must end up with a background after a full draw pass, so
// the theme shows even in multiplexers that swallow OSC background escapes.
// StyledString.Draw clears regions to unstyled cells before printing content,
// so this only holds if the background is filled in after content draws.
func TestDrawLeavesNoUnstyledBackground(t *testing.T) {
	app := drawTestApp(t, 200, 60, 6)
	app.View()
	buf := app.lastCanvas

	unstyled := 0
	for y := range 60 {
		for x := range 200 {
			c := buf.CellAt(x, y)
			if c == nil || c.Style.Bg == nil {
				unstyled++
			}
		}
	}
	if unstyled != 0 {
		t.Fatalf("%d of %d cells have no background after Draw", unstyled, 200*60)
	}
}

// Sidebar gaps must read as part of the sidebar panel, not the chat column.
func TestDrawSidebarGapsUseSidebarBackground(t *testing.T) {
	app := drawTestApp(t, 200, 60, 2)
	if app.styles.SidebarBg == nil {
		t.Skip("theme has no distinct sidebar background")
	}
	app.View()
	buf := app.lastCanvas

	sb := app.layout.sidebar
	if sb.Dx() <= 0 {
		t.Fatal("expected a sidebar region at 200 columns")
	}
	for y := sb.Min.Y; y < sb.Max.Y; y++ {
		for x := sb.Min.X; x < sb.Max.X; x++ {
			c := buf.CellAt(x, y)
			if c == nil || c.Style.Bg == nil {
				t.Fatalf("sidebar cell (%d,%d) has no background", x, y)
			}
		}
	}
}

func drawTestApp(t *testing.T, w, h, msgs int) *App {
	t.Helper()
	app, _ := newTestApp(t)
	for i := range msgs {
		role := components.RoleUser
		if i%2 == 1 {
			role = components.RoleAssistant
		}
		app.messages = append(app.messages, uiMessage{Role: role, Raw: "hello world, a reasonably long line of text"})
	}
	app.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return app
}
