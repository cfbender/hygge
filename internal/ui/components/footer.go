package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// Footer renders the bottom-of-screen identity line.
//
// Layout (segments separated by ` · `):
//
//	{AgentType Capitalized} · {ModelName} · {Provider Capitalized} · {ReasoningLevel}
type Footer struct {
	Width          int
	Theme          *styles.Styles
	Styles         *styles.Styles
	AgentType      string
	ModelName      string
	Provider       string
	ReasoningLevel string
	// ModeIndicator is the pre-rendered mode selector string (e.g. "smart · rush · deep")
	// with the active mode highlighted. Empty when only one mode exists.
	ModeIndicator string
	// Busy shows a spinner indicator on the left side of the footer.
	Busy bool
	// SpinnerView is the pre-rendered spinner frame (e.g. "⣾").
	SpinnerView string
	// WorkingVerb is the busy label selected by the owner. It should be stable
	// between periodic rotations rather than changing on every spinner frame.
	WorkingVerb string
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

	// Left side: mode indicator or agent type + [spinner].
	var left = []string{" "}
	if f.ModeIndicator != "" {
		left = append(left, f.ModeIndicator)
	} else {
		left = append(left, muted.Render(agentType))
	}
	if f.Busy && f.SpinnerView != "" {
		left = append(left, f.SpinnerView)
		verb := f.WorkingVerb
		if verb == "" {
			verb = "Working…"
		}
		left = append(left, f.workingVerbStyle().Render(verb))
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
		right = append(right, muted.Render(" "))
	}

	sep := muted.Render(" · ")
	leftStr := strings.Join(left, sep)
	rightStr := strings.Join(right, sep)

	// Flex left and right across the full width. When space is tight, keep the
	// left identity legible and truncate metadata before it can overflow.
	leftW := lipgloss.Width(leftStr)
	if leftW >= width {
		return lipgloss.NewStyle().MaxWidth(width).Render(leftStr)
	}
	rightMaxW := width - leftW - 1
	if lipgloss.Width(rightStr) > rightMaxW {
		rightStr = lipgloss.NewStyle().MaxWidth(rightMaxW).Render(rightStr)
	}
	rightW := lipgloss.Width(rightStr)
	gap := max(width-leftW-rightW, 1)
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
		return f.Theme.Style(styles.AtomMuted)
	}
	return lipgloss.NewStyle()
}

func (f Footer) workingVerbStyle() lipgloss.Style {
	return f.muted().Faint(true)
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
