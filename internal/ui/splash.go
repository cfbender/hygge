package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

const splashFrameSlowdown = 8

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

func (a *App) splashFrame() int {
	return a.spinnerTick / splashFrameSlowdown
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

	smoke := a.renderSplashSmoke()
	logo := a.renderSplashLogo()
	tip := a.splashTipStyle().Render("• Tip") + a.splashMutedStyle().Render("  Ctrl+E opens this prompt in your external editor")

	content := strings.Join([]string{smoke, logo, input, tip}, "\n")
	content = centerBlock(w, content)
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

func (a *App) renderSplashLogo() string {
	frames := []string{
		"██╗  ██╗██╗   ██╗ ██████╗  ██████╗ ███████╗\n██║  ██║╚██╗ ██╔╝██╔════╝ ██╔════╝ ██╔════╝\n███████║ ╚████╔╝ ██║  ███╗██║  ███╗█████╗  \n██╔══██║  ╚██╔╝  ██║   ██║██║   ██║██╔══╝  \n██║  ██║   ██║   ╚██████╔╝╚██████╔╝███████╗\n╚═╝  ╚═╝   ╚═╝    ╚═════╝  ╚═════╝ ╚══════╝",
		"▓▓╗  ▓▓╗▓▓╗   ▓▓╗ ▓▓▓▓▓▓╗  ▓▓▓▓▓▓╗ ▓▓▓▓▓▓▓╗\n▓▓║  ▓▓║╚▓▓╗ ▓▓╔╝▓▓╔════╝ ▓▓╔════╝ ▓▓╔════╝\n▓▓▓▓▓▓▓║ ╚▓▓▓▓╔╝ ▓▓║  ▓▓▓╗▓▓║  ▓▓▓╗▓▓▓▓▓╗  \n▓▓╔══▓▓║  ╚▓▓╔╝  ▓▓║   ▓▓║▓▓║   ▓▓║▓▓╔══╝  \n▓▓║  ▓▓║   ▓▓║   ╚▓▓▓▓▓▓╔╝╚▓▓▓▓▓▓╔╝▓▓▓▓▓▓▓╗\n╚═╝  ╚═╝   ╚═╝    ╚═════╝  ╚═════╝ ╚══════╝",
		"▒▒╗  ▒▒╗▒▒╗   ▒▒╗ ▒▒▒▒▒▒╗  ▒▒▒▒▒▒╗ ▒▒▒▒▒▒▒╗\n▒▒║  ▒▒║╚▒▒╗ ▒▒╔╝▒▒╔════╝ ▒▒╔════╝ ▒▒╔════╝\n▒▒▒▒▒▒▒║ ╚▒▒▒▒╔╝ ▒▒║  ▒▒▒╗▒▒║  ▒▒▒╗▒▒▒▒▒╗  \n▒▒╔══▒▒║  ╚▒▒╔╝  ▒▒║   ▒▒║▒▒║   ▒▒║▒▒╔══╝  \n▒▒║  ▒▒║   ▒▒║   ╚▒▒▒▒▒▒╔╝╚▒▒▒▒▒▒╔╝▒▒▒▒▒▒▒╗\n╚═╝  ╚═╝   ╚═╝    ╚═════╝  ╚═════╝ ╚══════╝",
	}
	frame := frames[a.splashFrame()%len(frames)]
	return a.splashLogoStyle().Render(frame)
}

func (a *App) renderSplashSmoke() string {
	frames := []string{
		"      (  )       \n    (    )       \n      ┌─┐        ",
		"    (    )       \n       (  )      \n      ┌─┐        ",
		"       (  )      \n     (    )      \n      ┌─┐        ",
		"     (    )      \n        (  )     \n      ┌─┐        ",
	}
	style := a.splashMutedStyle()
	if a.opts.Theme != nil {
		style = a.opts.Theme.Style(theme.AtomMuted).Italic(true)
	}
	return style.Render(frames[a.splashFrame()%len(frames)])
}

func (a *App) splashLogoStyle() lipgloss.Style {
	if a.opts.Theme != nil {
		return a.opts.Theme.Style(theme.AtomAccent).Bold(true)
	}
	return lipgloss.NewStyle().Bold(true)
}

func (a *App) splashMutedStyle() lipgloss.Style {
	if a.opts.Theme != nil {
		return a.opts.Theme.Style(theme.AtomMuted)
	}
	return lipgloss.NewStyle().Faint(true)
}

func (a *App) splashTipStyle() lipgloss.Style {
	if a.opts.Theme != nil {
		return a.opts.Theme.Style(theme.AtomWarn).Bold(true)
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
