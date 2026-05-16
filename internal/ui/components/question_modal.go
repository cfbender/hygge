package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// QuestionOption is one selectable answer in a question modal.
type QuestionOption struct {
	ID    string
	Label string
}

// QuestionRequest is the data the modal needs to render. Mirrors
// bus.QuestionAsked so components does not import bus.
type QuestionRequest struct {
	RequestID string
	ToolName  string
	Question  string
	Options   []QuestionOption
}

// QuestionModal renders a centered multiple-choice prompt for model questions.
type QuestionModal struct {
	Width   int
	Height  int
	Theme   *theme.Theme
	Request QuestionRequest
}

// View renders the modal centered in a Width×Height box.
func (m QuestionModal) View() string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	height := m.Height
	if height <= 0 {
		height = 24
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, m.renderBox())
}

func (m QuestionModal) renderBox() string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2)
	if m.Theme != nil {
		bs := m.Theme.Style(theme.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := m.Theme.Style(theme.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}

	var b strings.Builder
	b.WriteString(m.bold().Render("question"))
	b.WriteString("\n\n")
	if m.Request.ToolName != "" {
		b.WriteString(m.field("Tool", m.Request.ToolName))
		b.WriteString("\n")
	}
	b.WriteString(m.field("Ask", m.Request.Question))
	b.WriteString("\n\n")
	for i, option := range m.Request.Options {
		key := option.ID
		if key == "" {
			key = fmt.Sprintf("%d", i+1)
		}
		b.WriteString(m.style(theme.AtomMuted).Render("[" + key + "]"))
		b.WriteString(" ")
		b.WriteString(option.Label)
		if i < len(m.Request.Options)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString("\n\n")
	b.WriteString(m.style(theme.AtomMuted).Render("[esc] cancel"))
	return border.Render(b.String())
}

func (m QuestionModal) field(label, value string) string {
	return m.style(theme.AtomMuted).Render(label+": ") + value
}

func (m QuestionModal) bold() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	return m.Theme.Style(theme.AtomPrimary).Bold(true)
}

func (m QuestionModal) style(a theme.Atom) lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle()
	}
	return m.Theme.Style(a)
}
