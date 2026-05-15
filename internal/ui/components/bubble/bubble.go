// Package bubble provides the Bubble rendering primitive for the chat-bubble
// UI redesign (Phase 1).
//
// A Bubble wraps pre-rendered body content in a rounded border with an
// optional header row.  The caller is responsible for markdown rendering and
// line-wrapping the body text before passing it in — Bubble is purely a
// presentational frame.
//
// # Phase 1 scope
//
//   - Rounded border (normal) or faint normal border (distinct / subagent).
//   - Header row with left and right labels.
//   - Optional body-height cap with truncation indicator.
//   - Left or right alignment within a parent width budget.
//   - Phase 4: speech-bubble tail decorations (ShowTail).
//   - AgentColor seam: pass AccentColor directly; per-agent theming is a
//     later slice.  Nil value falls back to theme.AtomBubbleBorder.
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
	// StyleNormal renders a standard rounded border at full accent weight.
	StyleNormal SubStyle = iota
	// StyleDistinct renders a dimmer border (for subagent bubbles in Phase 3).
	StyleDistinct
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

	// AccentColor is the border and header accent color.
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

	// ShowTail, when true, appends a single decorative tail glyph below the
	// bubble on its own line.  The tail is aligned to match the bubble's
	// Alignment: bottom-right (◢) for AlignRight, bottom-left (◣) for AlignLeft.
	// Only user and assistant bubbles set this to true; tool/subagent bubbles
	// leave it false.
	ShowTail bool
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

	accentColor := b.resolveAccentColor()
	borderStyle := b.buildBorderStyle(accentColor, bubbleW)

	// Inner content width = bubble width minus 2 border columns.
	innerW := bubbleW - 2
	if innerW < 1 {
		innerW = 1
	}

	// Build header line if either side is non-empty.
	header := ""
	if b.HeaderLeft != "" || b.HeaderRight != "" {
		header = b.renderHeader(innerW, accentColor)
	}

	// Build body, applying optional height cap.
	body := b.renderBody()

	// Compose inner content.
	var inner string
	if header != "" {
		sep := b.renderSeparator(innerW, accentColor)
		inner = header + "\n" + sep + "\n" + body
	} else {
		inner = body
	}

	// Apply the border.
	composed := borderStyle.Render(inner)

	// Pad to align within Width.
	composedW := lipgloss.Width(composed)
	pad := width - composedW
	if pad < 0 {
		pad = 0
	}

	var result string
	if b.Alignment == AlignRight {
		// Right-align: leading whitespace on the left.
		result = strings.Repeat(" ", pad) + composed
	} else {
		// Left-align: trailing whitespace on the right.
		result = composed + strings.Repeat(" ", pad)
	}

	// Append tail line when requested.
	if b.ShowTail {
		tail := b.renderTail(accentColor, pad, composedW, width)
		result = result + "\n" + tail
	}

	return result
}

// renderTail builds the decorative single-character tail line.
//
// Glyph choice (Phase 4):
//
//	◣ (U+25E3) — solid black lower-left triangle, used for left-aligned
//	             (assistant) bubbles.  Points toward the bottom-left corner.
//	◢ (U+25E2) — solid black lower-right triangle, used for right-aligned
//	             (user) bubbles.  Points toward the bottom-right corner.
//
// These filled triangles look like natural speech-bubble pointer cusps when
// placed directly below the rounded border corner.  They were chosen over
// alternatives (╲/╱, └/┘, ◤/◥) because the filled shape is visually
// heavier and easier to spot as a "tail" at terminal font sizes.
func (b Bubble) renderTail(accentColor color.Color, pad, composedW, width int) string {
	style := lipgloss.NewStyle()
	if accentColor != nil {
		style = style.Foreground(accentColor)
	} else if b.Theme != nil {
		style = b.Theme.Style(theme.AtomBubbleBorder)
	}

	if b.Alignment == AlignRight {
		// ◢ at the bottom-right corner: leading spaces + glyph flush with bubble's right edge.
		glyph := style.Render("◢")
		glyphW := lipgloss.Width(glyph)
		lineW := pad + composedW - glyphW
		if lineW < 0 {
			lineW = 0
		}
		return strings.Repeat(" ", lineW) + glyph
	}
	// Left-aligned: ◣ at the bottom-left corner, then trailing pad to full width.
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

// buildBorderStyle returns a lipgloss.Style with the correct border type,
// color, and width for this bubble.
func (b Bubble) buildBorderStyle(accentColor color.Color, _ int) lipgloss.Style {
	style := lipgloss.NewStyle()
	if b.SubStyle == StyleDistinct {
		style = style.Border(lipgloss.NormalBorder())
		if accentColor != nil {
			distinctColor := b.resolveDistinctColor()
			if distinctColor != nil {
				style = style.BorderForeground(distinctColor)
			}
		}
	} else {
		style = style.Border(lipgloss.RoundedBorder())
		if accentColor != nil {
			style = style.BorderForeground(accentColor)
		}
	}
	return style
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
