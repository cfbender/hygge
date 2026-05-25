package ui_test

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/ui"
)

// TestExitSummary_EmptySessionIDReturnsEmpty verifies that no output is
// produced when no session was created (e.g. the user quit the onboarding
// wizard before a session existed).
func TestExitSummary_EmptySessionIDReturnsEmpty(t *testing.T) {
	got := ui.ExitSummary(ui.ExitSummaryOptions{SessionID: ""})
	if got != "" {
		t.Fatalf("expected empty string, got: %q", got)
	}
}

// TestExitSummary_ContainsBrandedElements verifies the three required
// elements: the "hygge" wordmark in the fog, the Session label, and the
// Continue/resume command line.
func TestExitSummary_ContainsBrandedElements(t *testing.T) {
	const sid = "01JPDKABCDEF1234"
	got := ui.ExitSummary(ui.ExitSummaryOptions{
		SessionID:    sid,
		SessionTitle: "my-project",
	})

	if got == "" {
		t.Fatal("ExitSummary returned empty for a non-empty session id")
	}

	// The fog banner should embed the "hygge" wordmark.
	if !strings.Contains(got, "hygge") {
		t.Errorf("exit summary missing hygge wordmark:\n%s", got)
	}

	// Must contain the session title.
	if !strings.Contains(got, "my-project") {
		t.Errorf("exit summary missing session title:\n%s", got)
	}

	// Must contain the resume command with the short id (first 8 chars).
	shortID := sid[:8]
	wantResume := "hygge --resume " + shortID
	if !strings.Contains(got, wantResume) {
		t.Errorf("exit summary missing resume command %q:\n%s", wantResume, got)
	}
}

// TestExitSummary_FallsBackToShortIDWhenNoTitle verifies that when no title
// is supplied, the short ID is used as the session label.
func TestExitSummary_FallsBackToShortIDWhenNoTitle(t *testing.T) {
	const sid = "01JPDKABCDEF1234"
	got := ui.ExitSummary(ui.ExitSummaryOptions{
		SessionID:    sid,
		SessionTitle: "",
	})

	shortID := sid[:8]
	if !strings.Contains(got, shortID) {
		t.Errorf("exit summary should use short id %q as title fallback:\n%s", shortID, got)
	}
}

// TestExitSummary_NilThemeDoesNotPanic verifies the nil-theme code path
// (happens when bootstrap hasn't loaded a theme, e.g. in dry-run mode).
func TestExitSummary_NilThemeDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ExitSummary panicked with nil Theme: %v", r)
		}
	}()
	_ = ui.ExitSummary(ui.ExitSummaryOptions{
		SessionID: "01JPDKABCDEF0001",
		Theme:     nil,
	})
}

// TestExitSummary_LongIDTruncatedToEightChars ensures the resume command
// always uses the 8-char short form, matching the existing shortID() convention.
func TestExitSummary_LongIDTruncatedToEightChars(t *testing.T) {
	// 26-char ULID-style id.
	const sid = "01JPDKABCDEF12345678901234"
	got := ui.ExitSummary(ui.ExitSummaryOptions{
		SessionID:    sid,
		SessionTitle: "test",
	})

	// Full id must NOT appear in the resume line.
	if strings.Contains(got, "hygge --resume "+sid) {
		t.Errorf("resume command should use short id, not full ULID; got:\n%s", got)
	}
	// Short id must appear.
	if !strings.Contains(got, "hygge --resume "+sid[:8]) {
		t.Errorf("resume command missing short id; got:\n%s", got)
	}
}
