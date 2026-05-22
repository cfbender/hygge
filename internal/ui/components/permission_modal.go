package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// PermissionRequest is the data the modal needs to render.  Mirrors
// bus.PermissionAsked so components does not import bus.
type PermissionRequest struct {
	RequestID string
	ToolName  string
	Category  string
	Target    string
	Why       string // optional rationale
}

// PermissionModal renders the centered modal that gates tool execution.
type PermissionModal struct {
	Width   int
	Height  int
	Theme   *styles.Styles
	Request PermissionRequest
	Toast   string // optional transient line (e.g. "edit not yet implemented")
}

// View renders the modal centered in a Width×Height box.  Caller is
// responsible for not calling View when no request is active.
func (m PermissionModal) View() string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	height := m.Height
	if height <= 0 {
		height = 24
	}

	box := m.renderBox()
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// renderBox renders the modal content with its chrome.
func (m PermissionModal) renderBox() string {
	contentW := m.contentWidth()
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2).
		Width(contentW)
	if m.Theme != nil {
		bs := m.Theme.Style(styles.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := m.Theme.Style(styles.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}

	var b strings.Builder

	// Title + subtitle row (matches question_modal pattern).
	b.WriteString(m.bold().Render("permission request"))
	b.WriteString(" ")
	b.WriteString(m.style(styles.AtomMuted).Render("approve or deny tool access"))
	b.WriteString("\n\n")

	// Request Details section.
	b.WriteString(m.sectionLabel("Request Details"))
	b.WriteString("\n")
	b.WriteString(m.field("Tool", m.Request.ToolName))
	b.WriteString("\n")
	b.WriteString(m.field("Category", m.Request.Category))
	b.WriteString("\n")
	b.WriteString(m.field("Target", m.Request.Target))
	if m.Request.Why != "" {
		b.WriteString("\n")
		b.WriteString(m.field("Why", m.Request.Why))
	}
	b.WriteString("\n\n")

	// Actions section.
	b.WriteString(m.sectionLabel("Actions"))
	b.WriteString("\n")
	b.WriteString(m.actionRow(
		[][2]string{{"[y]", "allow once"}, {"[Y]", "allow session"}, {"[A]", "allow always"}, {"[e]", "edit (v0.2)"}},
		false, contentW,
	))
	b.WriteString("\n")
	b.WriteString(m.actionRow(
		[][2]string{{"[n]", "deny"}, {"[esc]", "deny"}},
		true, contentW,
	))

	if m.Toast != "" {
		b.WriteString("\n\n")
		b.WriteString(m.warnStyle().Render(m.Toast))
	}

	return border.Render(b.String())
}

// sectionLabel renders a bold muted section header (matches question_modal).
func (m PermissionModal) sectionLabel(label string) string {
	return m.style(styles.AtomMuted).Bold(true).Render(label)
}

// field renders a "Label: value" pair with the label muted.
func (m PermissionModal) field(label, value string) string {
	return m.style(styles.AtomMuted).Render(label+": ") + value
}

// actionRow renders a horizontal group of key-description pairs inside a
// left-bordered row, using warn color for deny actions and accent for allow.
// pairs is a slice of [key, description] tuples.
func (m PermissionModal) actionRow(pairs [][2]string, isDeny bool, width int) string {
	var parts []string
	for _, kv := range pairs {
		keyStyle := m.style(styles.AtomAccent).Bold(true)
		if isDeny {
			keyStyle = m.style(styles.AtomWarn).Bold(true)
		}
		parts = append(parts, keyStyle.Render(kv[0])+" "+kv[1])
	}
	content := strings.Join(parts, "   ")

	borderColor := m.style(styles.AtomAccent).GetForeground()
	if isDeny {
		borderColor = m.style(styles.AtomWarn).GetForeground()
	}

	row := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(width)
	return row.Render(content)
}

// bold returns a bold style anchored to the primary atom.
func (m PermissionModal) bold() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	return m.Theme.Style(styles.AtomPrimary).Bold(true)
}

// warnStyle returns the warn atom style.
func (m PermissionModal) warnStyle() lipgloss.Style {
	return m.style(styles.AtomWarn)
}

func (m PermissionModal) style(a styles.Atom) lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle()
	}
	return m.Theme.Style(a)
}

func (m PermissionModal) contentWidth() int {
	return ModalContentWidth(m.Width)
}
