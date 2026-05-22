package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

const maxQueuedPromptRows = 3

// StatusPills renders compact input-adjacent status chips.
type StatusPills struct {
	Width          int
	Theme          *styles.Styles
	QueueCount     int
	QueuedPrompts  []string
	QueuedEditable bool
}

// View renders a single row of pills. Empty state renders nothing so the input
// keeps its current layout when no status exists.
func (p StatusPills) View() string {
	var pills []string
	if p.QueueCount > 0 {
		label := fmt.Sprintf("%d queued", p.QueueCount)
		if p.QueuedEditable {
			label += " · click to edit"
		}
		pills = append(pills, p.pill(label, styles.AtomAccent))
	}
	if len(pills) == 0 {
		return ""
	}

	row := strings.Join(pills, " ")
	width := p.Width
	if width <= 0 {
		return strings.Join(append([]string{row}, p.queuedPromptRows()...), "\n")
	}
	if lipgloss.Width(row) > width {
		row = lipgloss.NewStyle().MaxWidth(width).Render(row)
	}
	rows := append([]string{row}, p.queuedPromptRows()...)
	return strings.Join(rows, "\n")
}

func (p StatusPills) pill(label string, atom styles.Atom) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if p.Theme != nil {
		style = style.Foreground(p.Theme.Style(atom).GetForeground())
	}
	return style.Render("● " + label)
}

func (p StatusPills) queuedPromptRows() []string {
	if p.QueueCount <= 0 || len(p.QueuedPrompts) == 0 {
		return nil
	}
	limit := min(len(p.QueuedPrompts), maxQueuedPromptRows)
	rows := make([]string, 0, limit+1)
	for i, prompt := range p.QueuedPrompts[:limit] {
		rows = append(rows, p.queuedPromptRow(i+1, prompt))
	}
	if p.QueueCount > limit {
		rows = append(rows, p.queuedPromptRow(0, fmt.Sprintf("… %d more queued", p.QueueCount-limit)))
	}
	return rows
}

func (p StatusPills) queuedPromptRow(index int, prompt string) string {
	label := strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " ↵ "))
	if label == "" {
		label = "(empty message)"
	}
	if index > 0 {
		label = fmt.Sprintf("%d. %s", index, label)
	}
	if p.Width > 4 && lipgloss.Width(label) > p.Width-4 {
		label = lipgloss.NewStyle().MaxWidth(p.Width - 4).Render(label)
	}
	style := lipgloss.NewStyle().PaddingLeft(2)
	if p.Theme != nil {
		style = style.Foreground(p.Theme.Style(styles.AtomMuted).GetForeground())
	}
	return style.Render(label)
}
