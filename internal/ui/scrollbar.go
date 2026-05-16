package ui

type scrollBarGeometry struct {
	X       int
	Height  int
	ThumbY  int
	ThumbH  int
	TrackH  int
	MaxYOff int
}

func (a *App) scrollBarVisible() bool {
	return a.layout.sidebarW > 0 && a.scrollableLineCount() > 0 && a.height >= 3
}

func (a *App) scrollableLineCount() int {
	visible := max(a.msgViewport.VisibleLineCount(), 1)
	return max(a.msgViewport.TotalLineCount()-visible, 0)
}

func (a *App) scrollBarGeometry() (scrollBarGeometry, bool) {
	if !a.scrollBarVisible() {
		return scrollBarGeometry{}, false
	}
	height := a.height
	thumbH := max(1, min(3, height/8))
	trackH := max(height-thumbH, 1)
	thumbY := int(a.msgViewport.ScrollPercent() * float64(trackH))
	thumbY = min(max(thumbY, 0), trackH)
	return scrollBarGeometry{
		X:       a.layout.sidebar.Min.X,
		Height:  height,
		ThumbY:  thumbY,
		ThumbH:  thumbH,
		TrackH:  trackH,
		MaxYOff: a.scrollableLineCount(),
	}, true
}

func (a *App) beginScrollBarDrag(x, y int) bool {
	geom, ok := a.scrollBarGeometry()
	if !ok || x != geom.X || y < geom.ThumbY || y >= geom.ThumbY+geom.ThumbH {
		return false
	}
	a.scrollDragActive = true
	a.scrollDragThumbDelta = y - geom.ThumbY
	return true
}

func (a *App) dragScrollBar(y int) {
	geom, ok := a.scrollBarGeometry()
	if !ok {
		a.scrollDragActive = false
		return
	}
	thumbY := min(max(y-a.scrollDragThumbDelta, 0), geom.TrackH)
	target := 0
	if geom.TrackH > 0 && geom.MaxYOff > 0 {
		target = int((float64(thumbY)/float64(geom.TrackH))*float64(geom.MaxYOff) + 0.5)
	}
	a.msgViewport.SetYOffset(target)
	a.userScrolled = !a.msgViewport.AtBottom()
}
