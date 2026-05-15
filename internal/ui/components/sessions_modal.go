package components

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// SessionsModal is the bubbletea-style component for the session management
// overlay.  It is a pure-view struct: the App owns the state (sessions list,
// cursor, filter) and calls Update then View on each tick.
//
// The modal supports keyboard navigation, substring filtering, and the
// following actions: switch, rename, fork at latest, delete.
//
// All user-input / action messages emitted by the modal are returned as
// tea.Msg values; the App applies the side effects.
type SessionsModal struct {
	// Sessions is the full unfiltered session list (newest first).
	// The modal renders only the rows that survive the filter.
	Sessions []*session.Session

	// ForegroundID is the currently active session's id.  That row is
	// highlighted differently.
	ForegroundID string

	// Cursor is the currently highlighted row index into the filtered list.
	Cursor int

	// FilterValue is the active substring filter.
	FilterValue string

	// FilterFocused is true when keyboard input goes to the filter field.
	FilterFocused bool

	// ShowSubagents controls whether subagent rows are visible.
	ShowSubagents bool

	// ShowDeleted controls whether soft-deleted rows are visible.
	ShowDeleted bool

	// RenameMode is true when the inline rename field is open.
	RenameMode bool

	// RenameValue is the current value in the rename input.
	RenameValue string

	// ConfirmDelete is true when the delete-confirmation prompt is open.
	ConfirmDelete bool

	// ShowHelp toggles the inline keybind cheatsheet.
	ShowHelp bool

	// Toast is an ephemeral single-line message (error / info).
	Toast string

	// Width is the available terminal width.
	Width int

	// Height is the available terminal height.
	Height int

	// Theme is the active theme.
	Theme *theme.Theme

	// Now is the wall-clock used for "N ago" labels.
	Now time.Time

	// AllowNew, when true, shows a "No sessions yet. [n] new session [esc] cancel"
	// affordance when the filtered list is empty.  Used when the picker is
	// opened on start (resume_default="ask", or `hygge resume` in an empty project).
	AllowNew bool
}

// --- Emitted message types --------------------------------------------------
// These are returned from the Update method so the App can apply side effects
// without needing to import this package from appmsg.go.  The App casts them
// via a type switch.

// SessionsModalMsg is the tagged union of messages the modal emits.
type SessionsModalMsg interface{ sessionsModalMsg() }

// SwitchSessionAction asks the App to switch the foreground session.
type SwitchSessionAction struct{ ID string }

func (SwitchSessionAction) sessionsModalMsg() {}

// ForkSessionAction asks the App to fork a session.
// MessageID == "" means fork at the latest user message.
type ForkSessionAction struct {
	ID        string
	MessageID string
}

func (ForkSessionAction) sessionsModalMsg() {}

// RenameSessionAction asks the App to rename a session.
type RenameSessionAction struct {
	ID   string
	Slug string
}

func (RenameSessionAction) sessionsModalMsg() {}

// DeleteSessionAction asks the App to soft-delete a session.
type DeleteSessionAction struct{ ID string }

func (DeleteSessionAction) sessionsModalMsg() {}

// CloseSessionsModal asks the App to close the modal.
type CloseSessionsModal struct{}

func (CloseSessionsModal) sessionsModalMsg() {}

// NewSessionAction asks the App to start a fresh session.  Emitted when
// the user presses 'n' in the empty-list picker.
type NewSessionAction struct{}

func (NewSessionAction) sessionsModalMsg() {}

// --- Update -----------------------------------------------------------------

// SessionsKey is the subset of keys the modal handles.
type SessionsKey struct {
	Name  string
	Runes []rune
}

// HandleKey processes one key press in the modal.  Returns (newState,
// emittedMsg).  emittedMsg is nil when no action was triggered.
func (m SessionsModal) HandleKey(k SessionsKey) (SessionsModal, SessionsModalMsg) {
	// Clear transient toast on each keypress.
	m.Toast = ""

	if m.RenameMode {
		return m.handleRenameKey(k)
	}
	if m.ConfirmDelete {
		return m.handleDeleteConfirmKey(k)
	}
	if m.FilterFocused {
		return m.handleFilterKey(k)
	}
	return m.handleListKey(k)
}

// handleFilterKey routes keys when the filter text field has focus.
func (m SessionsModal) handleFilterKey(k SessionsKey) (SessionsModal, SessionsModalMsg) {
	switch k.Name {
	case "esc":
		if m.FilterValue != "" {
			// First esc: clear filter, keep focus.
			m.FilterValue = ""
			m.Cursor = 0
		} else {
			// Second esc (filter already empty): close modal.
			return m, CloseSessionsModal{}
		}
	case "tab", "enter":
		m.FilterFocused = false
	case "backspace":
		if len(m.FilterValue) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.FilterValue)
			m.FilterValue = m.FilterValue[:len(m.FilterValue)-size]
			m.Cursor = 0
		}
	default:
		if len(k.Runes) == 1 {
			m.FilterValue += string(k.Runes)
			m.Cursor = 0
		}
	}
	return m, nil
}

// handleListKey routes keys when the list has focus.
func (m SessionsModal) handleListKey(k SessionsKey) (SessionsModal, SessionsModalMsg) {
	filtered := m.filteredSessions()
	n := len(filtered)

	switch k.Name {
	case "esc":
		return m, CloseSessionsModal{}
	case "up", "k":
		if m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "j":
		if n > 0 && m.Cursor < n-1 {
			m.Cursor++
		}
	case "g":
		m.Cursor = 0
	case "G":
		if n > 0 {
			m.Cursor = n - 1
		}
	case "/":
		m.FilterFocused = true
	case "?":
		m.ShowHelp = !m.ShowHelp
	case "s":
		m.ShowSubagents = !m.ShowSubagents
		m.Cursor = 0
	case "d":
		m.ShowDeleted = !m.ShowDeleted
		m.Cursor = 0
	case "n":
		// 'n' starts a fresh session when AllowNew is set and the filtered
		// list is empty (the typical picker-on-start with no sessions case).
		if m.AllowNew && n == 0 {
			return m, NewSessionAction{}
		}
	case "enter":
		if n == 0 {
			return m, nil
		}
		selected := filtered[m.Cursor]
		if !selected.DeletedAt.IsZero() {
			m.Toast = "cannot switch to a deleted session"
			return m, nil
		}
		if selected.ID == m.ForegroundID {
			return m, CloseSessionsModal{}
		}
		return m, SwitchSessionAction{ID: selected.ID}
	case "r":
		if n == 0 {
			return m, nil
		}
		selected := filtered[m.Cursor]
		m.RenameMode = true
		m.RenameValue = selected.Slug
	case "f":
		if n == 0 {
			return m, nil
		}
		selected := filtered[m.Cursor]
		return m, ForkSessionAction{ID: selected.ID, MessageID: ""}
	case "x":
		if n == 0 {
			return m, nil
		}
		m.ConfirmDelete = true
	default:
		// rune 'r' handled above by name; others ignored
	}
	return m, nil
}

// handleRenameKey processes keys inside the inline rename field.
func (m SessionsModal) handleRenameKey(k SessionsKey) (SessionsModal, SessionsModalMsg) {
	filtered := m.filteredSessions()
	switch k.Name {
	case "esc":
		m.RenameMode = false
		m.RenameValue = ""
	case "backspace":
		if len(m.RenameValue) > 0 {
			_, size := utf8.DecodeLastRuneInString(m.RenameValue)
			m.RenameValue = m.RenameValue[:len(m.RenameValue)-size]
		}
	case "enter":
		if m.Cursor < len(filtered) {
			id := filtered[m.Cursor].ID
			slug := m.RenameValue
			m.RenameMode = false
			m.RenameValue = ""
			return m, RenameSessionAction{ID: id, Slug: slug}
		}
		m.RenameMode = false
		m.RenameValue = ""
	default:
		if len(k.Runes) == 1 {
			m.RenameValue += string(k.Runes)
		}
	}
	return m, nil
}

// handleDeleteConfirmKey routes y/n/esc inside the delete-confirmation prompt.
func (m SessionsModal) handleDeleteConfirmKey(k SessionsKey) (SessionsModal, SessionsModalMsg) {
	filtered := m.filteredSessions()
	switch k.Name {
	case "esc", "n", "N":
		m.ConfirmDelete = false
	case "y", "Y":
		m.ConfirmDelete = false
		if m.Cursor < len(filtered) {
			return m, DeleteSessionAction{ID: filtered[m.Cursor].ID}
		}
	}
	return m, nil
}

// FilteredCount returns the number of sessions that would survive the
// current filter/visibility settings.  Used by the App to clamp the cursor
// after a delete.
func (m SessionsModal) FilteredCount() int {
	return len(m.filteredSessions())
}

// filteredSessions returns the session list after applying filter, subagent
// visibility, and deleted visibility.
func (m SessionsModal) filteredSessions() []*session.Session {
	var out []*session.Session
	lower := strings.ToLower(m.FilterValue)
	for _, s := range m.Sessions {
		// Deleted visibility.
		if !m.ShowDeleted && !s.DeletedAt.IsZero() {
			continue
		}
		// Subagent visibility.
		if !m.ShowSubagents && s.Kind == session.KindSubagent {
			continue
		}
		// Substring filter.
		if lower != "" {
			slug := strings.ToLower(s.Slug)
			proj := strings.ToLower(s.ProjectDir)
			preview := strings.ToLower(s.FirstMessagePreview)
			if !strings.Contains(slug, lower) &&
				!strings.Contains(proj, lower) &&
				!strings.Contains(preview, lower) {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// --- View -------------------------------------------------------------------

// View renders the modal centered in Width×Height.
func (m SessionsModal) View() string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	height := m.Height
	if height <= 0 {
		height = 24
	}
	box := m.renderBox(width)
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m SessionsModal) renderBox(termWidth int) string {
	boxWidth := termWidth - 4
	if boxWidth < 60 {
		boxWidth = 60
	}
	if boxWidth > 120 {
		boxWidth = 120
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	if m.Theme != nil {
		bs := m.Theme.Style(theme.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := m.Theme.Style(theme.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}

	filtered := m.filteredSessions()
	total := len(m.Sessions)
	visible := len(filtered)

	var b strings.Builder

	// Header.
	header := fmt.Sprintf("Sessions (%d of %d)", visible, total)
	b.WriteString(m.bold().Render(header))
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", boxWidth))
	b.WriteString("\n")

	// Filter field.
	filterLine := "/ filter: " + m.FilterValue
	if m.FilterFocused {
		filterLine += "█"
	}
	b.WriteString(m.style(theme.AtomMuted).Render(filterLine))
	b.WriteString("\n\n")

	// Column header.
	b.WriteString(m.style(theme.AtomMuted).Render(
		fmt.Sprintf("  %-40s %-24s %-10s %5s  %7s  %s",
			"NAME", "MODEL", "UPDATED", "TURNS", "COST", "KIND",
		),
	))
	b.WriteString("\n")

	// Rows.
	if len(filtered) == 0 {
		b.WriteString("\n  ")
		if m.AllowNew {
			b.WriteString(m.style(theme.AtomMuted).Italic(true).Render("No sessions yet."))
			b.WriteString("  ")
			b.WriteString(m.style(theme.AtomAccent).Render("[n] new session"))
			b.WriteString("   ")
			b.WriteString(m.style(theme.AtomMuted).Render("[esc] cancel"))
		} else {
			b.WriteString(m.style(theme.AtomMuted).Italic(true).Render("no sessions match"))
		}
		b.WriteString("\n")
	} else {
		for i, s := range filtered {
			b.WriteString(m.renderRow(i, s, filtered, boxWidth))
		}
	}

	// Toast.
	if m.Toast != "" {
		b.WriteString("\n")
		b.WriteString(m.style(theme.AtomWarn).Render("  " + m.Toast))
		b.WriteString("\n")
	}

	// Help.
	b.WriteString("\n")
	b.WriteString(m.style(theme.AtomMuted).Render("  [↑/k↓/j] nav  [enter] switch  [r] rename  [f] fork  [x] delete  [s] subagents  [/] filter  [?] help  [esc] close"))

	if m.ShowHelp {
		b.WriteString("\n")
		b.WriteString(m.renderHelp())
	}

	return border.Render(b.String())
}

func (m SessionsModal) renderRow(i int, s *session.Session, _ []*session.Session, _ int) string {
	isCursor := i == m.Cursor
	isFG := s.ID == m.ForegroundID
	isDeleted := !s.DeletedAt.IsZero()
	isSubagent := s.Kind == session.KindSubagent

	var prefix string
	switch {
	case isCursor && isFG:
		prefix = "▶*"
	case isCursor:
		prefix = "▶ "
	case isFG:
		prefix = " *"
	case isSubagent:
		prefix = " └"
	default:
		prefix = "  "
	}

	// Name: prefer slug; fall back to first_message_preview, then id prefix.
	name := s.Slug
	if name == "" && s.FirstMessagePreview != "" {
		name = truncate(s.FirstMessagePreview, 38)
	}
	if name == "" {
		name = s.ID[:8]
	}

	model := s.Model.Provider + "/" + s.Model.Name
	updated := humanAgo(s.UpdatedAt, m.Now)
	ownCost := formatCostValue(s.OwnTotals.CostUSD)
	rolledUp := formatCostValue(s.Totals.CostUSD)
	// Show rolled-up in parentheses only when it materially exceeds own cost.
	showRollup := s.Totals.CostUSD > s.OwnTotals.CostUSD*1.001 && s.Totals.CostUSD > s.OwnTotals.CostUSD+0.00001
	kind := string(s.Kind)

	rowStyle := lipgloss.NewStyle()
	switch {
	case isDeleted:
		rowStyle = m.style(theme.AtomMuted).Strikethrough(true)
	case isFG && isCursor:
		rowStyle = m.style(theme.AtomAccent).Bold(true)
	case isFG:
		rowStyle = m.style(theme.AtomSuccess).Bold(true)
	case isCursor:
		rowStyle = m.style(theme.AtomPrimary)
	case isSubagent:
		rowStyle = m.style(theme.AtomMuted)
	}

	// The cost column is %7s wide.  When we have a rolled-up suffix we
	// render the own-cost in that slot and append the muted parens after.
	var b strings.Builder
	if showRollup {
		// Render the row with own cost, then append muted rolled-up suffix.
		row := fmt.Sprintf("%s %-38s %-22s %-10s %5s  %7s  %s",
			prefix,
			truncate(name, 38),
			truncate(model, 22),
			updated,
			"", // turns — store doesn't expose this cheaply; leave blank
			ownCost,
			kind,
		)
		b.WriteString(rowStyle.Render(row))
		b.WriteString(m.style(theme.AtomMuted).Render(" (" + rolledUp + ")"))
	} else {
		row := fmt.Sprintf("%s %-38s %-22s %-10s %5s  %7s  %s",
			prefix,
			truncate(name, 38),
			truncate(model, 22),
			updated,
			"", // turns — store doesn't expose this cheaply; leave blank
			ownCost,
			kind,
		)
		b.WriteString(rowStyle.Render(row))
	}
	b.WriteString("\n")

	// Inline rename.
	if m.RenameMode && isCursor {
		b.WriteString(m.style(theme.AtomAccent).Render(
			fmt.Sprintf("   rename: %s█", m.RenameValue),
		))
		b.WriteString("\n")
	}

	// Delete confirmation.
	if m.ConfirmDelete && isCursor {
		b.WriteString(m.style(theme.AtomWarn).Render(
			"   Delete this session? [y/n]",
		))
		b.WriteString("\n")
	}

	return b.String()
}

// formatCostValue formats a cost in USD as $X.XXXX (4 decimal places).
// Returns "—" when cost is zero.
func formatCostValue(cost float64) string {
	if cost == 0 {
		return "—"
	}
	return fmt.Sprintf("$%.4f", cost)
}

func (m SessionsModal) renderHelp() string {
	lines := []string{
		"  ──────── keybinds ────────────────────────────",
		"  ↑/k  up            ↓/j  down",
		"  g    top           G    bottom",
		"  enter  switch      r    rename",
		"  f    fork(latest)  x    delete",
		"  s    toggle subagents    d  toggle deleted",
		"  /    filter        ?    toggle help",
		"  esc  close",
	}
	return m.style(theme.AtomMuted).Render(strings.Join(lines, "\n"))
}

// --- Style helpers ----------------------------------------------------------

func (m SessionsModal) bold() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	return m.Theme.Style(theme.AtomPrimary).Bold(true)
}

func (m SessionsModal) style(a theme.Atom) lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle()
	}
	return m.Theme.Style(a)
}

// --- Utility ----------------------------------------------------------------

// HumanAgo renders the elapsed time since t as a compact human string.
// Exposed as a method for testability.
func (m SessionsModal) HumanAgo(t time.Time, now time.Time) string {
	return humanAgo(t, now)
}

// humanAgo renders the elapsed time since t as a compact human string.
func humanAgo(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := now.Sub(t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd ago", days)
	}
}
