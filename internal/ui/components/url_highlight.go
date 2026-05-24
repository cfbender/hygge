package components

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
)

// urlPattern matches plain http:// and https:// URLs in text.
// It captures the URL up to the first whitespace, ANSI/control byte, or common
// trailing punctuation that is unlikely to be part of the URL itself.
var urlPattern = regexp.MustCompile(`https?://[^\s\x00-\x1f\x7f<>"')\]},;]+`)

// ansiPattern strips ANSI CSI escape sequences and OSC sequences (including OSC 8
// hyperlinks) from a string so plain-text column positions can be computed.
// Matches:
//   - CSI sequences: ESC [ ... final-byte
//   - OSC sequences: ESC ] ... ST (where ST is BEL \x07 or ESC \)
//   - Simple ESC sequences: ESC followed by a single non-[ non-] byte
var ansiPattern = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[^[\]])`)

// stripANSI removes all ANSI escape sequences from s, returning only the
// visible/plain text content. Used to compute visual column positions of URLs
// within styled terminal output.
func stripANSI(s string) string {
	return ansiPattern.ReplaceAllString(s, "")
}

// URLPosition records a URL's location within a block of rendered text.
// Line and StartCol/EndCol are zero-indexed visual positions in the
// ANSI-stripped text.
type URLPosition struct {
	Line     int    // zero-indexed line within the rendered part
	StartCol int    // zero-indexed column where URL text begins
	EndCol   int    // zero-indexed column just past the last URL character
	URL      string // the matched URL (trailing punctuation stripped)
}

// ExtractURLPositions scans a rendered (potentially ANSI-styled) text block
// and returns the visual line/column position of every http(s):// URL found.
// It strips ANSI sequences before matching so escape codes do not shift column
// indices. Duplicate URLs on the same line are each returned separately.
func ExtractURLPositions(rendered string) []URLPosition {
	lines := strings.Split(rendered, "\n")
	var out []URLPosition
	for lineIdx, line := range lines {
		plain := stripANSI(line)
		locs := urlPattern.FindAllStringIndex(plain, -1)
		for _, loc := range locs {
			raw := plain[loc[0]:loc[1]]
			url := strings.TrimRight(raw, urlTrailingPunct)
			if url == "" {
				continue
			}
			startCol := len([]rune(plain[:loc[0]]))
			out = append(out, URLPosition{
				Line:     lineIdx,
				StartCol: startCol,
				EndCol:   startCol + len([]rune(url)),
				URL:      url,
			})
		}
	}
	return out
}

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
