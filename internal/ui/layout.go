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
	editorMinHeight     = 3
	editorMaxHeight     = 15
	footerHeight        = 1
	headerHeight        = 1
	chatBottomPadding   = 1 // breathing room between messages and editor

)

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

	// Sidebar: hidden on narrow terminals.
	if w >= sidebarMinTermWidth {
		l.sidebarW = sidebarFixedWidth
		l.compact = false
	} else {
		l.sidebarW = 0
		l.compact = true
	}
	l.leftW = w - l.sidebarW

	// Content wrap width: 80% of left column minus padding.
	l.msgContentW = int(float64(l.leftW)*0.80) - 3
	if l.msgContentW < 1 {
		l.msgContentW = 1
	}

	// Compute dynamic heights.
	editorH := a.editorHeight()
	pillsH := a.pillsHeight()
	bannerH := a.bannerHeight()
	noticeH := a.noticeHeight()
	paletteH := a.paletteHeight()

	// Chrome = everything except the chat viewport.
	chromeH := headerHeight + editorH + footerHeight + chatBottomPadding + pillsH + bannerH + noticeH + paletteH

	chatH := h - chromeH
	if chatH < 1 {
		chatH = 1
	}

	// Vertical split: chat | [banner] | [palette] | editor | [pills] | [notice] | footer
	// We use the layout engine for the main left column.
	var chatRect, editorRect, footerRect image.Rectangle
	leftArea := image.Rect(0, 0, l.leftW, h)

	layout.Vertical(
		layout.Len(chatH),
		layout.Len(editorH),
		layout.Fill(1), // footer gets remaining
	).Split(leftArea).Assign(&chatRect, &editorRect, &footerRect)

	l.chat = chatRect
	l.editor = editorRect
	l.footer = footerRect

	// Sidebar occupies the right column, full height.
	if l.sidebarW > 0 {
		l.sidebar = image.Rect(l.leftW, 0, w, h)
	}

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
	return h
}

// pillsHeight returns the height needed for status pills.
func (a *App) pillsHeight() int {
	if a.queueCount > 0 || a.todoIncomplete > 0 {
		return 1
	}
	return 0
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
	if a.compactionInFlight {
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
