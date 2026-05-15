package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// Footer renders the bottom-of-screen identity line.
//
// Layout (segments separated by ` · `):
//
//	{AgentType Capitalized} · {ModelName} · {Provider Capitalized} · {ReasoningLevel}
//
// Cost, context usage, and keybind hints have been moved to the header bar
// (Phase 1 chat-bubble redesign) or dropped entirely.  Only session identity
// remains.  When AgentType is empty (no active session), "(no session)" is shown.
type Footer struct {
	Width          int
	Theme          *theme.Theme
	AgentType      string // e.g. "General"; rendered capitalized
	ModelName      string // e.g. "Claude Opus 4.7"
	Provider       string // e.g. "openrouter"; rendered capitalized
	ReasoningLevel string // e.g. "medium"; empty → omitted
}

// View renders the footer.
func (f Footer) View() string {
	width := f.Width
	if width <= 0 {
		width = 80
	}

	muted := f.style(theme.AtomMuted)

	agentType := f.AgentType
	if agentType == "" {
		agentType = "(no session)"
	} else {
		agentType = capitalize(agentType)
	}

	var parts []string
	parts = append(parts, muted.Render(agentType))
	if f.ModelName != "" {
		parts = append(parts, muted.Render(f.ModelName))
	}
	if f.Provider != "" {
		parts = append(parts, muted.Render(capitalize(f.Provider)))
	}
	if f.ReasoningLevel != "" {
		parts = append(parts, muted.Render(f.ReasoningLevel))
	}

	line := strings.Join(parts, muted.Render(" · "))

	// Pad to width so the footer occupies a clean terminal line.
	visible := lipgloss.Width(line)
	if visible < width {
		line += strings.Repeat(" ", width-visible)
	}
	return line
}

// style returns a styled lipgloss.Style for the given atom, or a blank style
// when no theme is configured.
func (f Footer) style(a theme.Atom) lipgloss.Style {
	if f.Theme == nil {
		return lipgloss.NewStyle()
	}
	return f.Theme.Style(a)
}

// capitalize returns s with the first rune uppercased.  Used to display
// AgentType and Provider in title case without importing unicode.
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
