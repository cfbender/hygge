package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/glamour"

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
	Width         int
	Height        int
	Theme         *theme.Theme
	Request       QuestionRequest
	SelectedIndex int
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
	contentW := m.contentWidth()
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(contentW)
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
	b.WriteString(" ")
	b.WriteString(m.style(theme.AtomMuted).Render("choose an answer"))
	b.WriteString("\n\n")
	if m.Request.ToolName != "" {
		b.WriteString(m.field("Tool", m.Request.ToolName))
		b.WriteString("\n\n")
	}
	b.WriteString(m.sectionLabel("Ask"))
	b.WriteString("\n")
	b.WriteString(m.markdown(m.Request.Question, contentW))
	b.WriteString("\n\n")
	b.WriteString(m.sectionLabel("Answers"))
	b.WriteString("\n")
	for i, option := range m.Request.Options {
		b.WriteString(m.optionRow(i, option, contentW))
		if i < len(m.Request.Options)-1 {
			b.WriteString("\n\n")
		}
	}
	b.WriteString("\n\n")
	b.WriteString(m.style(theme.AtomMuted).Render("↑/↓ move  enter select  1-9 quick select  esc cancel"))
	return border.Render(b.String())
}

func (m QuestionModal) field(label, value string) string {
	return m.style(theme.AtomMuted).Render(label+": ") + value
}

func (m QuestionModal) sectionLabel(label string) string {
	return m.style(theme.AtomMuted).Bold(true).Render(label)
}

func (m QuestionModal) optionRow(index int, option QuestionOption, width int) string {
	key := option.ID
	if key == "" {
		key = fmt.Sprintf("%d", index+1)
	}
	selected := index == m.clampedSelectedIndex()

	marker := " "
	markerStyle := m.style(theme.AtomMuted)
	badgeStyle := m.style(theme.AtomMuted)
	if selected {
		marker = "›"
		markerStyle = m.style(theme.AtomAccent).Bold(true)
		badgeStyle = m.style(theme.AtomAccent).Bold(true)
	}
	prefix := markerStyle.Render(marker) + " " + badgeStyle.Render("["+key+"]") + " "
	labelW := max(width-lipgloss.Width(prefix)-4, 20)
	label := trimRenderedMarkdownLeft(m.markdown(option.Label, labelW))
	if strings.TrimSpace(label) == "" {
		label = "-"
	}
	lines := strings.Split(label, "\n")
	continuation := strings.Repeat(" ", lipgloss.Width(prefix))
	for i := range lines {
		if i == 0 {
			lines[i] = prefix + lines[i]
			continue
		}
		lines[i] = continuation + lines[i]
	}

	row := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(m.style(theme.AtomMuted).GetForeground()).
		Padding(0, 1).
		Width(width)
	if selected {
		row = row.
			BorderForeground(m.style(theme.AtomAccent).GetForeground())
	}
	return row.Render(strings.Join(lines, "\n"))
}

func (m QuestionModal) markdown(input string, width int) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(ThemeGlamourStyle(m.Theme)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return input
	}
	out, err := r.Render(input)
	if err != nil {
		return input
	}
	return trimRenderedMarkdown(out)
}

func trimRenderedMarkdown(out string) string {
	lines := strings.Split(strings.Trim(out, "\n"), "\n")
	minIndent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := leadingSpaces(line)
		if minIndent == -1 || indent < minIndent {
			minIndent = indent
		}
	}
	if minIndent <= 0 {
		return strings.Join(lines, "\n")
	}
	for i, line := range lines {
		if len(line) >= minIndent {
			lines[i] = line[minIndent:]
		}
	}
	return strings.Join(lines, "\n")
}

func leadingSpaces(line string) int {
	for i, r := range line {
		if r != ' ' && r != '\t' {
			return i
		}
	}
	return len(line)
}

func trimRenderedMarkdownLeft(out string) string {
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = trimRenderedLineLeft(line)
	}
	return strings.Join(lines, "\n")
}

func trimRenderedLineLeft(line string) string {
	var prefix strings.Builder
	for {
		trimmed := strings.TrimLeft(line, " \t")
		line = trimmed
		if !strings.HasPrefix(line, "\x1b[") {
			break
		}
		end := strings.IndexByte(line, 'm')
		if end < 0 {
			break
		}
		prefix.WriteString(line[:end+1])
		line = line[end+1:]
	}
	return prefix.String() + line
}

func (m QuestionModal) clampedSelectedIndex() int {
	if len(m.Request.Options) == 0 || m.SelectedIndex < 0 {
		return 0
	}
	if m.SelectedIndex >= len(m.Request.Options) {
		return len(m.Request.Options) - 1
	}
	return m.SelectedIndex
}

func (m QuestionModal) contentWidth() int {
	width := m.Width - 12
	if width <= 0 {
		width = 80
	}
	if width > 96 {
		width = 96
	}
	if width < 36 {
		width = 36
	}
	return width
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
