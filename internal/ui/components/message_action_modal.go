package components

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// MessageActionKey is a key event routed to MessageActionModal.HandleKey.
type MessageActionKey struct {
	Name  string
	Runes []rune
}

// MessageActionModalMsg is the sealed interface for messages emitted by
// MessageActionModal.HandleKey.
type MessageActionModalMsg interface{ messageActionModalMsg() }

// CloseMessageActionModal is emitted when the user dismisses the modal.
type CloseMessageActionModal struct{}

func (CloseMessageActionModal) messageActionModalMsg() {}

// CopyMessageAction is emitted when the user selects "Copy message".
type CopyMessageAction struct {
	Text string // raw message text to copy
}

func (CopyMessageAction) messageActionModalMsg() {}

// ForkMessageAction is emitted when the user selects "Fork from here".
type ForkMessageAction struct {
	SessionID string // session to fork from
	MessageID string // message ID to fork at
}

func (ForkMessageAction) messageActionModalMsg() {}

// messageActionItem is one selectable row in the modal.
type messageActionItem struct {
	label       string
	description string
}

var messageActionItems = []messageActionItem{
	{"copy", "copy message text to clipboard"},
	{"fork", "fork session from this message"},
}

// MessageActionModal is a lightweight two-action modal shown when the user
// clicks on a user message bubble.  It offers copy and fork actions.
//
// Following the components convention the modal is a pure value type:
// HandleKey returns an updated copy plus an optional action message.
// The App owns the mutable state.
type MessageActionModal struct {
	Width       int
	Height      int
	Theme       *styles.Styles
	SessionID   string // current foreground session id
	MessageID   string // message id clicked
	MessageText string // raw text of the clicked message
	Cursor      int    // selected row (0=copy, 1=fork)
}

// HandleKey processes a key press and returns the updated modal plus an
// optional action.  Returns nil msg when no action was triggered.
func (m MessageActionModal) HandleKey(k MessageActionKey) (MessageActionModal, MessageActionModalMsg) {
	switch k.Name {
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if m.Cursor < len(messageActionItems)-1 {
			m.Cursor++
		}
	case "enter":
		return m, m.selectedAction()
	case "esc", "q":
		return m, CloseMessageActionModal{}
	case "1":
		m.Cursor = 0
		return m, m.selectedAction()
	case "2":
		m.Cursor = 1
		return m, m.selectedAction()
	case "c":
		m.Cursor = 0
		return m, m.selectedAction()
	case "f":
		m.Cursor = 1
		return m, m.selectedAction()
	}
	return m, nil
}

func (m MessageActionModal) selectedAction() MessageActionModalMsg {
	switch m.Cursor {
	case 0:
		return CopyMessageAction{Text: m.MessageText}
	case 1:
		return ForkMessageAction{SessionID: m.SessionID, MessageID: m.MessageID}
	}
	return CloseMessageActionModal{}
}

// View renders the modal centered in Width×Height.
func (m MessageActionModal) View() string {
	w := m.Width
	if w <= 0 {
		w = 80
	}
	h := m.Height
	if h <= 0 {
		h = 24
	}
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, m.renderBox())
}

func (m MessageActionModal) renderBox() string {
	contentW := ModalContentWidth(m.Width)

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

	// Header.
	b.WriteString(m.primary().Render("message"))
	b.WriteString("  ")
	b.WriteString(m.muted().Render("choose an action"))
	b.WriteString("\n\n")

	// Preview of the message text (first line, capped).
	preview := m.previewText(contentW - 4)
	if preview != "" {
		b.WriteString(m.muted().Width(contentW - 4).Render(preview))
		b.WriteString("\n\n")
	}

	// Action rows.
	for i, item := range messageActionItems {
		b.WriteString(m.actionRow(i, item, contentW))
		if i < len(messageActionItems)-1 {
			b.WriteString("\n\n")
		}
	}

	b.WriteString("\n\n")
	b.WriteString(m.muted().Render("↑/↓ move  enter select  c copy  f fork  esc cancel"))

	return border.Render(b.String())
}

func (m MessageActionModal) previewText(width int) string {
	if m.MessageText == "" {
		return ""
	}
	// Show only the first non-empty line to keep the modal compact.
	for line := range strings.SplitSeq(m.MessageText, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		runes := []rune(line)
		maxLen := max(width-2, 4)
		if len(runes) > maxLen {
			line = string(runes[:maxLen-1]) + "…"
		}
		return "\u201c" + line + "\u201d"
	}
	return ""
}

func (m MessageActionModal) actionRow(index int, item messageActionItem, width int) string {
	selected := index == m.clampedCursor()

	marker := " "
	markerStyle := m.muted()
	badgeStyle := m.muted()
	if selected {
		marker = "›"
		markerStyle = m.accent().Bold(true)
		badgeStyle = m.accent().Bold(true)
	}

	// Key hint: [c] or [f]
	key := []string{"c", "f"}[index]
	prefix := markerStyle.Render(marker) + " " + badgeStyle.Render("["+key+"]") + " "
	prefixW := lipgloss.Width(prefix)

	labelStyle := lipgloss.NewStyle()
	if selected {
		labelStyle = m.accent().Bold(true)
	} else if m.Theme != nil {
		labelStyle = m.Theme.Style(styles.AtomPrimary)
	}

	descStyle := m.muted()
	label := labelStyle.Render(item.label)
	desc := descStyle.Render("  " + item.description)

	row := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder(), false, false, false, true).
		BorderForeground(m.muted().GetForeground()).
		Padding(0, 1).
		Width(width - prefixW)
	if selected {
		row = row.BorderForeground(m.accent().GetForeground())
	}

	inner := label + desc
	return prefix + row.Render(inner)
}

func (m MessageActionModal) clampedCursor() int {
	if m.Cursor < 0 {
		return 0
	}
	if m.Cursor >= len(messageActionItems) {
		return len(messageActionItems) - 1
	}
	return m.Cursor
}

func (m MessageActionModal) primary() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	return m.Theme.Style(styles.AtomPrimary).Bold(true)
}

func (m MessageActionModal) muted() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Faint(true)
	}
	return m.Theme.Style(styles.AtomMuted)
}

func (m MessageActionModal) accent() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	return m.Theme.Style(styles.AtomAccent)
}
