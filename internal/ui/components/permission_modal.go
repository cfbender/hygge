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
		[][2]string{{"[y]", "allow once"}, {"[Y]", "allow session"}, {"[A]", "allow always"}},
		false, contentW,
	))
	b.WriteString("\n")
	b.WriteString(m.actionRow(
		[][2]string{{"[n]", "deny"}, {"[esc]", "deny"}},
		true, contentW,
	))

	return border.Render(b.String())
}

// sectionLabel renders a bold muted section header (matches question_modal).
func (m PermissionModal) sectionLabel(label string) string {
	return m.style(styles.AtomMuted).Bold(true).Render(label)
}

// field renders a "Label: value" pair with the label muted.
// Long values can use a second aligned line before truncating with "…" so they
// never push the modal wider than the content width or hide the action buttons.
func (m PermissionModal) field(label, value string) string {
	prefix := label + ": "
	prefixW := lipgloss.Width(prefix)
	// The surrounding border style adds two columns of horizontal padding on each
	// side, so field content must fit inside that padded text area.
	lineW := max(m.contentWidth()-4, 1)
	avail := max(lineW-prefixW, 1)
	lines := wrapTruncatedFieldValue(value, avail, 2)

	var rendered strings.Builder
	rendered.WriteString(m.style(styles.AtomMuted).Render(prefix) + lines[0])
	for _, line := range lines[1:] {
		rendered.WriteString("\n" + strings.Repeat(" ", prefixW) + line)
	}
	return rendered.String()
}

// wrapTruncatedFieldValue wraps s across at most maxLines of width cells,
// appending "…" on the final line when the string is longer.
func wrapTruncatedFieldValue(s string, width, maxLines int) []string {
	if maxLines <= 0 {
		return []string{""}
	}
	width = max(width, 1)
	lines := make([]string, 0, maxLines)
	remaining := s
	for line := range maxLines {
		if lipgloss.Width(remaining) <= width {
			lines = append(lines, remaining)
			break
		}
		if line == maxLines-1 {
			lines = append(lines, truncateFieldValue(remaining, width))
			break
		}
		part, rest := splitFieldValue(remaining, width)
		lines = append(lines, part)
		remaining = rest
	}
	if len(lines) == 0 {
		return []string{""}
	}
	return lines
}

// splitFieldValue splits s so the first return value fits within width cells.
func splitFieldValue(s string, width int) (string, string) {
	if width <= 0 {
		return "", s
	}
	var b strings.Builder
	for i, r := range s {
		candidate := b.String() + string(r)
		if lipgloss.Width(candidate) > width {
			return b.String(), s[i:]
		}
		b.WriteRune(r)
	}
	return b.String(), ""
}

// truncateFieldValue truncates s to at most width visible cells, appending "…"
// when the string is longer.
func truncateFieldValue(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}
	part, _ := splitFieldValue(s, width-1)
	return part + "…"
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

func (m PermissionModal) style(a styles.Atom) lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle()
	}
	return m.Theme.Style(a)
}

func (m PermissionModal) contentWidth() int {
	return ModalContentWidth(m.Width)
}
