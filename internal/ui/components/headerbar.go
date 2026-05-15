package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// HeaderBar renders the top-of-screen one-line header.
//
// Layout:
//
//	{AppName} {Version}          profile: {Profile} · {DisplayPath}{BranchPart} · {ctx} · {cost}
//
// The left side shows the application name (in accent color) and version.
// The right side shows session identity: profile, project path, git branch,
// context usage percentage, and cost-to-date.
//
// No border is rendered; the header is followed by a blank line in the
// layout to provide visual separation from the message list.
// Rationale: a border would add a row of chrome for minimal visual gain; a
// blank line in the layout (height counted separately by the App) is cleaner
// and gives the header breathing room without cluttering the border budget.
//
// Branch rendering:
//   - NerdFonts == true  → "  \ueafc {branch}" (nerd-font git glyph U+EAFC)
//   - NerdFonts == false → ":{branch}"
//   - Empty branch       → no suffix
//
// Glyph choice: U+EAFC is the Codicon "git-branch" glyph present in all major
// nerd-font distributions (Nerd Fonts ≥ 2.1.0, e.g. FiraCode Nerd Font,
// JetBrainsMono Nerd Font, Hack Nerd Font).  If the user's font does not
// include this glyph, set ui.nerd_fonts = false in config.
type HeaderBar struct {
	// Width is the total column width of the header line.
	Width int

	// AppName is the application name, e.g. "Hygge".
	AppName string

	// Version is the application version string, e.g. "v0.4".
	Version string

	// Profile is the active config profile name, e.g. "default".
	Profile string

	// ProjectPath is the absolute path of the working directory.
	// It is displayed tilde-collapsed (e.g. "/Users/cfb/code" → "~/code").
	ProjectPath string

	// GitBranch is the current branch name (or "@short-sha" for detached HEAD).
	// Empty when not in a git repository.
	GitBranch string

	// CtxPercent is the fraction of the context window in use (0.0–1.0).
	// Hidden when zero.
	CtxPercent float64

	// CostUSD is the accumulated cost in US dollars.
	// Hidden when zero.
	CostUSD float64

	// Theme is the active theme.  Nil is accepted (plain styles used).
	Theme *theme.Theme

	// NerdFonts controls whether to use the nerd-font git-branch glyph.
	// When true, renders "  \ueafc branch".
	// When false, renders ":branch".
	NerdFonts bool

	// HomeDir is used for tilde-collapsing the project path.  Defaults to
	// "" (no collapse) when not set; inject a real home dir in production.
	HomeDir string
}

// nerdFontBranch is the Codicon git-branch glyph (U+EAFC).
const nerdFontBranch = "\ueafc"

// View renders the header bar as a single line padded to Width.
func (h HeaderBar) View() string {
	width := h.Width
	if width <= 0 {
		width = 80
	}

	// Left: app name (accented) + version (muted).
	left := h.renderLeft()

	// Right: identity tokens joined by " · ".
	right := h.renderRight(width, left)

	leftW := lipgloss.Width(left)
	rightW := lipgloss.Width(right)

	gap := width - leftW - rightW
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + right
}

// renderLeft returns the styled left segment "{AppName} {Version}".
func (h HeaderBar) renderLeft() string {
	nameStyle := lipgloss.NewStyle()
	verStyle := lipgloss.NewStyle()
	if h.Theme != nil {
		nameStyle = h.Theme.Style(theme.AtomAccent).Bold(true)
		verStyle = h.Theme.Style(theme.AtomMuted)
	}
	return nameStyle.Render(h.AppName) + " " + verStyle.Render(h.Version)
}

// renderRight builds and returns the right-side segment, optionally
// truncating the project path to keep branch/ctx/cost visible.
func (h HeaderBar) renderRight(totalWidth int, left string) string {
	muted := lipgloss.NewStyle()
	if h.Theme != nil {
		muted = h.Theme.Style(theme.AtomMuted)
	}

	sep := muted.Render(" · ")

	// Build fixed tokens (always shown when non-empty).
	profileToken := muted.Render("profile: " + h.Profile)

	branchPart := h.branchSuffix()
	pathToken := h.collapsedPath() + branchPart

	var extraTokens []string
	if h.CtxPercent > 0 {
		extraTokens = append(extraTokens, muted.Render(fmt.Sprintf("%.0f%% ctx", h.CtxPercent*100)))
	}
	if h.CostUSD > 0 {
		extraTokens = append(extraTokens, muted.Render(fmt.Sprintf("$%.4f", h.CostUSD)))
	}

	// Reserve space for left + gap(1) + profile + sep + branchPart + extras + seps.
	leftW := lipgloss.Width(left)
	sepW := lipgloss.Width(sep)

	// Compute the width taken by extras (tokens after path).
	extrasStr := ""
	for _, tok := range extraTokens {
		extrasStr += sep + tok
	}

	// Available width for path token:
	// totalWidth - leftW - 1(gap) - sep - profileW - sep - extrasW
	profileW := lipgloss.Width(profileToken)
	extrasW := lipgloss.Width(extrasStr)
	pathBudget := totalWidth - leftW - 1 - sepW - profileW - sepW - extrasW
	if len(extraTokens) == 0 {
		pathBudget = totalWidth - leftW - 1 - sepW - profileW
	}

	// Render path, optionally truncated.
	renderedPath := muted.Render(h.truncatePath(pathToken, pathBudget))

	// Assemble right side.
	parts := []string{profileToken, renderedPath}
	parts = append(parts, extraTokens...)
	return strings.Join(parts, sep)
}

// collapsedPath returns ProjectPath with the home directory prefix replaced by ~.
func (h HeaderBar) collapsedPath() string {
	p := h.ProjectPath
	if h.HomeDir != "" && strings.HasPrefix(p, h.HomeDir) {
		rest := strings.TrimPrefix(p, h.HomeDir)
		if rest == "" {
			return "~"
		}
		return "~" + rest
	}
	return p
}

// branchSuffix returns the branch display string for the right side of the path.
func (h HeaderBar) branchSuffix() string {
	if h.GitBranch == "" {
		return ""
	}
	if h.NerdFonts {
		return "  " + nerdFontBranch + " " + h.GitBranch
	}
	return ":" + h.GitBranch
}

// truncatePath truncates a display path to at most budget visual columns,
// replacing a leading prefix with "…" so the branch/end stays visible.
func (h HeaderBar) truncatePath(path string, budget int) string {
	if budget <= 0 {
		return "…"
	}
	if lipgloss.Width(path) <= budget {
		return path
	}
	// Trim from the left until it fits, prepend "…".
	runes := []rune(path)
	for len(runes) > 0 {
		candidate := "…" + string(runes)
		if lipgloss.Width(candidate) <= budget {
			return candidate
		}
		runes = runes[1:]
	}
	return "…"
}
