package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// newURLTestApp constructs a test App with an injectable URL opener and a
// known window size.  It populates a single assistant message containing a
// URL and forces a View() render so urlHitZones are populated.
//
// Returns the app and a pointer to the slice of URLs passed to the opener.
func newURLTestApp(t *testing.T) (*App, *[]string, *bus.Bus) {
	t.Helper()
	b := bus.New()

	var openedURLs []string
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         styles.DefaultTheme(),
		ProjectDir:    "~/proj",
		ModelProvider: "anthropic",
		ModelName:     "claude-sonnet-4-5",
		// Injectable opener: records URLs instead of launching a browser.
		OpenURL: func(url string) error {
			openedURLs = append(openedURLs, url)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, &openedURLs, b
}

// TestURLAtScreen_HitsURLZone verifies that urlAtScreen returns the correct
// URL when the screen coordinate maps to a URLHitZone, and returns "" for
// coordinates outside all zones.
func TestURLAtScreen_HitsURLZone(t *testing.T) {
	t.Parallel()
	app, _, _ := newURLTestApp(t)
	app.opts.SessionID = "url-session"

	const url = "https://click.example.com"
	app.messages = []uiMessage{{
		Role: components.RoleUser,
		Raw:  "visit " + url,
	}}

	// Render to populate urlHitZones.
	_ = app.View()

	if len(app.urlHitZones) == 0 {
		t.Fatal("expected urlHitZones to be populated after View()")
	}

	// Find the zone for our URL.
	var zone *components.URLHitZone
	for i := range app.urlHitZones {
		if app.urlHitZones[i].URL == url {
			zone = &app.urlHitZones[i]
			break
		}
	}
	if zone == nil {
		t.Fatalf("URL %q not found in urlHitZones: %+v", url, app.urlHitZones)
	}

	// Translate zone line + mid-column to real screen coordinates. The viewport
	// content has a leading blank line before the message list, so a message-list
	// zone on line K is displayed on viewport content line K+1.
	visibleLine := zone.Line + 1 - app.msgViewport.YOffset()
	screenY := headerHeight + visibleLine
	// Screen X: use the midpoint of the column range.
	screenX := (zone.StartCol + zone.EndCol) / 2

	got := app.urlAtScreen(screenX, screenY)
	if got != url {
		t.Errorf("urlAtScreen(%d, %d) = %q; want %q (zone=%+v offset=%d)",
			screenX, screenY, got, url, *zone, app.msgViewport.YOffset())
	}

	// A coordinate clearly outside the zone should return "".
	if got2 := app.urlAtScreen(0, screenY); got2 != "" {
		t.Errorf("urlAtScreen(0, %d) = %q; want empty (should not match at col 0)", screenY, got2)
	}
}

// TestMouseClickOnURL_OpensURL verifies that a tea.MouseClickMsg whose
// coordinates land on a URLHitZone invokes the OpenURL function with the
// correct URL without falling through to text selection.
func TestMouseClickOnURL_OpensURL(t *testing.T) {
	t.Parallel()
	app, opened, _ := newURLTestApp(t)
	app.opts.SessionID = "url-click-session"

	const url = "https://open.example.com"
	app.messages = []uiMessage{{
		Role: components.RoleUser,
		Raw:  "see " + url,
	}}

	// Render to populate urlHitZones.
	_ = app.View()

	if len(app.urlHitZones) == 0 {
		t.Fatal("urlHitZones empty after View() — cannot test click handler")
	}

	var zone *components.URLHitZone
	for i := range app.urlHitZones {
		if app.urlHitZones[i].URL == url {
			zone = &app.urlHitZones[i]
			break
		}
	}
	if zone == nil {
		t.Fatalf("URL %q not in urlHitZones: %+v", url, app.urlHitZones)
	}

	// Build a click at the zone's visible row and column midpoint. The viewport
	// content has a leading blank line before message-list line 0.
	screenY := headerHeight + zone.Line + 1 - app.msgViewport.YOffset()
	screenX := (zone.StartCol + zone.EndCol) / 2

	app.Update(tea.MouseClickMsg{X: screenX, Y: screenY, Button: tea.MouseLeft})

	if len(*opened) == 0 {
		t.Fatal("OpenURL was not called after clicking on a URL zone")
	}
	if (*opened)[0] != url {
		t.Errorf("OpenURL called with %q; want %q", (*opened)[0], url)
	}
}

// TestMouseClickOutsideURL_DoesNotOpenURL verifies that a click outside any
// URLHitZone does not invoke OpenURL.
func TestMouseClickOutsideURL_DoesNotOpenURL(t *testing.T) {
	t.Parallel()
	app, opened, _ := newURLTestApp(t)
	app.opts.SessionID = "url-miss-session"

	const url = "https://miss.example.com"
	app.messages = []uiMessage{{
		Role: components.RoleUser,
		Raw:  "see " + url,
	}}

	// Render to populate hit zones.
	_ = app.View()

	// Click in the top-left corner — likely outside any URL zone.
	app.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})

	if len(*opened) != 0 {
		t.Errorf("OpenURL must not be called for a click outside a URL zone; got %v", *opened)
	}
}

// TestOpenURLWithOS_EmptyURL verifies that OpenURLWithOS rejects an empty URL
// without attempting to exec a subprocess.
func TestOpenURLWithOS_EmptyURL(t *testing.T) {
	t.Parallel()
	err := OpenURLWithOS("")
	if err == nil {
		t.Fatal("expected error for empty URL; got nil")
	}
}

func TestOpenURLWithOS_RejectsUnsupportedScheme(t *testing.T) {
	t.Parallel()
	err := OpenURLWithOS("file:///tmp/secret")
	if err == nil {
		t.Fatal("expected error for unsupported URL scheme; got nil")
	}
}
