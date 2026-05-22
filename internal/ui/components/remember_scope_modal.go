package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// RememberScopeModal lets the user choose where a no-scope /remember should save.
type RememberScopeModal struct {
	Width, Height int
	Theme         *styles.Styles
	Content       string
	Cursor        int
}

// RememberScopeModalMsg is emitted when the scope picker wants the App to act.
type RememberScopeModalMsg interface{ rememberScopeModalMsg() }

// CloseRememberScopeModal requests closing the remember scope picker.
type CloseRememberScopeModal struct{}

// RememberScopeAction requests saving the draft content at a chosen scope.
type RememberScopeAction struct {
	Scope   session.MemoryScope
	Content string
}

func (CloseRememberScopeModal) rememberScopeModalMsg() {}
func (RememberScopeAction) rememberScopeModalMsg()     {}

// RememberScopeKey is the dialog-local key event shape used by tests and the UI app.
type RememberScopeKey struct {
	Name string
}

// HandleKey updates dialog state for one key and may emit an action message.
func (m RememberScopeModal) HandleKey(k RememberScopeKey) (RememberScopeModal, RememberScopeModalMsg) {
	scopes := rememberScopeOptions()
	switch k.Name {
	case "esc":
		return m, CloseRememberScopeModal{}
	case "up", "ctrl+p":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "ctrl+n":
		if m.Cursor < len(scopes)-1 {
			m.Cursor++
		}
	case "enter":
		return m, RememberScopeAction{Scope: scopes[m.Cursor].scope, Content: m.Content}
	}
	return m, nil
}

// View renders the dialog into a centered terminal string.
func (m RememberScopeModal) View() string {
	width, height := m.Width, m.Height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 22
	}
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(minInt(width-8, 88))
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Faint(true)
	highlight := lipgloss.NewStyle().Bold(true)
	if m.Theme != nil {
		border = border.BorderForeground(m.Theme.Style(styles.AtomModalBorder).GetForeground())
		primary = m.Theme.Style(styles.AtomPrimary).Bold(true)
		muted = m.Theme.Style(styles.AtomMuted)
		highlight = m.Theme.Style(styles.AtomAccent).Bold(true)
	}

	var b strings.Builder
	b.WriteString(primary.Render("Remember memory") + "\n")
	b.WriteString(muted.Render("Choose where this memory should be saved") + "\n\n")
	content := strings.TrimSpace(m.Content)
	if content == "" {
		b.WriteString(muted.Render("No fact entered yet; choosing a scope will show usage guidance.") + "\n\n")
	} else {
		fmt.Fprintf(&b, "Fact: %s\n\n", muted.Render(truncateText(content, 100)))
	}

	for i, opt := range rememberScopeOptions() {
		line := fmt.Sprintf("%s  %s", opt.label, muted.Render(opt.description))
		if i == m.Cursor {
			line = highlight.Render("› " + line)
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + muted.Render("↑/↓ navigate   enter save   esc close"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, border.Render(b.String()))
}

type rememberScopeOption struct {
	scope       session.MemoryScope
	label       string
	description string
}

func rememberScopeOptions() []rememberScopeOption {
	return []rememberScopeOption{
		{scope: session.MemoryScopeSession, label: "Session", description: "this conversation only"},
		{scope: session.MemoryScopeProject, label: "Project", description: "this repository's .hygge/memory"},
		{scope: session.MemoryScopeGlobal, label: "Global", description: "all Hygge projects for this user"},
	}
}
