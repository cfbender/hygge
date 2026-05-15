package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	toastLifetime = 3 * time.Second
)

// toast is a single notification shown as a floating card below the header.
type toast struct {
	title string
	body  string
	id    int
}

// clearToastByID is sent after the toast lifetime to dismiss a specific toast.
type clearToastByID struct{ id int }

// showToast sets the current toast and schedules its dismissal.
func (a *App) showToast(title, body string) tea.Cmd {
	a.toastCounter++
	id := a.toastCounter
	a.toast = &toast{title: title, body: body, id: id}
	return tea.Tick(toastLifetime, func(time.Time) tea.Msg {
		return clearToastByID{id: id}
	})
}

// handleToastClear removes the toast if its id matches.
func (a *App) handleToastClear(id int) {
	if a.toast != nil && a.toast.id == id {
		a.toast = nil
	}
}

// renderToast returns the rendered toast card, or "" if no toast is active.
func (a *App) renderToast() string {
	if a.toast == nil {
		return ""
	}

	titleStyle := lipgloss.NewStyle().Bold(true)
	bodyStyle := lipgloss.NewStyle()
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)

	if a.styles != nil {
		titleStyle = titleStyle.Foreground(a.styles.WorkingLabelColor)
		bodyStyle = bodyStyle.Foreground(a.styles.Header.Muted.GetForeground())
		boxStyle = boxStyle.
			BorderForeground(a.styles.Dialog.TitleGradFrom).
			Background(a.styles.Background)
	}

	content := titleStyle.Render(a.toast.title)
	if a.toast.body != "" {
		content += "\n" + bodyStyle.Render(a.toast.body)
	}

	return boxStyle.Render(content)
}
