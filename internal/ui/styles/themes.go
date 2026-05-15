package styles

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// hex converts a hex string to a color.Color via lipgloss.
func hex(s string) color.Color { return lipgloss.Color(s) }

// DefaultTheme returns the default Hygge theme (Claret).
func DefaultTheme() Styles {
	return quickStyle(quickStyleOpts{
		primary:   hex("#C75B7A"), // rose
		secondary: hex("#D4A76A"), // gold
		accent:    hex("#8FA86E"), // sage

		fgBase:       hex("#DDD3C7"), // text
		fgSubtle:     hex("#BDB3A7"), // text_2
		fgMoreSubtle: hex("#9E9288"), // text_3
		fgMostSubtle: hex("#71685E"), // text_4

		onPrimary: hex("#180810"), // bg

		bgBase:         hex("#180810"), // bg
		bgLeastVisible: hex("#211618"), // bg_soft
		bgLessVisible:  hex("#2B1F22"), // bg_mute
		bgMostVisible:  hex("#3A2E25"), // divider

		separator: hex("#3A2E25"), // divider

		destructive:       hex("#C44536"), // terra
		error:             hex("#C44536"), // terra
		warning:           hex("#D4A76A"), // gold
		warningSubtle:     hex("#C5975B"), // gold_2
		busy:              hex("#D4A76A"), // gold
		info:              hex("#8995A8"), // slate
		infoMoreSubtle:    hex("#6E7A90"), // slate_2
		infoMostSubtle:    hex("#6E7A90"), // slate_2
		success:           hex("#8FA86E"), // sage
		successMoreSubtle: hex("#7A9460"), // sage_2
		successMostSubtle: hex("#7A9460"), // sage_2
	})
}

// ThemeByName returns a Styles for the given theme name.
// Falls back to the default theme for unknown names.
func ThemeByName(_ string) Styles {
	// The default theme is built-in. Additional themes can be added as
	// TOML config files — see examples/themes/ for a Catppuccin Mocha example.
	return DefaultTheme()
}
