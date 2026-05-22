package components

import (
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// Breadcrumb renders a navigation path of the form "root › child › grandchild".
//
// It is shown above the message list whenever the foreground-stack depth is
// greater than 1 (i.e. the user has followed into a sub-session via Ctrl+G).
// When depth is 1 (root only), the breadcrumb is empty — View returns "".
//
// Width is the maximum character width of the rendered line.  If the natural
// rendering would exceed Width, middle segments are elided:
//
//	"root › … › 2 more › leaf"
//
// Segments are user-visible labels for each stack entry:
//   - The session's slug if non-empty.
//   - Otherwise the first 24 characters of the first user message preview.
//   - Otherwise "sess_" + the first 8 characters of the session id.
type Breadcrumb struct {
	// Segments is the ordered list of navigation labels, root first.
	Segments []string

	// Width is the maximum column width of the rendered line.  0 means 80.
	Width int

	// Theme is the active theme; nil is accepted (plain style used).
	Theme *styles.Styles
}

const breadcrumbSep = " › "

// View renders the breadcrumb, or "" when there is nothing to show.
// A single-segment stack (just the root) returns "".  Downstream
// callers do NOT need to special-case the empty string — a zero-height
// string does not occupy layout space.
func (b Breadcrumb) View() string {
	if len(b.Segments) <= 1 {
		return ""
	}

	width := b.Width
	if width <= 0 {
		width = 80
	}

	muted := b.style(styles.AtomMuted)

	// Build the full rendering to see if it fits.
	full := strings.Join(b.Segments, breadcrumbSep)
	rendered := muted.Render(full)
	if lipgloss.Width(rendered) <= width {
		return rendered
	}

	// The line is too wide — we need to elide content.
	root := b.Segments[0]
	leaf := b.Segments[len(b.Segments)-1]

	if len(b.Segments) == 2 {
		// Nothing to elide — just truncate the leaf label.
		avail := width - utf8.RuneCountInString(root) - utf8.RuneCountInString(breadcrumbSep) - 1
		leaf = breadcrumbTruncate(leaf, avail) + "…"
		return muted.Render(root + breadcrumbSep + leaf)
	}

	// 3+ segments: show "root › … › N more › leaf".
	middle := len(b.Segments) - 2 // count of collapsed segments
	more := itoa(middle) + " more"
	compact := root + breadcrumbSep + "…" + breadcrumbSep + more + breadcrumbSep + leaf
	return muted.Render(compact)
}

// style returns a lipgloss.Style for the given atom, or a blank style when
// no theme is configured.
func (b Breadcrumb) style(a styles.Atom) lipgloss.Style {
	if b.Theme == nil {
		return lipgloss.NewStyle()
	}
	return b.Theme.Style(a)
}

// breadcrumbTruncate returns s truncated to at most n runes.  When n <= 0 returns "".
// Named breadcrumbTruncate to avoid collision with the existing truncate function
// in command_palette.go.
func breadcrumbTruncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// SessionLabel returns the display label for a session, preferring slug over
// first-message preview over short id.  Used by the App to build Breadcrumb
// segments.
func SessionLabel(slug, firstMsgPreview, id string) string {
	if slug != "" {
		return slug
	}
	if firstMsgPreview != "" {
		runes := []rune(firstMsgPreview)
		if len(runes) > 24 {
			return string(runes[:24])
		}
		return firstMsgPreview
	}
	if len(id) >= 8 {
		return "sess_" + id[:8]
	}
	return "sess_" + id
}
