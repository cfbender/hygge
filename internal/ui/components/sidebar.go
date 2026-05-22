package components

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
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

// SidebarTodoStatus is the rendering status for a single sidebar todo row.
// Mirrors session.TodoStatus values; defined here so the components package
// has no import dependency on internal/session.
type SidebarTodoStatus string

// Sidebar todo status constants.
const (
	// SidebarTodoPending is a queued, not-yet-started item.
	SidebarTodoPending SidebarTodoStatus = "pending"
	// SidebarTodoInProgress is the currently-active item.
	SidebarTodoInProgress SidebarTodoStatus = "in_progress"
	// SidebarTodoCompleted is a finished item.
	SidebarTodoCompleted SidebarTodoStatus = "completed"
	// SidebarTodoCancelled is an abandoned item.
	SidebarTodoCancelled SidebarTodoStatus = "cancelled"
)

// SidebarTodo is one row in the sidebar Todos section.
type SidebarTodo struct {
	// Title is the human-readable todo text.
	Title string
	// Status drives the leading glyph and color.
	Status SidebarTodoStatus
}

// Sidebar renders the fixed-width right-side panel containing session
// context, MCPs, todos, and footer identity.
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
//	Todos  (section header)
//	  ✓ completed item
//	  → in-progress item
//	  ○ pending item
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

	// Usage and context data.
	UsedTokens         int64
	MaxTokens          int64
	PctUsed            float64
	CostUSD            float64
	BilledInputTokens  int64 // cumulative input+cache tokens billed
	BilledOutputTokens int64 // cumulative output tokens billed

	// MCPs is the list of configured MCP server statuses.
	MCPs []SidebarMCPStatus

	// Todos is the agent's lightweight todo list for the current session.
	// Nil or empty renders "—" (the fallback stub).
	Todos []SidebarTodo

	// ProjectPath is the tilde-collapsed project directory path.
	ProjectPath string
	// GitBranch is the current git branch (may be empty).
	GitBranch string
	// AppName is the application name, e.g. "Hygge".
	AppName string
	// Version is the application version string, e.g. "v0.1.0-dev".
	Version string

	// Theme is the active theme. Nil is accepted (plain styles used).
	Theme  *styles.Styles
	Styles *styles.Styles
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
	innerW := max(s.Width-3, 1)

	sectionStyle := s.atomStyle(styles.AtomSidebarSection).Bold(true)
	mutedStyle := s.atomStyle(styles.AtomSidebarMuted)
	accentStyle := s.atomStyle(styles.AtomSidebarAccent)

	var lines []string

	// ── Session title ─────────────────────────────────────────────────────
	if s.SessionTitle != "" {
		lines = append(lines, "")
		lines = append(lines, s.wrapTitle(s.SessionTitle, innerW)...)
		lines = append(lines, "")
	}

	// ── Usage section ─────────────────────────────────────────────────────
	if s.BilledInputTokens > 0 || s.BilledOutputTokens > 0 || s.CostUSD > 0 {
		lines = append(lines, sectionStyle.Render(sidebarTruncate("Usage", innerW)))
		if s.BilledInputTokens > 0 || s.BilledOutputTokens > 0 {
			billedLine := compactTokens(s.BilledInputTokens) + " ↑ / " + compactTokens(s.BilledOutputTokens) + " ↓"
			lines = append(lines, s.atomStyle(styles.AtomSidebarValue).Render(
				sidebarTruncate(billedLine, innerW)))
		}
		if s.CostUSD > 0 {
			lines = append(lines, s.atomStyle(styles.AtomSidebarValue).Render(
				sidebarTruncate(fmt.Sprintf("$%.4f", s.CostUSD), innerW)))
		}
		lines = append(lines, "")
	}

	// ── Context section ───────────────────────────────────────────────────
	if s.UsedTokens > 0 {
		lines = append(lines, sectionStyle.Render(sidebarTruncate("Context", innerW)))
		lines = append(lines, s.atomStyle(styles.AtomSidebarValue).Render(
			sidebarTruncate(formatTokens(s.UsedTokens)+" tokens", innerW)))
		pct := "limit unknown"
		if s.MaxTokens > 0 {
			pct = fmt.Sprintf("%d%% used", int(s.PctUsed*100))
		}
		lines = append(lines, s.atomStyle(styles.AtomSidebarValue).Render(
			sidebarTruncate(pct, innerW)))
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

	// ── Todos section ─────────────────────────────────────────────────────
	lines = append(lines, sectionStyle.Render(sidebarTruncate("Todos", innerW)))
	lines = append(lines, s.renderTodos(innerW, mutedStyle)...)
	lines = append(lines, "")

	// ── Bottom block ──────────────────────────────────────────────────────
	bottomLines := s.renderBottom(innerW, mutedStyle, accentStyle)

	// ── Flex space ────────────────────────────────────────────────────────
	// Fill with empty lines so the bottom block is flush to the bottom.
	topCount := len(lines)
	bottomCount := len(bottomLines)
	flexRows := max(height-topCount-bottomCount, 0)
	for range flexRows {
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
		Width(s.Width)
	bgOpen := ""
	if bg := s.sidebarBackgroundColor(); bg != nil {
		borderStyle = borderStyle.Background(bg)
		bgOpen = sidebarBackgroundOpenSequence(bg)
	}
	var sb strings.Builder
	for i, l := range lines {
		if bgOpen != "" {
			l = sidebarReassertBackgroundAfterReset(l, bgOpen)
		}
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
		dotRendered = s.atomStyle(styles.AtomError).Render("✕")
	} else if m.Ready {
		dotRendered = s.atomStyle(styles.AtomSidebarAccent).Render("●")
	} else {
		dotRendered = mutedStyle.Render("○")
	}

	label := dotRendered + " " + m.Name
	if m.ToolCount > 0 {
		label += mutedStyle.Render(fmt.Sprintf(" · %d tools", m.ToolCount))
	}
	return label
}

// renderTodos renders the Todos section body: up to maxVisibleTodos rows
// with a status glyph + truncated title, then an "… +K more" row when there
// are more.  Returns "—" when the list is empty.
func (s Sidebar) renderTodos(innerW int, mutedStyle lipgloss.Style) []string {
	if len(s.Todos) == 0 {
		return []string{mutedStyle.Render(sidebarTruncate("—", innerW))}
	}

	const maxVisible = 6

	var lines []string
	visible := s.Todos
	extra := 0
	if len(visible) > maxVisible {
		extra = len(visible) - maxVisible
		visible = visible[:maxVisible]
	}

	for _, t := range visible {
		lines = append(lines, s.renderTodoRow(t, innerW, mutedStyle)...)
	}
	if extra > 0 {
		lines = append(lines, mutedStyle.Render(sidebarTruncate(fmt.Sprintf("… +%d more", extra), innerW)))
	}
	return lines
}

// renderTodoRow renders one todo as "<glyph> <title>", wrapping long titles
// onto continuation lines. The glyph and title styling depend on the todo status.
func (s Sidebar) renderTodoRow(t SidebarTodo, innerW int, mutedStyle lipgloss.Style) []string {
	var (
		glyph      string
		glyphStyle lipgloss.Style
		titleStyle lipgloss.Style
	)
	switch t.Status {
	case SidebarTodoCompleted:
		glyph = "✓"
		glyphStyle = s.atomStyle(styles.AtomSuccess)
		titleStyle = mutedStyle
	case SidebarTodoInProgress:
		glyph = "→"
		glyphStyle = s.atomStyle(styles.AtomSidebarAccent)
		titleStyle = s.atomStyle(styles.AtomSidebarValue).Bold(true)
	case SidebarTodoCancelled:
		glyph = "✕"
		glyphStyle = s.atomStyle(styles.AtomError)
		titleStyle = mutedStyle
	default: // pending or unknown
		glyph = "○"
		glyphStyle = mutedStyle
		titleStyle = s.atomStyle(styles.AtomSidebarValue)
	}

	// Normalize manual line breaks and repeated spaces before wrapping.
	title := strings.ReplaceAll(t.Title, "\n", " ")
	title = strings.Join(strings.Fields(title), " ")

	glyphW := lipgloss.Width(glyph)
	titleBudget := innerW - glyphW - 1
	if titleBudget < 1 {
		return []string{glyphStyle.Render(glyph)}
	}
	if title == "" {
		return []string{glyphStyle.Render(glyph)}
	}

	wrapped := titleStyle.Width(titleBudget).Render(title)
	titleLines := strings.Split(wrapped, "\n")
	lines := make([]string, 0, len(titleLines))
	prefix := glyphStyle.Render(glyph) + " "
	continuationPrefix := strings.Repeat(" ", glyphW+1)
	for i, line := range titleLines {
		if i == 0 {
			lines = append(lines, prefix+line)
			continue
		}
		lines = append(lines, continuationPrefix+line)
	}
	return lines
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
	boldStyle := s.atomStyle(styles.AtomSidebarValue).Bold(true)

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
	style := s.Theme.Style(styles.AtomSidebarBorder)
	fg := style.GetForeground()
	if fg == nil {
		return lipgloss.Color("8")
	}
	if _, isNo := fg.(lipgloss.NoColor); isNo {
		return lipgloss.Color("8")
	}
	return fg
}

func (s Sidebar) sidebarBackgroundColor() color.Color {
	if s.Styles != nil {
		return s.Styles.SidebarBg
	}
	if s.Theme == nil {
		return nil
	}
	bg := s.Theme.Style(styles.AtomSidebarBg).GetBackground()
	if _, isNo := bg.(lipgloss.NoColor); bg == nil || isNo {
		return nil
	}
	return bg
}

func sidebarBackgroundOpenSequence(bg color.Color) string {
	if bg == nil {
		return ""
	}
	rendered := lipgloss.NewStyle().Background(bg).Render("x")
	idx := strings.IndexRune(rendered, 'x')
	if idx <= 0 {
		return ""
	}
	return rendered[:idx]
}

func sidebarReassertBackgroundAfterReset(s, bgOpen string) string {
	if bgOpen == "" || !strings.Contains(s, "\x1b[") {
		return s
	}
	s = strings.ReplaceAll(s, "\x1b[m", "\x1b[m"+bgOpen)
	s = strings.ReplaceAll(s, "\x1b[0m", "\x1b[0m"+bgOpen)
	s = strings.ReplaceAll(s, "\x1b[49m", "\x1b[49m"+bgOpen)
	return s
}

// atomStyle returns the lipgloss.Style for the given atom, or a blank style
// when no theme is configured. Sidebar text also receives the sidebar
// background so styled fragments do not punch transparent holes in the fill.
func (s Sidebar) atomStyle(a styles.Atom) lipgloss.Style {
	if s.Theme == nil {
		return lipgloss.NewStyle()
	}
	style := s.Theme.Style(a)
	if bg := s.sidebarBackgroundColor(); bg != nil {
		style = style.Background(bg)
	}
	return style
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

// compactTokens formats a token count in a compact human-readable form.
// Numbers under 1,000 are shown as-is; thousands are shown as e.g. "12.7k";
// millions are shown as e.g. "1.37M". Fractional digits are truncated
// (not rounded) so the displayed value never overstates the token count.
//
// Examples:
//
//	0       → "0"
//	999     → "999"
//	1000    → "1.0k"
//	12788   → "12.7k"
//	1374982 → "1.37M"
func compactTokens(n int64) string {
	if n < 0 {
		n = 0
	}
	if n < 1_000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1_000_000 {
		// Truncate to one decimal place (floor, not round).
		tenths := n / 100 // e.g. 12788 / 100 = 127
		if tenths%10 == 0 {
			return fmt.Sprintf("%dk", tenths/10)
		}
		return fmt.Sprintf("%d.%dk", tenths/10, tenths%10)
	}
	// Truncate to two decimal places (floor, not round).
	hundredths := n / 10_000 // e.g. 1374982 / 10000 = 137
	return fmt.Sprintf("%d.%02dM", hundredths/100, hundredths%100)
}
