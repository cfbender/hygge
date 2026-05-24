package components

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// urlPattern matches plain http:// and https:// URLs in text.
// It captures the URL up to the first whitespace or common trailing
// punctuation that is unlikely to be part of the URL itself.
var urlPattern = regexp.MustCompile(`https?://[^\s<>"')\]},;]+`)

// urlTrailingPunct is the set of characters to strip from the right end of a
// matched URL. A period or comma at the very end almost always belongs to the
// surrounding sentence, not the URL.
const urlTrailingPunct = ".,:"

// LinkifyURLs wraps every plain http(s):// URL found in s with an OSC 8
// terminal hyperlink sequence so that supporting terminals render the URL
// as a clickable link while keeping the visible text unchanged.
//
// The function operates on the raw string including any ANSI CSI styling
// already applied; it will not match URLs that appear inside an ANSI escape
// sequence because those sequences contain \x1b which cannot be part of a
// valid URL match.
func LinkifyURLs(s string) string {
	if s == "" {
		return s
	}
	locs := urlPattern.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range locs {
		start, end := m[0], m[1]
		b.WriteString(s[last:start])
		url := strings.TrimRight(s[start:end], urlTrailingPunct)
		if url == "" {
			b.WriteString(s[start:end])
			last = end
			continue
		}
		// Wrap with OSC 8 hyperlink; visible text is the URL itself.
		b.WriteString(lipgloss.NewStyle().Hyperlink(url).Render(url))
		// Emit any stripped trailing characters as plain text.
		b.WriteString(s[start+len(url) : end])
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}
