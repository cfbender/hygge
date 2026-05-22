package styles

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Style returns a lipgloss.Style for the given atom. Atoms ending in ".bg" are
// applied as Background colours; everything else as Foreground. If the atom is
// not present (which shouldn't happen for a properly-built Styles) the
// returned style is empty.
func (s *Styles) Style(a Atom) lipgloss.Style {
	if s == nil || s.Colors == nil {
		return lipgloss.NewStyle()
	}
	c, ok := s.Colors[a]
	if !ok || c == nil {
		return lipgloss.NewStyle()
	}
	if isBackgroundAtom(a) {
		return lipgloss.NewStyle().Background(c)
	}
	return lipgloss.NewStyle().Foreground(c)
}

// BlockStyle returns a lipgloss.Style with both Foreground (fg atom) and
// Background (bg atom) set. Missing atoms are silently skipped.
func (s *Styles) BlockStyle(fg, bg Atom) lipgloss.Style {
	style := lipgloss.NewStyle()
	if s == nil || s.Colors == nil {
		return style
	}
	if c, ok := s.Colors[fg]; ok && c != nil {
		style = style.Foreground(c)
	}
	if c, ok := s.Colors[bg]; ok && c != nil {
		style = style.Background(c)
	}
	return style
}

// GlamourColor returns the raw color string for the given atom in a form
// accepted by glamour's StylePrimitive.Color / BackgroundColor fields
// (typically a hex string like "#aabbcc"). Returns nil when the atom is
// missing or has no concrete color.
func (s *Styles) GlamourColor(a Atom) *string {
	if s == nil || s.Colors == nil {
		return nil
	}
	c, ok := s.Colors[a]
	if !ok || c == nil {
		return nil
	}
	hex := colorToHex(c)
	if hex == "" {
		return nil
	}
	return &hex
}

// FormatTheme renders a debug summary of every atom and its resolved hex
// color. Used by `hygge theme show`.
func (s *Styles) FormatTheme() string {
	var sb strings.Builder
	name := "(unnamed)"
	if s != nil && s.Name != "" {
		name = s.Name
	}
	fmt.Fprintf(&sb, "theme: %s\n", name)
	for _, a := range AllAtoms() {
		c := lipgloss.Color("")
		if s != nil && s.Colors != nil {
			if v, ok := s.Colors[a]; ok && v != nil {
				if rgba, ok2 := v.(color.RGBA); ok2 {
					fmt.Fprintf(&sb, "  %-22s = #%02x%02x%02x\n", string(a), rgba.R, rgba.G, rgba.B)
					continue
				}
				fmt.Fprintf(&sb, "  %-22s = %v\n", string(a), v)
				continue
			}
		}
		fmt.Fprintf(&sb, "  %-22s = (default)\n", string(a))
		_ = c
	}
	return sb.String()
}

func isBackgroundAtom(a Atom) bool {
	return strings.HasSuffix(string(a), ".bg")
}

// colorToHex converts a color.Color to a 6-digit hex string. Returns "" if the
// color cannot be reduced to RGB (e.g. nil or a sentinel "no color").
func colorToHex(c color.Color) string {
	if c == nil {
		return ""
	}
	r32, g32, b32, a32 := c.RGBA()
	if a32 == 0 {
		return ""
	}
	r := uint8((r32 >> 8) & 0xff)
	g := uint8((g32 >> 8) & 0xff)
	b := uint8((b32 >> 8) & 0xff)
	return fmt.Sprintf("#%02x%02x%02x", r, g, b)
}

// paletteAtoms builds the full Atom → color map from a quickStyleOpts palette.
// This is the single source of truth for the semantic atom mapping; tweaking
// an atom's meaning happens here.
func paletteAtoms(o quickStyleOpts) map[Atom]color.Color {
	m := map[Atom]color.Color{
		// Brand + roles
		AtomPrimary: o.primary,
		AtomAccent:  o.primary, // theme atom "accent" == brand primary in styles parlance
		AtomMuted:   o.fgMoreSubtle,
		AtomSuccess: o.success,
		AtomWarn:    o.warning,
		AtomError:   o.error,

		// Code surfaces
		AtomCodeFg: o.fgBase,
		AtomCodeBg: o.bgLeastVisible,

		// Diff fills — left unset by default so the diff view uses semantic
		// foreground tones only. User themes can paint a coloured fill via
		// the [atoms] TOML override.
		AtomDiffAddBg: nil,
		AtomDiffDelBg: nil,

		// Status bar
		AtomStatusBarFg: o.fgBase,
		AtomStatusBarBg: o.bgLessVisible,

		// Modal
		AtomModalBg:     o.bgBase,
		AtomModalBorder: o.primary,

		// Bubble surfaces
		AtomBubbleBorder:         o.primary,
		AtomBubbleBorderDistinct: o.fgMoreSubtle,
		AtomBubbleHeader:         o.primary,
		AtomBubbleHeaderMuted:    o.fgMostSubtle,
		AtomBubbleBodyMuted:      o.fgMostSubtle,
		AtomBubbleBg:             o.bgLeastVisible,
		AtomBubbleUserBorder:     o.accent,
		AtomBubbleAgentBorder:    o.primary,

		// Sidebar
		AtomSidebarBorder:  o.bgMostVisible,
		AtomSidebarSection: o.fgSubtle,
		AtomSidebarValue:   o.fgBase,
		AtomSidebarAccent:  o.primary,
		AtomSidebarMuted:   o.fgMostSubtle,
		AtomSidebarBg:      o.bgLeastVisible,
	}
	return m
}
