// Package bubble provides the Bubble rendering primitive for the chat-bubble
// UI redesign (Phase 1).
//
// A Bubble wraps pre-rendered body content in a filled block with a one-cell
// accent bar on the message side. The caller is responsible for markdown rendering and
// line-wrapping the body text before passing it in — Bubble is purely a
// presentational frame.
//
// # Phase 1 scope
//
//   - Side accent bar (left for agent/tool, right for user).
//   - Header row with left and right labels.
//   - Optional body-height cap with truncation indicator.
//   - Left or right alignment within a parent width budget.
//   - ShowTail field retained for forward-compatibility but never set to true.
//   - Bar color is the configured accent atom directly — no saturation boost.
//   - Optional interior background fill, supplied by theme.AtomBubbleBg.
package bubble

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// Alignment controls which side of the parent width the bubble is flush with.
type Alignment int

const (
	// AlignLeft places the bubble on the left side of the available width.
	AlignLeft Alignment = iota
	// AlignRight places the bubble on the right side of the available width.
	AlignRight
)

// SubStyle distinguishes normal bubbles from visually subdued ones.
type SubStyle int

const (
	// StyleNormal renders a standard side bar at full accent weight.
	StyleNormal SubStyle = iota
	// StyleDistinct renders a dimmer border (for subagent bubbles in Phase 3).
	StyleDistinct
)

const (
	sideBarWidth      = 1
	horizontalPadding = 1
	outerInset        = 2
)

// Bubble is the chat-bubble rendering primitive.
//
// Caller contract:
//   - Set Width to the full terminal column width.
//   - Optionally cap BubbleWidth; 0 = auto (~70% of Width, max 100).
//   - Pre-wrap and pre-render Body content; Bubble does NOT reflow it.
//   - AccentColor is the seam for future per-agent color theming.  Nil value
//     falls back to theme.AtomBubbleBorder (or monochrome when Theme == nil).
//
// The AccentColor field accepts a color.Color value produced by lipgloss.Color,
// e.g. lipgloss.Color("5") or lipgloss.Color("#FF8800").  This is the seam for
// per-agent-mode theming: once per-agent colors land, the orchestrator
// will resolve the agent's color and pass it here.
type Bubble struct {
	// Width is the total column budget (typically the terminal width).
	Width int

	// BubbleWidth caps the bubble's outer width.  0 = auto-calculate.
	// Auto: min(Width, max(40, int(float64(Width)*0.70), 100)).
	BubbleWidth int

	// Alignment is AlignLeft (default) or AlignRight.
	Alignment Alignment

	// HeaderLeft is the left-aligned text in the header row.
	HeaderLeft string

	// HeaderRight is the right-aligned text in the header row.
	HeaderRight string

	// Body is the pre-rendered content string (may contain newlines and ANSI).
	Body string

	// Theme is the active theme.  Nil is accepted; a built-in monochrome
	// style is used instead.
	Theme *theme.Theme

	// AccentColor is the side bar and header accent color.
	// Nil falls back to theme.AtomBubbleBorder.
	// This is the seam for future per-agent-mode color; assign the agent's
	// color (via lipgloss.Color("N") or lipgloss.Color("#RRGGBB")) here once
	// per-agent theming lands.
	AccentColor color.Color

	// SubStyle selects normal (rounded, full-weight) or distinct (faint) rendering.
	SubStyle SubStyle

	// MaxBodyHeight caps the rendered body to this many lines.
	// 0 means no cap.  When the body exceeds this, the last visible line is
	// replaced by a "… +K more" indicator in muted color.
	MaxBodyHeight int

	// ShowTail is retained for forward-compatibility but is not wired to true
	// anywhere.  Leaving it false means no tail glyphs are emitted.
	ShowTail bool

	// BackgroundColor fills the bubble content block behind text and padding.
	BackgroundColor color.Color
}

// View renders the bubble and returns the composed string.
func (b Bubble) View() string {
	width := b.Width
	if width <= 0 {
		width = 80
	}

	bubbleW := b.BubbleWidth
	if bubbleW <= 0 {
		// Auto: ~70% of parent, clamped between 40 and 100.
		bubbleW = int(float64(width) * 0.70)
		if bubbleW < 40 {
			bubbleW = 40
		}
		if bubbleW > 100 {
			bubbleW = 100
		}
	}
	if bubbleW > width {
		bubbleW = width
	}

	// Inner content width = bubble width minus the one-cell side bar and
	// horizontal content padding.
	innerW := bubbleW - sideBarWidth
	if innerW < 1 {
		innerW = 1
	}
	contentW := innerW - horizontalPadding*2
	if contentW < 1 {
		contentW = 1
	}

	accentColor := b.resolveAccentColor()
	backgroundColor := b.resolveBackgroundColor()

	// Build header line if either side is non-empty.
	header := ""
	if b.HeaderLeft != "" || b.HeaderRight != "" {
		header = b.renderHeader(contentW, accentColor)
	}

	// Build body with optional height cap.
	body := b.renderBody()

	// Compose inner lines, applying per-line width padding.
	// This ensures every line of the bubble is exactly innerW cells wide
	// so the rendered rectangle is uniform.
	inner := b.composeInner(header, body, contentW, accentColor, backgroundColor)

	// Apply the one-cell side bar. This avoids rounded-border/background ANSI
	// interactions while preserving message direction and accent identity.
	composed := b.renderFrame(inner, accentColor, backgroundColor, innerW)

	// The actual rendered bubble width (should equal bubbleW after border).
	composedW := lipgloss.Width(strings.SplitN(composed, "\n", 2)[0])
	pad := width - composedW
	if pad < 0 {
		pad = 0
	}

	// Pad EACH LINE so that every row of the output occupies exactly `width`
	// terminal columns.  Appending spaces to the whole multi-line string only
	// pads the last line, leaving interior lines shorter than `width`.
	composedLines := strings.Split(composed, "\n")
	paddedLines := make([]string, len(composedLines))
	inset := outerInset
	if pad < inset {
		inset = pad
	}
	for i, line := range composedLines {
		if b.Alignment == AlignRight {
			paddedLines[i] = strings.Repeat(" ", pad-inset) + line + strings.Repeat(" ", inset)
		} else {
			paddedLines[i] = strings.Repeat(" ", inset) + line + strings.Repeat(" ", pad-inset)
		}
	}
	result := strings.Join(paddedLines, "\n")

	// ShowTail is never set to true; this branch is dead code but kept so the
	// struct field is exercised and its test still compiles.
	if b.ShowTail {
		tail := b.renderTail(accentColor, pad, composedW, width)
		result = result + "\n" + tail
	}

	return result
}

// composeInner builds the full inner content string with each line padded to
// innerW. Every line is exactly innerW cells wide, including horizontal padding.
// When backgroundColor is set, the padded cells are filled too.
func (b Bubble) composeInner(header, body string, contentW int, accentColor, backgroundColor color.Color) string {
	// Collect logical segments with vertical padding.
	var segments []string
	if header != "" {
		segments = append(segments, "") // top padding
		segments = append(segments, header)
		sep := b.renderSeparator(contentW, accentColor)
		segments = append(segments, sep)
	}
	if body != "" {
		segments = append(segments, body)
		segments = append(segments, "") // bottom padding
	}

	// Flatten into lines.
	var allLines []string
	for _, seg := range segments {
		allLines = append(allLines, strings.Split(seg, "\n")...)
	}

	// Per-line: apply Width(contentW) and wrap it in horizontal padding so every
	// line occupies exactly innerW cells.
	lineStyle := lipgloss.NewStyle().Width(contentW)
	padStyle := lipgloss.NewStyle()
	bgOpen := ""
	if backgroundColor != nil {
		lineStyle = lineStyle.Background(backgroundColor)
		padStyle = padStyle.Background(backgroundColor)
		bgOpen = backgroundOpenSequence(backgroundColor)
	}

	var padded []string
	for _, line := range allLines {
		if bgOpen != "" {
			line = reassertBackgroundAfterReset(line, bgOpen)
		}
		renderedLines := strings.Split(lineStyle.Render(line), "\n")
		for _, rendered := range renderedLines {
			padded = append(padded,
				padStyle.Render(strings.Repeat(" ", horizontalPadding))+
					rendered+
					padStyle.Render(strings.Repeat(" ", horizontalPadding)),
			)
		}
	}
	return strings.Join(padded, "\n")
}

// renderTail builds the decorative single-character tail line.
// This function is retained so ShowTail=true still works in tests, but no
// production call site sets ShowTail=true.
func (b Bubble) renderTail(accentColor color.Color, pad, composedW, width int) string {
	style := lipgloss.NewStyle()
	if accentColor != nil {
		style = style.Foreground(accentColor)
	} else if b.Theme != nil {
		style = b.Theme.Style(theme.AtomBubbleBorder)
	}

	if b.Alignment == AlignRight {
		glyph := style.Render("◢")
		glyphW := lipgloss.Width(glyph)
		lineW := pad + composedW - glyphW
		if lineW < 0 {
			lineW = 0
		}
		return strings.Repeat(" ", lineW) + glyph
	}
	glyph := style.Render("◣")
	glyphW := lipgloss.Width(glyph)
	trailing := width - glyphW
	if trailing < 0 {
		trailing = 0
	}
	return glyph + strings.Repeat(" ", trailing)
}

// resolveAccentColor returns the accent color to use.
func (b Bubble) resolveAccentColor() color.Color {
	if b.AccentColor != nil {
		return b.AccentColor
	}
	if b.Theme != nil {
		style := b.Theme.Style(theme.AtomBubbleBorder)
		fg := style.GetForeground()
		if _, isNoColor := fg.(lipgloss.NoColor); !isNoColor && fg != nil {
			return fg
		}
	}
	// Monochrome fallback (no theme, no explicit color).
	return nil
}

// resolveBackgroundColor returns the fill color to use for the bubble surface.
func (b Bubble) resolveBackgroundColor() color.Color {
	if b.BackgroundColor != nil {
		return b.BackgroundColor
	}
	if b.Theme != nil {
		style := b.Theme.Style(theme.AtomBubbleBg)
		bg := style.GetBackground()
		if _, isNoColor := bg.(lipgloss.NoColor); !isNoColor && bg != nil {
			return bg
		}
	}
	return nil
}

func backgroundOpenSequence(bg color.Color) string {
	if bg == nil {
		return ""
	}
	rendered := lipgloss.NewStyle().Background(bg).Render("x")
	idx := strings.IndexRune(rendered, 'x')
	if idx <= 0 {
		return ""
	}
	return rendered[:idx]
}

func reassertBackgroundAfterReset(s, bgOpen string) string {
	if bgOpen == "" || !strings.Contains(s, "\x1b[") {
		return s
	}
	// Fast path for the reset sequences emitted by Lip Gloss/Glamour in hot
	// scroll renders. Avoid a byte-by-byte ANSI parser for every visible line.
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+bgOpen)
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bgOpen)
	s = strings.ReplaceAll(s, "\x1b[49m", "\x1b[49m"+bgOpen)
	return s
}

func (b Bubble) renderFrame(inner string, borderColor, _ color.Color, _ int) string {
	if b.SubStyle == StyleDistinct {
		if distinctColor := b.resolveDistinctColor(); distinctColor != nil {
			borderColor = distinctColor
		}
	}
	barStyle := lipgloss.NewStyle()
	if borderColor != nil {
		barStyle = barStyle.Foreground(borderColor)
	}
	bar := barStyle.Render("▌")
	if b.Alignment == AlignRight {
		bar = barStyle.Render("▐")
	}
	lines := strings.Split(inner, "\n")
	framed := make([]string, len(lines))
	for i, line := range lines {
		if b.Alignment == AlignRight {
			framed[i] = line + bar
		} else {
			framed[i] = bar + line
		}
	}
	return strings.Join(framed, "\n")
}

// resolveDistinctColor returns the muted/distinct border color.
func (b Bubble) resolveDistinctColor() color.Color {
	if b.Theme != nil {
		style := b.Theme.Style(theme.AtomBubbleBorderDistinct)
		fg := style.GetForeground()
		if _, isNoColor := fg.(lipgloss.NoColor); !isNoColor && fg != nil {
			return fg
		}
	}
	return nil
}

// renderHeader composes the header row: HeaderLeft left-aligned,
// HeaderRight right-aligned, with at least 2 spaces of gap between them.
func (b Bubble) renderHeader(innerW int, accentColor color.Color) string {
	leftStyle := lipgloss.NewStyle()
	rightStyle := lipgloss.NewStyle()

	if b.Theme != nil {
		leftStyle = b.Theme.Style(theme.AtomBubbleHeader)
		rightStyle = b.Theme.Style(theme.AtomBubbleHeaderMuted)
	} else if accentColor != nil {
		leftStyle = lipgloss.NewStyle().Foreground(accentColor)
	}

	left := leftStyle.Render(b.HeaderLeft)
	right := rightStyle.Render(b.HeaderRight)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	gap := innerW - leftW - rightW
	if gap < 2 {
		gap = 2
	}

	return left + strings.Repeat(" ", gap) + right
}

// renderSeparator renders a thin horizontal rule between header and body.
func (b Bubble) renderSeparator(innerW int, accentColor color.Color) string {
	line := strings.Repeat("─", innerW)
	style := lipgloss.NewStyle()
	if b.Theme != nil {
		style = b.Theme.Style(theme.AtomBubbleHeaderMuted)
	} else if accentColor != nil {
		style = lipgloss.NewStyle().Foreground(accentColor)
	}
	return style.Render(line)
}

// renderBody applies MaxBodyHeight truncation and returns the final body string.
func (b Bubble) renderBody() string {
	body := b.Body
	if b.MaxBodyHeight <= 0 || body == "" {
		return body
	}

	lines := strings.Split(body, "\n")
	if len(lines) <= b.MaxBodyHeight {
		return body
	}

	// Truncate: keep first (MaxBodyHeight-1) lines, then append indicator.
	visible := lines[:b.MaxBodyHeight-1]
	overflow := len(lines) - (b.MaxBodyHeight - 1)

	indicator := b.renderMoreIndicator(overflow)
	return strings.Join(visible, "\n") + "\n" + indicator
}

// renderMoreIndicator renders the "… +K more" line in muted color.
func (b Bubble) renderMoreIndicator(overflow int) string {
	text := "… +" + itoa(overflow) + " more"
	style := lipgloss.NewStyle()
	if b.Theme != nil {
		style = b.Theme.Style(theme.AtomBubbleBodyMuted)
	}
	return style.Render(text)
}

// itoa converts a non-negative int to its decimal string representation.
// Avoids importing fmt in the hot path.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 10)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
