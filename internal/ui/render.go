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

// invalidateMsgCache marks the message content cache as stale so the next
// render rebuilds it. Call this whenever messages change (append, stream
// delta, resize, theme switch).
func (a *App) invalidateMsgCache() {
	a.msgCacheValid = false
	a.msgCacheStreamingDirty = false
}

func (a *App) invalidateMsgCacheForStreamingDelta() {
	if a.userScrolled && a.msgCacheValid {
		a.msgCacheStreamingDirty = true
		return
	}
	a.invalidateMsgCache()
}

// renderChatContent produces the string content for the chat viewport.
// Uses a cache to avoid re-rendering the full message list every frame —
// only scroll position changes between frames during mouse/keyboard scrolling.
func (a *App) renderChatContent() string {
	l := a.layout
	if a.splashActive() {
		return a.renderSplashContent()
	}

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
	if a.compactionInFlight && foreID == rootID {
		visibleMessages = append(append([]uiMessage(nil), visibleMessages...), a.compactionWorkingMessage())
	}

	// Check if the cache is still valid. Invalidate every 30 seconds
	// so relative timestamps stay fresh.
	now := a.opts.Now()
	needsRebuild := !a.msgCacheValid ||
		a.msgCacheW != l.leftW ||
		a.msgCacheLen != len(visibleMessages) ||
		(!a.userScrolled && a.msgCacheStreamingDirty) ||
		now.Sub(a.msgCacheTime) > 30*time.Second

	// Streaming deltas mutate existing messages and explicitly invalidate the
	// cache via handleBusEvent/append helpers. Do not rebuild solely because the
	// tail is streaming: during scroll frames that would re-render the entire
	// transcript even when no new bytes arrived.

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
		a.msgCacheStreamingDirty = false
		a.msgCacheW = l.leftW
		a.msgCacheLen = len(visibleMessages)
		a.msgCacheTime = now
	}

	// Update viewport dimensions.
	chatH := max(l.chat.Dy(), 1)
	a.msgViewport.SetWidth(l.leftW)
	a.msgViewport.SetHeight(chatH)

	// Only push content to the viewport when it changed — SetContent
	// parses the full string into lines which is expensive for large
	// conversations.
	if needsRebuild {
		a.msgViewport.SetContent("\n" + a.msgCache)
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
		WorkingVerb:    a.workingVerb,
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
		BilledTokens: a.billedTok,
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
	case overlayQuestion:
		return components.QuestionModal{
			Width:         w,
			Height:        h,
			Theme:         a.opts.Theme,
			Request:       a.pendingQuestions[0],
			SelectedIndex: a.questionSelectedIndex,
		}.View()
	case overlaySessions:
		a.sessionsModal.Width = w
		a.sessionsModal.Height = h
		a.sessionsModal.Now = a.opts.Now()
		return a.sessionsModal.View()
	case overlayMemory, overlayMemoryForget:
		a.memoryModal.Width = w
		a.memoryModal.Height = h
		return a.memoryModal.View()
	case overlayMemoryRemember:
		a.rememberScopeModal.Width = w
		a.rememberScopeModal.Height = h
		return a.rememberScopeModal.View()
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
		yesBtn = normalStyle.Render("yeah")
		noBtn = selectedStyle.Render("nah")
	} else {
		yesBtn = selectedStyle.Render("yeah")
		noBtn = normalStyle.Render("nah")
	}
	btnSep := lipgloss.NewStyle().Background(boxBg).Render(" ")
	buttonRow := yesBtn + btnSep + noBtn

	// Build content manually to avoid JoinVertical centering artifacts.
	// Ensure every line has the box background.
	qText := question
	qW := lipgloss.Width(qText)
	bW := lipgloss.Width(buttonRow)
	innerW := max(bW, qW)

	// Center the button row within the inner width.
	btnPad := max((innerW-bW)/2, 0)
	bgPad := lipgloss.NewStyle().Background(boxBg)
	centeredButtons := bgPad.Render(strings.Repeat(" ", btnPad)) + buttonRow + bgPad.Render(strings.Repeat(" ", innerW-bW-btnPad))

	// Center the question too.
	qPad := max((innerW-qW)/2, 0)
	qStyle := lipgloss.NewStyle().Foreground(textFg).Background(boxBg)
	centeredQ := bgPad.Render(strings.Repeat(" ", qPad)) + qStyle.Render(qText) + bgPad.Render(strings.Repeat(" ", innerW-qW-qPad))

	blankLine := bgPad.Render(strings.Repeat(" ", innerW))

	boxStyle := lipgloss.NewStyle().
		Padding(1, 4).
		Background(boxBg)

	content := centeredQ + "\n" + blankLine + "\n" + centeredButtons
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
	for line := range strings.SplitSeq(box, "\n") {
		lines = append(lines, strings.Repeat(" ", padLeft)+line)
	}
	return strings.Join(lines, "\n")
}

// renderChromeContent produces the "chrome" elements between chat and footer:
// status pills, banners, notices. Completion palettes are drawn separately as
// overlays so they don't consume layout height or push the editor down.
func (a *App) renderChromeContent() string {
	l := a.layout
	var sections []string

	// Status pills.
	statusPills := components.StatusPills{
		Width:          l.leftW,
		Theme:          a.opts.Theme,
		QueueCount:     a.queueCount,
		QueuedPrompts:  a.queuedPrompts,
		QueuedEditable: len(a.queuedDrafts) > 0,
	}.View()
	if statusPills != "" {
		sections = append(sections, statusPills)
	}

	// Attachment chips.
	attachmentChips := a.renderAttachmentChips(l.leftW)
	if attachmentChips != "" {
		sections = append(sections, attachmentChips)
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
	if a.compactionToast != "" {
		style := lipgloss.NewStyle()
		if a.opts.Theme != nil {
			style = a.opts.Theme.Style(theme.AtomMuted)
		}
		sections = append(sections, style.Render(a.compactionToast))
	}

	return strings.Join(sections, "\n")
}

func (a *App) compactionWorkingMessage() uiMessage {
	frame := "▰▰▰▱▱▱"
	if a.compactionAnim != nil {
		frame = a.compactionAnim.Render()
	}
	return uiMessage{
		Role:              components.RoleMarker,
		IsStreaming:       true,
		Raw:               frame,
		MarkerSummary:     compactionWorkingSummary(a.compactionInFlightCount),
		MarkerTokensSaved: 0,
	}
}

func compactionWorkingSummary(count int) string {
	if count == 1 {
		return "Crunching 1 message into a compact context summary…"
	}
	if count <= 0 {
		return "Crunching conversation history into a compact context summary…"
	}
	return fmt.Sprintf("Crunching %d messages into a compact context summary…", count)
}

// renderCompletionPalette produces the active slash-command or @-mention
// palette. The draw pass paints this as a floating overlay anchored above the
// editor rather than inserting it into the chrome flow.
func (a *App) renderCompletionPalette() string {
	l := a.layout

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
			return v
		}
	}

	// @ mention palette for repository files and configured sub-agents.
	if _, _, ok := a.activeMentionQuery(); ok && !a.mentionDismissed {
		matches := a.mentionMatches()
		query, _, _ := a.activeMentionQuery()
		p := components.MentionPalette{
			Width:     l.leftW - 2,
			Theme:     a.opts.Theme,
			Matches:   mentionItems(matches),
			Highlight: a.clampedMentionHighlight(matches),
			Query:     query,
		}
		if v := p.View(); v != "" {
			return v
		}
	}

	return ""
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
