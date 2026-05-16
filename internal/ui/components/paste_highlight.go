package components

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
)

var pastedInputMarkerPattern = regexp.MustCompile(`\[ Pasted \d+ lines? \]`)

// HighlightPastedInputMarkers applies style to collapsed multi-line paste markers.
func HighlightPastedInputMarkers(s string, style lipgloss.Style) string {
	if s == "" {
		return s
	}
	visible, starts, ends := visibleTextSpans(s)
	matches := pastedInputMarkerPattern.FindAllStringIndex(visible, -1)
	if len(matches) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		startRune := utf8.RuneCountInString(visible[:m[0]])
		endRune := utf8.RuneCountInString(visible[:m[1]])
		if startRune >= len(starts) || endRune == 0 || endRune-1 >= len(ends) {
			continue
		}
		start := starts[startRune]
		end := ends[endRune-1]
		b.WriteString(s[last:start])
		b.WriteString(style.Render(visible[m[0]:m[1]]))
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func visibleTextSpans(s string) (string, []int, []int) {
	var visible strings.Builder
	starts := make([]int, 0, len(s))
	ends := make([]int, 0, len(s))
	for i := 0; i < len(s); {
		if s[i] == '\x1b' {
			next := i + 1
			if next < len(s) && s[next] == '[' {
				next++
				for next < len(s) {
					c := s[next]
					next++
					if c >= '@' && c <= '~' {
						break
					}
				}
				i = next
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 0 {
			break
		}
		visible.WriteRune(r)
		starts = append(starts, i)
		ends = append(ends, i+size)
		i += size
	}
	return visible.String(), starts, ends
}
