package components

import (
	"strings"
	"testing"
)

func TestBreadcrumb_EmptyWhenOneSegment(t *testing.T) {
	t.Parallel()
	b := Breadcrumb{Segments: []string{"root"}, Width: 80}
	if got := b.View(); got != "" {
		t.Errorf("single segment: expected empty string, got %q", got)
	}
}

func TestBreadcrumb_EmptyWhenNoSegments(t *testing.T) {
	t.Parallel()
	b := Breadcrumb{Segments: nil, Width: 80}
	if got := b.View(); got != "" {
		t.Errorf("no segments: expected empty string, got %q", got)
	}
}

func TestBreadcrumb_TwoSegments(t *testing.T) {
	t.Parallel()
	b := Breadcrumb{Segments: []string{"root", "child"}, Width: 120, Theme: nil}
	got := b.View()
	if !strings.Contains(got, "root") || !strings.Contains(got, "child") || !strings.Contains(got, "›") {
		t.Errorf("two-segment breadcrumb %q missing expected parts", got)
	}
}

func TestBreadcrumb_ThreeSegments(t *testing.T) {
	t.Parallel()
	b := Breadcrumb{Segments: []string{"root", "mid", "leaf"}, Width: 120, Theme: nil}
	got := b.View()
	if !strings.Contains(got, "root") || !strings.Contains(got, "leaf") {
		t.Errorf("three-segment breadcrumb %q missing root or leaf", got)
	}
}

func TestBreadcrumb_TruncatesWhenTooWide_TwoSegments(t *testing.T) {
	t.Parallel()
	// Force a narrow width to trigger truncation.
	b := Breadcrumb{
		Segments: []string{"root", "a-very-long-session-name-that-wont-fit"},
		Width:    20,
		Theme:    nil,
	}
	got := b.View()
	// Must not be wider than Width+some small ANSI overhead.
	// The truncation test focuses on presence of "…" and root.
	if !strings.Contains(got, "root") {
		t.Errorf("truncated two-segment %q missing root", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("truncated two-segment %q missing ellipsis", got)
	}
}

func TestBreadcrumb_EllidesMiddleWhenTooWide(t *testing.T) {
	t.Parallel()
	b := Breadcrumb{
		Segments: []string{"root", "mid1", "mid2", "leaf"},
		Width:    20,
		Theme:    nil,
	}
	got := b.View()
	if !strings.Contains(got, "root") {
		t.Errorf("elided breadcrumb %q missing root", got)
	}
	if !strings.Contains(got, "leaf") {
		t.Errorf("elided breadcrumb %q missing leaf", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("elided breadcrumb %q missing ellipsis", got)
	}
}

func TestBreadcrumb_DefaultWidth(t *testing.T) {
	t.Parallel()
	// Width = 0 should fall back to 80.
	b := Breadcrumb{Segments: []string{"root", "child"}, Width: 0}
	got := b.View()
	if got == "" {
		t.Errorf("zero Width: expected non-empty breadcrumb, got empty")
	}
}

func TestSessionLabel_PrefersSlug(t *testing.T) {
	t.Parallel()
	got := SessionLabel("my-slug", "first msg preview", "01ABCDEFGHIJKLMNOPQRSTUVWX")
	if got != "my-slug" {
		t.Errorf("expected slug, got %q", got)
	}
}

func TestSessionLabel_FallsBackToPreview(t *testing.T) {
	t.Parallel()
	got := SessionLabel("", "this is my first message in the session which is long", "id123")
	// Must be truncated to 24 runes.
	runes := []rune(got)
	if len(runes) > 24 {
		t.Errorf("preview label too long: %d runes in %q", len(runes), got)
	}
	if !strings.HasPrefix(got, "this is my first message") {
		t.Errorf("unexpected preview label %q", got)
	}
}

func TestSessionLabel_FallsBackToID(t *testing.T) {
	t.Parallel()
	got := SessionLabel("", "", "01ABCDEF1234567890ABCDEF12")
	if !strings.HasPrefix(got, "sess_") {
		t.Errorf("expected 'sess_' prefix, got %q", got)
	}
	if len(got) < 8 {
		t.Errorf("id label too short: %q", got)
	}
}
