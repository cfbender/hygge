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
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2)
	if m.Theme != nil {
		bs := m.Theme.Style(styles.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := m.Theme.Style(styles.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}

	var b strings.Builder
	header := m.bold().Render("permission request")
	b.WriteString(header)
	b.WriteString("\n\n")
	b.WriteString(m.field("Tool", m.Request.ToolName))
	b.WriteString("\n")
	b.WriteString(m.field("Category", m.Request.Category))
	b.WriteString("\n")
	b.WriteString(m.field("Target", m.Request.Target))
	if m.Request.Why != "" {
		b.WriteString("\n\n")
		b.WriteString(m.field("Why", m.Request.Why))
	}
	b.WriteString("\n\n")
	b.WriteString(m.options())
	if m.Toast != "" {
		b.WriteString("\n\n")
		b.WriteString(m.warnStyle().Render(m.Toast))
	}

	return border.Render(b.String())
}

// field renders a "Label: value" pair with the label muted.
func (m PermissionModal) field(label, value string) string {
	muted := m.style(styles.AtomMuted)
	return muted.Render(label+": ") + value
}

// options renders the keybind hint block.
func (m PermissionModal) options() string {
	muted := m.style(styles.AtomMuted)
	lines := []string{
		muted.Render("[y]") + " allow once    " + muted.Render("[Y]") + " allow session",
		muted.Render("[A]") + " allow always   " + muted.Render("[n]") + " deny",
		muted.Render("[e]") + " edit (v0.2)    " + muted.Render("[esc]") + " deny",
	}
	return strings.Join(lines, "\n")
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
