package ui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
)

func (a *App) workingVerbTick() tea.Cmd {
	return tea.Tick(15*time.Second, func(time.Time) tea.Msg {
		return workingVerbTickMsg{}
	})
}

// handleKey dispatches a key.  When the modal is open, only the modal
// keybinds work; everything else is dropped.
func (a *App) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if top, ok := a.overlays.Top(); ok {
		switch top {
		case overlayPermission:
			return a.handleModalKey(k)
		case overlayQuestion:
			return a.handleQuestionModalKey(k)
		case overlaySessions:
			return a.handleSessionsModalKey(k)
		case overlayMemory, overlayMemoryForget:
			return a.handleMemoryModalKey(k)
		case overlayMemoryRemember:
			return a.handleRememberScopeModalKey(k)
		case overlayCompactConfirm:
			return a.handleCompactionModalKey(k)
		case overlayHelp:
			if k.String() == "esc" || k.String() == "q" {
				a.closeOverlay(overlayHelp)
			}
			return a, nil
		case overlayModel:
			return a.handleModelModalKey(k)
		case overlayAPIKey:
			return a.handleAPIKeyModalKey(k)
		case overlayTheme:
			return a.handleThemeModalKey(k)
		case overlayOnboarding:
			return a.handleOnboardingKey(k)
		case overlayQuit:
			switch k.String() {
			case "y", "Y", "ctrl+c":
				return a, tea.Quit
			case "n", "N", "esc":
				a.closeOverlay(overlayQuit)
				return a, nil
			case "left", "right", "tab", "h", "l":
				a.quitSelectedNo = !a.quitSelectedNo
				return a, nil
			case "enter", " ":
				if !a.quitSelectedNo {
					return a, tea.Quit
				}
				a.closeOverlay(overlayQuit)
				return a, nil
			default:
				return a, nil
			}
		}
	}
	if k.Code == tea.KeyEnter && (k.Mod.Contains(tea.ModShift) || k.Mod.Contains(tea.ModAlt)) {
		if a.viewingSubagent() {
			return a, nil
		}
		return a.insertInputNewline()
	}

	switch k.String() {
	case "ctrl+c":
		if a.busy && a.inflightCancel != nil {
			a.inflightCancel()
			return a, nil
		}
		if a.overlays.Has(overlayQuit) {
			return a, tea.Quit
		}
		a.quitSelectedNo = true // default to "No" (safe choice)
		a.openOverlay(overlayQuit)
		return a, nil
	case "ctrl+l":
		a.input.Reset()
		a.pastedInputBlocks = nil
		a.slashPaletteDismissed = false
		a.mentionDismissed = false
		a.cancelQueuedDraftEdit()
		return a, nil
	case "ctrl+x":
		// Dismiss the compaction threshold-suggestion banner for this crossing.
		if a.bannerVisible && !a.bannerDismissed {
			a.bannerDismissed = true
			return a, nil
		}
	case "ctrl+t":
		// Cycle reasoning level: off → low → medium → high → off.
		levels := []string{"", "low", "medium", "high"}
		cur := a.opts.Reasoning.Effort
		idx := 0
		for i, l := range levels {
			if l == cur {
				idx = i
				break
			}
		}
		next := levels[(idx+1)%len(levels)]
		a.opts.Reasoning.Effort = next
		label := next
		if label == "" {
			label = "off"
		}
		a.invalidateMsgCache()
		return a, a.showToast("Reasoning", label)
	case "ctrl+g":
		return a, a.followIntoLatestSubagent()
	case "ctrl+e":
		if a.viewingSubagent() {
			return a, nil
		}
		return a, a.openPromptEditorCmd()
	case "enter":
		if a.viewingSubagent() {
			return a, nil
		}
		if a.acceptMentionCompletion() {
			return a, nil
		}
		// Alt+Enter inserts a newline; route by key code/modifier so
		// terminal-specific string rendering cannot accidentally submit it.
		if k.Mod.Contains(tea.ModAlt) {
			return a.insertInputNewline()
		}
		rawText := a.input.Value()
		displayText := strings.TrimSpace(rawText)
		if displayText == "" {
			return a, nil
		}
		if strings.HasPrefix(displayText, "/") {
			if slashPrefixOnly(displayText) {
				name, _ := splitSlash(displayText)
				exact := false
				if a.opts.Commands != nil {
					_, exact = a.opts.Commands.Get(name)
				}
				if !exact && a.acceptPaletteCompletion() {
					return a, nil
				}
			}
			a.input.Reset()
			a.pastedInputBlocks = nil
			a.slashPaletteDismissed = false
			return a, a.runSlashCommand(displayText)
		}
		pastedAttachments := a.pastedInputAttachments(rawText)
		text := a.expandPastedInputText(rawText)
		displayText = a.displayPastedInputText(rawText)
		mentionAttachments, err := a.promptAttachmentsForMentions(text)
		if err != nil {
			return a, a.setNotice("mention: " + err.Error())
		}
		a.history.Add(displayText)
		attachments := a.drainPromptAttachments(appendUniquePromptAttachments(pastedAttachments, mentionAttachments...))
		a.input.Reset()
		a.pastedInputBlocks = nil
		a.slashPaletteDismissed = false
		a.mentionDismissed = false
		// Resume auto-scroll when the user sends a message.
		a.userScrolled = false
		return a, a.startSendWithAttachments(displayText, attachments)
	case "pgup":
		// Scroll message list up one page; pause auto-scroll.
		if !a.msgViewport.AtTop() {
			a.msgViewport.PageUp()
			a.userScrolled = true
		}
		return a, nil
	case "pgdown":
		// Scroll message list down one page.
		a.msgViewport.PageDown()
		if a.msgViewport.AtBottom() {
			a.userScrolled = false
		}
		return a, nil
	case "tab":
		// Tab completes active palettes before falling back to mode cycling.
		if a.acceptMentionCompletion() {
			return a, nil
		}
		if a.acceptPaletteCompletion() {
			return a, nil
		}
		if cmd := a.cycleMode(); cmd != nil {
			return a, cmd
		}
	case "esc":
		// Subagent view: Esc pops the foreground stack.
		if len(a.foregroundStack) > 1 {
			a.popForeground()
			return a, nil
		}
		// Dismiss command palette first.
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") && !a.slashPaletteDismissed {
			a.paletteHighlight = -1
			a.slashPaletteDismissed = true
			return a, nil
		}
		if _, _, ok := a.activeMentionQuery(); ok && !a.mentionDismissed {
			a.mentionHighlight = -1
			a.mentionDismissed = true
			return a, nil
		}
		// Double-Esc within 500ms: interrupt everything.
		now := a.opts.Now()
		if a.busy && now.Sub(a.lastEscAt) < 500*time.Millisecond {
			// Cancel the active run.
			if a.inflightCancel != nil {
				a.inflightCancel()
			}
			// Clear the queue.
			a.queuedDrafts = nil
			a.cancelQueuedDraftEdit()
			a.syncQueuedDraftDisplay()
			rootID := a.rootSessionID()
			if a.testAgentClearQueueFn != nil {
				a.testAgentClearQueueFn(rootID)
			} else if a.opts.Agent != nil {
				a.opts.Agent.ClearQueue(rootID)
			}
			a.lastEscAt = time.Time{}
			return a, a.showToast("Interrupted", "Stopped current turn")
		}
		a.lastEscAt = now
		if !a.busy {
			return a, nil
		}
		// First Esc while busy: clear the queue if any.
		if len(a.queuedDrafts) > 0 {
			dropped := len(a.queuedDrafts)
			a.queuedDrafts = nil
			a.cancelQueuedDraftEdit()
			a.syncQueuedDraftDisplay()
			return a, a.setNotice(fmt.Sprintf("cleared %d queued message(s) — press Esc again to interrupt", dropped))
		}
		if a.queueCount > 0 {
			rootID := a.rootSessionID()
			var dropped int
			if a.testAgentClearQueueFn != nil {
				dropped = a.testAgentClearQueueFn(rootID)
			} else if a.opts.Agent != nil {
				dropped = a.opts.Agent.ClearQueue(rootID)
			}
			if dropped > 0 {
				return a, a.setNotice(fmt.Sprintf("cleared %d queued message(s) — press Esc again to interrupt", dropped))
			}
		}
		return a, a.setNotice("press Esc again to interrupt")
	case "up":
		if a.viewingSubagent() {
			a.navigateSubagent(-1) // older subagent
			return a, nil
		}
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(-1)
			return a, nil
		}
		if a.moveMentionHighlight(-1) {
			return a, nil
		}
		if text, ok := a.history.Up(a.input.Value()); ok {
			a.input.Textarea.SetValue(text)
			a.input.Textarea.CursorEnd()
			return a, nil
		}
		return a, nil
	case "ctrl+p":
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(-1)
			return a, nil
		}
		if a.moveMentionHighlight(-1) {
			return a, nil
		}
	case "down":
		if a.viewingSubagent() {
			a.navigateSubagent(+1) // newer subagent
			return a, nil
		}
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(+1)
			return a, nil
		}
		if a.moveMentionHighlight(+1) {
			return a, nil
		}
		if a.history.Browsing() {
			if text, ok := a.history.Down(); ok {
				a.input.Textarea.SetValue(text)
				a.input.Textarea.CursorEnd()
				return a, nil
			}
			return a, nil
		}
	case "ctrl+n":
		if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") {
			a.movePaletteHighlight(+1)
			return a, nil
		}
		if a.moveMentionHighlight(+1) {
			return a, nil
		}
	}

	return a.updateInputKey(k)
}

func (a *App) handleCompletionWheel(m tea.MouseWheelMsg) bool {
	mouse := tea.Mouse(m)
	delta := 0
	switch mouse.Button {
	case tea.MouseWheelDown:
		delta = 1
	case tea.MouseWheelUp:
		delta = -1
	default:
		return false
	}
	if a.opts.Commands != nil && strings.HasPrefix(a.input.Value(), "/") && !a.slashPaletteDismissed {
		a.movePaletteHighlight(delta)
		return true
	}
	if _, _, ok := a.activeMentionQuery(); ok && !a.mentionDismissed {
		a.moveMentionHighlight(delta)
		return true
	}
	return false
}

func (a *App) updateInputKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if a.handleAtomicPasteEdit(k) {
		return a, nil
	}
	var cmd tea.Cmd
	before := a.input.Value()
	a.input.Textarea, cmd = a.input.Textarea.Update(k)
	if a.input.Value() != before {
		a.history.Reset()
		a.paletteHighlight = -1
		a.slashPaletteDismissed = false
		a.mentionHighlight = -1
		a.mentionDismissed = false
	}
	a.keepCursorOutsidePastedMarkers(k)
	return a, cmd
}

func (a *App) insertInputNewline() (tea.Model, tea.Cmd) {
	before := a.input.Value()
	a.input.Textarea.InsertString("\n")
	if a.input.Value() != before {
		a.history.Reset()
		a.paletteHighlight = -1
		a.slashPaletteDismissed = false
		a.mentionHighlight = -1
		a.mentionDismissed = false
	}
	return a, nil
}

// handleModalKey routes keys to the permission modal.
func (a *App) handleModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(a.pendingPerms) == 0 {
		return a, nil
	}
	current := a.pendingPerms[0]
	reply := func(decision, scope string) tea.Cmd {
		return func() tea.Msg {
			bus.Publish(a.opts.Bus, bus.PermissionReplied{
				RequestID: current.RequestID,
				Decision:  decision,
				Scope:     scope,
				At:        a.opts.Now(),
			})
			return nil
		}
	}

	switch k.String() {
	case "esc":
		a.pendingPerms = a.pendingPerms[1:]
		a.modalToast = ""
		a.syncPermissionOverlay()
		a.updateInputFocus()
		return a, reply("deny", "once")
	default:
		if len(k.Text) != 1 {
			return a, nil
		}
		switch rune(k.Text[0]) {
		case 'y':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("allow", "once")
		case 'Y':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("allow", "session")
		case 'A':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("allow", "always")
		case 'n', 'N':
			a.pendingPerms = a.pendingPerms[1:]
			a.modalToast = ""
			a.syncPermissionOverlay()
			a.updateInputFocus()
			return a, reply("deny", "once")
		case 'e', 'E':
			a.modalToast = "edit not yet implemented (v0.2)"
			return a, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return clearToastMsg{} })
		}
	}
	return a, nil
}

func (a *App) handleQuestionModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if len(a.pendingQuestions) == 0 {
		return a, nil
	}
	current := a.pendingQuestions[0]
	reply := func(answerID, answer string, canceled bool) tea.Cmd {
		return func() tea.Msg {
			bus.Publish(a.opts.Bus, bus.QuestionAnswered{
				RequestID: current.RequestID,
				AnswerID:  answerID,
				Answer:    answer,
				Canceled:  canceled,
				At:        a.opts.Now(),
			})
			return nil
		}
	}
	dismiss := func() {
		a.pendingQuestions = a.pendingQuestions[1:]
		a.questionSelectedIndex = 0
		a.syncQuestionOverlay()
		a.updateInputFocus()
	}
	selectOption := func(idx int) (tea.Model, tea.Cmd) {
		if idx < 0 || idx >= len(current.Options) {
			return a, nil
		}
		opt := current.Options[idx]
		dismiss()
		return a, reply(opt.ID, opt.Label, false)
	}

	switch k.String() {
	case "esc":
		dismiss()
		return a, reply("", "", true)
	case "up", "k", "ctrl+p":
		a.moveQuestionSelection(-1)
		return a, nil
	case "down", "j", "ctrl+n":
		a.moveQuestionSelection(1)
		return a, nil
	case "enter":
		return selectOption(a.questionSelectedIndex)
	}
	if k.Code == tea.KeySpace {
		return selectOption(a.questionSelectedIndex)
	}
	if len(k.Text) != 1 {
		return a, nil
	}
	ch := k.Text[0]
	if ch < '1' || ch > '9' {
		return a, nil
	}
	idx := int(ch - '1')
	if idx < 0 || idx >= len(current.Options) {
		return a, nil
	}
	return selectOption(idx)
}

func (a *App) moveQuestionSelection(delta int) {
	if len(a.pendingQuestions) == 0 || len(a.pendingQuestions[0].Options) == 0 {
		a.questionSelectedIndex = 0
		return
	}
	n := len(a.pendingQuestions[0].Options)
	a.questionSelectedIndex = (a.questionSelectedIndex + delta + n) % n
}
