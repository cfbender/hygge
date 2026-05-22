package styles

// Atom is the canonical identifier for a styleable surface. Each atom maps to
// a concrete color in the active Styles palette.
type Atom string

// Locked v0.x style atom identifiers. Callers should use these constants
// rather than bare string literals.
//
// Background vs. foreground semantics are encoded in the atom name:
//   - Atoms ending in ".bg" are applied as Background colours.
//   - All other atoms are applied as Foreground colours.
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

	AtomBubbleBorder         Atom = "bubble.border"
	AtomBubbleBorderDistinct Atom = "bubble.border.distinct"
	AtomBubbleHeader         Atom = "bubble.header"
	AtomBubbleHeaderMuted    Atom = "bubble.header.muted"
	AtomBubbleBodyMuted      Atom = "bubble.body.muted"
	AtomBubbleBg             Atom = "bubble.bg"
	AtomBubbleUserBorder     Atom = "bubble.user.border"
	AtomBubbleAgentBorder    Atom = "bubble.agent.border"

	AtomSidebarBorder  Atom = "sidebar.border"
	AtomSidebarSection Atom = "sidebar.section"
	AtomSidebarValue   Atom = "sidebar.value"
	AtomSidebarAccent  Atom = "sidebar.accent"
	AtomSidebarMuted   Atom = "sidebar.muted"
	AtomSidebarBg      Atom = "sidebar.bg"
)

// allAtoms is the stable, ordered list of every defined atom.
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
	AtomBubbleBorder,
	AtomBubbleBorderDistinct,
	AtomBubbleHeader,
	AtomBubbleHeaderMuted,
	AtomBubbleBodyMuted,
	AtomBubbleBg,
	AtomBubbleUserBorder,
	AtomBubbleAgentBorder,
	AtomSidebarBorder,
	AtomSidebarSection,
	AtomSidebarValue,
	AtomSidebarAccent,
	AtomSidebarMuted,
	AtomSidebarBg,
}

// AllAtoms returns a copy of the locked atom list in stable order.
func AllAtoms() []Atom {
	out := make([]Atom, len(allAtoms))
	copy(out, allAtoms)
	return out
}
