// Package components contains the bubbletea sub-views that compose the App.
//
// Each file in this package exports one component as a simple struct with a
// View(...) method.  The components are intentionally NOT tea.Model
// implementations: the App owns the state machine; components are pure
// presentation layers.
package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// spinnerFrames is the rotating glyph sequence used in the status bar while a
// Send is in flight.  Picked to be cheap on monospace terminals.
var spinnerFrames = []string{"●", "◐", "◑", "◒"}

// StatusBar renders the top header line.
type StatusBar struct {
	Profile     string
	Provider    string
	Model       string
	Pwd         string
	Busy        bool
	SpinnerTick int
	Width       int
	Theme       *theme.Theme
}

// View renders the status bar.  Layout (left-aligned identity, right-aligned
// spinner):
//
//	[profile:work]  anthropic/claude-sonnet-4-5  ~/proj                ●
func (s StatusBar) View() string {
	width := s.Width
	if width <= 0 {
		width = 80
	}
	style := lipgloss.NewStyle()
	if s.Theme != nil {
		style = s.Theme.BlockStyle(theme.AtomStatusBarFg, theme.AtomStatusBarBg)
	}
	style = style.Width(width).Padding(0, 1)

	var leftParts []string
	if s.Profile != "" {
		leftParts = append(leftParts, "[profile:"+s.Profile+"]")
	}
	if s.Provider != "" || s.Model != "" {
		leftParts = append(leftParts, s.Provider+"/"+s.Model)
	}
	if s.Pwd != "" {
		leftParts = append(leftParts, s.Pwd)
	}
	left := strings.Join(leftParts, "  ")

	right := ""
	if s.Busy {
		right = spinnerFrames[s.SpinnerTick%len(spinnerFrames)]
	}

	// Reserve 2 columns of padding for the lipgloss.Style.Padding(0, 1).
	inner := width - 2
	if inner < 0 {
		inner = 0
	}
	gap := inner - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + strings.Repeat(" ", gap) + right
	return style.Render(line)
}
