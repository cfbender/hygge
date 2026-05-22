package components

import (
	"image/color"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// TestThemeGlamourStyle_NilTheme verifies that a nil theme returns the dark
// baseline without panicking.
func TestThemeGlamourStyle_NilTheme(t *testing.T) {
	t.Parallel()
	cfg := ThemeGlamourStyle(nil)
	// DarkStyleConfig heading has a non-nil color ("39").
	if cfg.Heading.Color == nil {
		t.Error("nil theme: Heading.Color should be non-nil (dark baseline)")
	}
}

// TestThemeGlamourStyle_DefaultTheme verifies that the Claret default theme's
// primary hex color is wired into the heading color, and that link/H1 follow
// the same accent (which in Claret maps to the brand primary).
func TestThemeGlamourStyle_DefaultTheme(t *testing.T) {
	t.Parallel()
	cfg := ThemeGlamourStyle(styles.DefaultTheme())

	const claretPrimary = "#c75b7a"
	if cfg.Heading.Color == nil || *cfg.Heading.Color != claretPrimary {
		t.Errorf("Heading.Color = %v, want %q", cfg.Heading.Color, claretPrimary)
	}
	if cfg.Link.Color == nil || *cfg.Link.Color != claretPrimary {
		t.Errorf("Link.Color = %v, want %q", cfg.Link.Color, claretPrimary)
	}
	if cfg.H1.BackgroundColor == nil || *cfg.H1.BackgroundColor != claretPrimary {
		t.Errorf("H1.BackgroundColor = %v, want %q", cfg.H1.BackgroundColor, claretPrimary)
	}
	if cfg.LinkText.Color == nil || *cfg.LinkText.Color != claretPrimary {
		t.Errorf("LinkText.Color = %v, want %q", cfg.LinkText.Color, claretPrimary)
	}
}

// TestThemeGlamourStyle_DistinctiveTheme verifies that a theme with a
// distinctive hex primary color overrides the heading color accordingly.
func TestThemeGlamourStyle_DistinctiveTheme(t *testing.T) {
	t.Parallel()
	const customPrimary = "#abcdef"
	th := &styles.Styles{
		Name: "custom",
		Colors: map[styles.Atom]color.Color{
			styles.AtomPrimary: lipgloss.Color(customPrimary),
		},
	}
	cfg := ThemeGlamourStyle(th)

	if cfg.Heading.Color == nil {
		t.Fatal("Heading.Color is nil")
	}
	if *cfg.Heading.Color != customPrimary {
		t.Errorf("Heading.Color = %q, want %q (custom override should win)", *cfg.Heading.Color, customPrimary)
	}
}

// TestThemeGlamourStyle_CodeFg verifies code fg is applied from AtomCodeFg.
func TestThemeGlamourStyle_CodeFg(t *testing.T) {
	t.Parallel()
	cfg := ThemeGlamourStyle(styles.DefaultTheme())
	// Claret AtomCodeFg = fgBase = #ddd3c7.
	if cfg.Code.Color == nil || *cfg.Code.Color != "#ddd3c7" {
		t.Errorf("Code.Color = %v, want %q", cfg.Code.Color, "#ddd3c7")
	}
}

// TestQuestionModal_markdown_UsesTheme verifies that the markdown helper
// on QuestionModal uses ThemeGlamourStyle to wire the configured theme's
// code-fg color through to the rendered output, rather than the hard-coded
// dark-baseline color (203 → ESC[38;5;203m).
func TestQuestionModal_markdown_UsesTheme(t *testing.T) {
	t.Parallel()
	m := QuestionModal{
		Width:  120,
		Height: 40,
		Theme:  styles.DefaultTheme(),
		Request: QuestionRequest{
			Question: "use `inline code` here",
			Options:  []QuestionOption{{ID: "1", Label: "yes"}},
		},
	}
	view := m.View()

	if strings.Contains(view, "38;5;203m") {
		t.Errorf("markdown output contains dark-baseline code color 203; theme override not applied:\n%s", view)
	}
	if !strings.Contains(view, "inline code") {
		t.Errorf("markdown output does not contain 'inline code':\n%s", view)
	}
}
