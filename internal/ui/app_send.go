package ui

import (
	"context"
	"fmt"
	"log/slog"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// startSend launches a goroutine that calls Agent.Send and returns a tea.Cmd
// that immediately emits sendStarted.  sendCompleted (or sendFailed via
// sendOutOfBand) arrives later, once the goroutine finishes.
//
// The goroutine is the single concurrency boundary for a user turn: it runs
// ensureSession + Agent.Send outside the bubbletea event loop so the UI
// remains responsive while the agent is working.  sendOutOfBand(sendCompleted)
// re-enters the event loop when the turn finishes.
//
// In tests that do not wire a *tea.Program, sendOutOfBand is a no-op; tests
// drive sendCompleted manually via app.Update(sendCompleted{...}).
func (a *App) startSend(text string, mentionAttachments ...promptAttachment) tea.Cmd {
	return a.startSendWithAttachments(text, a.drainPromptAttachments(mentionAttachments))
}

func (a *App) drainPromptAttachments(mentionAttachments []promptAttachment) []promptAttachment {
	attachments := append([]promptAttachment(nil), a.pendingAttachments...)
	attachments = appendUniquePromptAttachments(attachments, mentionAttachments...)
	a.pendingAttachments = nil
	return attachments
}

func (a *App) startSendWithAttachments(text string, attachments []promptAttachment) tea.Cmd {
	if a.opts.Agent == nil && a.testAgentSendFn == nil {
		// No agent wired up — useful for tests that just want to verify
		// input handling.  Just emit sendStarted so the busy state flips.
		return func() tea.Msg {
			return sendStarted{UserInput: text, StartedAt: a.opts.Now()}
		}
	}
	// Optimistically render only the active turn. When the app is already busy,
	// Agent.Send will enqueue this prompt; queued prompts should remain in the
	// sticky chrome until their persisted MessageAppended event arrives.
	if !a.busy {
		userMsg := uiMessage{
			Role:      components.RoleUser,
			Raw:       text,
			Timestamp: a.opts.Now(),
		}
		if text != "" {
			userMsg.FinalMarkdown = renderMarkdown(a.ensureRenderer(), text)
		}
		a.messages = append(a.messages, userMsg)
		a.lastAssistantFlushIdx = -1
		a.optimisticUserPending = true
	}

	ctx, cancel := context.WithCancel(a.ctx)
	// Only the active turn's cancel belongs in inflightCancel. When the app is
	// already busy, this send will be queued at the agent level (Agent.Send
	// returns immediately) and its context cannot stop the running turn.
	// Overwriting inflightCancel here would orphan the active turn's cancel and
	// break double-Esc / Ctrl+C interruption.
	if !a.busy {
		a.inflightCancel = cancel
	}

	// Resolve which send function to call: real agent or test stub.
	sendFn := func(ctx context.Context, sid string, parts []session.Part) (*session.Message, error) {
		return a.opts.Agent.Send(ctx, sid, parts)
	}
	if a.testAgentSendFn != nil {
		sendFn = a.testAgentSendFn
	}

	startedAt := a.opts.Now()
	go func() {
		defer cancel()
		sid, err := a.ensureSession(ctx)
		if err != nil {
			a.sendOutOfBand(sendCompleted{Err: err})
			return
		}
		msg, err := sendFn(ctx, sid, attachmentParts(text, attachments))
		a.sendOutOfBand(sendCompleted{Result: msg, Err: err})
	}()

	return func() tea.Msg {
		return sendStarted{UserInput: text, StartedAt: startedAt}
	}
}

func (a *App) enqueuePromptDraft(draft queuedPromptDraft) {
	if a.queuedDraftEditing {
		idx := min(max(a.queuedDraftEditIdx, 0), len(a.queuedDrafts))
		a.queuedDrafts = append(a.queuedDrafts, queuedPromptDraft{})
		copy(a.queuedDrafts[idx+1:], a.queuedDrafts[idx:])
		a.queuedDrafts[idx] = draft
		a.cancelQueuedDraftEdit()
		a.syncQueuedDraftDisplay()
		return
	}
	a.queuedDrafts = append(a.queuedDrafts, draft)
	a.syncQueuedDraftDisplay()
}

func (a *App) cancelQueuedDraftEdit() {
	a.queuedDraftEditing = false
	a.queuedDraftEditIdx = 0
}

func (a *App) syncQueuedDraftDisplay() {
	if len(a.queuedDrafts) == 0 {
		a.queueCount = 0
		a.queuedPrompts = nil
		return
	}
	a.queueCount = len(a.queuedDrafts)
	a.queuedPrompts = make([]string, 0, len(a.queuedDrafts))
	for _, draft := range a.queuedDrafts {
		a.queuedPrompts = append(a.queuedPrompts, draft.Text)
	}
	if a.busy {
		a.input.SetBusy(true, fmt.Sprintf(" (%d queued)", a.queueCount))
	}
}

func (a *App) flushQueuedDraftsCmd() tea.Cmd {
	if len(a.queuedDrafts) == 0 {
		return nil
	}
	draft := a.queuedDrafts[0]
	a.queuedDrafts = a.queuedDrafts[1:]
	a.cancelQueuedDraftEdit()
	a.syncQueuedDraftDisplay()
	if a.opts.Agent == nil && a.testAgentSendFn == nil {
		return nil
	}

	ctx, cancel := context.WithCancel(a.ctx)
	a.inflightCancel = cancel
	sendFn := func(ctx context.Context, sid string, parts []session.Part) (*session.Message, error) {
		return a.opts.Agent.Send(ctx, sid, parts)
	}
	if a.testAgentSendFn != nil {
		sendFn = a.testAgentSendFn
	}
	startedAt := a.opts.Now()
	go func() {
		defer cancel()
		sid, err := a.ensureSession(ctx)
		if err != nil {
			a.sendOutOfBand(sendCompleted{Err: err})
			return
		}
		msg, err := sendFn(ctx, sid, attachmentParts(draft.Text, draft.Attachments))
		if err != nil {
			a.sendOutOfBand(sendCompleted{Err: err})
			return
		}
		a.sendOutOfBand(sendCompleted{Result: msg})
	}()
	return func() tea.Msg {
		return sendStarted{UserInput: draft.Text, StartedAt: startedAt}
	}
}

func (a *App) appendPersistedUserMessage(messageID string) {
	if a.opts.Store == nil || messageID == "" {
		a.optimisticUserPending = false
		return
	}
	msg, err := a.opts.Store.GetMessage(a.ctx, messageID)
	if err != nil {
		slog.Warn("ui: appendPersistedUserMessage: failed to load message", "message_id", messageID, "err", err)
		a.optimisticUserPending = false
		return
	}
	entries := uiEntriesFromStoreMessage(msg, map[string]session.Part{}, map[string]struct{}{})
	if len(entries) == 0 {
		a.optimisticUserPending = false
		return
	}
	entry := entries[0]
	entry.MessageID = messageID
	if entry.Raw != "" {
		entry.FinalMarkdown = renderMarkdown(a.ensureRenderer(), entry.Raw)
	}
	// Guard: if this messageID is already present in the buffer (e.g. from a
	// hydrated resume session or a duplicate MessageAppended event), replace
	// the existing entry in-place rather than appending a duplicate.
	for i := range a.messages {
		if a.messages[i].MessageID == messageID && a.messages[i].Role == components.RoleUser {
			a.messages[i] = entry
			a.optimisticUserPending = false
			a.invalidateMsgCache()
			return
		}
	}
	if a.optimisticUserPending && len(a.messages) > 0 && a.messages[len(a.messages)-1].Role == components.RoleUser {
		a.messages[len(a.messages)-1] = entry
	} else {
		a.messages = append(a.messages, entry)
	}
	a.optimisticUserPending = false
	a.invalidateMsgCache()
}

func (a *App) queuedDraftAtScreen(screenX, screenY int) int {
	if len(a.queuedDrafts) == 0 || screenX < 0 || screenX >= a.layout.leftW {
		return -1
	}
	chromeY := headerHeight + a.layout.chat.Dy() + chatBottomPadding
	firstPromptY := chromeY + 1 // row 0 is the queue/status pill itself.
	idx := screenY - firstPromptY
	if idx < 0 || idx >= len(a.queuedDrafts) || idx >= queuedDraftHitRows {
		return -1
	}
	return idx
}

func (a *App) editQueuedDraft(index int) {
	if index < 0 || index >= len(a.queuedDrafts) {
		return
	}
	draft := a.queuedDrafts[index]
	a.queuedDrafts = append(a.queuedDrafts[:index], a.queuedDrafts[index+1:]...)
	a.queuedDraftEditing = true
	a.queuedDraftEditIdx = index
	a.syncQueuedDraftDisplay()
	a.pendingAttachments = append([]promptAttachment(nil), draft.Attachments...)
	a.setInputValueAndCursor(draft.Text, len([]rune(draft.Text)))
	a.history.Reset()
	a.paletteHighlight = -1
	a.slashPaletteDismissed = false
	a.mentionHighlight = -1
	a.mentionDismissed = false
}

func (a *App) renderAttachmentChips(width int) string {
	if len(a.pendingAttachments) == 0 {
		return ""
	}
	style := lipgloss.NewStyle().Padding(0, 1)
	if a.opts.Theme != nil {
		style = a.opts.Theme.Style(styles.AtomMuted).Padding(0, 1)
	}
	chips := make([]string, 0, len(a.pendingAttachments)+1)
	for _, att := range a.pendingAttachments {
		label := fmt.Sprintf("📎 %s %s", att.Name, formatBytes(att.Size))
		chips = append(chips, style.Render(label))
	}
	chips = append(chips, style.Render("/attachments clear to remove"))
	out := lipgloss.JoinHorizontal(lipgloss.Left, chips...)
	if width > 0 && lipgloss.Width(out) > width {
		return lipgloss.NewStyle().MaxWidth(width).Render(out)
	}
	return out
}

// ensureSession returns a usable session id.  If opts.SessionID is empty,
// a fresh session is created via opts.Store, the id is stored back into
// opts.SessionID, bus.SessionStart is published, and any
// OnSessionCreated callback is invoked.  Subsequent calls return the
// stored id without touching the store.
//
// Concurrency: callers are the per-Send goroutine launched from startSend.
// At most one Send is in flight per App (the inflight cancel field
