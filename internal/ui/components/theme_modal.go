package components

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// ThemeModal renders and updates the theme-selection dialog.
type ThemeModal struct {
	Width, Height int
	Theme         *theme.Theme
	Current       string
	Query         string
	Cursor        int
	Themes        []string
}

// ThemeKey is the dialog-local key event shape used by tests and the UI app.
type ThemeKey struct {
	Name  string
	Runes []rune
}

// ThemeModalMsg is emitted when the dialog wants the App to perform an action.
type ThemeModalMsg interface{ themeModalMsg() }

// CloseThemeModal requests closing the theme dialog without changing theme.
type CloseThemeModal struct{}

// SelectThemeAction requests switching to Name for the current session.
type SelectThemeAction struct{ Name string }

func (CloseThemeModal) themeModalMsg()   {}
func (SelectThemeAction) themeModalMsg() {}

// Filtered returns the theme list after applying the current search query.
func (m ThemeModal) Filtered() []string {
	q := strings.ToLower(strings.TrimSpace(m.Query))
	out := make([]string, 0, len(m.Themes))
	seen := map[string]bool{}
	for _, name := range m.Themes {
		if seen[name] {
			continue
		}
		seen[name] = true
		if q == "" || strings.Contains(strings.ToLower(name), q) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// HandleKey updates dialog state for one key and may emit an action message.
func (m ThemeModal) HandleKey(k ThemeKey) (ThemeModal, ThemeModalMsg) {
	filtered := m.Filtered()
	switch k.Name {
	case "esc":
		return m, CloseThemeModal{}
	case "up", "ctrl+p":
		if len(filtered) > 0 && m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "ctrl+n":
		if len(filtered) > 0 && m.Cursor < len(filtered)-1 {
			m.Cursor++
		}
	case "enter":
		if len(filtered) == 0 {
			return m, nil
		}
		return m, SelectThemeAction{Name: filtered[m.Cursor]}
	case "backspace":
		if m.Query != "" {
			r := []rune(m.Query)
			m.Query = string(r[:len(r)-1])
			m.Cursor = 0
		}
	default:
		if len(k.Runes) > 0 {
			m.Query += string(k.Runes)
			m.Cursor = 0
		}
	}
	return m, nil
}

// View renders the dialog into a centered terminal string.
func (m ThemeModal) View() string {
	width, height := m.Width, m.Height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(minInt(width-8, 90))
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Faint(true)
	highlight := lipgloss.NewStyle().Bold(true)
	if m.Theme != nil {
		border = border.BorderForeground(m.Theme.Style(theme.AtomModalBorder).GetForeground())
		primary = m.Theme.Style(theme.AtomPrimary).Bold(true)
		muted = m.Theme.Style(theme.AtomMuted)
		highlight = m.Theme.Style(theme.AtomAccent).Bold(true)
	}
	filtered := m.Filtered()
	var b strings.Builder
	b.WriteString(primary.Render("Select theme") + "\n")
	b.WriteString(muted.Render("Search available themes") + "\n\n")
	fmt.Fprintf(&b, "Search: %s\n", m.Query)
	b.WriteString(muted.Render("Theme      Preview") + "\n")
	if len(filtered) == 0 {
		b.WriteString("\n" + muted.Render("No themes match the current search."))
	} else {
		limit := minInt(len(filtered), maxInt(4, height-12))
		start := 0
		if m.Cursor >= limit {
			start = m.Cursor - limit + 1
		}
		for i := start; i < len(filtered) && i < start+limit; i++ {
			name := filtered[i]
			line := fmt.Sprintf("%-10s %s %s %s", name, primary.Render("primary"), muted.Render("muted"), highlight.Render("accent"))
			if name == m.Current {
				line += "  " + muted.Render("current")
			}
			if i == m.Cursor {
				line = highlight.Render("› " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + "\n")
		}
	}
	b.WriteString("\n" + muted.Render("↑/↓ ctrl+n/ctrl+p navigate   enter select   esc close"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, border.Render(b.String()))
}
