package components

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// MemoryModal renders active memories grouped by scope.
type MemoryModal struct {
	Width, Height int
	Theme         *styles.Styles
	Memories      []*session.Memory
	Query         string
	Cursor        int
	ForgetOnly    bool
}

// MemoryKey is the dialog-local key event shape used by tests and the UI app.
type MemoryKey struct {
	Name  string
	Runes []rune
}

// MemoryModalMsg is emitted when the memory dialog wants the App to act.
type MemoryModalMsg interface{ memoryModalMsg() }

// CloseMemoryModal requests closing the memory dialog.
type CloseMemoryModal struct{}

// ForgetMemoryAction requests deleting a memory by scope and id.
type ForgetMemoryAction struct {
	Scope session.MemoryScope
	ID    string
}

func (CloseMemoryModal) memoryModalMsg()   {}
func (ForgetMemoryAction) memoryModalMsg() {}

// Filtered returns active memories after applying the current search query.
func (m MemoryModal) Filtered() []*session.Memory {
	q := strings.ToLower(strings.TrimSpace(m.Query))
	out := make([]*session.Memory, 0, len(m.Memories))
	for _, mem := range m.Memories {
		if mem == nil {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{string(mem.Scope), mem.ID, mem.Title, memoryBody(mem), mem.Path}, " "))
		if q == "" || strings.Contains(hay, q) {
			out = append(out, mem)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Scope == out[j].Scope {
			if out[i].CreatedAt.Equal(out[j].CreatedAt) {
				return out[i].ID < out[j].ID
			}
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return memoryScopeRank(out[i].Scope) < memoryScopeRank(out[j].Scope)
	})
	return out
}

// HandleKey updates dialog state for one key and may emit an action message.
func (m MemoryModal) HandleKey(k MemoryKey) (MemoryModal, MemoryModalMsg) {
	filtered := m.Filtered()
	switch k.Name {
	case "esc":
		return m, CloseMemoryModal{}
	case "up", "ctrl+p":
		if len(filtered) > 0 && m.Cursor > 0 {
			m.Cursor--
		}
	case "down", "ctrl+n":
		if len(filtered) > 0 && m.Cursor < len(filtered)-1 {
			m.Cursor++
		}
	case "enter":
		if m.ForgetOnly && len(filtered) > 0 {
			mem := filtered[m.Cursor]
			return m, ForgetMemoryAction{Scope: mem.Scope, ID: mem.ID}
		}
	case "f":
		if len(filtered) > 0 {
			mem := filtered[m.Cursor]
			return m, ForgetMemoryAction{Scope: mem.Scope, ID: mem.ID}
		}
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
func (m MemoryModal) View() string {
	width, height := m.Width, m.Height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(minInt(width-8, 110))
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Faint(true)
	highlight := lipgloss.NewStyle().Bold(true)
	if m.Theme != nil {
		border = border.BorderForeground(m.Theme.Style(styles.AtomModalBorder).GetForeground())
		primary = m.Theme.Style(styles.AtomPrimary).Bold(true)
		muted = m.Theme.Style(styles.AtomMuted)
		highlight = m.Theme.Style(styles.AtomAccent).Bold(true)
	}
	filtered := m.Filtered()
	var b strings.Builder
	title := "Memories"
	if m.ForgetOnly {
		title = "Forget memory"
	}
	b.WriteString(primary.Render(title) + "\n")
	b.WriteString(muted.Render("Search active session, project, and global memories") + "\n\n")
	fmt.Fprintf(&b, "Search: %s\n", m.Query)
	if len(m.Memories) == 0 {
		b.WriteString("\n" + muted.Render("No active memories."))
	} else if len(filtered) == 0 {
		b.WriteString("\n" + muted.Render("No memories match the current search."))
	} else {
		limit := minInt(len(filtered), maxInt(4, height-12))
		start := 0
		if m.Cursor >= limit {
			start = m.Cursor - limit + 1
		}
		lastScope := session.MemoryScope("")
		for i := start; i < len(filtered) && i < start+limit; i++ {
			mem := filtered[i]
			if mem.Scope != lastScope {
				if i != start {
					b.WriteString("\n")
				}
				b.WriteString(muted.Render(memoryScopeTitle(mem.Scope)) + "\n")
				lastScope = mem.Scope
			}
			line := memoryLine(mem, muted)
			if i == m.Cursor {
				line = highlight.Render("› " + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + "\n")
		}
	}
	help := "↑/↓ ctrl+n/ctrl+p navigate   type to search   f forget   esc close"
	if m.ForgetOnly {
		help = "↑/↓ navigate   enter forget   type to search   esc close"
	}
	b.WriteString("\n" + muted.Render(help))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, border.Render(b.String()))
}

func memoryLine(mem *session.Memory, muted lipgloss.Style) string {
	title := strings.TrimSpace(mem.Title)
	if title == "" {
		title = strings.TrimSpace(memoryBody(mem))
	}
	if title == "" {
		title = mem.ID
	}
	body := strings.TrimSpace(memoryBody(mem))
	if body == title {
		body = ""
	}
	parts := []string{fmt.Sprintf("%s  %s", title, muted.Render(mem.ID))}
	if body != "" {
		parts = append(parts, muted.Render(truncateText(body, 90)))
	}
	if mem.Path != "" {
		parts = append(parts, muted.Render(mem.Path))
	}
	return strings.Join(parts, "  ")
}

func memoryBody(mem *session.Memory) string {
	if strings.TrimSpace(mem.Body) != "" {
		return mem.Body
	}
	return mem.Content
}

func memoryScopeRank(scope session.MemoryScope) int {
	switch scope {
	case session.MemoryScopeSession:
		return 0
	case session.MemoryScopeProject:
		return 1
	case session.MemoryScopeGlobal:
		return 2
	default:
		return 3
	}
}

func memoryScopeTitle(scope session.MemoryScope) string {
	switch scope {
	case session.MemoryScopeSession:
		return "Session"
	case session.MemoryScopeProject:
		return "Project"
	case session.MemoryScopeGlobal:
		return "Global"
	default:
		return "Other"
	}
}

func truncateText(s string, maxLen int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= maxLen {
		return string(r)
	}
	if maxLen <= 1 {
		return "…"
	}
	return string(r[:maxLen-1]) + "…"
}
