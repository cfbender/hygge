package components

import (
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

const defaultDiffPreviewLines = 12

const (
	sideBySideDiffMinWidth = 72
	diffPaneSeparator      = "  │  "
)

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

	width := d.Width
	if width <= 0 {
		width = 80
	}
	width = max(width, 1)

	rows := diffRows(lines)
	numW := diffLineNumberWidth(rows)
	if width >= sideBySideDiffMinWidth {
		pairs, truncated := d.visibleSideBySidePairs(rows)
		return renderSideBySideDiff(pairs, numW, width, truncated, diffStyles(d.Theme))
	}

	lines, truncated := d.visibleLines(lines)
	rows = diffRows(lines)
	numW = diffLineNumberWidth(rows)
	contentW := max(width-(numW*2)-6, 1)

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

type diffPair struct {
	old diffRow
	new diffRow
}

// IsTruncated reports whether the diff exceeds its configured preview line limit.
func (d DiffView) IsTruncated() bool {
	lines := strings.Split(strings.TrimRight(d.Raw, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return false
	}
	_, truncated := d.visibleLines(lines)
	return truncated
}

func (d DiffView) visibleLines(lines []string) ([]string, bool) {
	maxLines := d.MaxLines
	if maxLines <= 0 {
		maxLines = defaultDiffPreviewLines
	}
	if len(lines) <= maxLines {
		return lines, false
	}
	return lines[:maxLines], true
}

func (d DiffView) visibleSideBySidePairs(rows []diffRow) ([]diffPair, bool) {
	maxLines := d.MaxLines
	if maxLines <= 0 {
		maxLines = defaultDiffPreviewLines
	}
	if len(rows) <= maxLines {
		return sideBySidePairs(rows), false
	}

	end := maxLines
	if end > 0 {
		switch rows[end-1].kind {
		case diffRowDel:
			for end < len(rows) && rows[end].kind == diffRowDel {
				end++
			}
			for end < len(rows) && rows[end].kind == diffRowAdd {
				end++
			}
		case diffRowAdd:
			for end < len(rows) && rows[end].kind == diffRowAdd {
				end++
			}
		}
	}
	return sideBySidePairs(rows[:end]), true
}

func renderSideBySideDiff(pairs []diffPair, numW, width int, truncated bool, s diffLineStyles) string {
	separatorW := lipgloss.Width(diffPaneSeparator)
	paneW := max((width-separatorW)/2, numW+4)
	contentW := max(paneW-numW-3, 1)
	out := make([]string, 0, len(pairs)+1)
	for _, pair := range pairs {
		if pair.old.kind == diffRowMeta || pair.new.kind == diffRowMeta {
			text := pair.old.text
			if text == "" {
				text = pair.new.text
			}
			out = append(out, s.meta.Render(clipVisible(text, width)))
			continue
		}
		left := renderDiffPane(pair.old, numW, contentW, false, s)
		right := renderDiffPane(pair.new, numW, contentW, true, s)
		out = append(out, left+diffPaneSeparator+right)
	}
	if truncated {
		out = append(out, s.meta.Italic(true).Render("… diff truncated"))
	}
	return strings.Join(out, "\n")
}

func sideBySidePairs(rows []diffRow) []diffPair {
	pairs := make([]diffPair, 0, len(rows))
	for i := 0; i < len(rows); i++ {
		row := rows[i]
		if row.kind == diffRowDel {
			delStart := i
			for i < len(rows) && rows[i].kind == diffRowDel {
				i++
			}
			addStart := i
			for i < len(rows) && rows[i].kind == diffRowAdd {
				i++
			}
			delRows := rows[delStart:addStart]
			addRows := rows[addStart:i]
			shared := min(len(delRows), len(addRows))
			for j := range shared {
				pairs = append(pairs, diffPair{old: delRows[j], new: addRows[j]})
			}
			for j := shared; j < len(delRows); j++ {
				pairs = append(pairs, diffPair{old: delRows[j]})
			}
			for j := shared; j < len(addRows); j++ {
				pairs = append(pairs, diffPair{new: addRows[j]})
			}
			i--
			continue
		}
		if row.kind == diffRowAdd {
			pairs = append(pairs, diffPair{new: row})
			continue
		}
		pairs = append(pairs, diffPair{old: row, new: row})
	}
	return pairs
}

func renderDiffPane(row diffRow, numW, contentW int, useNewLine bool, s diffLineStyles) string {
	if row.text == "" && row.kind == diffRowBody && row.oldLine == 0 && row.newLine == 0 {
		return strings.Repeat(" ", numW+3+contentW)
	}
	n := row.oldLine
	if useNewLine {
		n = row.newLine
	}
	if row.kind == diffRowAdd || n == 0 {
		n = row.newLine
	}
	gutter := s.gutter.Render(formatDiffLineNumber(n, numW) + " │ ")
	text := clipVisible(row.text, contentW)
	switch row.kind {
	case diffRowAdd:
		return gutter + s.add.Render(padDiffRight(text, contentW))
	case diffRowDel:
		return gutter + s.del.Render(padDiffRight(text, contentW))
	case diffRowMeta:
		return gutter + s.meta.Render(padDiffRight(text, contentW))
	default:
		return gutter + s.body.Render(padDiffRight(text, contentW))
	}
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

func padDiffRight(s string, width int) string {
	for lipgloss.Width(s) < width {
		s += " "
	}
	return s
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
