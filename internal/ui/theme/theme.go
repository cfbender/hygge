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

	"charm.land/lipgloss/v2"
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

	// Bubble-specific atoms introduced in Phase 1 of the chat-bubble redesign.
	// The accent color for bubble borders defaults to a slightly muted version
	// of the terminal accent.  Future per-agent-mode theming will override
	// AccentColor at the call site; these atoms provide the session-level default.
	AtomBubbleBorder         Atom = "bubble.border"          // default bubble border (≈ accent, slightly muted)
	AtomBubbleBorderDistinct Atom = "bubble.border.distinct" // SubStyle=Distinct bubble border (≈ muted)
	AtomBubbleHeader         Atom = "bubble.header"          // bubble header accent (matches accent)
	AtomBubbleHeaderMuted    Atom = "bubble.header.muted"    // bubble right-side header muted text
	AtomBubbleBodyMuted      Atom = "bubble.body.muted"      // muted body text (thinking in Phase 2)

	// Phase 5: distinct border colors for user vs agent bubbles.
	// AtomBubbleUserBorder is the border/accent for user (right-aligned) bubbles.
	// AtomBubbleAgentBorder is the border/accent for assistant (left-aligned) bubbles.
	// Both are seams for per-agent-mode customisation; AtomBubbleAgentBorder
	// will be overridden per agent session once per-agent theming lands.
	AtomBubbleUserBorder  Atom = "bubble.user.border"  // user bubble border (default: blue / ANSI 4)
	AtomBubbleAgentBorder Atom = "bubble.agent.border" // agent bubble border (default: magenta / ANSI 5)

	// Sidebar atoms (right-side panel added in the sidebar phase).
	// AtomSidebarBorder   — left-side divider color.
	// AtomSidebarSection  — section header label (muted, bold).
	// AtomSidebarValue    — value text (default: terminal fg).
	// AtomSidebarAccent   — accent dot and version glyph.
	// AtomSidebarMuted    — muted text such as "None" or "—".
	AtomSidebarBorder  Atom = "sidebar.border"  // left-border divider color (default: ANSI 8)
	AtomSidebarSection Atom = "sidebar.section" // section header label (default: ANSI 8)
	AtomSidebarValue   Atom = "sidebar.value"   // value text (default: terminal fg)
	AtomSidebarAccent  Atom = "sidebar.accent"  // accent dot/glyph (default: same as AtomAccent)
	AtomSidebarMuted   Atom = "sidebar.muted"   // muted/empty state text (default: ANSI 8)
)

// allAtoms is the stable, ordered list of style atoms.
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
	// Bubble atoms (Phase 1 chat-bubble redesign).
	AtomBubbleBorder,
	AtomBubbleBorderDistinct,
	AtomBubbleHeader,
	AtomBubbleHeaderMuted,
	AtomBubbleBodyMuted,
	// Phase 5: distinct user vs agent border colors.
	AtomBubbleUserBorder,
	AtomBubbleAgentBorder,
	// Sidebar atoms.
	AtomSidebarBorder,
	AtomSidebarSection,
	AtomSidebarValue,
	AtomSidebarAccent,
	AtomSidebarMuted,
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
