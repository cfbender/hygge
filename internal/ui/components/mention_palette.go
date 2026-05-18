package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

const mentionPaletteMaxRows = 8

// MentionItem is one selectable @ mention candidate.
type MentionItem struct {
	Kind        string
	Label       string
	Description string
}

// MentionPalette is the inline autocomplete popover shown above the input
// while the user is typing an @ mention.
type MentionPalette struct {
	Width     int
	Theme     *theme.Theme
	Matches   []MentionItem
	Highlight int
	Query     string
}

// View renders the mention autocomplete popover.
func (p MentionPalette) View() string {
	width := p.Width
	if width <= 0 {
		width = 60
	}
	innerWidth := max(width-4, 20)

	if len(p.Matches) == 0 {
		if p.Query == "" {
			return ""
		}
		return p.box(p.style(theme.AtomMuted).Render(
			fmt.Sprintf("no mentions match @%s", p.Query),
		), width)
	}

	selected := p.Highlight
	if selected < 0 || selected >= len(p.Matches) {
		selected = -1
	}
	windowHighlight := selected
	if windowHighlight < 0 {
		windowHighlight = 0
	}
	start := paletteWindowStart(len(p.Matches), mentionPaletteMaxRows, windowHighlight)
	end := min(start+mentionPaletteMaxRows, len(p.Matches))
	visible := p.Matches[start:end]
	overflowBefore := start
	overflowAfter := len(p.Matches) - end

	labelWidth := 0
	for _, item := range visible {
		if n := len(item.Label) + 1; n > labelWidth { // +1 for @
			labelWidth = n
		}
	}
	if labelWidth > innerWidth/2 {
		labelWidth = innerWidth / 2
	}

	rows := make([]string, 0, len(visible)+2)
	if overflowBefore > 0 {
		rows = append(rows, p.style(theme.AtomMuted).Render(
			fmt.Sprintf("  ↑ %d more", overflowBefore),
		))
	}
	for i, item := range visible {
		labelCol := padRight("@"+item.Label, labelWidth)
		desc := item.Kind
		if item.Description != "" {
			desc += " · " + item.Description
		}
		descCol := truncate(desc, innerWidth-labelWidth-2)

		if start+i == selected {
			accent := p.style(theme.AtomAccent)
			rows = append(rows, accent.Render(fmt.Sprintf("▶ %s  %s", labelCol, descCol)))
		} else {
			muted := p.style(theme.AtomMuted)
			rows = append(rows, fmt.Sprintf("  %s  %s", muted.Render(labelCol), descCol))
		}
	}
	if overflowAfter > 0 {
		rows = append(rows, p.style(theme.AtomMuted).Render(
			fmt.Sprintf("  ↓ %d more — keep typing to narrow", overflowAfter),
		))
	}

	return p.box(strings.Join(rows, "\n"), width)
}

func (p MentionPalette) box(body string, width int) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(width - 2)
	if p.Theme != nil {
		bs := p.Theme.Style(theme.AtomModalBorder)
		style = style.BorderForeground(bs.GetForeground())
	}
	return style.Render(body)
}

func (p MentionPalette) style(a theme.Atom) lipgloss.Style {
	if p.Theme == nil {
		return lipgloss.NewStyle()
	}
	return p.Theme.Style(a)
}
