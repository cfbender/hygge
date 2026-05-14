package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// Footer renders the bottom-of-screen status line.
//
// Layout (separated by `·`):
//
//	$0.0123 · ctx 12.4% (24.8k/200k) · enter send · ctrl-c cancel
//
// When Busy is true the keybind hints change to reflect the in-flight Send.
type Footer struct {
	Width   int
	Theme   *theme.Theme
	Cost    float64 // dollars
	UsedTok int64
	MaxTok  int64
	PctUsed float64 // 0..1
	Busy    bool
}

// View renders the footer.
func (f Footer) View() string {
	width := f.Width
	if width <= 0 {
		width = 80
	}

	cost := fmt.Sprintf("$%.4f", f.Cost)

	ctx := f.contextSegment()

	hints := "enter send · ctrl-c cancel"
	if f.Busy {
		hints = "ctrl-c cancel · enter blocked"
	}

	muted := f.style(theme.AtomMuted)

	parts := []string{
		muted.Render(cost),
		ctx,
		muted.Render(hints),
	}
	line := strings.Join(parts, muted.Render(" · "))

	// Pad/trim to width so the footer occupies a clean line.
	visible := lipgloss.Width(line)
	if visible < width {
		line += strings.Repeat(" ", width-visible)
	}
	return line
}

// contextSegment renders the `ctx X%` token with severity coloring.
func (f Footer) contextSegment() string {
	if f.MaxTok <= 0 {
		return f.style(theme.AtomMuted).Render("ctx —")
	}
	atom := theme.AtomSuccess
	switch {
	case f.PctUsed > 0.90:
		atom = theme.AtomError
	case f.PctUsed > 0.70:
		atom = theme.AtomWarn
	}
	text := fmt.Sprintf("ctx %.1f%% (%s/%s)",
		f.PctUsed*100,
		formatTokens(f.UsedTok),
		formatTokens(f.MaxTok),
	)
	return f.style(atom).Render(text)
}

// SeverityAtom returns the theme atom selected for the current PctUsed.
// Exposed for tests so they can assert on the chosen tone without
// inspecting ANSI escape sequences.
func (f Footer) SeverityAtom() theme.Atom {
	if f.MaxTok <= 0 {
		return theme.AtomMuted
	}
	switch {
	case f.PctUsed > 0.90:
		return theme.AtomError
	case f.PctUsed > 0.70:
		return theme.AtomWarn
	default:
		return theme.AtomSuccess
	}
}

// style returns a styled lipgloss.Style for the given atom, or a blank style
// when no theme is configured.  Centralised so View/contextSegment don't
// nil-check each call site.
func (f Footer) style(a theme.Atom) lipgloss.Style {
	if f.Theme == nil {
		return lipgloss.NewStyle()
	}
	return f.Theme.Style(a)
}

// formatTokens renders a token count as a compact "12.3k" string.
func formatTokens(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}
