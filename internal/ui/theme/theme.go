// Package theme provides the theme registry and style-atom system for Hygge's
// terminal UI.
//
// # Shell-default philosophy
//
// The built-in "shell" theme assigns each style atom to a numbered slot in the
// terminal's ANSI 16/256-color palette.  It never hard-codes RGB values; the
// actual hues are whatever the user has configured in their terminal emulator.
// This means Hygge "inherits" the user's chosen terminal color scheme for free,
// without requiring a separate theme file.
//
// # Atom contract
//
// A style atom is a named, logical surface (e.g. "primary", "code.bg"). The
// full set of v0.1 atoms is locked: adding or removing atoms requires its own
// task.  Every theme — builtin or user-supplied — must declare every atom.
// There is intentionally no fallback inheritance from the shell theme; if a
// user writes a custom theme they must be explicit about every surface.
//
// Background vs. foreground semantics are encoded in the atom name:
//   - Atoms ending in ".bg" are applied as Background colours.
//   - All other atoms (bare names or ".fg" suffix) are applied as Foreground colours.
//
// # Loading
//
// Call Load to resolve a theme by name. Builtin themes are returned without
// disk I/O. User themes live at ~/.config/hygge/themes/<name>.toml.
package theme

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Atom is the canonical identifier for a styleable surface.
type Atom string

// The following constants are the locked v0.1 style atom identifiers.
// Callers should use these constants rather than bare string literals.
const (
	AtomPrimary     Atom = "primary"
	AtomAccent      Atom = "accent"
	AtomMuted       Atom = "muted"
	AtomSuccess     Atom = "success"
	AtomWarn        Atom = "warn"
	AtomError       Atom = "error"
	AtomCodeBg      Atom = "code.bg"
	AtomCodeFg      Atom = "code.fg"
	AtomDiffAddBg   Atom = "diff.add.bg"
	AtomDiffDelBg   Atom = "diff.del.bg"
	AtomStatusBarBg Atom = "statusbar.bg"
	AtomStatusBarFg Atom = "statusbar.fg"
	AtomModalBg     Atom = "modal.bg"
	AtomModalBorder Atom = "modal.border"
)

// allAtoms is the stable, ordered list of v0.1 style atoms.
var allAtoms = []Atom{
	AtomPrimary,
	AtomAccent,
	AtomMuted,
	AtomSuccess,
	AtomWarn,
	AtomError,
	AtomCodeBg,
	AtomCodeFg,
	AtomDiffAddBg,
	AtomDiffDelBg,
	AtomStatusBarBg,
	AtomStatusBarFg,
	AtomModalBg,
	AtomModalBorder,
}

// AllAtoms returns the locked list of v0.1 style atoms in stable order.
// The slice is a copy; callers may not modify it.
func AllAtoms() []Atom {
	out := make([]Atom, len(allAtoms))
	copy(out, allAtoms)
	return out
}

// Theme is a fully-resolved theme: every atom maps to a Color.
type Theme struct {
	Name   string
	Colors map[Atom]Color
}

// isBackground reports whether atom a should be applied as a background color.
func isBackground(a Atom) bool {
	return strings.HasSuffix(string(a), ".bg")
}

// Style returns a lipgloss.Style for the given atom.
//
// Background atoms (.bg suffix) apply Background(); all others apply
// Foreground().  If the atom's color is the empty/default kind, the returned
// style has neither attribute set (the terminal default carries through).
//
// Inherit chains are resolved on every call.  If resolution fails (cycle or
// missing atom) a blank style is returned and the error is silently dropped —
// the caller receives a workable (blank) style rather than a panic.
func (t *Theme) Style(a Atom) lipgloss.Style {
	c, ok := t.Colors[a]
	if !ok {
		return lipgloss.NewStyle()
	}

	resolved, err := resolveColor(c, t.Colors)
	if err != nil {
		// Cycle or bad chain: return a blank style rather than crashing.
		return lipgloss.NewStyle()
	}

	if resolved.IsDefault() {
		return lipgloss.NewStyle()
	}

	lg := resolved.lipglossColor()
	if isBackground(a) {
		return lipgloss.NewStyle().Background(lg)
	}
	return lipgloss.NewStyle().Foreground(lg)
}

// BlockStyle returns a lipgloss.Style that has both Foreground and Background
// set, combining a foreground atom and a background atom.  This is a
// convenience for callers that need both in one style (e.g. "code").
//
// If either atom's color is default or resolution fails, that attribute is
// left unset.
func (t *Theme) BlockStyle(fg, bg Atom) lipgloss.Style {
	style := lipgloss.NewStyle()

	if fgColor, ok := t.Colors[fg]; ok {
		if resolved, err := resolveColor(fgColor, t.Colors); err == nil && !resolved.IsDefault() {
			style = style.Foreground(resolved.lipglossColor())
		}
	}

	if bgColor, ok := t.Colors[bg]; ok {
		if resolved, err := resolveColor(bgColor, t.Colors); err == nil && !resolved.IsDefault() {
			style = style.Background(resolved.lipglossColor())
		}
	}

	return style
}

// LoadOptions controls theme loading.
type LoadOptions struct {
	// ConfigHome overrides $XDG_CONFIG_HOME; "" uses the real environment.
	ConfigHome string

	// HomeDir overrides $HOME; "" uses the real home directory.
	HomeDir string
}

// Load returns the theme named by themeName, looking up:
//  1. Builtin themes (currently: "shell")
//  2. ~/.config/hygge/themes/<name>.toml
//
// If themeName is "" or "shell", returns the shell theme without disk I/O.
// Returns ErrThemeNotFound if the name is not builtin and not on disk.
func Load(themeName string, opts LoadOptions) (*Theme, error) {
	if themeName == "" || themeName == "shell" {
		return ShellTheme(), nil
	}

	path, err := resolveThemePath(themeName, opts)
	if err != nil {
		return nil, err
	}

	t, err := loadTOMLTheme(path)
	if err != nil {
		return nil, fmt.Errorf("theme: load %q from %s: %w", themeName, path, err)
	}
	return t, nil
}

// FormatTheme returns a multi-line string showing every atom and its resolved
// color form.  Used by `hygge theme show` (Task 13).
//
// Example output:
//
//	theme: shell
//	  primary        = ansi:4
//	  code.bg        = (default)
func FormatTheme(t *Theme) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "theme: %s\n", t.Name)

	for _, a := range AllAtoms() {
		c, ok := t.Colors[a]
		if !ok {
			fmt.Fprintf(&sb, "  %-16s = (missing)\n", string(a))
			continue
		}
		fmt.Fprintf(&sb, "  %-16s = %s\n", string(a), c.String())
	}

	return sb.String()
}
