package components

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// ── unit: stripANSI ──────────────────────────────────────────────────────────

func TestStripANSI_PlainText(t *testing.T) {
	t.Parallel()
	if got := stripANSI("hello world"); got != "hello world" {
		t.Fatalf("expected unchanged plain text; got %q", got)
	}
}

func TestStripANSI_CSISequence(t *testing.T) {
	t.Parallel()
	// Bold SGR and reset.
	in := "\x1b[1mhello\x1b[0m world"
	want := "hello world"
	if got := stripANSI(in); got != want {
		t.Fatalf("stripANSI(%q) = %q; want %q", in, got, want)
	}
}

func TestStripANSI_OSC8Hyperlink(t *testing.T) {
	t.Parallel()
	// OSC 8 hyperlink produced by LinkifyURLs: ESC]8;;<url>BEL<visible>ESC]8;;BEL
	url := "https://example.com"
	in := "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
	// After stripping, only the visible text should remain.
	want := url
	got := stripANSI(in)
	if got != want {
		t.Fatalf("stripANSI(OSC8 hyperlink) = %q; want %q", got, want)
	}
}

func TestStripANSI_Empty(t *testing.T) {
	t.Parallel()
	if got := stripANSI(""); got != "" {
		t.Fatalf("expected empty output; got %q", got)
	}
}

// ── unit: ExtractURLPositions ────────────────────────────────────────────────

func TestExtractURLPositions_SingleURL(t *testing.T) {
	t.Parallel()
	text := "line zero\nhttps://example.com is here\nline two"
	positions := ExtractURLPositions(text)
	if len(positions) != 1 {
		t.Fatalf("expected 1 URL position; got %d: %+v", len(positions), positions)
	}
	p := positions[0]
	if p.URL != "https://example.com" {
		t.Errorf("URL = %q; want https://example.com", p.URL)
	}
	if p.Line != 1 {
		t.Errorf("Line = %d; want 1", p.Line)
	}
	if p.StartCol != 0 {
		t.Errorf("StartCol = %d; want 0", p.StartCol)
	}
	if p.EndCol != len("https://example.com") {
		t.Errorf("EndCol = %d; want %d", p.EndCol, len("https://example.com"))
	}
}

func TestExtractURLPositions_URLWithLeadingText(t *testing.T) {
	t.Parallel()
	text := "see https://example.com for details"
	positions := ExtractURLPositions(text)
	if len(positions) != 1 {
		t.Fatalf("expected 1 URL position; got %d", len(positions))
	}
	p := positions[0]
	wantStart := len([]rune("see "))
	if p.StartCol != wantStart {
		t.Errorf("StartCol = %d; want %d", p.StartCol, wantStart)
	}
	if p.EndCol != wantStart+len([]rune("https://example.com")) {
		t.Errorf("EndCol = %d; want %d", p.EndCol, wantStart+len([]rune("https://example.com")))
	}
}

func TestExtractURLPositions_URLAfterUnicodePrefix(t *testing.T) {
	t.Parallel()
	prefix := "→ "
	url := "https://example.com"
	positions := ExtractURLPositions(prefix + url)
	if len(positions) != 1 {
		t.Fatalf("expected 1 URL position; got %d", len(positions))
	}
	if got, want := positions[0].StartCol, len([]rune(prefix)); got != want {
		t.Fatalf("StartCol = %d; want visual rune column %d", got, want)
	}
}

func TestExtractURLPositions_MultipleURLsSameLine(t *testing.T) {
	t.Parallel()
	text := "https://a.example.com and https://b.example.com"
	positions := ExtractURLPositions(text)
	if len(positions) != 2 {
		t.Fatalf("expected 2 URL positions; got %d", len(positions))
	}
	if positions[0].URL != "https://a.example.com" {
		t.Errorf("first URL = %q; want https://a.example.com", positions[0].URL)
	}
	if positions[1].URL != "https://b.example.com" {
		t.Errorf("second URL = %q; want https://b.example.com", positions[1].URL)
	}
}

func TestExtractURLPositions_ANSIStyledLine(t *testing.T) {
	t.Parallel()
	// Simulate a styled line where the URL is preceded by bold SGR.
	prefix := "\x1b[1mbold prefix\x1b[0m "
	url := "https://styled.example.com"
	text := prefix + url
	positions := ExtractURLPositions(text)
	if len(positions) != 1 {
		t.Fatalf("expected 1 URL position; got %d", len(positions))
	}
	p := positions[0]
	if p.URL != url {
		t.Errorf("URL = %q; want %q", p.URL, url)
	}
	// StartCol should be the column of the URL in the plain/stripped text:
	// "bold prefix " = 12 characters.
	wantStart := len("bold prefix ")
	if p.StartCol != wantStart {
		t.Errorf("StartCol = %d; want %d (in stripped text %q)", p.StartCol, wantStart, stripANSI(text))
	}
}

func TestExtractURLPositions_TrailingPunctuationStripped(t *testing.T) {
	t.Parallel()
	text := "visit https://example.com/path."
	positions := ExtractURLPositions(text)
	if len(positions) != 1 {
		t.Fatalf("expected 1 URL position; got %d", len(positions))
	}
	if positions[0].URL != "https://example.com/path" {
		t.Errorf("URL = %q; want https://example.com/path", positions[0].URL)
	}
}

func TestExtractURLPositions_NoURLs(t *testing.T) {
	t.Parallel()
	positions := ExtractURLPositions("no urls here at all")
	if len(positions) != 0 {
		t.Fatalf("expected no URL positions; got %d: %+v", len(positions), positions)
	}
}

func TestExtractURLPositions_OSC8AlreadyLinkified(t *testing.T) {
	t.Parallel()
	// After LinkifyURLs, the URL text is wrapped in OSC 8 but visible text
	// still contains the URL.  ExtractURLPositions must still find it.
	url := "https://example.com"
	linkified := "\x1b]8;;" + url + "\x07" + url + "\x1b]8;;\x07"
	positions := ExtractURLPositions(linkified)
	if len(positions) != 1 {
		t.Fatalf("expected 1 URL from linkified text; got %d: %+v", len(positions), positions)
	}
	if positions[0].URL != url {
		t.Errorf("URL = %q; want %q", positions[0].URL, url)
	}
}

// ── integration: ViewWithHitZones returns URL hit zones ───────────────────────

func TestViewWithHitZones_URLZonesInUserBubble(t *testing.T) {
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
	_, _, _, _, urlZones, _ := ml.ViewWithHitZones()
	if len(urlZones) == 0 {
		t.Fatal("expected at least one URL hit zone for user bubble containing a URL")
	}
	var found bool
	for _, z := range urlZones {
		if z.URL == url {
			found = true
			if z.StartCol >= z.EndCol {
				t.Errorf("URLHitZone StartCol(%d) >= EndCol(%d)", z.StartCol, z.EndCol)
			}
		}
	}
	if !found {
		t.Fatalf("URL %q not found in hit zones: %+v", url, urlZones)
	}
}

func TestViewWithHitZones_URLZonesInAssistantBubble(t *testing.T) {
	t.Parallel()
	url := "https://docs.example.com/guide"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{{
			Role: RoleAssistant,
			Raw:  "Check the docs at " + url + " for more.",
		}},
	}
	_, _, _, _, urlZones, _ := ml.ViewWithHitZones()
	if len(urlZones) == 0 {
		t.Fatal("expected at least one URL hit zone for assistant bubble")
	}
	var found bool
	for _, z := range urlZones {
		if z.URL == url {
			found = true
		}
	}
	if !found {
		t.Fatalf("URL %q not found in hit zones: %+v", url, urlZones)
	}
}

func TestViewWithHitZones_URLZonesAbsentForAssistantPlaceholder(t *testing.T) {
	t.Parallel()
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
	_, _, _, _, urlZones, _ := ml.ViewWithHitZones()
	for _, z := range urlZones {
		if z.URL == url {
			t.Fatalf("assistant placeholder URL must not generate a hit zone; got zone %+v", z)
		}
	}
}

func TestViewWithHitZones_URLZonesAbsentForToolMessages(t *testing.T) {
	t.Parallel()
	// Tool messages contain file paths / output that may match URL patterns
	// but should NOT generate URL hit zones.
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "hello"},
			{
				Role:     RoleTool,
				ToolName: "grep",
				Raw:      "https://internal.example.com/file",
			},
		},
	}
	_, _, _, _, urlZones, _ := ml.ViewWithHitZones()
	for _, z := range urlZones {
		if z.URL == "https://internal.example.com/file" {
			t.Errorf("tool-message URL must not generate a hit zone; got zone %+v", z)
		}
	}
}

func TestViewWithHitZones_URLHitZoneLineIsInRenderedRange(t *testing.T) {
	t.Parallel()
	// The URL zone's Line must be inside the line range of the rendered output.
	url := "https://example.com"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{{
			Role: RoleUser,
			Raw:  "visit " + url,
		}},
	}
	content, _, _, _, urlZones, _ := ml.ViewWithHitZones()
	totalLines := strings.Count(content, "\n") + 1
	for _, z := range urlZones {
		if z.Line < 0 || z.Line >= totalLines {
			t.Errorf("URLHitZone.Line=%d out of rendered range [0,%d)", z.Line, totalLines)
		}
	}
}

func TestViewWithHitZones_MultipleMessagesURLZones(t *testing.T) {
	t.Parallel()
	url1 := "https://first.example.com"
	url2 := "https://second.example.com"
	ml := MessageList{
		Width: 100,
		Theme: styles.DefaultTheme(),
		Messages: []UIMessage{
			{Role: RoleUser, Raw: "first: " + url1},
			{Role: RoleAssistant, Raw: "second: " + url2},
		},
	}
	_, _, _, _, urlZones, _ := ml.ViewWithHitZones()
	foundURL1, foundURL2 := false, false
	for _, z := range urlZones {
		if z.URL == url1 {
			foundURL1 = true
		}
		if z.URL == url2 {
			foundURL2 = true
		}
	}
	if !foundURL1 {
		t.Errorf("URL %q not found in hit zones: %+v", url1, urlZones)
	}
	if !foundURL2 {
		t.Errorf("URL %q not found in hit zones: %+v", url2, urlZones)
	}
	// Zones from different messages must be on different lines.
	if len(urlZones) >= 2 && urlZones[0].Line == urlZones[1].Line {
		t.Errorf("URLs from different messages ended up on the same line (%d)", urlZones[0].Line)
	}
}
