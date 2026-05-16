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

// View renders the header as a padded three-row brand band.
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

	content := centerHeaderLine(wordmark+version, h.Width)
	rule := s.Header.Separator.Render(strings.Repeat("─", max(h.Width-4, 0)))
	return strings.Join([]string{
		headerBandLine("", h.Width, s),
		headerBandLine(content, h.Width, s),
		headerBandLine(centerHeaderLine(rule, h.Width), h.Width, s),
	}, "\n")
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

// View renders the compact header as the same three-row band with denser info.
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
	content := fmt.Sprintf("%s%s%s", wordmark, strings.Repeat(" ", gap), right)
	return strings.Join([]string{
		headerBandLine("", h.Width, s),
		headerBandLine(content, h.Width, s),
		headerBandLine(s.Header.Separator.Render(strings.Repeat("─", h.Width)), h.Width, s),
	}, "\n")
}

func centerHeaderLine(content string, width int) string {
	contentW := lipgloss.Width(content)
	if contentW >= width {
		return content
	}
	return strings.Repeat(" ", (width-contentW)/2) + content
}

func headerBandLine(content string, width int, s *styles.Styles) string {
	if width <= 0 {
		return ""
	}
	visible := lipgloss.Width(content)
	if visible < width {
		content += strings.Repeat(" ", width-visible)
	}
	return s.Header.Wrapper.Width(width).Render(content)
}
