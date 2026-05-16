package components

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

const defaultDiffPreviewLines = 12

var hunkHeaderPattern = regexp.MustCompile(`@@\s+-(\d+)(?:,(\d+))?\s+\+(\d+)(?:,(\d+))?\s+@@`)

// DiffView renders unified-diff-like text with line-level add/delete styling.
type DiffView struct {
	Raw      string
	Width    int
	Theme    *theme.Theme
	MaxLines int
}

// View returns the styled diff text, collapsed when MaxLines is exceeded.
func (d DiffView) View() string {
	lines := strings.Split(strings.TrimRight(d.Raw, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return ""
	}

	maxLines := d.MaxLines
	if maxLines <= 0 {
		maxLines = defaultDiffPreviewLines
	}
	truncated := false
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		truncated = true
	}

	width := d.Width
	if width <= 0 {
		width = 80
	}
	width = max(width, 1)

	rows := diffRows(lines)
	numW := diffLineNumberWidth(rows)
	contentW := max(width-(numW*2)-5, 1)

	styles := diffStyles(d.Theme)
	var out []string
	for _, row := range rows {
		out = append(out, renderDiffRow(row, numW, contentW, styles))
	}
	if truncated {
		out = append(out, styles.meta.Italic(true).Render("… diff truncated"))
	}
	return strings.Join(out, "\n")
}

type diffRow struct {
	oldLine int
	newLine int
	text    string
	kind    diffRowKind
}

type diffRowKind int

const (
	diffRowBody diffRowKind = iota
	diffRowAdd
	diffRowDel
	diffRowMeta
)

func diffRows(lines []string) []diffRow {
	oldLine := 1
	newLine := 1
	inHunk := false
	rows := make([]diffRow, 0, len(lines))
	for _, line := range lines {
		if oldStart, newStart, ok := parseHunkHeader(line); ok {
			oldLine = oldStart
			newLine = newStart
			inHunk = true
			rows = append(rows, diffRow{text: line, kind: diffRowMeta})
			continue
		}
		switch {
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			row := diffRow{newLine: newLine, text: line, kind: diffRowAdd}
			newLine++
			rows = append(rows, row)
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			row := diffRow{oldLine: oldLine, text: line, kind: diffRowDel}
			oldLine++
			rows = append(rows, row)
		case isDiffMetaLine(line):
			rows = append(rows, diffRow{text: line, kind: diffRowMeta})
		case inHunk:
			row := diffRow{oldLine: oldLine, newLine: newLine, text: line, kind: diffRowBody}
			oldLine++
			newLine++
			rows = append(rows, row)
		default:
			rows = append(rows, diffRow{text: line, kind: diffRowBody})
		}
	}
	return rows
}

func parseHunkHeader(line string) (int, int, bool) {
	matches := hunkHeaderPattern.FindStringSubmatch(line)
	if matches == nil {
		return 0, 0, false
	}
	oldStart, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, false
	}
	newStart, err := strconv.Atoi(matches[3])
	if err != nil {
		return 0, 0, false
	}
	return oldStart, newStart, true
}

func diffLineNumberWidth(rows []diffRow) int {
	maxLine := 0
	for _, row := range rows {
		maxLine = max(maxLine, row.oldLine, row.newLine)
	}
	if maxLine <= 0 {
		return 1
	}
	return len(strconv.Itoa(maxLine))
}

func renderDiffRow(row diffRow, numW, contentW int, s diffLineStyles) string {
	oldNum := formatDiffLineNumber(row.oldLine, numW)
	newNum := formatDiffLineNumber(row.newLine, numW)
	gutter := s.gutter.Render(oldNum + " │ " + newNum + " │ ")
	text := clipVisible(row.text, contentW)
	switch row.kind {
	case diffRowAdd:
		return gutter + s.add.Render(text)
	case diffRowDel:
		return gutter + s.del.Render(text)
	case diffRowMeta:
		return gutter + s.meta.Render(text)
	default:
		return gutter + s.body.Render(text)
	}
}

func formatDiffLineNumber(n, width int) string {
	if n <= 0 {
		return strings.Repeat(" ", width)
	}
	return padLeft(strconv.Itoa(n), width)
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

type diffLineStyles struct {
	add    lipgloss.Style
	del    lipgloss.Style
	meta   lipgloss.Style
	body   lipgloss.Style
	gutter lipgloss.Style
}

func diffStyles(t *theme.Theme) diffLineStyles {
	if t == nil {
		return diffLineStyles{
			add:    lipgloss.NewStyle(),
			del:    lipgloss.NewStyle(),
			meta:   lipgloss.NewStyle().Faint(true),
			body:   lipgloss.NewStyle(),
			gutter: lipgloss.NewStyle().Faint(true),
		}
	}
	return diffLineStyles{
		add:    t.Style(theme.AtomCodeFg).Background(t.Style(theme.AtomDiffAddBg).GetBackground()),
		del:    t.Style(theme.AtomCodeFg).Background(t.Style(theme.AtomDiffDelBg).GetBackground()),
		meta:   t.Style(theme.AtomMuted).Faint(true),
		body:   t.Style(theme.AtomCodeFg),
		gutter: t.Style(theme.AtomMuted).Faint(true),
	}
}

func isDiffMetaLine(line string) bool {
	return strings.HasPrefix(line, "@@") ||
		strings.HasPrefix(line, "diff --git") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "---") ||
		strings.HasPrefix(line, "+++")
}

func looksLikeDiff(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "\ndiff --git ") || strings.HasPrefix(s, "diff --git ") {
		return true
	}
	if strings.Contains(s, "\n@@ ") || strings.HasPrefix(s, "@@ ") {
		return true
	}
	return (strings.Contains(s, "\n--- ") || strings.HasPrefix(s, "--- ")) &&
		(strings.Contains(s, "\n+++ ") || strings.HasPrefix(s, "+++ "))
}

func clipVisible(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 && lipgloss.Width(string(runes))+1 > width {
		runes = runes[:len(runes)-1]
	}
	return string(runes) + "…"
}
