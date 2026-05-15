package theme

import (
	"image/color"

	colorful "github.com/lucasb-eyer/go-colorful"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// SaturationBoost returns a more vivid version of c for use as a border color
// on a tinted background.
//
//   - ansi.BasicColor 0-7  → bright equivalent 8-15  (e.g. 4 → 12, 5 → 13)
//   - ansi.BasicColor 8-15 → unchanged  (already bright)
//   - ansi.IndexedColor (256-color 16-255) → unchanged  (no safe boost defined)
//   - color.RGBA (from hex) → parse via go-colorful, increase HSL saturation
//     by 0.2 clamped to 1.0, re-emit as hex.
//
// If the input cannot be interpreted (nil, no-color, unknown type), it is
// returned as-is.
func SaturationBoost(c color.Color) color.Color {
	if c == nil {
		return c
	}

	if _, isNoColor := c.(lipgloss.NoColor); isNoColor {
		return c
	}

	// ANSI base 0-7: map to bright 8-15.
	if bc, ok := c.(ansi.BasicColor); ok {
		n := int(bc)
		if n >= 0 && n <= 7 {
			return ansi.BasicColor(n + 8) //nolint:gosec
		}
		// 8-15: already bright, return as-is.
		return c
	}

	// ANSI 256-color (16-255): return as-is.
	if _, ok := c.(ansi.IndexedColor); ok {
		return c
	}

	// Hex / RGB (color.RGBA from lipgloss.Color("#...")): boost saturation.
	if rgba, ok := c.(color.RGBA); ok {
		cf, ok := colorful.MakeColor(rgba)
		if !ok {
			return c
		}
		h, s, l := cf.Hsl()
		s += 0.2
		if s > 1.0 {
			s = 1.0
		}
		boosted := colorful.Hsl(h, s, l)
		return lipgloss.Color(boosted.Hex())
	}

	return c
}
