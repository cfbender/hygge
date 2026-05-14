package ui

import (
	"github.com/charmbracelet/glamour"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// newRenderer constructs a glamour TermRenderer for the given theme and word-wrap width.
//
// The renderer is intentionally simple in v0.1: it relies on glamour's auto style
// (which picks dark or light based on the terminal background) and overrides the
// word-wrap width.  The theme parameter is kept in the signature so future work can
// thread theme atoms into a glamour.WithStyles config without changing call sites.
//
// If width is non-positive, glamour's default wrapping (80) is used.
func newRenderer(_ *theme.Theme, width int) (*glamour.TermRenderer, error) {
	opts := []glamour.TermRendererOption{
		glamour.WithAutoStyle(),
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
