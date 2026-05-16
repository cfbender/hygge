package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// Header renders the top-of-screen branded header bar with gradient
// wordmark and session info.
type Header struct {
	Width   int
	Styles  *styles.Styles
	AppName string
	Version string
}

// View renders the header as a single line with gradient branding.
func (h Header) View() string {
	if h.Width <= 0 || h.Styles == nil {
		return ""
	}

	s := h.Styles

	// Branded wordmark with gradient.
	name := h.AppName
	if name == "" {
		name = "hygge"
	}
	wordmark := styles.ApplyBoldForegroundGrad(
		lipgloss.NewStyle(),
		"·"+name+"·",
		s.Logo.GradFromColor,
		s.Logo.GradToColor,
	)

	// Version label.
	var version string
	if h.Version != "" {
		version = lipgloss.NewStyle().
			Foreground(s.Logo.VersionColor).
			Render(" " + h.Version)
	}

	// Build the header line: centered wordmark with optional version.
	content := wordmark + version
	contentW := lipgloss.Width(content)

	// Center the content in the available width.
	if contentW < h.Width {
		pad := (h.Width - contentW) / 2
		content = strings.Repeat(" ", pad) + content
	}

	// Pad to full width.
	visible := lipgloss.Width(content)
	if visible < h.Width {
		content += strings.Repeat(" ", h.Width-visible)
	}

	return content
}

// CompactHeader renders a minimal one-line header for narrow terminals.
type CompactHeader struct {
	Width    int
	Styles   *styles.Styles
	AppName  string
	Model    string
	Provider string
	Tokens   string
	Cost     string
}

// View renders the compact header.
func (h CompactHeader) View() string {
	if h.Width <= 0 || h.Styles == nil {
		return ""
	}

	s := h.Styles

	// Gradient wordmark.
	name := h.AppName
	if name == "" {
		name = "hygge"
	}
	wordmark := styles.ApplyBoldForegroundGrad(
		lipgloss.NewStyle(),
		"·"+name+"·",
		s.Logo.GradFromColor,
		s.Logo.GradToColor,
	)

	// Right-aligned info.
	var info []string
	if h.Model != "" {
		info = append(info, s.ModelInfo.Name.Render(h.Model))
	}
	if h.Tokens != "" {
		info = append(info, s.ModelInfo.Tokens.Render(h.Tokens))
	}
	if h.Cost != "" {
		info = append(info, s.ModelInfo.Cost.Render(h.Cost))
	}

	right := strings.Join(info, s.Header.Separator.Render(" · "))
	rightW := lipgloss.Width(right)
	wordmarkW := lipgloss.Width(wordmark)

	// Fill gap between wordmark and info.
	gap := max(h.Width-wordmarkW-rightW, 1)

	return fmt.Sprintf("%s%s%s", wordmark, strings.Repeat(" ", gap), right)
}
