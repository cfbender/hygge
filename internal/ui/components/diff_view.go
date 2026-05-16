package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

const defaultDiffPreviewLines = 12

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

	styles := diffStyles(d.Theme)
	var out []string
	for _, line := range lines {
		out = append(out, styleDiffLine(clipVisible(line, width), styles))
	}
	if truncated {
		out = append(out, styles.meta.Italic(true).Render("… diff truncated"))
	}
	return strings.Join(out, "\n")
}

type diffLineStyles struct {
	add  lipgloss.Style
	del  lipgloss.Style
	meta lipgloss.Style
	body lipgloss.Style
}

func diffStyles(t *theme.Theme) diffLineStyles {
	if t == nil {
		return diffLineStyles{
			add:  lipgloss.NewStyle(),
			del:  lipgloss.NewStyle(),
			meta: lipgloss.NewStyle().Faint(true),
			body: lipgloss.NewStyle(),
		}
	}
	return diffLineStyles{
		add:  t.Style(theme.AtomCodeFg).Background(t.Style(theme.AtomDiffAddBg).GetBackground()),
		del:  t.Style(theme.AtomCodeFg).Background(t.Style(theme.AtomDiffDelBg).GetBackground()),
		meta: t.Style(theme.AtomMuted).Faint(true),
		body: t.Style(theme.AtomCodeFg),
	}
}

func styleDiffLine(line string, s diffLineStyles) string {
	switch {
	case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
		return s.add.Render(line)
	case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
		return s.del.Render(line)
	case strings.HasPrefix(line, "@@"),
		strings.HasPrefix(line, "diff --git"),
		strings.HasPrefix(line, "index "),
		strings.HasPrefix(line, "---"),
		strings.HasPrefix(line, "+++"):
		return s.meta.Render(line)
	default:
		return s.body.Render(line)
	}
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

func editArgsDiff(args map[string]string) string {
	oldText, hasOld := args["oldString"]
	newText, hasNew := args["newString"]
	if !hasOld || !hasNew || oldText == newText {
		return ""
	}
	return beforeAfterDiff("old", "new", oldText, newText)
}

func writeArgsDiff(args map[string]string) string {
	content, ok := args["content"]
	if !ok || content == "" {
		return ""
	}
	return beforeAfterDiff("old", "new", "", content)
}

func beforeAfterDiff(oldLabel, newLabel, oldText, newText string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n@@\n", oldLabel, newLabel)
	for _, line := range splitDiffText(oldText) {
		b.WriteString("-")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, line := range splitDiffText(newText) {
		b.WriteString("+")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func splitDiffText(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
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
