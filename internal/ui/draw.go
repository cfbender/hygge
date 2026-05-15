package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
)

// Draw renders the entire UI to a screen buffer. The left column (chat,
// chrome, editor, footer) is composed as a single content flow, then the
// sidebar and overlays are drawn into their own regions.
func (a *App) Draw(scr uv.Screen, area uv.Rectangle) *tea.Cursor {
	l := a.layout

	// Fill every cell with the theme background so no terminal default
	// bleeds through (works even in multiplexers like Zellij/tmux that
	// may not forward OSC background-color escapes).
	if a.styles != nil && a.styles.Background != nil {
		bgCell := &uv.Cell{
			Content: " ",
			Style:   uv.Style{Bg: a.styles.Background},
			Width:   1,
		}
		for y := area.Min.Y; y < area.Max.Y; y++ {
			for x := area.Min.X; x < area.Max.X; x++ {
				scr.SetCell(x, y, bgCell)
			}
		}
	}

	// Compose the left column as a content flow.
	leftContent := a.renderLeftColumn()
	leftArea := area
	leftArea.Max.X = l.leftW
	uv.NewStyledString(leftContent).Draw(scr, leftArea)

	// Draw sidebar into the right column.
	if l.sidebarW > 0 {
		a.drawSidebar(scr, l.sidebar)
	}

	// Apply text selection highlight (reverse video on selected cells).
	a.applyHighlight(scr, area)

	// Toast notification floats below the header in the top-left.
	if toastStr := a.renderToast(); toastStr != "" {
		toastW := lipgloss.Width(toastStr)
		toastH := lipgloss.Height(toastStr)
		toastArea := area
		toastArea.Min.Y = area.Min.Y + headerHeight
		toastArea.Max.Y = toastArea.Min.Y + toastH
		toastArea.Max.X = toastArea.Min.X + toastW
		uv.NewStyledString(toastStr).Draw(scr, toastArea)
	}

	// Overlays paint last, over everything.
	if top, ok := a.overlays.Top(); ok {
		return a.drawOverlay(scr, l.overlay, top)
	}

	return nil
}

// renderLeftColumn composes chat + chrome + editor + footer into a single
// content string for the left column.
func (a *App) renderLeftColumn() string {
	var sections []string

	// Branded header bar.
	if header := a.renderHeaderContent(); header != "" {
		sections = append(sections, header)
	}

	// Chat viewport.
	sections = append(sections, a.renderChatContent())

	// Breathing room between messages and editor.
	sections = append(sections, "")

	// Chrome elements (pills, palette, banners, notices).
	if chrome := a.renderChromeContent(); chrome != "" {
		sections = append(sections, chrome)
	}

	// Editor input (hidden when viewing a subagent transcript).
	if !a.viewingSubagent() {
		sections = append(sections, a.input.View())
	}

	// Footer.
	sections = append(sections, a.renderFooterContent())

	return strings.Join(sections, "\n")
}

// drawSidebar renders the right-side panel.
func (a *App) drawSidebar(scr uv.Screen, area uv.Rectangle) {
	content := a.renderSidebarContent()
	uv.NewStyledString(content).Draw(scr, area)
}

// drawOverlay renders the topmost modal/dialog over the full screen.
func (a *App) drawOverlay(scr uv.Screen, area uv.Rectangle, overlay overlayKind) *tea.Cursor {
	content := a.renderOverlayContent(overlay)
	uv.NewStyledString(content).Draw(scr, area)
	return nil
}
