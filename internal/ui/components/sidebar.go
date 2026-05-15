package components

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// SidebarMCPStatus is the UI-side representation of one MCP server's runtime
// status.  Populated at the wiring layer (runTUI) from the CLI's
// MCPServerStatus type so the sidebar has no import dependency on cmd/.
type SidebarMCPStatus struct {
	Name      string
	Ready     bool
	Error     string
	ToolCount int
}

// Sidebar renders the fixed-width right-side panel containing session
// context, MCPs, modified files (stub), and footer identity.
//
// Layout (top to bottom):
//
//	Session title (1–2 lines, bold)
//
//	Context  (section header)
//	  {usedTok} tokens
//	  {pctUsed}% used
//	  ${costDollars}
//
//	MCPs  (section header)
//	  ● server-a · N tools
//	  ○ server-b
//
//	Modified Files  (section header)
//	  —  (stub; see TODO)
//
//	(flex space)
//
//	~/path…:branch
//	● Hygge v0.1.0-dev
//
// The left edge carries a single │ border in the sidebar border color.
// Inner padding: 1 cell on each side so content doesn't touch the divider.
type Sidebar struct {
	// Width is the total column width including the left border.
	Width int
	// Height is the total height of the sidebar column.
	Height int

	// SessionTitle is the display title for the current session.
	// Empty means no active session — the section is omitted entirely.
	SessionTitle string

	// Context data.
	UsedTokens int64
	MaxTokens  int64
	PctUsed    float64
	CostUSD    float64

	// MCPs is the list of configured MCP server statuses.
	MCPs []SidebarMCPStatus

	// ProjectPath is the tilde-collapsed project directory path.
	ProjectPath string
	// GitBranch is the current git branch (may be empty).
	GitBranch string
	// AppName is the application name, e.g. "Hygge".
	AppName string
	// Version is the application version string, e.g. "v0.1.0-dev".
	Version string

	// Theme is the active theme.  Nil is accepted (plain styles used).
	Theme *theme.Theme
	// NerdFonts controls whether to use the nerd-font git-branch glyph.
	NerdFonts bool
}

// View renders the sidebar column.
func (s Sidebar) View() string {
	if s.Width <= 0 {
		return ""
	}
	height := s.Height
	if height <= 0 {
		height = 24
	}

	// The left border takes 1 cell; inner content area:
	//   innerW = Width - 1 (border) - 1 (left pad) - 1 (right pad)
	innerW := s.Width - 3
	if innerW < 1 {
		innerW = 1
	}

	sectionStyle := s.atomStyle(theme.AtomSidebarSection).Bold(true)
	mutedStyle := s.atomStyle(theme.AtomSidebarMuted)
	accentStyle := s.atomStyle(theme.AtomSidebarAccent)

	var lines []string

	// ── Session title ─────────────────────────────────────────────────────
	if s.SessionTitle != "" {
		lines = append(lines, "")
		lines = append(lines, s.wrapTitle(s.SessionTitle, innerW)...)
		lines = append(lines, "")
	}

	// ── Context section ───────────────────────────────────────────────────
	if s.UsedTokens > 0 || s.CostUSD > 0 {
		lines = append(lines, sectionStyle.Render(sidebarTruncate("Context", innerW)))
		lines = append(lines, s.atomStyle(theme.AtomSidebarValue).Render(
			sidebarTruncate(formatTokens(s.UsedTokens)+" tokens", innerW)))
		lines = append(lines, s.atomStyle(theme.AtomSidebarValue).Render(
			sidebarTruncate(fmt.Sprintf("%d%% used", int(s.PctUsed*100)), innerW)))
		lines = append(lines, s.atomStyle(theme.AtomSidebarValue).Render(
			sidebarTruncate(fmt.Sprintf("$%.4f", s.CostUSD), innerW)))
		lines = append(lines, "")
	}

	// ── MCPs section ──────────────────────────────────────────────────────
	lines = append(lines, sectionStyle.Render(sidebarTruncate("MCPs", innerW)))
	if len(s.MCPs) == 0 {
		lines = append(lines, mutedStyle.Render(sidebarTruncate("None", innerW)))
	} else {
		for _, m := range s.MCPs {
			lines = append(lines, s.renderMCP(m, innerW, mutedStyle))
		}
	}
	lines = append(lines, "")

	// ── Modified Files section ─────────────────────────────────────────────
	lines = append(lines, sectionStyle.Render(sidebarTruncate("Modified Files", innerW)))
	// TODO(post-sidebar): wire real file-modification tracking; see TODOS.md
	// under "Sidebar follow-ups".
	lines = append(lines, mutedStyle.Render(sidebarTruncate("—", innerW)))
	lines = append(lines, "")

	// ── Bottom block ──────────────────────────────────────────────────────
	bottomLines := s.renderBottom(innerW, mutedStyle, accentStyle)

	// ── Flex space ────────────────────────────────────────────────────────
	// Fill with empty lines so the bottom block is flush to the bottom.
	topCount := len(lines)
	bottomCount := len(bottomLines)
	flexRows := height - topCount - bottomCount
	if flexRows < 0 {
		flexRows = 0
	}
	for i := 0; i < flexRows; i++ {
		lines = append(lines, "")
	}
	lines = append(lines, bottomLines...)

	// ── Assemble with left border ──────────────────────────────────────────
	// Determine the border foreground color via the theme's Style method.
	borderFg := s.sidebarBorderFg()
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.Border{Left: "│"}, false, false, false, true).
		BorderForeground(borderFg).
		PaddingLeft(1).
		PaddingRight(1).
		Width(innerW)
	var sb strings.Builder
	for i, l := range lines {
		rendered := borderStyle.Render(l)
		sb.WriteString(rendered)
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// renderMCP renders a single MCP server status line.
func (s Sidebar) renderMCP(m SidebarMCPStatus, _ int, mutedStyle lipgloss.Style) string {
	var dotRendered string
	if m.Error != "" {
		dotRendered = s.atomStyle(theme.AtomError).Render("✕")
	} else if m.Ready {
		dotRendered = s.atomStyle(theme.AtomSidebarAccent).Render("●")
	} else {
		dotRendered = mutedStyle.Render("○")
	}

	label := dotRendered + " " + m.Name
	if m.ToolCount > 0 {
		label += mutedStyle.Render(fmt.Sprintf(" · %d tools", m.ToolCount))
	}
	return label
}

// renderBottom builds the bottom identity block lines.
func (s Sidebar) renderBottom(innerW int, mutedStyle, accentStyle lipgloss.Style) []string {
	var lines []string

	// Path + branch line.
	if pb := s.pathBranchLine(innerW); pb != "" {
		lines = append(lines, mutedStyle.Render(pb))
	}

	// "● AppName Version" line.
	lines = append(lines, s.appVersionLine(accentStyle, mutedStyle))

	// Trailing blank row.
	lines = append(lines, "")

	return lines
}

// pathBranchLine returns the project path + branch string truncated to budget.
func (s Sidebar) pathBranchLine(budget int) string {
	if s.ProjectPath == "" && s.GitBranch == "" {
		return ""
	}
	var branch string
	if s.GitBranch != "" {
		if s.NerdFonts {
			branch = "  \ueafc " + s.GitBranch
		} else {
			branch = ":" + s.GitBranch
		}
	}
	full := s.ProjectPath + branch
	if lipgloss.Width(full) <= budget {
		return full
	}
	// Trim from the left.
	runes := []rune(full)
	for len(runes) > 0 {
		candidate := "…" + string(runes)
		if lipgloss.Width(candidate) <= budget {
			return candidate
		}
		runes = runes[1:]
	}
	return "…"
}

// appVersionLine builds "● AppName Version" with accent dot.
func (s Sidebar) appVersionLine(accentStyle, mutedStyle lipgloss.Style) string {
	dot := accentStyle.Render("●")
	name := s.AppName
	if name == "" {
		name = "Hygge"
	}
	ver := ""
	if s.Version != "" {
		ver = " " + s.Version
	}
	return dot + " " + mutedStyle.Render(name+ver)
}

// wrapTitle wraps the session title to at most 2 lines of width w.
// If the text exceeds 2 lines after wrapping, the second line is
// truncated with "…".
func (s Sidebar) wrapTitle(title string, w int) []string {
	if w <= 0 {
		return nil
	}
	boldStyle := lipgloss.NewStyle().Bold(true)

	runes := []rune(title)
	if len(runes) <= w {
		return []string{boldStyle.Render(title)}
	}

	first := string(runes[:w])
	rest := runes[w:]

	if len(rest) <= w {
		return []string{boldStyle.Render(first), boldStyle.Render(string(rest))}
	}
	second := string(rest[:w-1]) + "…"
	return []string{boldStyle.Render(first), boldStyle.Render(second)}
}

// sidebarBorderFg extracts the foreground color for the sidebar border atom.
// Falls back to ANSI 8 (grey) when the atom is unset or default.
func (s Sidebar) sidebarBorderFg() color.Color {
	if s.Theme == nil {
		return lipgloss.Color("8")
	}
	style := s.Theme.Style(theme.AtomSidebarBorder)
	fg := style.GetForeground()
	if fg == nil {
		return lipgloss.Color("8")
	}
	if _, isNo := fg.(lipgloss.NoColor); isNo {
		return lipgloss.Color("8")
	}
	return fg
}

// atomStyle returns the lipgloss.Style for the given atom, or a blank style
// when no theme is configured.
func (s Sidebar) atomStyle(a theme.Atom) lipgloss.Style {
	if s.Theme == nil {
		return lipgloss.NewStyle()
	}
	return s.Theme.Style(a)
}

// sidebarTruncate truncates a plain string to at most w visual columns.
// Named with a package prefix to avoid collision with other truncate funcs.
func sidebarTruncate(str string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(str) <= w {
		return str
	}
	runes := []rune(str)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if lipgloss.Width(string(runes)+"…") <= w {
			return string(runes) + "…"
		}
	}
	return "…"
}

// formatTokens formats a token count with thousands separators, e.g. 97229 → "97,229".
func formatTokens(n int64) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	result := make([]byte, 0, len(s)+4)
	for i := 0; i < len(s); i++ {
		pos := len(s) - i
		if i > 0 && pos%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, s[i])
	}
	return string(result)
}
