package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// APIKeyModal renders and updates the provider API-key dialog.
type APIKeyModal struct {
	Width, Height int
	Theme         *styles.Styles
	Provider      string
	HasExisting   bool
	Value         string
}

// APIKeyKey is the dialog-local key event shape used by tests and the UI app.
type APIKeyKey struct {
	Name  string
	Runes []rune
}

// APIKeyModalMsg is emitted when the dialog wants the App to perform an action.
type APIKeyModalMsg interface{ apiKeyModalMsg() }

// CloseAPIKeyModal requests closing the API-key dialog without saving.
type CloseAPIKeyModal struct{}

// SaveAPIKeyAction requests saving APIKey for Provider.
type SaveAPIKeyAction struct{ Provider, APIKey string }

func (CloseAPIKeyModal) apiKeyModalMsg() {}
func (SaveAPIKeyAction) apiKeyModalMsg() {}

// HandleKey updates dialog state for one key and may emit an action message.
func (m APIKeyModal) HandleKey(k APIKeyKey) (APIKeyModal, APIKeyModalMsg) {
	switch k.Name {
	case "esc":
		return m, CloseAPIKeyModal{}
	case "enter":
		if strings.TrimSpace(m.Value) == "" {
			return m, nil
		}
		return m, SaveAPIKeyAction{Provider: m.Provider, APIKey: m.Value}
	case "backspace":
		if m.Value != "" {
			r := []rune(m.Value)
			m.Value = string(r[:len(r)-1])
		}
	case "ctrl+u":
		m.Value = ""
	default:
		if len(k.Runes) > 0 {
			m.Value += string(k.Runes)
		}
	}
	return m, nil
}

// View renders the dialog into a centered terminal string.
func (m APIKeyModal) View() string {
	width, height := m.Width, m.Height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(minInt(width-8, 88))
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Faint(true)
	if m.Theme != nil {
		border = border.BorderForeground(m.Theme.Style(styles.AtomModalBorder).GetForeground())
		primary = m.Theme.Style(styles.AtomPrimary).Bold(true)
		muted = m.Theme.Style(styles.AtomMuted)
	}
	existing := "not configured"
	if m.HasExisting {
		existing = "configured (••••)"
	}
	masked := strings.Repeat("•", len([]rune(m.Value)))
	var b strings.Builder
	b.WriteString(primary.Render("Set API key") + "\n")
	b.WriteString(muted.Render("Provider credentials are stored in config.toml") + "\n\n")
	fmt.Fprintf(&b, "Provider: %s\n", m.Provider)
	fmt.Fprintf(&b, "Current:  %s\n\n", existing)
	fmt.Fprintf(&b, "New key:  %s\n", masked)
	b.WriteString("\n" + muted.Render("enter save   esc cancel   backspace edit   ctrl+u clear"))
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, border.Render(b.String()))
}
