package ui

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

const (
	doubleClickThreshold = 400 * time.Millisecond
	clickPosTolerance    = 2
)

// selection tracks mouse-driven text selection state.
type selection struct {
	active   bool // mouse button is held down
	hasRange bool // a non-empty selection exists

	// Start and end positions in screen coordinates.
	startX, startY int
	endX, endY     int

	// Multi-click detection.
	lastClickTime time.Time
	lastClickX    int
	lastClickY    int
	clickCount    int
}

// clampToContent constrains x to the left column content area, excluding
// the sidebar and any leading/trailing chrome.
func (a *App) clampToContent(x int) int {
	maxX := a.layout.leftW
	if x >= maxX {
		x = maxX - 1
	}
	if x < 0 {
		x = 0
	}
	return x
}

// handleMouseDown processes a mouse press event.
func (a *App) handleMouseDown(x, y int) {
	x = a.clampToContent(x)
	now := time.Now()
	s := &a.sel

	// Detect multi-click (double/triple).
	if now.Sub(s.lastClickTime) < doubleClickThreshold &&
		abs(x-s.lastClickX) <= clickPosTolerance &&
		abs(y-s.lastClickY) <= clickPosTolerance {
		s.clickCount++
		if s.clickCount > 3 {
			s.clickCount = 3
		}
	} else {
		s.clickCount = 1
	}
	s.lastClickTime = now
	s.lastClickX = x
	s.lastClickY = y

	switch s.clickCount {
	case 2:
		a.selectWord(x, y)
	case 3:
		a.selectLine(y)
	default:
		s.active = true
		s.hasRange = false
		s.startX = x
		s.startY = y
		s.endX = x
		s.endY = y
	}
}

// handleMouseMotion processes a mouse drag event.
func (a *App) handleMouseMotion(x, y int) {
	x = a.clampToContent(x)
	s := &a.sel
	if !s.active {
		return
	}
	s.endX = x
	s.endY = y
	s.hasRange = s.startX != s.endX || s.startY != s.endY
}

// handleMouseUp processes a mouse release event and copies selected text.
func (a *App) handleMouseUp(x, y int) tea.Cmd {
	x = a.clampToContent(x)
	s := &a.sel
	if !s.active && !s.hasRange {
		return nil
	}
	s.active = false
	s.endX = x
	s.endY = y
	s.hasRange = s.startX != s.endX || s.startY != s.endY

	if !s.hasRange {
		return nil
	}

	// Extract and copy the selected text.
	text := a.extractSelectedText()
	if text == "" {
		return nil
	}

	return a.copyToClipboard(text)
}

// clearSelection removes the current selection.
func (a *App) clearSelection() {
	a.sel.hasRange = false
	a.sel.active = false
}

// selectWord selects the word at the given screen position.
func (a *App) selectWord(x, y int) {
	s := &a.sel
	if a.lastCanvas.RenderBuffer == nil {
		return
	}
	bounds := a.lastCanvas.Bounds()
	if y < bounds.Min.Y || y >= bounds.Max.Y {
		return
	}

	// Extract the line text from the buffer.
	var line strings.Builder
	for cx := bounds.Min.X; cx < bounds.Max.X; cx++ {
		cell := a.lastCanvas.CellAt(cx, y)
		if cell == nil || cell.Content == "" {
			line.WriteByte(' ')
		} else {
			line.WriteString(cell.Content)
		}
	}
	lineStr := line.String()

	// Find word boundaries around x.
	startCol, endCol := wordBoundaries(lineStr, x)
	if startCol == endCol {
		return
	}

	s.active = false
	s.hasRange = true
	s.startX = startCol
	s.startY = y
	s.endX = endCol
	s.endY = y
}

// selectLine selects the entire line at the given screen row.
func (a *App) selectLine(y int) {
	s := &a.sel
	if a.lastCanvas.RenderBuffer == nil {
		return
	}
	bounds := a.lastCanvas.Bounds()
	if y < bounds.Min.Y || y >= bounds.Max.Y {
		return
	}

	// Find last non-space column.
	lastCol := bounds.Min.X
	for cx := bounds.Max.X - 1; cx >= bounds.Min.X; cx-- {
		cell := a.lastCanvas.CellAt(cx, y)
		if cell != nil && cell.Content != "" && cell.Content != " " {
			lastCol = cx + 1
			break
		}
	}

	s.active = false
	s.hasRange = true
	s.startX = bounds.Min.X
	s.startY = y
	s.endX = lastCol
	s.endY = y
}

// applyHighlight modifies the UV buffer cells to show reverse video for the
// current selection. Called during Draw() after all content is rendered.
func (a *App) applyHighlight(scr uv.Screen, area uv.Rectangle) {
	s := &a.sel
	if !s.hasRange {
		return
	}

	// Normalize selection direction.
	sy, sx, ey, ex := s.startY, s.startX, s.endY, s.endX
	if sy > ey || (sy == ey && sx > ex) {
		sy, sx, ey, ex = ey, ex, sy, sx
	}

	// Clamp to the left column (exclude sidebar).
	maxX := a.layout.leftW
	if maxX <= 0 {
		maxX = area.Max.X
	}

	// Resolve highlight colors: muted fg on a subtle bg.
	var hlFg, hlBg color.Color
	if a.styles != nil {
		hlFg = a.styles.Background
		hlBg = a.styles.WorkingLabelColor
	}

	for y := sy; y <= ey; y++ {
		if y < area.Min.Y || y >= area.Max.Y {
			continue
		}

		// Find content bounds: first and last non-space cell on this row.
		contentStart := maxX
		contentEnd := area.Min.X
		for cx := area.Min.X; cx < maxX; cx++ {
			cell := scr.CellAt(cx, y)
			if cell != nil && cell.Content != "" && cell.Content != " " {
				if cx < contentStart {
					contentStart = cx
				}
				contentEnd = cx + 1
			}
		}
		if contentStart >= contentEnd {
			continue
		}

		lineStart := contentStart
		lineEnd := contentEnd
		if y == sy && sx > lineStart {
			lineStart = sx
		}
		if y == ey && ex < lineEnd {
			lineEnd = ex
		}
		for x := lineStart; x < lineEnd; x++ {
			if x < area.Min.X || x >= maxX {
				continue
			}
			cell := scr.CellAt(x, y)
			if cell == nil {
				continue
			}
			// Skip border/chrome glyphs — preserve their accent color.
			if isBorderGlyph(cell.Content) {
				continue
			}
			if hlBg != nil {
				cell.Style.Bg = hlBg
				cell.Style.Fg = hlFg
			} else {
				cell.Style.Attrs |= uv.AttrReverse
			}
			scr.SetCell(x, y, cell)
		}
	}
}

// extractSelectedText reads text from the last rendered canvas buffer.
func (a *App) extractSelectedText() string {
	if a.lastCanvas.RenderBuffer == nil || !a.sel.hasRange {
		return ""
	}

	s := &a.sel
	sy, sx, ey, ex := s.startY, s.startX, s.endY, s.endX
	if sy > ey || (sy == ey && sx > ex) {
		sy, sx, ey, ex = ey, ex, sy, sx
	}

	bounds := a.lastCanvas.Bounds()

	// Clamp to left column.
	maxX := a.layout.leftW
	if maxX <= 0 || maxX > bounds.Max.X {
		maxX = bounds.Max.X
	}

	var lines []string

	for y := sy; y <= ey; y++ {
		if y < bounds.Min.Y || y >= bounds.Max.Y {
			continue
		}

		// Find content bounds for this row.
		contentStart := maxX
		contentEnd := bounds.Min.X
		for cx := bounds.Min.X; cx < maxX; cx++ {
			cell := a.lastCanvas.CellAt(cx, y)
			if cell != nil && cell.Content != "" && cell.Content != " " {
				if cx < contentStart {
					contentStart = cx
				}
				contentEnd = cx + 1
			}
		}
		if contentStart >= contentEnd {
			continue
		}

		lineStart := contentStart
		lineEnd := contentEnd
		if y == sy && sx > lineStart {
			lineStart = sx
		}
		if y == ey && ex < lineEnd {
			lineEnd = ex
		}

		var line strings.Builder
		for x := lineStart; x < lineEnd; x++ {
			if x < bounds.Min.X || x >= bounds.Max.X {
				continue
			}
			cell := a.lastCanvas.CellAt(x, y)
			if cell == nil || cell.Content == "" {
				line.WriteByte(' ')
			} else {
				line.WriteString(cell.Content)
			}
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}

	result := strings.Join(lines, "\n")

	// Strip bubble chrome glyphs that leak into the selection.
	result = stripBubbleChrome(result)

	return strings.TrimSpace(result)
}

// copyToClipboard copies text via both OSC 52 and native clipboard.
func (a *App) copyToClipboard(text string) tea.Cmd {
	// Native clipboard (works locally).
	_ = clipboard.WriteAll(text)

	// OSC 52 (works over SSH).
	return tea.SetClipboard(text)
}

// wordBoundaries finds the start and end column of the word at column x.
func wordBoundaries(line string, x int) (int, int) {
	stripped := ansi.Strip(line)
	runes := []rune(stripped)
	if x < 0 || x >= len(runes) {
		return 0, 0
	}

	// If clicked on space, no selection.
	if runes[x] == ' ' {
		return 0, 0
	}

	// Walk left to find word start.
	start := x
	for start > 0 && runes[start-1] != ' ' {
		start--
	}

	// Walk right to find word end.
	end := x
	for end < len(runes) && runes[end] != ' ' {
		end++
	}

	return start, end
}

// stripBubbleChrome removes leading border glyphs from selected text lines.
func stripBubbleChrome(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// Strip leading bubble border characters (▌, │, ┃) and whitespace.
		trimmed := line
		for len(trimmed) > 0 {
			r := []rune(trimmed)
			if len(r) == 0 {
				break
			}
			ch := r[0]
			if ch == '▌' || ch == '│' || ch == '┃' || ch == '║' {
				trimmed = string(r[1:])
				continue
			}
			break
		}
		// Only strip if we actually removed a border glyph (preserve intentional leading text).
		if trimmed != line {
			lines[i] = strings.TrimLeft(trimmed, " ")
		}
	}
	return strings.Join(lines, "\n")
}

// isBorderGlyph returns true for bubble border/chrome characters that should
// not have their colors overwritten by selection highlighting.
func isBorderGlyph(s string) bool {
	if len(s) == 0 {
		return false
	}
	r := []rune(s)
	switch r[0] {
	case '▌', '│', '┃', '║', '╭', '╮', '╰', '╯', '─', '━', '╱':
		return true
	}
	return false
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
