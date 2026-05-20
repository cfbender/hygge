package theme

// ShellTheme returns the builtin shell-palette theme.
//
// Every atom maps to a numbered slot in the terminal's ANSI palette.  No RGB
// values are hard-coded; the actual hues depend on the user's terminal emulator
// color scheme.  An empty raw string means "no override" — the terminal's
// default background or foreground carries through.
func ShellTheme() *Theme {
	return &Theme{
		Name: "shell",
		Colors: map[Atom]Color{
			// Semantic foreground tones — ANSI 16-color slots.
			AtomPrimary: {kind: colorKindANSI, raw: "4"}, // blue
			AtomAccent:  {kind: colorKindANSI, raw: "5"}, // magenta
			AtomMuted:   {kind: colorKindANSI, raw: "8"}, // bright black / grey
			AtomSuccess: {kind: colorKindANSI, raw: "2"}, // green
			AtomWarn:    {kind: colorKindANSI, raw: "3"}, // yellow
			AtomError:   {kind: colorKindANSI, raw: "1"}, // red

			// Code block surfaces.
			AtomCodeFg: {kind: colorKindANSI, raw: "7"}, // white / terminal foreground
			AtomCodeBg: {kind: colorKindDefault},        // no override — terminal background

			// Diff hunk backgrounds.
			// The shell theme leaves these unset so diffs use semantic ANSI
			// foregrounds without painting large red/green blocks. Custom themes may
			// still opt into background fills via diff.add.bg / diff.del.bg.
			AtomDiffAddBg: {kind: colorKindDefault},
			AtomDiffDelBg: {kind: colorKindDefault},

			// Status bar.
			AtomStatusBarFg: {kind: colorKindANSI, raw: "15"}, // bright white
			AtomStatusBarBg: {kind: colorKindANSI, raw: "8"},  // bright black / dark grey

			// Modal.
			AtomModalBg:     {kind: colorKindDefault},        // no override
			AtomModalBorder: {kind: colorKindANSI, raw: "8"}, // grey

			// Bubble borders and header text (Phase 1 chat-bubble redesign).
			// Border uses the accent slot (magenta); distinct/subdued uses the
			// muted slot (bright-black/grey).  Header accents mirror accent/muted.
			// Phase 5 distinct borders: user=blue (4), agent default=red (1).
			// Each agent profile will configure its own color; red is the fallback.
			AtomBubbleBorder:         {kind: colorKindANSI, raw: "5"},   // magenta — agent accent
			AtomBubbleBorderDistinct: {kind: colorKindANSI, raw: "8"},   // grey — subdued
			AtomBubbleHeader:         {kind: colorKindANSI, raw: "5"},   // magenta
			AtomBubbleHeaderMuted:    {kind: colorKindANSI, raw: "7"},   // foreground on highlight fill
			AtomBubbleBodyMuted:      {kind: colorKindANSI, raw: "7"},   // foreground on highlight fill
			AtomBubbleBg:             {kind: colorKindANSI, raw: "235"}, // dim terminal-background-adjacent fallback

			// Phase 5: distinct user vs agent bubble border colors.
			AtomBubbleUserBorder:  {kind: colorKindANSI, raw: "4"}, // blue  — user bubble
			AtomBubbleAgentBorder: {kind: colorKindANSI, raw: "5"}, // magenta — agent bubble default

			// Sidebar atoms.
			AtomSidebarBorder:  {kind: colorKindANSI, raw: "8"},   // grey divider
			AtomSidebarSection: {kind: colorKindANSI, raw: "7"},   // readable on highlight fill
			AtomSidebarValue:   {kind: colorKindDefault},          // terminal default fg
			AtomSidebarAccent:  {kind: colorKindANSI, raw: "5"},   // magenta accent dot
			AtomSidebarMuted:   {kind: colorKindANSI, raw: "7"},   // readable on highlight fill
			AtomSidebarBg:      {kind: colorKindANSI, raw: "235"}, // dim terminal-background-adjacent fallback
		},
	}
}
