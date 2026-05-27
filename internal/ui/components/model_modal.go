package components

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// ModelOption is one selectable catalog model in the model picker.
type ModelOption struct {
	Provider string
	Entry    catalog.Entry
}

// ConfiguredModelOption builds a selectable entry for a configured model that
// is missing from the catalog snapshot.
func ConfiguredModelOption(provider, model string) ModelOption {
	return ModelOption{
		Provider: provider,
		Entry: catalog.Entry{
			Provider: provider,
			ID:       model,
			Name:     model,
			Source:   catalog.SourceEmbedded,
		},
	}
}

// ModelModal renders and updates the model-selection dialog.
type ModelModal struct {
	Width, Height int
	Theme         *styles.Styles
	Current       string
	Query         string
	Cursor        int
	Models        []ModelOption
	// Favorites is the ordered list of "provider/model" refs the user has
	// starred.  Loaded from global state when the modal opens; toggled via
	// ctrl+f.  Favorites are shown first under a "Favorites" heading.
	Favorites []string
}

// ModelKey is the dialog-local key event shape used by tests and the UI app.
type ModelKey struct {
	Name  string
	Runes []rune
}

// ModelModalMsg is emitted when the dialog wants the App to perform an action.
type ModelModalMsg interface{ modelModalMsg() }

// CloseModelModal requests closing the model dialog without changing model.
type CloseModelModal struct{}

// SelectModelAction requests switching to Provider/Model for the current session.
type SelectModelAction struct{ Provider, Model string }

// ToggleFavoriteModelAction requests adding or removing the given model from
// the global favorites list.
type ToggleFavoriteModelAction struct{ Provider, Model string }

func (CloseModelModal) modelModalMsg()           {}
func (SelectModelAction) modelModalMsg()         {}
func (ToggleFavoriteModelAction) modelModalMsg() {}

// filteredResult is the internal split of filtered models into favorites (first)
// and the rest.  FavCount is the number of entries at the start that are
// favorites; Rest entries follow immediately.
type filteredResult struct {
	All      []ModelOption
	FavCount int // number of leading favorite entries
}

// favoriteSet returns a set of "provider/model" refs for O(1) lookup.
func (m ModelModal) favoriteSet() map[string]bool {
	if len(m.Favorites) == 0 {
		return nil
	}
	s := make(map[string]bool, len(m.Favorites))
	for _, f := range m.Favorites {
		s[f] = true
	}
	return s
}

// filteredSections returns the filtered list split into favorites-first order
// together with the count of leading favorite entries.
func (m ModelModal) filteredSections() filteredResult {
	q := strings.ToLower(strings.TrimSpace(m.Query))
	favSet := m.favoriteSet()

	var favs, rest []ModelOption
	for _, opt := range m.Models {
		hay := strings.ToLower(opt.Provider + " " + opt.Entry.ID + " " + opt.Entry.Name)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		ref := opt.Provider + "/" + opt.Entry.ID
		if favSet[ref] {
			favs = append(favs, opt)
		} else {
			rest = append(rest, opt)
		}
	}

	// Sort each section by provider then model id.
	sortOpts := func(slice []ModelOption) {
		sort.SliceStable(slice, func(i, j int) bool {
			if slice[i].Provider == slice[j].Provider {
				return slice[i].Entry.ID < slice[j].Entry.ID
			}
			return slice[i].Provider < slice[j].Provider
		})
	}
	sortOpts(favs)
	sortOpts(rest)

	all := make([]ModelOption, 0, len(favs)+len(rest))
	all = append(all, favs...)
	all = append(all, rest...)
	return filteredResult{All: all, FavCount: len(favs)}
}

// Filtered returns the model list after applying the current search query.
// Favorite models appear first (sorted within their group), followed by the
// remaining models sorted by provider then model id.
func (m ModelModal) Filtered() []ModelOption {
	return m.filteredSections().All
}

// HandleKey updates dialog state for one key and may emit an action message.
func (m ModelModal) HandleKey(k ModelKey) (ModelModal, ModelModalMsg) {
	filtered := m.Filtered()
	switch k.Name {
	case "esc":
		return m, CloseModelModal{}
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
		opt := filtered[m.Cursor]
		return m, SelectModelAction{Provider: opt.Provider, Model: opt.Entry.ID}
	case "ctrl+f":
		// Toggle favorite on the currently-highlighted model.
		if len(filtered) == 0 {
			return m, nil
		}
		opt := filtered[m.Cursor]
		m.Cursor = 0
		return m, ToggleFavoriteModelAction{Provider: opt.Provider, Model: opt.Entry.ID}
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
func (m ModelModal) View() string {
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
	heading := lipgloss.NewStyle().Bold(true)
	if m.Theme != nil {
		border = border.BorderForeground(m.Theme.Style(styles.AtomModalBorder).GetForeground())
		primary = m.Theme.Style(styles.AtomPrimary).Bold(true)
		muted = m.Theme.Style(styles.AtomMuted)
		highlight = m.Theme.Style(styles.AtomAccent).Bold(true)
		heading = m.Theme.Style(styles.AtomPrimary).Bold(true)
	}
	sections := m.filteredSections()
	filtered := sections.All
	var b strings.Builder
	b.WriteString(primary.Render("Select model") + "\n")
	b.WriteString(muted.Render("Search provider, model id, or display name") + "\n\n")
	fmt.Fprintf(&b, "Search: %s\n", m.Query)
	b.WriteString(muted.Render("Provider   Model / metadata") + "\n")
	if len(m.Models) == 0 {
		b.WriteString("\n" + muted.Render("No catalog models available."))
	} else if len(filtered) == 0 {
		b.WriteString("\n" + muted.Render("No models match the current search."))
	} else {
		limit := minInt(len(filtered), maxInt(4, height-12))
		start := 0
		if m.Cursor >= limit {
			start = m.Cursor - limit + 1
		}
		for i := start; i < len(filtered) && i < start+limit; i++ {
			if sections.FavCount > 0 && i == 0 {
				b.WriteString(heading.Render("  Favorites") + "\n")
			}
			if sections.FavCount > 0 && sections.FavCount < len(filtered) && i == sections.FavCount {
				b.WriteString("\n" + heading.Render("  All models") + "\n")
			}

			opt := filtered[i]
			name := opt.Entry.Name
			if name == "" {
				name = opt.Entry.ID
			}
			caps := []string{}
			if opt.Entry.Capabilities.Reasoning {
				caps = append(caps, "reasoning")
			}
			if opt.Entry.Capabilities.InputImages {
				caps = append(caps, "image")
			}
			meta := fmt.Sprintf("ctx %s  in $%.2f/M  out $%.2f/M", formatModelTokens(opt.Entry.Limit.ContextWindow), opt.Entry.Cost.Input, opt.Entry.Cost.Output)
			if len(caps) > 0 {
				meta += "  " + strings.Join(caps, ",")
			}
			line := fmt.Sprintf("%-10s %s  %s", opt.Provider, name, muted.Render(meta))
			if opt.Provider+"/"+opt.Entry.ID == m.Current {
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
	b.WriteString("\n" + muted.Render("↑/↓ ctrl+n/ctrl+p navigate   enter select   ctrl+f favorite   esc close"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, border.Render(b.String()))
}

func formatModelTokens(n int64) string {
	if n <= 0 {
		return "unknown"
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
