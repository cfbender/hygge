package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/notify"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/components/anim"
	"github.com/cfbender/hygge/internal/ui/styles"
)

func (a *App) handleBusEvent(ev any) tea.Cmd {
	// Most bus events may mutate visible messages/chrome. Streaming deltas are
	// special-cased so users can scroll through large histories smoothly while
	// the assistant is still producing text.
	switch ev.(type) {
	case bus.AssistantTextDelta, bus.AssistantThinkingDelta, bus.ToolCallProgress:
		// Invalidated in the specific handlers below.
	default:
		a.invalidateMsgCache()
	}

	switch e := ev.(type) {

	case bus.SubagentStarted:
		return a.onSubagentStarted(e)

	case bus.SubagentCompleted:
		return a.onSubagentCompleted(e)

	case bus.AssistantTextDelta:
		if a.routeToSubagent(e.SessionID) {
			a.appendSubagentDelta(e.SessionID, e.Text)
			a.invalidateMsgCacheForStreamingDelta()
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// Finalize any trailing streaming thinking block before appending text.
		a.finalizeTrailingThinking()
		a.appendAssistantDelta(e.Text)
		a.invalidateMsgCacheForStreamingDelta()

	case bus.AssistantThinkingDelta:
		if a.routeToSubagent(e.SessionID) {
			// Subagent thinking is not surfaced in the nested block view.
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.appendThinkingDelta(e.Text)
		a.invalidateMsgCacheForStreamingDelta()

	case bus.MessageAppended:
		if a.routeToSubagent(e.SessionID) {
			a.flushSubagentStream(e.SessionID, e.Role, e.MessageID)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// Refresh the sidebar title cache: the first user message sets
		// FirstMessagePreview in the store.  We call loadSessionTitle here
		// (on the Update goroutine, not the render goroutine) so
		// sidebarSessionTitle() stays cheap.
		var titleCmd tea.Cmd
		if e.SessionID == a.rootSessionID() {
			a.sessionTitle = a.loadSessionTitle(e.SessionID)
			if e.Role == string(session.RoleUser) {
				titleCmd = a.maybeRefreshSessionTitle(e.SessionID)
			}
		}
		// Finalize any trailing thinking block when the message is committed.
		a.finalizeTrailingThinking()
		if e.Role == string(session.RoleUser) {
			a.appendPersistedUserMessage(e.MessageID)
			// A new user message ends any prior assistant span.
			a.lastAssistantFlushIdx = -1
			return titleCmd
		}
		return a.flushAssistantStream(e.Role, e.MessageID)

	case bus.ToolCallRequested:
		if a.routeToSubagent(e.SessionID) {
			a.appendSubagentTool(e.SessionID, e.ToolName, e.ToolUseID, extractTarget(e.Args))
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// A tool call ends the current assistant span; any subsequent
		// AssistantTextDelta should not be folded back into the
		// just-flushed assistant bubble.
		a.lastAssistantFlushIdx = -1
		// The parent has started its next step. Drop any "continuing…"
		// placeholder before appending the tool row so the transcript
		// reflows naturally.
		a.dropContinuingPlaceholder()
		// Finalize any trailing thinking block before a tool call.
		a.finalizeTrailingThinking()
		target := a.displayTargetForTool(e.ToolName, e.Args)
		// Track files that write/edit tools are about to modify.  We record the
		// path at request time (not completion) so the list updates as soon as
		// the tool is dispatched, giving the sidebar something to show even
		// while a long-running write is in progress.
		if e.ToolName == "write" || e.ToolName == "edit" {
			if p := extractPathFromArgs(e.Args); p != "" {
				a.touched.Add(p, a.opts.ProjectDir)
			}
		}
		a.messages = append(a.messages, uiMessage{
			Role:        components.RoleTool,
			ToolName:    e.ToolName,
			ToolUseID:   e.ToolUseID,
			Target:      target,
			ToolArgs:    e.Args,
			Raw:         "(running…)",
			IsStreaming: true,
			Status:      components.ToolStatusPending,
		})

	case bus.ToolCallProgress:
		if a.routeToSubagent(e.SessionID) {
			a.appendSubagentToolProgress(e)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.appendToolProgress(e)
		a.invalidateMsgCacheForStreamingDelta()

	case bus.ToolCallCompleted:
		if a.routeToSubagent(e.SessionID) {
			a.finishSubagentTool(e.SessionID, e)
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.updateLastTool(e)
	case bus.CostUpdated:
		// T2.1 cost roll-up: always update subagent cost tracking so the
		// nested block header stays current for any sub-session event.
		if a.routeToSubagent(e.SessionID) {
			a.updateSubagentCost(e.SessionID, e)
			// Also fall through to check if this is the root, to keep
			// costDollars correct.
		}
		// The footer always shows the ROOT session's rolled-up total.
		// rootSessionID() returns opts.SessionID when the stack is empty
		// (pre-T2.2 path), preserving existing behaviour.
		rootID := a.rootSessionID()
		if rootID == "" || e.SessionID == rootID {
			a.costDollars = e.DollarsTotal
			a.billedInputTok = e.InputTokens + e.CacheReadTokens + e.CacheWriteTokens
			a.billedOutputTok = e.OutputTokens
		}

	case bus.ContextUsageUpdated:
		// Context usage is a parent-level concern.  Sub-agents have
		// their own context windows that are not surfaced in the
		// primary footer.
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.usedTok = e.UsedTokens
		a.maxTok = e.MaxTokens
		a.pctUsed = e.PctUsed

	case bus.PermissionAsked:
		// Permission asks always pop the modal regardless of which
		// session originated them -- they block tool execution and
		// the user needs to decide either way.  The modal does not
		// (yet) badge which session asked; that's a v0.3 polish.
		a.pendingPerms = append(a.pendingPerms, components.PermissionRequest{
			RequestID: e.RequestID,
			ToolName:  e.ToolName,
			Category:  e.Category,
			Target:    e.Target,
			Why:       e.Reason,
		})
		a.syncPermissionOverlay()
		a.updateInputFocus()
		// Stamp the matching tool row as awaiting permission.  We correlate
		// by ToolName (most recent streaming row with that name) because
		// PermissionAsked does not carry a ToolUseID.
		a.setToolStatus(e.ToolName, components.ToolStatusAwaitingPermission)
		// Send a notification when the terminal is unfocused so the user
		// knows action is needed even when they've switched away.
		a.maybeNotify(notify.Notification{
			Title:   "Hygge is waiting…",
			Message: fmt.Sprintf("Permission required to execute %q", e.ToolName),
		}, "permission_ask")

	case bus.PermissionReplied:
		// Transition the awaiting-permission tool row based on the decision.
		// We find the most-recently-created row that is in AwaitingPermission
		// state — one reply resolves one request, so processing in FIFO order
		// is correct for the common single-permission case.
		if e.Decision == "allow" {
			a.setToolStatusByCurrentStatus(
				components.ToolStatusAwaitingPermission,
				components.ToolStatusRunning,
			)
		} else {
			a.setToolStatusByCurrentStatus(
				components.ToolStatusAwaitingPermission,
				components.ToolStatusCancelled,
			)
		}

	case bus.QuestionAsked:
		opts := make([]components.QuestionOption, 0, len(e.Options))
		for _, opt := range e.Options {
			opts = append(opts, components.QuestionOption{ID: opt.ID, Label: opt.Label})
		}
		a.pendingQuestions = append(a.pendingQuestions, components.QuestionRequest{
			RequestID: e.RequestID,
			ToolName:  e.ToolName,
			Question:  e.Question,
			Options:   opts,
		})
		a.clampQuestionSelection()
		a.syncQuestionOverlay()
		a.updateInputFocus()
		a.maybeNotify(notify.Notification{
			Title:   "Hygge is waiting…",
			Message: e.Question,
		}, "permission_ask")

	case bus.QuestionAnswered:
		// The key handler normally removes the active prompt before publishing the
		// answer. This handles replies from tests or future non-TUI frontends.
		for i, q := range a.pendingQuestions {
			if q.RequestID == e.RequestID {
				a.pendingQuestions = append(a.pendingQuestions[:i], a.pendingQuestions[i+1:]...)
				if i == 0 {
					a.questionSelectedIndex = 0
				}
				break
			}
		}
		a.syncQuestionOverlay()
		a.updateInputFocus()

	case bus.MCPStatusUpdated:
		a.upsertMCPStatus(components.SidebarMCPStatus{
			Name:      e.Name,
			Ready:     e.Ready,
			Error:     e.Error,
			ToolCount: e.ToolCount,
		})

	// --- Compaction events (T2.3) ---

	case bus.CompactionRequested:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		if e.Source == "threshold" {
			// Advisory suggestion: show the banner (or reset dismiss for a new
			// crossing — the agent only fires this once per crossing, so
			// receiving it again means usage dropped and came back).
			a.bannerVisible = true
			a.bannerPct = e.UsagePct
			a.bannerDismissed = false
		}
		// Source "user" is handled by applyOutcome via the modal outcome path;
		// the bus event is not used to open the modal (that's the slash command's
		// job).

	case bus.CompactionStarted:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.compactionInFlight = true
		a.compactionInFlightCount = e.MessagesToCompact
		a.compactionAnim = anim.New(anim.Settings{
			Width:    14,
			Theme:    a.opts.Theme,
			GradFrom: styles.AtomWarn,
			GradTo:   styles.AtomAccent,
		})
		a.invalidateMsgCache()
		return a.compactionAnim.Start()

	case bus.CompactionCompleted:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// The compactionCompleteMsg path handles toast rendering.
		// Here we also clear the banner since compaction has finished,
		// and append a persistent marker row to the message list.
		a.bannerVisible = false
		a.bannerDismissed = false
		// Fetch the marker summary from store so the banner row carries
		// the full text.  Best-effort: if the store is unavailable or the
		// fetch fails we skip the marker row (the toast still fires).
		if a.opts.Store != nil && e.MarkerID != "" {
			marker, err := a.opts.Store.LatestMarker(a.ctx, e.SessionID)
			if err == nil && marker != nil {
				a.messages = append(a.messages, uiMessage{
					Role:              components.RoleMarker,
					MarkerSummary:     marker.Summary,
					MarkerTokensSaved: marker.InputTokensSaved,
				})
			}
		}

	case bus.CompactionFailed:
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// compactionCompleteMsg will carry the error for toast display.
		// Nothing extra to do here — the in-flight notice is cleared by
		// compactionCompleteMsg handling.

	case bus.QueueChanged:
		if len(a.queuedDrafts) > 0 {
			return nil
		}
		// Only update the queue state for the root (active send) session.
		rootID := a.rootSessionID()
		if rootID != "" && e.SessionID != rootID {
			return nil
		}
		a.queueCount = e.Count
		a.queuedPrompts = e.Prompts
		// Update the busy placeholder to reflect the queued count.
		if a.busy {
			suffix := ""
			if a.queueCount > 0 {
				suffix = fmt.Sprintf(" (%d queued)", a.queueCount)
			}
			a.input.SetBusy(true, suffix)
		}
		// If there are queued items but the UI thinks it's idle, the agent
		// is running a turn the UI missed.  Schedule a reconcile tick so
		// the busy state is recovered within ~1 second.
		if e.Count > 0 && !a.busy {
			return a.busyReconcileTick()
		}

	case bus.TodoChanged:
		rootID := a.rootSessionID()
		if rootID != "" && e.SessionID != rootID {
			return nil
		}
		a.todoIncomplete = e.Incomplete
		a.todoInProgress = e.InProgress
		a.refreshTodosCache()

	case bus.TurnStarted:
		// Gate on foreground session.  Increment the in-flight turn counter
		// and make sure the UI shows busy.  This fires from the agent's own
		// context goroutine, so it always arrives even when the caller's ctx
		// was cancelled (queue-drain path).
		if !a.isForeground(e.SessionID) {
			return nil
		}
		a.activeTurns++
		wasBusy := a.busy
		a.busy = true
		if a.workingVerb == "" {
			a.workingVerb = components.RandomWorkingVerb()
		}
		suffix := ""
		if a.queueCount > 0 {
			suffix = fmt.Sprintf(" (%d queued)", a.queueCount)
		}
		a.input.SetBusy(true, suffix)
		if !wasBusy {
			// Also arm the reconcile ticker so any future desync during this
			// turn is detected and corrected promptly.
			return tea.Batch(a.workingVerbTick(), a.busyReconcileTick())
		}
		// Already busy: arm the reconcile ticker (cheap, idempotent).
		return a.busyReconcileTick()

	case bus.TurnCompleted:
		// Gate on foreground session and send turn-complete notification.
		if !a.isForeground(e.SessionID) {
			return nil
		}
		// Drop any lingering "continuing…" placeholder: the turn is over, so
		// further parent deltas (which would have cleared it) are not coming.
		a.dropContinuingPlaceholder()
		// Turn over: no more assistant deltas should be folded into the
		// final bubble of this turn.
		a.lastAssistantFlushIdx = -1
		a.maybeNotify(notify.Notification{
			Title:   "Hygge is waiting…",
			Message: fmt.Sprintf("Turn completed in %q", a.sessionTitle),
		}, "turn_complete")
		// Decrement the in-flight turn counter.  Flip busy=false only when no
		// more turns are running AND the queue is empty.  If a queued send is
		// about to start, its TurnStarted will arrive shortly and re-set busy;
		// keeping it set here avoids a visible flicker.
		if a.activeTurns > 0 {
			a.activeTurns--
		}
		if a.activeTurns == 0 && len(a.queuedDrafts) > 0 {
			// If there is still a streaming assistant bubble in the message
			// list, bus.MessageAppended(assistant) has not yet been processed
			// (TurnCompleted and MessageAppended travel through separate bridge
			// goroutines and can arrive out of order).  Defer the flush until
			// flushAssistantStream finalises the bubble so the next queued send
			// never overlaps with the outgoing assistant turn's render.
			if a.currentAssistantMessageIndex() >= 0 && a.messages[a.currentAssistantMessageIndex()].IsStreaming {
				a.pendingQueueFlush = true
				return nil
			}
			a.busy = false
			a.workingVerb = ""
			a.input.SetBusy(false, "")
			return a.flushQueuedDraftsCmd()
		}
		if a.activeTurns == 0 && a.queueCount == 0 {
			a.busy = false
			a.workingVerb = ""
			a.input.SetBusy(false, "")
			return a.maybeRefreshSessionTitle(e.SessionID)
		}

	case bus.SessionTitleUpdated:
		// Title changes come from the agent (preview seed, model-generated
		// summary, or the rename_session tool). Update the sidebar cache
		// when the event matches the active root session.
		if e.SessionID != a.rootSessionID() {
			return nil
		}
		if strings.TrimSpace(e.Title) == "" {
			return nil
		}
		a.sessionTitle = e.Title

	case bus.LazyContextLoaded:
		// Show a non-intrusive annotation when the lazy tracker discovers
		// subdir AGENTS.md / CLAUDE.md files during a tool call.  Route to
		// subagent view when appropriate; ignore background sessions.
		if a.routeToSubagent(e.SessionID) {
			// Subagent lazy context is not surfaced in the nested block view.
			return nil
		}
		if !a.isForeground(e.SessionID) {
			return nil
		}
		label := strings.Join(e.Files, ", ")
		a.messages = append(a.messages, uiMessage{
			Role: components.RoleSystem,
			Raw:  "loaded project context: " + label,
		})
	}
	return nil
}

func (a *App) upsertMCPStatus(status components.SidebarMCPStatus) {
	for i := range a.opts.MCPStatuses {
		if a.opts.MCPStatuses[i].Name == status.Name {
			a.opts.MCPStatuses[i] = status
			return
		}
	}
	a.opts.MCPStatuses = append(a.opts.MCPStatuses, status)
}
