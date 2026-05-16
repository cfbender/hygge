package ui

import (
	"image/color"
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

	// Draw sidebar into the right column with its own background.
	if l.sidebarW > 0 {
		if a.styles != nil && a.styles.SidebarBg != nil {
			bgCell := &uv.Cell{
				Content: " ",
				Style:   uv.Style{Bg: a.styles.SidebarBg},
				Width:   1,
			}
			for y := l.sidebar.Min.Y; y < l.sidebar.Max.Y; y++ {
				for x := l.sidebar.Min.X; x < l.sidebar.Max.X; x++ {
					scr.SetCell(x, y, bgCell)
				}
			}
		}
		a.drawSidebar(scr, l.sidebar)
	}

	// Draw scroll bar on the left edge of the sidebar (or right edge of chat).
	if l.sidebarW > 0 && a.userScrolled && !a.msgViewport.AtBottom() {
		a.drawScrollBar(scr, l.sidebar.Min.X, headerHeight, l.chat.Dy())
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

// drawScrollBar renders a thin scroll position indicator on a single column.
func (a *App) drawScrollBar(scr uv.Screen, x, startY, height int) {
	if height < 3 {
		return
	}
	pct := a.msgViewport.ScrollPercent()
	thumbH := max(1, height/5)
	trackH := height - thumbH
	thumbY := startY + int(float64(trackH)*pct)

	var trackColor, thumbColor color.Color
	if a.styles != nil {
		trackColor = a.styles.Background
		thumbColor = a.styles.WorkingLabelColor
	}

	for y := startY; y < startY+height; y++ {
		content := "│"
		fg := trackColor
		if y >= thumbY && y < thumbY+thumbH {
			content = "┃"
			fg = thumbColor
		}
		scr.SetCell(x, y, &uv.Cell{
			Content: content,
			Style:   uv.Style{Fg: fg},
			Width:   1,
		})
	}
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
