package components

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// expectedOSC8 returns the OSC 8 hyperlink sequence that LinkifyURLs emits
// for the given URL (visible text == URL).
func expectedOSC8(url string) string {
	// OSC 8 format: \x1b]8;;<url>\x07<visible>\x1b]8;;\x07
	return "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
}

func TestLinkifyURLs_PlainHTTPS(t *testing.T) {
	t.Parallel()
	url := "https://example.com"
	out := LinkifyURLs("visit " + url + " today")
	want := expectedOSC8(url)
	if !strings.Contains(out, want) {
		t.Fatalf("expected OSC 8 hyperlink for %q; got:\n%q", url, out)
	}
	if !strings.Contains(out, "visit ") || !strings.Contains(out, " today") {
		t.Fatalf("surrounding text lost; got:\n%q", out)
	}
}

func TestLinkifyURLs_PlainHTTP(t *testing.T) {
	t.Parallel()
	url := "http://example.com/path?q=1"
	out := LinkifyURLs(url)
	want := expectedOSC8(url)
	if !strings.Contains(out, want) {
		t.Fatalf("expected OSC 8 hyperlink for %q; got:\n%q", url, out)
	}
}

func TestLinkifyURLs_MultipleURLs(t *testing.T) {
	t.Parallel()
	u1 := "https://foo.example"
	u2 := "http://bar.example/page"
	out := LinkifyURLs(u1 + " and " + u2)
	if !strings.Contains(out, expectedOSC8(u1)) {
		t.Fatalf("first URL not linkified; got:\n%q", out)
	}
	if !strings.Contains(out, expectedOSC8(u2)) {
		t.Fatalf("second URL not linkified; got:\n%q", out)
	}
}

func TestLinkifyURLs_NoURL(t *testing.T) {
	t.Parallel()
	in := "no URLs here, just plain text"
	if out := LinkifyURLs(in); out != in {
		t.Fatalf("expected unchanged output; got:\n%q", out)
	}
}

func TestLinkifyURLs_Empty(t *testing.T) {
	t.Parallel()
	if out := LinkifyURLs(""); out != "" {
		t.Fatalf("expected empty string; got %q", out)
	}
}

func TestLinkifyURLs_TrailingPunctuation(t *testing.T) {
	t.Parallel()
	// Trailing period, comma, and closing paren should not be included in the
	// URL capture — the regex excludes those characters from the URL.
	url := "https://example.com/path"
	cases := []struct {
		input   string
		wantURL string
	}{
		{url + ".", url},
		{url + ",", url},
		// Closing ) is excluded by the character class in urlPattern.
		{"(" + url + ")", url},
	}
	for _, tc := range cases {
		out := LinkifyURLs(tc.input)
		if !strings.Contains(out, expectedOSC8(tc.wantURL)) {
			t.Errorf("input %q: want OSC 8 for %q; got:\n%q", tc.input, tc.wantURL, out)
		}
	}
}

// TestLinkifyURLs_InsideStyledText verifies that URLs inside ANSI CSI-styled
// text (as produced by HighlightMentions) are still recognised because the
// URL text itself does not contain escape bytes.
func TestLinkifyURLs_InsideStyledText(t *testing.T) {
	t.Parallel()
	// Simulate styled prefix + unstyled URL (CSI sequences won't appear in the URL).
	prefix := "\x1b[1mbolded prefix\x1b[0m "
	url := "https://example.com"
	in := prefix + url
	out := LinkifyURLs(in)
	if !strings.Contains(out, expectedOSC8(url)) {
		t.Fatalf("URL not linkified after styled prefix; got:\n%q", out)
	}
	if !strings.Contains(out, prefix) {
		t.Fatalf("styled prefix lost; got:\n%q", out)
	}
}

// ── Integration: user bubble in MessageList ─────────────────────────────────

func TestURLsLinkedInUserBubble(t *testing.T) {
	t.Parallel()
	url := "https://example.com"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{{
			Role: RoleUser,
			Raw:  "see " + url + " for details",
		}},
	}
	out := ml.View()
	if !strings.Contains(out, expectedOSC8(url)) {
		t.Fatalf("URL not linkified in user bubble; want OSC 8 for %q in:\n%q", url, out)
	}
}

// ── Integration: assistant bubble in MessageList ─────────────────────────────

func TestURLsLinkedInAssistantBubble(t *testing.T) {
	t.Parallel()
	url := "https://docs.example.com/guide"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{{
			Role: RoleAssistant,
			Raw:  "Check the docs at " + url + " for more information.",
		}},
	}
	out := ml.View()
	if !strings.Contains(out, expectedOSC8(url)) {
		t.Fatalf("URL not linkified in assistant bubble; want OSC 8 for %q in:\n%q", url, out)
	}
}

func TestURLsNotLinkedInAssistantPlaceholder(t *testing.T) {
	t.Parallel()
	// Placeholder bubbles should NOT get OSC 8 linkification because their
	// content is never user-supplied — wrapping would needlessly perturb the
	// muted-italic styling sequence.
	url := "https://placeholder.example.com"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{{
			Role:          RoleAssistant,
			Raw:           "Thinking… " + url,
			IsPlaceholder: true,
		}},
	}
	out := ml.View()
	if strings.Contains(out, expectedOSC8(url)) {
		t.Fatalf("OSC 8 link must not appear in placeholder bubble; got:\n%q", out)
	}
}

// ── Integration: input component ────────────────────────────────────────────

// Note: LinkifyURLs is intentionally not applied to the Input component's
// rendered output. The bubbles textarea renderer injects CSI cursor-position
// sequences inside the rendered text, which fragments OSC 8 hyperlink
// sequences (whose BEL terminator \x07 is misinterpreted by the pipeline).
// URL hyperlinking in the input would require upstream textarea support for
// OSC 8 passthrough. The feature is therefore scoped to chat output (user
// bubble / assistant bubble) where OSC 8 sequences are preserved end-to-end.
