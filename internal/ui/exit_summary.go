package ui

import (
	"fmt"
	"strings"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// ExitSummaryOptions holds the data needed to render the branded exit card
// that is printed to the terminal after the TUI exits normally.
type ExitSummaryOptions struct {
	// SessionID is the ULID of the session that was active when the TUI
	// closed.  An empty string suppresses the summary entirely (e.g. the
	// onboarding wizard exited before a session was created).
	SessionID string

	// SessionTitle is a human-readable label shown under "Session".
	// Prefer the slug; fall back to FirstMessagePreview; fall back to the
	// short ID.
	SessionTitle string

	// Theme, when non-nil, is used to tint the fog sample with the user's
	// active accent colour.  When nil the fog falls back to its built-in
	// soft-magenta default.
	Theme *styles.Styles
}

// ExitSummary renders the branded exit card as a plain string ready for
// direct output to the terminal.  It consists of:
//
//   - a small static fog sample with the "hygge" wordmark
//   - Session: <title>
//   - Continue: hygge --resume <short-id>
//
// Returns "" when opts.SessionID is empty so callers can skip output with
// a simple truthiness check.
func ExitSummary(opts ExitSummaryOptions) string {
	if opts.SessionID == "" {
		return ""
	}

	// Short ID consistent with shortID() in cmd/hygge/cli (first 8 chars).
	shortID := opts.SessionID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}

	title := opts.SessionTitle
	if title == "" {
		title = shortID
	}

	// Static fog snapshot: width=36, height=5 at t=0.  A fixed timestamp
	// keeps the output deterministic and avoids pulling a wall clock in a
	// pure rendering helper.  The sample is narrow enough to fit on an
	// 80-column terminal without wrapping.
	accent := resolveAccentRGB(opts.Theme, nil)
	fog := renderFogBanner(36, 5, 0.0, accent, "hygge")

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(fog)
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "  Session   %s\n", title)
	fmt.Fprintf(&sb, "  Continue  hygge --resume %s\n", shortID)
	sb.WriteString("\n")
	return sb.String()
}
