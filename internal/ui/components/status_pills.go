package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// StatusPills renders compact input-adjacent status chips.
type StatusPills struct {
	Width         int
	Theme         *theme.Theme
	QueueCount    int
	QueuedPrompts []string
	TodoCount     int
	TodoRunning   bool
}

// View renders a single row of pills. Empty state renders nothing so the input
// keeps its current layout when no status exists.
func (p StatusPills) View() string {
	var pills []string
	if p.QueueCount > 0 {
		pills = append(pills, p.pill(fmt.Sprintf("%d queued", p.QueueCount), theme.AtomAccent))
	}
	// TODO(phase-6): wire TodoCount/TodoRunning when first-class todo state lands.
	if p.TodoCount > 0 {
		label := fmt.Sprintf("%d todo", p.TodoCount)
		if p.TodoCount != 1 {
			label += "s"
		}
		if p.TodoRunning {
			label = "◌ " + label
		}
		pills = append(pills, p.pill(label, theme.AtomMuted))
	}
	if len(pills) == 0 {
		return ""
	}

	row := strings.Join(pills, " ")
	width := p.Width
	if width <= 0 {
		return row
	}
	if lipgloss.Width(row) > width {
		return lipgloss.NewStyle().MaxWidth(width).Render(row)
	}
	return row
}

func (p StatusPills) pill(label string, atom theme.Atom) string {
	style := lipgloss.NewStyle().Padding(0, 1)
	if p.Theme != nil {
		style = style.Foreground(p.Theme.Style(atom).GetForeground())
	}
	return style.Render("● " + label)
}
