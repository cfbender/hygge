package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// hex converts a hex string to a color.Color via lipgloss.
func hex(s string) color.Color { return lipgloss.Color(s) }

// DefaultTheme returns the default Hygge theme (Claret). User-supplied TOML
// themes inherit unspecified values from this default — see Load.
func DefaultTheme() *Styles {
	s := quickStyle(claretOpts())
	s.Name = "claret"
	return &s
}

// ThemeByName returns the Styles for the given theme name, loading from disk
// if the name is not the built-in default. Falls back to Claret on failure.
func ThemeByName(name string) *Styles {
	if s, err := Load(name, LoadOptions{}); err == nil {
		return s
	}
	return DefaultTheme()
}
