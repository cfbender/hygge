package components

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

var mentionTokenPattern = regexp.MustCompile(`(^|[\s(])(@(?:agent:[A-Za-z0-9_]+|[A-Za-z0-9_./~:-]*[A-Za-z0-9_~:-]))`)

// HighlightMentions applies style to @ mention tokens in already-rendered text.
func HighlightMentions(s string, style lipgloss.Style) string {
	if s == "" {
		return s
	}
	matches := mentionTokenPattern.FindAllStringSubmatchIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		prefixStart, prefixEnd := m[2], m[3]
		mentionStart, mentionEnd := m[4], m[5]
		b.WriteString(s[last:prefixStart])
		b.WriteString(s[prefixStart:prefixEnd])
		b.WriteString(style.Render(s[mentionStart:mentionEnd]))
		last = mentionEnd
	}
	b.WriteString(s[last:])
	return b.String()
}
