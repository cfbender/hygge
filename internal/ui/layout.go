package ui

import (
	"image"

	"github.com/charmbracelet/ultraviolet/layout"
)

// uiLayout holds the pre-computed screen regions for a single frame.
// Regions are calculated once per resize (or height change) and reused
// for all drawing operations, preventing layout thrashing.
type uiLayout struct {
	// Overall terminal area.
	area image.Rectangle

	// Main content regions.
	chat    image.Rectangle
	editor  image.Rectangle
	footer  image.Rectangle
	sidebar image.Rectangle

	// Overlay regions (dialogs draw to the full area).
	overlay image.Rectangle

	// Derived state.
	compact     bool // sidebar hidden (narrow terminal)
	sidebarW    int
	leftW       int
	msgContentW int // word-wrap width for message content
}

const (
	sidebarMinTermWidth = 110
	sidebarFixedWidth   = 40
	contentWidthRatio   = 0.80
	editorMinHeight     = 3
	editorMaxHeight     = 8
	footerHeight        = 1
	headerHeight        = 1
	chatBottomPadding   = 1 // breathing room between messages and editor

)

func sidebarWidthForTerminal(w int) int {
	if w >= sidebarMinTermWidth {
		return sidebarFixedWidth
	}
	return 0
}

func msgContentWidthForLeft(leftW int) int {
	w := int(float64(leftW)*contentWidthRatio) - 3
	if w < 1 {
		return 1
	}
	return w
}

func inputWidthForLeft(leftW int) int {
	if leftW < 1 {
		return 1
	}
	return leftW
}

// generateLayout computes all screen regions from the terminal dimensions
// and dynamic element heights.
func (a *App) generateLayout(w, h int) uiLayout {
	if w <= 0 {
		w = 80
	}
	if h <= 0 {
		h = 24
	}

	area := image.Rect(0, 0, w, h)
	l := uiLayout{
		area:    area,
		overlay: area,
	}

	// Horizontal split: the main viewport fills remaining space; the sidebar is
	// fixed width when visible and zero width on narrow terminals.
	l.sidebarW = sidebarWidthForTerminal(w)
	var mainArea, sidebarArea image.Rectangle
	layout.Horizontal(
		layout.Fill(1),
		layout.Len(l.sidebarW),
	).WithFlex(layout.FlexStart).Split(area).Assign(&mainArea, &sidebarArea)

	l.compact = l.sidebarW == 0
	l.leftW = mainArea.Dx()
	l.sidebar = sidebarArea

	// Content wrap width: 80% of left column minus padding.
	l.msgContentW = msgContentWidthForLeft(l.leftW)

	// Compute dynamic chrome heights. The editor is clamped to its desired
	// 3–8 row box height; the chat segment below uses Fill(1) to absorb all
	// remaining vertical space.
	editorH := a.editorHeight()
	footerH := footerHeight
	if a.splashActive() {
		editorH = 0
	}
	pillsH := a.pillsHeight()
	bannerH := a.bannerHeight()
	noticeH := a.noticeHeight()
	paletteH := a.paletteHeight()

	// Vertical split: chat | [banner] | [palette] | editor | [pills] | [notice] | footer
	// We use the layout engine for the main left column.
	var chatRect, editorRect, footerRect image.Rectangle
	chatAndFixedArea := mainArea
	chatAndFixedArea.Max.Y = chatAndFixedArea.Min.Y + h - headerHeight - chatBottomPadding - pillsH - bannerH - noticeH - paletteH
	if chatAndFixedArea.Dy() < 1 {
		chatAndFixedArea.Max.Y = chatAndFixedArea.Min.Y + 1
	}

	layout.Vertical(
		layout.Fill(1),
		layout.Len(editorH),
		layout.Len(footerH),
	).WithFlex(layout.FlexStart).Split(chatAndFixedArea).Assign(&chatRect, &editorRect, &footerRect)

	l.chat = chatRect
	l.editor = editorRect
	l.footer = footerRect

	return l
}

// editorHeight returns the current editor height in rows.
// Returns 0 when viewing a subagent (input is hidden).
func (a *App) editorHeight() int {
	if a.viewingSubagent() {
		return 0
	}
	h := a.input.Textarea.Height()
	if a.styles != nil {
		h += 2 // border top + bottom
	}
	if h < editorMinHeight {
		return editorMinHeight
	}
	if h > editorMaxHeight {
		return editorMaxHeight
	}
	return h
}

// pillsHeight returns the height needed for status pills.
func (a *App) pillsHeight() int {
	if a.queueCount <= 0 && a.todoIncomplete <= 0 {
		return 0
	}
	h := 1
	if a.queueCount > 0 && len(a.queuedPrompts) > 0 {
		visible := min(len(a.queuedPrompts), 3)
		h += visible
		if a.queueCount > visible {
			h++
		}
	}
	return h
}

// bannerHeight returns the height for the compaction banner.
func (a *App) bannerHeight() int {
	if a.bannerVisible && !a.bannerDismissed {
		return 2
	}
	return 0
}

// noticeHeight returns the height for ephemeral notices.
func (a *App) noticeHeight() int {
	h := 0
	if a.notice != "" {
		h++
	}
	if a.compactionToast != "" {
		h++
	}
	return h
}

// paletteHeight returns the height for the command palette popup.
func (a *App) paletteHeight() int {
	// Placeholder — will be computed from actual match count.
	return 0
}
