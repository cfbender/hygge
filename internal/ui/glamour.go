package ui

import (
	"github.com/charmbracelet/glamour"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// newRenderer constructs a glamour TermRenderer for the given theme and word-wrap width.
//
// Use an explicit built-in style rather than glamour.WithAutoStyle. Auto style
// asks the terminal for its background colour (OSC 11) the first time markdown
// is rendered; in Bubble Tea v2 that query can block the UI while waiting for a
// response and the response can leak into stdin as prompt text. Crush avoids the
// same class of bugs by passing explicit markdown styles, so Hygge does the same
// with Glamour's deterministic dark style until the theme atoms are threaded into
// a full glamour.StyleConfig.
//
// If width is non-positive, glamour's default wrapping (80) is used.
func newRenderer(_ *theme.Theme, width int) (*glamour.TermRenderer, error) {
	opts := []glamour.TermRendererOption{
		glamour.WithStandardStyle("dark"),
	}
	if width > 0 {
		opts = append(opts, glamour.WithWordWrap(width))
	}
	r, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return nil, err //nolint:wrapcheck // pass-through, caller wraps with context
	}
	return r, nil
}

// renderMarkdown is a defensive wrapper around glamour rendering.  On any error
// it returns the input string as plain text so the UI never crashes.
func renderMarkdown(r *glamour.TermRenderer, in string) string {
	if r == nil || in == "" {
		return in
	}
	out, err := r.Render(in)
	if err != nil {
		return in
	}
	return out
}
