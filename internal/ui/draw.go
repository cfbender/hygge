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

	// Completion palettes float above the editor, covering chat content instead
	// of participating in the left-column flow and pushing the editor down.
	a.drawCompletionPalette(scr, area)

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

	// Draw scroll bar on the left edge of the sidebar when scrolled up.
	if l.sidebarW > 0 && a.userScrolled && !a.msgViewport.AtBottom() {
		a.drawScrollBar(scr, l.sidebar.Min.X)
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

	// Overlay drawn last, over everything.
	var cursor *tea.Cursor
	if top, ok := a.overlays.Top(); ok {
		cursor = a.drawOverlay(scr, a.layout.overlay, top)
	}

	return cursor
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

// drawScrollBar renders a thin scroll position indicator on the first column
// of the sidebar, spanning the full terminal height.
func (a *App) drawScrollBar(scr uv.Screen, x int) {
	h := a.height
	if h < 3 {
		return
	}
	pct := a.msgViewport.ScrollPercent()
	thumbH := max(1, min(3, h/8))
	trackH := h - thumbH
	thumbY := int(float64(trackH) * pct)

	var trackBg, thumbColor color.Color
	if a.styles != nil {
		trackBg = a.styles.SidebarBg
		thumbColor = a.styles.WorkingLabelColor
	}

	for y := range h {
		if y >= thumbY && y < thumbY+thumbH {
			scr.SetCell(x, y, &uv.Cell{
				Content: "▐",
				Style:   uv.Style{Fg: thumbColor, Bg: trackBg},
				Width:   1,
			})
		}
	}
}

// drawSidebar renders the right-side panel.
func (a *App) drawSidebar(scr uv.Screen, area uv.Rectangle) {
	content := a.renderSidebarContent()
	uv.NewStyledString(content).Draw(scr, area)
}

func (a *App) drawCompletionPalette(scr uv.Screen, area uv.Rectangle) {
	if a.viewingSubagent() {
		return
	}
	palette := a.renderCompletionPalette()
	if palette == "" {
		return
	}

	paletteH := lipgloss.Height(palette)
	if paletteH <= 0 {
		return
	}

	// The left column is composed as a flow, so anchor to the editor's actual
	// screen position: immediately above the fixed footer.
	editorTop := area.Max.Y - footerHeight - a.layout.editor.Dy()
	minY := area.Min.Y + headerHeight
	if editorTop <= minY {
		return
	}

	y := max(editorTop-paletteH, minY)

	paletteArea := area
	paletteArea.Max.X = min(area.Min.X+a.layout.leftW, area.Max.X)
	paletteArea.Min.Y = y
	paletteArea.Max.Y = editorTop
	uv.NewStyledString(palette).Draw(scr, paletteArea)
}

// drawOverlay renders the topmost modal/dialog over the full screen.
func (a *App) drawOverlay(scr uv.Screen, area uv.Rectangle, overlay overlayKind) *tea.Cursor {
	content := a.renderOverlayContent(overlay)
	uv.NewStyledString(content).Draw(scr, area)
	return nil
}
