package components

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// CompactionBanner is the thin advisory banner shown above the input when
// context usage crosses the configured threshold.  It is non-blocking:
// the user can dismiss it with Ctrl+X without triggering compaction.
//
// Dismiss semantics (managed by the App, not here):
//
//	Ctrl+X          — dismiss for this crossing (hidden until re-crossing
//	                  after hysteresis or compaction).
//	CompactionCompleted event — auto-cleared by the App.
type CompactionBanner struct {
	Width   int
	Theme   *theme.Theme
	Visible bool
	Pct     float64 // context usage percentage (0–100)
}

// View renders the banner as a single line, or "" when not visible.
func (b CompactionBanner) View() string {
	if !b.Visible {
		return ""
	}
	width := b.Width
	if width <= 0 {
		width = 80
	}

	warnSt := lipgloss.NewStyle()
	mutedSt := lipgloss.NewStyle()
	if b.Theme != nil {
		warnSt = b.Theme.Style(theme.AtomWarn)
		mutedSt = b.Theme.Style(theme.AtomMuted)
	}

	text := fmt.Sprintf("⚠  Context usage at %.0f%%. /compact to summarise older messages.", b.Pct)
	dismiss := mutedSt.Render("  [Ctrl+X] dismiss")

	// Left-align the warning text; right-align the dismiss hint.
	full := warnSt.Width(width-lipgloss.Width(dismiss)).Render(text) + dismiss
	return full
}
