package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

func formatTokens(tok int64) string {
	if tok <= 0 {
		return ""
	}
	if tok >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(tok)/1_000_000)
	}
	if tok >= 1_000 {
		return fmt.Sprintf("%.0fK", float64(tok)/1_000)
	}
	return fmt.Sprintf("%d", tok)
}

func formatCost(dollars float64) string {
	if dollars <= 0 {
		return ""
	}
	return fmt.Sprintf("$%.2f", dollars)
}

// renderHeaderContent produces the branded header bar.
func (a *App) renderHeaderContent() string {
	if a.styles == nil {
		return ""
	}
	l := a.layout

	if l.compact {
		return components.CompactHeader{
			Width:   l.leftW,
			Styles:  a.styles,
			AppName: "hygge",
			Model:   displayModelName(a.opts.ModelName),
			Tokens:  formatTokens(a.usedTok),
			Cost:    formatCost(a.costDollars),
		}.View()
	}
	return components.Header{
		Width:   l.leftW,
		Styles:  a.styles,
		AppName: "hygge",
		Version: a.opts.Version,
	}.View()
}

// invalidateMsgCache marks the message content cache as stale so the next
// render rebuilds it. Call this whenever messages change (append, stream
// delta, resize, theme switch).
func (a *App) invalidateMsgCache() {
	a.msgCacheValid = false
}

// renderChatContent produces the string content for the chat viewport.
// Uses a cache to avoid re-rendering the full message list every frame —
// only scroll position changes between frames during mouse/keyboard scrolling.
func (a *App) renderChatContent() string {
	l := a.layout

	// Breadcrumb: moved to footer in subagent view, not shown at top.
	breadcrumb := ""
	if !a.viewingSubagent() {
		breadcrumb = components.Breadcrumb{
			Segments: a.breadcrumbSegments(),
			Width:    l.leftW,
			Theme:    a.opts.Theme,
		}.View()
	}

	// Select visible messages based on foreground stack.
	visibleMessages := a.messages
	foreID := a.foregroundID()
	rootID := a.rootSessionID()
	if foreID != rootID && foreID != "" {
		if st, ok := a.subagents[foreID]; ok {
			visibleMessages = st.Messages
		}
	}

	// Check if the cache is still valid. Invalidate every 30 seconds
	// so relative timestamps stay fresh.
	now := a.opts.Now()
	needsRebuild := !a.msgCacheValid ||
		a.msgCacheW != l.leftW ||
		a.msgCacheLen != len(visibleMessages) ||
		now.Sub(a.msgCacheTime) > 30*time.Second

	// Always rebuild when streaming (content changes intra-message).
	if !needsRebuild && len(visibleMessages) > 0 {
		last := visibleMessages[len(visibleMessages)-1]
		if last.IsStreaming {
			needsRebuild = true
		}
	}

	if needsRebuild {
		ml := components.MessageList{
			Width:           l.leftW,
			Theme:           a.opts.Theme,
			Styles:          a.styles,
			Messages:        visibleMessages,
			Subagents:       a.subagents,
			AnimFor:         a.subagentAnims,
			Now:             now,
			HoverSubagentID: a.hoverSubagentID,
			ExpandedTools:   a.expandedTools,
		}
		a.msgCache, a.subagentHitZones, a.toolHitZones = ml.ViewWithHitZones()
		a.msgCacheValid = true
		a.msgCacheW = l.leftW
		a.msgCacheLen = len(visibleMessages)
		a.msgCacheTime = now
	}

	// Update viewport dimensions.
	chatH := l.chat.Dy()
	if chatH < 1 {
		chatH = 1
	}
	a.msgViewport.SetWidth(l.leftW)
	a.msgViewport.SetHeight(chatH)

	// Only push content to the viewport when it changed — SetContent
	// parses the full string into lines which is expensive for large
	// conversations.
	if needsRebuild {
		a.msgViewport.SetContent(a.msgCache)
	}

	if !a.userScrolled {
		a.msgViewport.GotoBottom()
	}

	body := a.msgViewport.View()

	if breadcrumb != "" {
		return breadcrumb + "\n" + body
	}
	return body
}

// renderFooterContent produces the string content for the footer bar.
func (a *App) renderFooterContent() string {
	if a.viewingSubagent() {
		// Subagent view: show nav hints instead of mode/model info.
		return components.Breadcrumb{
			Segments: a.breadcrumbSegments(),
			Width:    a.layout.leftW,
			Theme:    a.opts.Theme,
		}.View()
	}

	agentType := a.ActiveModeName()

	return components.Footer{
		Width:          a.layout.leftW,
		Theme:          a.opts.Theme,
		Styles:         a.styles,
		AgentType:      agentType,
		ModelName:      displayModelName(a.opts.ModelName),
		Provider:       displayProviderName(a.opts.ModelProvider),
		ReasoningLevel: a.opts.Reasoning.Effort,
		ModeIndicator:  a.formatModeIndicator(),
		Busy:           a.busy,
		SpinnerView:    a.spinner.View(),
	}.View()
}

// renderSidebarContent produces the string content for the sidebar.
func (a *App) renderSidebarContent() string {
	return components.Sidebar{
		Width:        a.layout.sidebarW,
		Height:       a.layout.area.Dy(),
		SessionTitle: a.sidebarSessionTitle(),
		UsedTokens:   a.usedTok,
		MaxTokens:    a.maxTok,
		PctUsed:      a.pctUsed,
		CostUSD:      a.costDollars,
		MCPs:         a.opts.MCPStatuses,
		ProjectPath:  a.collapsedProjectPath(),
		GitBranch:    a.gitBranch(),
		AppName:      "Hygge",
		Version:      a.opts.Version,
		Theme:        a.opts.Theme,
		Styles:       a.styles,
		NerdFonts:    a.opts.NerdFonts,
		Todos:        a.todosCache,
	}.View()
}

// renderOverlayContent produces the string content for the active overlay.
func (a *App) renderOverlayContent(overlay overlayKind) string {
	w := a.width
	h := a.height

	switch overlay {
	case overlayPermission:
		return components.PermissionModal{
			Width:   w,
			Height:  h,
			Theme:   a.opts.Theme,
			Request: a.pendingPerms[0],
			Toast:   a.modalToast,
		}.View()
	case overlaySessions:
		a.sessionsModal.Width = w
		a.sessionsModal.Height = h
		a.sessionsModal.Now = a.opts.Now()
		return a.sessionsModal.View()
	case overlayCompactConfirm:
		a.compactionModal.Width = w
		a.compactionModal.Height = h
		return a.compactionModal.View()
	case overlayHelp:
		return a.renderHelpOverlay(w, h)
	case overlayModel:
		a.modelModal.Width = w
		a.modelModal.Height = h
		return a.modelModal.View()
	case overlayAPIKey:
		a.apiKeyModal.Width = w
		a.apiKeyModal.Height = h
		return a.apiKeyModal.View()
	case overlayTheme:
		a.themeModal.Width = w
		a.themeModal.Height = h
		return a.themeModal.View()
	case overlayQuit:
		return a.renderQuitOverlay(w, h)
	}
	return ""
}

// renderQuitOverlay renders a centered quit confirmation dialog with
// selectable Yes/No buttons.
func (a *App) renderQuitOverlay(w, h int) string {
	question := "Are you sure you want to quit?"

	// Button styles.
	var selectedBg, selectedFg, normalFg, boxBg, textFg color.Color
	selectedBg = lipgloss.Color("#C75B7A")
	selectedFg = lipgloss.Color("#180810")
	normalFg = lipgloss.Color("#71685E")
	textFg = lipgloss.Color("#DDD3C7")
	boxBg = lipgloss.Color("#2B1F22")
	if a.styles != nil {
		boxBg = a.styles.BubbleBg
	}

	selectedStyle := lipgloss.NewStyle().
		Bold(true).
		Padding(0, 3).
		Background(selectedBg).
		Foreground(selectedFg)
	normalStyle := lipgloss.NewStyle().
		Padding(0, 3).
		Foreground(normalFg).
		Background(boxBg)

	var yesBtn, noBtn string
	if a.quitSelectedNo {
		yesBtn = normalStyle.Render("Yep!")
		noBtn = selectedStyle.Render("Nope")
	} else {
		yesBtn = selectedStyle.Render("Yep!")
		noBtn = normalStyle.Render("Nope")
	}
	buttons := yesBtn + " " + noBtn

	boxStyle := lipgloss.NewStyle().
		Padding(1, 4).
		Background(boxBg)

	qStyle := lipgloss.NewStyle().Foreground(textFg)

	content := lipgloss.JoinVertical(lipgloss.Center, qStyle.Render(question), "", buttons)
	box := boxStyle.Render(content)

	boxW := lipgloss.Width(box)
	boxH := lipgloss.Height(box)

	padLeft := (w - boxW) / 2
	padTop := (h - boxH) / 2
	if padLeft < 0 {
		padLeft = 0
	}
	if padTop < 0 {
		padTop = 0
	}

	var lines []string
	for range padTop {
		lines = append(lines, "")
	}
	for _, line := range strings.Split(box, "\n") {
		lines = append(lines, strings.Repeat(" ", padLeft)+line)
	}
	return strings.Join(lines, "\n")
}

// renderChromeContent produces the "chrome" elements between chat and footer:
// status pills, command palette, banners, notices.
func (a *App) renderChromeContent() string {
	l := a.layout
	var sections []string

	// Status pills.
	statusPills := components.StatusPills{
		Width:         l.leftW,
		Theme:         a.opts.Theme,
		QueueCount:    a.queueCount,
		QueuedPrompts: a.queuedPrompts,
		TodoCount:     a.todoIncomplete,
		TodoRunning:   a.busy && a.todoInProgress > 0,
	}.View()
	if statusPills != "" {
		sections = append(sections, statusPills)
	}

	// Attachment chips.
	attachmentChips := a.renderAttachmentChips(l.leftW)
	if attachmentChips != "" {
		sections = append(sections, attachmentChips)
	}

	// Command palette.
	if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") && !a.slashPaletteDismissed {
		matches := a.paletteMatches()
		head, _ := splitSlash(a.input.Value())
		p := components.CommandPalette{
			Width:           l.leftW - 2,
			Theme:           a.opts.Theme,
			Matches:         matches,
			Highlight:       a.clampedPaletteHighlight(matches),
			QueryAfterSlash: head,
		}
		if v := p.View(); v != "" {
			sections = append(sections, v)
		}
	}

	// Compaction banner.
	bannerView := components.CompactionBanner{
		Width:   l.leftW,
		Theme:   a.opts.Theme,
		Visible: a.bannerVisible && !a.bannerDismissed,
		Pct:     a.bannerPct,
	}.View()
	if bannerView != "" {
		sections = append(sections, bannerView)
	}

	// Notices.
	if a.notice != "" {
		style := lipgloss.NewStyle()
		if a.opts.Theme != nil {
			style = a.opts.Theme.Style(theme.AtomMuted)
		}
		sections = append(sections, style.Render(a.notice))
	}
	if a.compactionInFlight {
		style := lipgloss.NewStyle()
		if a.opts.Theme != nil {
			style = a.opts.Theme.Style(theme.AtomMuted)
		}
		sections = append(sections, style.Render(fmt.Sprintf("⌛  Compacting %d messages…", a.compactionInFlightCount)))
	}
	// Scroll position indicator when user has scrolled up.
	if a.userScrolled && !a.msgViewport.AtBottom() {
		pct := int(a.msgViewport.ScrollPercent() * 100)
		style := lipgloss.NewStyle()
		if a.opts.Theme != nil {
			style = a.opts.Theme.Style(theme.AtomMuted)
		}
		sections = append(sections, style.Render(fmt.Sprintf("↑ scrolled — %d%%", pct)))
	}

	if a.compactionToast != "" {
		style := lipgloss.NewStyle()
		if a.opts.Theme != nil {
			style = a.opts.Theme.Style(theme.AtomMuted)
		}
		sections = append(sections, style.Render(a.compactionToast))
	}

	return strings.Join(sections, "\n")
}

// displayProviderName prettifies a canonical provider ID for display.
func displayProviderName(name string) string {
	switch strings.ToLower(name) {
	case "openai":
		return "OpenAI"
	case "openrouter":
		return "OpenRouter"
	case "anthropic":
		return "Anthropic"
	case "deepseek":
		return "DeepSeek"
	case "google", "gemini":
		return "Google"
	case "xai":
		return "xAI"
	case "groq":
		return "Groq"
	case "mistral":
		return "Mistral"
	default:
		return name
	}
}

// displayModelName prettifies a canonical model ID for display.
func displayModelName(name string) string {
	// GPT models: gpt-5.5 → GPT-5.5, gpt-5-mini → GPT-5-mini
	if strings.HasPrefix(strings.ToLower(name), "gpt-") {
		return "GPT-" + name[4:]
	}
	return name
}
