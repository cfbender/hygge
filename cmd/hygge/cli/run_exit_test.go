package cli

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cfbender/hygge/internal/session"
)

// TestExitSummaryFor_UsesSlugWhenAvailable verifies that exitSummaryFor
// prefers the session slug over FirstMessagePreview.
func TestExitSummaryFor_UsesSlugWhenAvailable(t *testing.T) {
	home := hermeticHome(t)
	_ = home

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	ctx := context.Background()
	sess, err := rt.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Set a slug.
	if err := rt.Store.RenameSession(ctx, sess.ID, "my-cool-project"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}

	got := exitSummaryFor(ctx, rt, sess.ID)

	if !strings.Contains(got, "my-cool-project") {
		t.Errorf("exit summary missing slug; got:\n%s", got)
	}
	if !strings.Contains(got, "hygge --resume "+shortID(sess.ID)) {
		t.Errorf("exit summary missing resume command; got:\n%s", got)
	}
}

// TestExitSummaryFor_UsesFirstMessagePreviewWhenNoSlug verifies that the
// first-message preview is used when no slug is set.
func TestExitSummaryFor_UsesFirstMessagePreviewWhenNoSlug(t *testing.T) {
	home := hermeticHome(t)
	_ = home

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	ctx := context.Background()
	sess, err := rt.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Append a user message so the store populates FirstMessagePreview.
	if _, err := rt.Store.AppendMessage(ctx, sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "hello from exit summary test"}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	got := exitSummaryFor(ctx, rt, sess.ID)

	if !strings.Contains(got, "hello from exit summary test") {
		t.Errorf("exit summary missing first-message preview; got:\n%s", got)
	}
}

// TestExitSummaryFor_EmptyWhenNoSession verifies that exitSummaryFor called
// with an empty sessionID produces no output (guarded at call site, but
// ui.ExitSummary must still handle it cleanly).
func TestExitSummaryFor_EmptyWhenNoSession(t *testing.T) {
	home := hermeticHome(t)
	_ = home

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	got := exitSummaryFor(context.Background(), rt, "")
	if got != "" {
		t.Errorf("expected empty string for empty session id, got: %q", got)
	}
}

func TestTruncateRunesPreservesUTF8(t *testing.T) {
	got := truncateRunes("こんにちは世界こんにちは世界", 8)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateRunes returned invalid UTF-8: %q", got)
	}
	if got != "こんにちは..." {
		t.Errorf("truncateRunes = %q, want %q", got, "こんにちは...")
	}
}
