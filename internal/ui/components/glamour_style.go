package components

import (
	glamouransi "github.com/charmbracelet/glamour/ansi"
	glamourstyles "github.com/charmbracelet/glamour/styles"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// ThemeGlamourStyle builds a glamour ansi.StyleConfig from the active Hygge
// theme. It starts from the standard dark config (deterministic, no OSC 11
// query) and overrides only the key color fields that map cleanly to theme
// atoms:
//
//   - Heading color   ← AtomPrimary
//   - H1 background   ← AtomAccent
//   - Link color      ← AtomAccent
//   - LinkText color  ← AtomPrimary
//   - Inline code fg  ← AtomCodeFg
//   - Inline code bg  ← AtomCodeBg
//
// When t is nil the unmodified DarkStyleConfig is returned.
func ThemeGlamourStyle(t *theme.Theme) glamouransi.StyleConfig {
	cfg := glamourstyles.DarkStyleConfig
	if t == nil {
		return cfg
	}

	// Heading color (general heading)
	if c := t.GlamourColor(theme.AtomPrimary); c != nil {
		cfg.Heading.Color = c
	}

	// H1 background and link color: use the accent atom
	if c := t.GlamourColor(theme.AtomAccent); c != nil {
		cfg.Link.Color = c
		cfg.H1.BackgroundColor = c
	}
	// LinkText color: primary
	if c := t.GlamourColor(theme.AtomPrimary); c != nil {
		cfg.LinkText.Color = c
	}

	// Inline code fg/bg
	if c := t.GlamourColor(theme.AtomCodeFg); c != nil {
		cfg.Code.Color = c
	}
	if c := t.GlamourColor(theme.AtomCodeBg); c != nil {
		cfg.Code.BackgroundColor = c
	}

	return cfg
}
