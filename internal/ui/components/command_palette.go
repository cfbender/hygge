package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// commandPaletteMaxRows is the cap on visible rows in the popover.
// More matches collapse into a "+N more" indicator at the bottom so
// the popover never grows taller than the terminal can comfortably
// host above the input.
const commandPaletteMaxRows = 8

// CommandPalette is the inline autocomplete popover shown above the
// input whenever the user's buffer starts with `/`.  It is a pure
// view-model: the App owns lookup state (matches, highlight index)
// and feeds it in; the palette has no event-handling logic of its
// own.
//
// Filtering is done by the App via command.Registry.LookupPrefix so
// this component is also reusable for non-slash menus in the future.
//
// Anchor: the App draws the palette as a floating layer above the editor. The
// palette draws its own rounded border so it visually floats above the input
// border.
type CommandPalette struct {
	// Width is the rendered width in cells (matches the input width).
	Width int

	// Theme is the active theme.  Nil falls back to default lipgloss
	// styles — the palette stays usable but loses muted/accent
	// distinction.
	Theme *theme.Theme

	// Matches is the prefix-filtered command list, sorted by Name.
	Matches []command.Command

	// Highlight is the index into Matches that is currently selected.
	// Clamped into [0, len(Matches)) at render time so the App does
	// not need to.  An out-of-range value (e.g. -1) renders as "no
	// row highlighted" — useful when the buffer has not yet picked a
	// row.
	Highlight int

	// QueryAfterSlash is the user's typed buffer minus the leading
	// slash.  Used only for the optional hint row at the bottom; the
	// actual filtering happens upstream.
	QueryAfterSlash string
}

// Empty reports whether the palette has no matches to render.
// The App uses this to skip the popover entirely (no border, no
// blank padding) when the user has typed `/` plus characters that
// don't match anything.
func (p CommandPalette) Empty() bool {
	return len(p.Matches) == 0
}

// View renders the palette.  Returns an empty string when there
// are no matches AND no query: this lets the App unconditionally
// concatenate the palette into the layout without worrying about
// reserving vertical space.
//
// When there are no matches BUT the user has typed a query (e.g.
// `/foo`) we render a single "no commands match" row so the user
// has feedback rather than a silently disappearing popover.
func (p CommandPalette) View() string {
	width := p.Width
	if width <= 0 {
		width = 60
	}
	// Allow a little headroom for the border + padding.
	innerWidth := max(width-4, 20)

	if len(p.Matches) == 0 {
		if p.QueryAfterSlash == "" {
			return ""
		}
		return p.box(p.style(theme.AtomMuted).Render(
			fmt.Sprintf("no commands match /%s", p.QueryAfterSlash),
		), width)
	}

	selected := p.Highlight
	if selected < 0 || selected >= len(p.Matches) {
		selected = -1
	}
	windowHighlight := max(selected, 0)
	start := paletteWindowStart(len(p.Matches), commandPaletteMaxRows, windowHighlight)
	end := min(start+commandPaletteMaxRows, len(p.Matches))
	visible := p.Matches[start:end]
	overflowBefore := start
	overflowAfter := len(p.Matches) - end

	// Establish a per-row layout: "  /name<padding>  description"
	nameWidth := 0
	for _, c := range visible {
		if n := len(c.Name()) + 1; n > nameWidth { // +1 for slash
			nameWidth = n
		}
	}
	if nameWidth > innerWidth/2 {
		nameWidth = innerWidth / 2
	}

	rows := make([]string, 0, len(visible)+2)
	if overflowBefore > 0 {
		rows = append(rows, p.style(theme.AtomMuted).Render(
			fmt.Sprintf("  ↑ %d more", overflowBefore),
		))
	}
	for i, c := range visible {
		nameTok := "/" + c.Name()
		nameCol := padRight(nameTok, nameWidth)
		descCol := truncate(c.Description(), innerWidth-nameWidth-2)

		var row string
		if start+i == selected {
			accent := p.style(theme.AtomAccent)
			rowText := fmt.Sprintf("▶ %s  %s", nameCol, descCol)
			row = accent.Render(rowText)
		} else {
			muted := p.style(theme.AtomMuted)
			rowText := fmt.Sprintf("  %s  %s", muted.Render(nameCol), descCol)
			row = rowText
		}
		rows = append(rows, row)
	}
	if overflowAfter > 0 {
		rows = append(rows, p.style(theme.AtomMuted).Render(
			fmt.Sprintf("  ↓ %d more — keep typing to narrow", overflowAfter),
		))
	}
	body := strings.Join(rows, "\n")
	return p.box(body, width)
}

func paletteWindowStart(total, maxRows, highlight int) int {
	if total <= maxRows || maxRows <= 0 {
		return 0
	}
	start := highlight - maxRows/2
	if start < 0 {
		return 0
	}
	lastStart := total - maxRows
	if start > lastStart {
		return lastStart
	}
	return start
}

// box wraps body in the palette's themed border at the requested width.
func (p CommandPalette) box(body string, width int) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(width - 2) // border consumes 2 cells
	if p.Theme != nil {
		bs := p.Theme.Style(theme.AtomModalBorder)
		style = style.BorderForeground(bs.GetForeground())
	}
	return style.Render(body)
}

func (p CommandPalette) style(a theme.Atom) lipgloss.Style {
	if p.Theme == nil {
		return lipgloss.NewStyle()
	}
	return p.Theme.Style(a)
}

// padRight pads s with spaces to exactly width cells, truncating
// when s is longer.  Cell-width approximation is len-based; we
// intentionally don't run rune-width math here — command names are
// constrained to ASCII by [a-z][a-z0-9_-]*.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// truncate clips s to width with a trailing ellipsis when truncated.
// Reused locally because the public truncateInline helper lives in
// cmd/hygge/cli; the dependency direction would be wrong.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if len(s) <= width {
		return s
	}
	if width <= 1 {
		return string(s[0])
	}
	return s[:width-1] + "…"
}
