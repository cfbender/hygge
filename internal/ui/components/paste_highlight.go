package components

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

var pastedInputMarkerPattern = regexp.MustCompile(`\[ Pasted \d+ lines? \]`)

// HighlightPastedInputMarkers applies style to collapsed multi-line paste markers.
func HighlightPastedInputMarkers(s string, style lipgloss.Style) string {
	if s == "" {
		return s
	}
	matches := pastedInputMarkerPattern.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(s[last:m[0]])
		b.WriteString(style.Render(s[m[0]:m[1]]))
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
