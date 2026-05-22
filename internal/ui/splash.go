package ui

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
)

// Splash layout sizing.
//
// The fog banner expands to fill the chat region minus the space reserved for
// the input + tip stack plus a small breathing margin. A floor keeps the fog
// visually substantial on short windows.
const (
	splashFogMinHeight  = 14
	splashFogVMargin    = 3 // rows reserved above input + below tip
	splashTipHeight     = 1
	splashInputReserved = 5 // input box (~3 rows + 2 border)
)

func (a *App) splashActive() bool {
	return !a.viewingSubagent() &&
		!a.busy &&
		!a.compactionInFlight &&
		a.compactionToast == "" &&
		len(a.pendingAttachments) == 0 &&
		len(a.messages) == 0 &&
		a.queueCount == 0 &&
		len(a.queuedDrafts) == 0
}

func (a *App) renderSplashContent() string {
	w := max(a.layout.leftW, 1)
	h := max(a.layout.chat.Dy(), 1)

	inputW := splashInputWidth(w)
	a.input.SetWidth(inputW)
	a.input.BorderColor = a.activeModeColor()
	a.input.PasteMarkerStyle = a.pasteInputMarkerStyle()
	a.input.VerticalPadding = 1
	placeholder := a.input.Textarea.Placeholder
	a.input.Textarea.Placeholder = `Ask anything... "Fix broken tests"`
	input := a.input.View()
	a.input.Textarea.Placeholder = placeholder

	fogW := max(1, w)
	available := h - splashInputReserved - splashTipHeight - splashFogVMargin
	// Hold the fog to roughly 60% of the vertical room below the chrome, with
	// a sensible floor so it stays visually substantial on short windows.
	fogH := max(splashFogMinHeight, available*3/5)
	fog := a.renderSplashFog(fogW, fogH)
	tip := a.splashTipStyle().Render("• Tip") + a.splashMutedStyle().Render("  Ctrl+E opens this prompt in your external editor")

	inputBlock := centerBlock(w, strings.Join([]string{input, tip}, "\n"))
	content := strings.Join([]string{fog, inputBlock}, "\n")
	padTop := max((h-lipgloss.Height(content))/2, 0)
	content = strings.Repeat("\n", padTop) + content
	padBottom := max(h-lipgloss.Height(content), 0)
	return content + strings.Repeat("\n", padBottom)
}

func splashInputWidth(width int) int {
	if width < 1 {
		return 1
	}
	return min(max(width*3/5, 56), min(width-6, 88))
}

// renderSplashFog renders the animated fog banner with the lowercase "hygge"
// wordmark overlaid inside the cloud body (inset from the right edge so it
// sits among visible glyphs rather than drifting into the vignetted corner).
func (a *App) renderSplashFog(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	if a.fogStart.IsZero() {
		a.fogStart = time.Now()
	}
	t := time.Since(a.fogStart).Seconds()
	accent := resolveAccentRGB(a.styles, a.opts.Theme)
	return renderFogBanner(width, height, t, accent, "hygge")
}

func (a *App) splashMutedStyle() lipgloss.Style {
	if a.opts.Theme != nil {
		return a.opts.Theme.Style(styles.AtomMuted)
	}
	return lipgloss.NewStyle().Faint(true)
}

func (a *App) splashTipStyle() lipgloss.Style {
	if a.opts.Theme != nil {
		return a.opts.Theme.Style(styles.AtomWarn).Bold(true)
	}
	return lipgloss.NewStyle().Bold(true)
}

func centerBlock(width int, content string) string {
	if width < 1 {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		pad := max((width-lipgloss.Width(line))/2, 0)
		lines[i] = strings.Repeat(" ", pad) + line
	}
	return strings.Join(lines, "\n")
}
