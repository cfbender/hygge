package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// Footer renders the bottom-of-screen identity line.
//
// Layout (segments separated by ` · `):
//
//	{AgentType Capitalized} · {ModelName} · {Provider Capitalized} · {ReasoningLevel}
type Footer struct {
	Width          int
	Theme          *theme.Theme
	Styles         *styles.Styles
	AgentType      string
	ModelName      string
	Provider       string
	ReasoningLevel string
	// ModeIndicator is the pre-rendered mode selector string (e.g. "smart · rush · deep")
	// with the active mode highlighted. Empty when only one mode exists.
	ModeIndicator string
}

// View renders the footer.
func (f Footer) View() string {
	width := f.Width
	if width <= 0 {
		width = 80
	}

	agentType := f.AgentType
	if agentType == "" {
		agentType = "(no session)"
	} else {
		agentType = capitalize(agentType)
	}

	muted := f.muted()

	// Left side: mode indicator or agent type + model info.
	var left []string
	if f.ModeIndicator != "" {
		left = append(left, f.ModeIndicator)
	} else {
		left = append(left, muted.Render(agentType))
	}

	// Right side: model + provider + reasoning.
	var right []string
	if f.ModelName != "" {
		right = append(right, muted.Render(f.ModelName))
	}
	if f.Provider != "" {
		right = append(right, muted.Render(capitalize(f.Provider)))
	}
	if f.ReasoningLevel != "" {
		right = append(right, muted.Render(f.ReasoningLevel))
	}

	sep := muted.Render(" · ")
	leftStr := strings.Join(left, sep)
	rightStr := strings.Join(right, sep)

	// Spread left and right across the full width.
	leftW := lipgloss.Width(leftStr)
	rightW := lipgloss.Width(rightStr)
	gap := width - leftW - rightW
	if gap < 1 {
		gap = 1
	}
	line := leftStr + strings.Repeat(" ", gap) + rightStr

	visible := lipgloss.Width(line)
	if visible < width {
		line += strings.Repeat(" ", width-visible)
	}
	return line
}

func (f Footer) muted() lipgloss.Style {
	if f.Styles != nil {
		return f.Styles.Header.Muted
	}
	if f.Theme != nil {
		return f.Theme.Style(theme.AtomMuted)
	}
	return lipgloss.NewStyle()
}

// capitalize returns s with the first rune uppercased.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	if runes[0] >= 'a' && runes[0] <= 'z' {
		runes[0] -= 'a' - 'A'
	}
	return string(runes)
}
