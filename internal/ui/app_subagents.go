package ui

import (
	"log/slog"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	appstate "github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/components/anim"
)

func (a *App) foregroundID() string {
	if n := len(a.foregroundStack); n > 0 {
		return a.foregroundStack[n-1]
	}
	return a.opts.SessionID
}

// subagentAtScreen returns the subagent ID at the given screen coordinates,
// accounting for the viewport offset, chat region position, and bubble width.
// Returns "" if no subagent bubble is at that position.
func (a *App) subagentAtScreen(screenX, screenY int) string {
	if len(a.subagentHitZones) == 0 {
		return ""
	}

	// Only register clicks within the left column's bubble area.
	bubbleW := int(float64(a.layout.leftW) * 0.80)
	if screenX > bubbleW {
		return ""
	}

	viewportTop := headerHeight
	chatH := a.layout.chat.Dy()
	viewportBottom := viewportTop + chatH
	if screenY < viewportTop || screenY >= viewportBottom {
		return ""
	}

	// Content line = position within viewport + scroll offset.
	contentLine := (screenY - viewportTop) + a.msgViewport.YOffset()
	if contentLine < 0 {
		return ""
	}

	for _, zone := range a.subagentHitZones {
		if contentLine >= zone.StartLine && contentLine < zone.EndLine {
			return zone.SubSessionID
		}
	}
	return ""
}

// toolAtScreen returns the ToolUseID of a bash tool block at the given screen
// coordinates, or "" if none. Uses the same coordinate translation as subagentAtScreen.
func (a *App) toolAtScreen(screenX, screenY int) string {
	if len(a.toolHitZones) == 0 {
		return ""
	}
	bubbleW := int(float64(a.layout.leftW) * 0.80)
	if screenX > bubbleW {
		return ""
	}
	viewportTop := headerHeight
	chatH := a.layout.chat.Dy()
	viewportBottom := viewportTop + chatH
	if screenY < viewportTop || screenY >= viewportBottom {
		return ""
	}
	contentLine := (screenY - viewportTop) + a.msgViewport.YOffset()
	if contentLine < 0 {
		return ""
	}
	for _, zone := range a.toolHitZones {
		if contentLine >= zone.StartLine && contentLine < zone.EndLine {
			return zone.ToolUseID
		}
	}
	return ""
}

// thinkingAtScreen returns the message index of a thinking block at the given
// screen coordinates, or -1 if none.  Uses the same coordinate translation as
// subagentAtScreen.
func (a *App) thinkingAtScreen(screenX, screenY int) int {
	if len(a.thinkingHitZones) == 0 {
		return -1
	}
	bubbleW := int(float64(a.layout.leftW) * 0.80)
	if screenX > bubbleW {
		return -1
	}
	viewportTop := headerHeight
	chatH := a.layout.chat.Dy()
	viewportBottom := viewportTop + chatH
	if screenY < viewportTop || screenY >= viewportBottom {
		return -1
	}
	contentLine := (screenY - viewportTop) + a.msgViewport.YOffset()
	if contentLine < 0 {
		return -1
	}
	for _, zone := range a.thinkingHitZones {
		if contentLine >= zone.StartLine && contentLine < zone.EndLine {
			return zone.MsgIndex
		}
	}
	return -1
}

// viewingSubagent reports whether the user is viewing a subagent's
// transcript (foreground stack depth > 1).
func (a *App) viewingSubagent() bool {
	return len(a.foregroundStack) > 1
}

// navigateSubagent switches to the next (+1) or previous (-1) subagent
// in the current session.
func (a *App) navigateSubagent(delta int) {
	ids := a.sortedSubagentIDs()
	if len(ids) < 2 {
		return
	}
	cur := a.foregroundID()
	idx := -1
	for i, id := range ids {
		if id == cur {
			idx = i
			break
		}
	}
	if idx < 0 {
		return
	}
	next := (idx + delta + len(ids)) % len(ids)
	if next == idx {
		return
	}
	// Replace the top of the foreground stack.
	a.foregroundStack[len(a.foregroundStack)-1] = ids[next]
	a.refreshMessagesForForeground()
}

// sortedSubagentIDs returns subagent IDs belonging to the parent session
// of the current foreground, sorted by start time.
func (a *App) sortedSubagentIDs() []string {
	// Find the parent session (one level below top of stack).
	parentID := a.rootSessionID()
	if len(a.foregroundStack) >= 2 {
		parentID = a.foregroundStack[len(a.foregroundStack)-2]
	}

	type entry struct {
		id string
		at time.Time
	}
	var entries []entry
	for id, st := range a.subagents {
		if st != nil && st.ParentSessionID == parentID {
			entries = append(entries, entry{id, st.StartedAt})
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].at.Before(entries[j].at)
	})
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.id
	}
	return ids
}

// foregroundSubagent returns the SubagentState for the currently viewed
// subagent, or nil if viewing the root session.
func (a *App) foregroundSubagent() *components.SubagentState {
	if !a.viewingSubagent() {
		return nil
	}
	return a.subagents[a.foregroundID()]
}

// rootSessionID returns the session id at the bottom of the foreground
// stack — the original primary session.  Used by the TUI footer and the
// cost event handler so the rolled-up total is always visible regardless
// of which level the user is viewing.
//
// Falls back to opts.SessionID when the stack is empty.
func (a *App) rootSessionID() string {
	if len(a.foregroundStack) > 0 {
		return a.foregroundStack[0]
	}
	return a.opts.SessionID
}

// pushForeground appends id to the top of the foreground stack.
// If the stack is currently empty, the current foreground (opts.SessionID)
// is used as the implicit root and pushed first, so the stack always has
// the root at index 0 before the new entry.
// Refreshes the message list from the in-memory subagent buffer (for
// now; a future version may reload from the store for the full history).
func (a *App) pushForeground(id string) {
	// Seed the root entry if the stack hasn't been explicitly initialised.
	if len(a.foregroundStack) == 0 && a.opts.SessionID != "" {
		a.foregroundStack = []string{a.opts.SessionID}
	}
	// Guard: do not double-push the same id.
	if a.foregroundID() == id {
		return
	}
	a.foregroundStack = append(a.foregroundStack, id)
	a.refreshMessagesForForeground()
}

// popForeground removes the top of the foreground stack.  No-op when the
// stack would otherwise lose its root entry (depth == 1).
func (a *App) popForeground() {
	if len(a.foregroundStack) <= 1 {
		return
	}
	a.foregroundStack = a.foregroundStack[:len(a.foregroundStack)-1]
	a.refreshMessagesForForeground()
}

// resetForeground replaces the entire stack with [id].  Used by the
// sessions modal "switch" action: the chosen session becomes the new root
// and the breadcrumb is cleared (stack depth == 1).
func (a *App) resetForeground(id string) {
	a.foregroundStack = []string{id}
	a.refreshMessagesForForeground()
}

// refreshMessagesForForeground updates the messages buffer to show the
// foregrounded session.  If the foregrounded session is a known subagent,
// the subagent's transcript is loaded.  Otherwise the primary message
// list is kept as-is (the in-memory buffer is already the primary view).
//
// NOTE: A future version will reload from the store so previously-stored
// messages are visible when following into a completed subagent.
func (a *App) refreshMessagesForForeground() {
	a.invalidateMsgCache()
	a.userScrolled = false

	id := a.foregroundID()
	if id == a.rootSessionID() {
		return
	}
	st, ok := a.subagents[id]
	if !ok {
		return
	}
	// Replace the primary message buffer with the sub-session's messages
	// so the MessageList renders the sub-session's conversation.
	// On pop we restore the primary buffer — but since we only swap
	// a.messages we need to stash it.  Use the foregrounded session
	// approach: when depth > 1 we source from the subagent state.
	// To keep it simple: messages are NOT swapped here; the View()
	// method checks foregroundStack depth and renders accordingly.
	_ = st // used by View() directly
}

// breadcrumbSegments builds the label slice for the Breadcrumb component.
// When viewing a subagent, shows the subagent type/description and nav hints.
func (a *App) breadcrumbSegments() []string {
	if len(a.foregroundStack) <= 1 {
		return nil
	}
	st := a.foregroundSubagent()
	if st == nil {
		return []string{"subagent", "esc to go back"}
	}

	label := st.Type
	if st.Description != "" {
		label += " — " + st.Description
	}

	ids := a.sortedSubagentIDs()
	nav := "esc to go back"
	if len(ids) > 1 {
		nav = "↑ ↓ navigate · esc to go back"
	}
	return []string{label, nav}
}

// latestSubagentID returns the sub-session id of the most recently started
// sub-agent, or "" when no sub-agents have been tracked.  This is the
// "most recent" heuristic shared with toggleLatestSubagent / Ctrl+T.
func (a *App) latestSubagentID() string {
	var latest *components.SubagentState
	for _, st := range a.subagents {
		if st == nil {
			continue
		}
		if latest == nil || st.StartedAt.After(latest.StartedAt) {
			latest = st
		}
	}
	if latest == nil {
		return ""
	}
	return latest.SubSessionID
}

// followIntoLatestSubagent pushes the most-recently-started sub-agent's
// session onto the foreground stack.  If no sub-agents are tracked, a
// notice is set and the call is a no-op.  Returns the notice tea.Cmd.
func (a *App) followIntoLatestSubagent() tea.Cmd {
	id := a.latestSubagentID()
	if id == "" {
		return a.setNotice("no subagent to follow (Ctrl+G)")
	}
	a.pushForeground(id)
	return nil
}

// isForeground reports whether sessionID is the App's active foreground
// session.  An empty foreground id matches anything: this preserves the
// pre-Stage-C behaviour where the App accepted all events because the
// session was lazily created on first user input.
func (a *App) isForeground(sessionID string) bool {
	fg := a.foregroundID()
	if fg == "" {
		return true
	}
	return sessionID == fg
}

// routeToSubagent reports whether sessionID matches a tracked sub-agent
// state. Sub-agent events must always update the SubagentState, even when the
// user has followed into that sub-session: renderChatContent sources the
// foreground subagent view from that state, while a.messages remains the parent
// transcript.
func (a *App) routeToSubagent(sessionID string) bool {
	if sessionID == "" {
		return false
	}
	_, ok := a.subagents[sessionID]
	return ok
}

// onSubagentStarted reacts to bus.SubagentStarted.  Filtering: only
// track sub-agents whose parent chain roots at the foreground session.
// The state is bound to the task tool message via exact ToolUseID match
// (see attachSubagentToSubagentMessage).
func (a *App) onSubagentStarted(e bus.SubagentStarted) tea.Cmd {
	if !a.isInForegroundChain(e.ParentSessionID) {
		return nil
	}
	messages := []components.UIMessage(nil)
	if e.InitialPrompt != "" {
		messages = append(messages, components.UIMessage{
			Role:      components.RoleUser,
			Raw:       e.InitialPrompt,
			Timestamp: e.At,
		})
	}
	state := &components.SubagentState{
		SubSessionID:    e.SubSessionID,
		ParentSessionID: e.ParentSessionID,
		ParentMessageID: e.ParentMessageID,
		Type:            e.Type,
		Description:     e.Description,
		Model:           e.Model,
		StartedAt:       e.At,
		Messages:        messages,
	}
	a.subagents[e.SubSessionID] = state

	a.attachSubagentToSubagentMessage(state)

	// Create an Anim for the running sub-agent.  Resumed sessions are
	// never live-started (they arrive via hydrateMessagesFromStore with
	// EndedAt already set), so we only create Anims here.
	an := anim.New(anim.Settings{
		Width: 8,
		Theme: a.opts.Theme,
	})
	a.subagentAnims[e.SubSessionID] = an

	// Drive the elapsed-time tick while running.  Coalesces with
	// the spinner Tick that's already in flight; bubbletea handles
	// multiple Tick'ers fine.
	return tea.Batch(a.subagentTick(e.SubSessionID), an.Start())
}

// attachSubagentToSubagentMessage walks the message buffer for the
// matching `task` tool message and stamps SubagentID on it.
//
// Primary path: exact ToolUseID match — the task tool UIMessage whose
// ToolUseID equals SubagentStarted.ParentMessageID is the canonical
// anchor and is always unambiguous when ToolUseID is populated.
//
// Defensive fallback: when no exact match is found (e.g. the event
// predates the ToolUseID field being populated), the most recent
// unclaimed streaming task message is used.  An slog.Warn is emitted
// so the condition is observable in logs.
func (a *App) attachSubagentToSubagentMessage(state *components.SubagentState) {
	if state.ParentMessageID != "" {
		// Primary path: exact ToolUseID match.
		for i := len(a.messages) - 1; i >= 0; i-- {
			msg := &a.messages[i]
			if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
				continue
			}
			if msg.ToolUseID != state.ParentMessageID {
				continue
			}
			if msg.SubagentID != "" && msg.SubagentID != state.SubSessionID {
				continue
			}
			msg.SubagentID = state.SubSessionID
			return
		}
	}

	// Defensive fallback: most recent unclaimed streaming task message.
	for i := len(a.messages) - 1; i >= 0; i-- {
		msg := &a.messages[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		if msg.SubagentID != "" && msg.SubagentID != state.SubSessionID {
			continue
		}
		slog.Warn("ui: subagent anchor fell back to recency heuristic; ToolUseID missing on subagent message",
			"sub_session_id", state.SubSessionID,
			"parent_message_id", state.ParentMessageID,
		)
		msg.SubagentID = state.SubSessionID
		return
	}
}

// onSubagentCompleted reacts to bus.SubagentCompleted. Marks EndedAt and
// freezes the running cost/usage with the event's authoritative totals.
//
// When the completing subagent's parent is the foreground session and the
// parent is still mid-turn, also append a transient "continuing…" placeholder
// assistant bubble. The parent's next LLM call often has high time-to-first-
// token (the subagent's full output is now in the prompt), and that latency
// reads as a freeze if the only feedback is the busy pill on the input box.
// The placeholder gives feedback near the chat content; the first real
// AssistantTextDelta (or AssistantThinkingDelta) clears its content before
// accumulating real text — see appendAssistantDelta.
func (a *App) onSubagentCompleted(e bus.SubagentCompleted) tea.Cmd {
	state, ok := a.subagents[e.SubSessionID]
	if !ok {
		return nil
	}
	end := e.At
	if end.IsZero() {
		end = a.opts.Now()
	}
	state.EndedAt = end
	// CostUSD is the final authoritative cost.  Override the
	// running counter even if it drifted (the design doc calls
	// this out explicitly).
	state.Cost = e.CostUSD
	// Stop the anim ticking for this sub-agent: delete from the map so
	// future anim.StepMsg arrivals are silently dropped.
	delete(a.subagentAnims, e.SubSessionID)

	a.maybeAppendContinuingPlaceholder(state)
	return nil
}

// maybeAppendContinuingPlaceholder appends a transient "continuing…" assistant
// bubble to the foreground message list when state's parent session is the
// active foreground and the parent is still busy. No-op when the conditions
// for showing the placeholder are not met (see inline guards).
func (a *App) maybeAppendContinuingPlaceholder(state *components.SubagentState) {
	if state == nil || !a.busy {
		return
	}
	// Only show the placeholder in the parent session's transcript. If the
	// user has followed into the subagent or another session, a.messages is
	// not the right surface.
	fg := a.foregroundID()
	if fg == "" || fg != state.ParentSessionID {
		return
	}
	// Skip when the tail already shows a streaming assistant (the parent's
	// next response has already started — no gap to fill).
	if n := len(a.messages); n > 0 {
		last := a.messages[n-1]
		if last.Role == components.RoleAssistant && last.IsStreaming {
			return
		}
	}
	a.messages = append(a.messages, uiMessage{
		Role:          components.RoleAssistant,
		Raw:           "continuing…",
		VisibleRaw:    "continuing…",
		IsStreaming:   true,
		IsPlaceholder: true,
		AgentType:     a.ActiveModeName(),
		ModelName:     a.opts.ModelName,
		ModeColor:     a.activeModeColor(),
	})
	// A placeholder bubble is a new assistant span; the previously flushed
	// bubble can no longer be extended.
	a.lastAssistantFlushIdx = -1
}

// dropContinuingPlaceholder removes the trailing "continuing…" placeholder
// bubble if present. Called when the parent turn completes without producing
// any text or thinking deltas (the cases that normally clear the placeholder
// via appendAssistantDelta / appendThinkingDelta).
func (a *App) dropContinuingPlaceholder() {
	n := len(a.messages)
	if n == 0 {
		return
	}
	last := a.messages[n-1]
	if !last.IsPlaceholder {
		return
	}
	a.messages = a.messages[:n-1]
	// Tail shifted; the previously flushed assistant index (if any) is no
	// longer guaranteed to point at a valid extend target.
	if a.lastAssistantFlushIdx >= len(a.messages) {
		a.lastAssistantFlushIdx = -1
	}
}

// isInForegroundChain reports whether parentSessionID is the foreground
// session or any descendant of it.  Used to filter incoming
// SubagentStarted events so a sub-agent dispatched by a non-foreground
// session does not leak into the current view.
func (a *App) isInForegroundChain(parentSessionID string) bool {
	if parentSessionID == "" {
		return false
	}
	fg := a.foregroundID()
	if fg == "" {
		// No foreground bound yet -- accept the dispatcher's
		// session as the implicit root.  This preserves
		// pre-Stage-C "no filtering" behaviour for the empty-id
		// edge case.
		return true
	}
	if parentSessionID == fg {
		return true
	}
	// Walk known sub-agents.  Bounded by the size of the map; the
	// runtime currently caps recursion at depth 1.
	cur := parentSessionID
	for i := 0; i < len(a.subagents)+1; i++ {
		st, ok := a.subagents[cur]
		if !ok {
			return false
		}
		if st.ParentSessionID == fg {
			return true
		}
		cur = st.ParentSessionID
	}
	return false
}

// subagentTick returns a tea.Cmd that fires a subagentTickMsg one
// second from now if the named sub-agent is still running.  Update
// re-issues the tick on every fire until the sub-agent completes.
// The single global spinner Tick already drives spinner frames, but
// the spinner tick is locked to the spinner.Model's own cadence;
// dedicating a sub-agent tick lets the elapsed-time counter update
// independently and stop when the sub-agent finishes.
func (a *App) subagentTick(subSessionID string) tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return subagentTickMsg{SubSessionID: subSessionID}
	})
}

// appendSubagentDelta streams text into the matching sub-agent's
// transcript.  Mirrors appendAssistantDelta but scoped to a
// SubagentState.
func (a *App) appendSubagentDelta(subSessionID, text string) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	if n := len(st.Messages); n > 0 {
		last := &st.Messages[n-1]
		if last.Role == components.RoleAssistant && last.IsStreaming {
			last.Raw += text
			return
		}
	}
	st.Messages = append(st.Messages, uiMessage{
		Role:          components.RoleAssistant,
		Raw:           text,
		IsStreaming:   true,
		AgentType:     st.Type,
		ModelName:     st.Model,
		SubagentColor: components.ColorForSubagentType(st.Type),
	})
}

// flushSubagentStream marks the matching sub-agent's most recent
// assistant message as final. Mirrors flushAssistantStream for metadata, but
// leaves markdown rendering to the nested message list view.
func (a *App) flushSubagentStream(subSessionID, role, messageID string) {
	if role != "assistant" {
		return
	}
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	n := len(st.Messages)
	if n == 0 {
		return
	}
	last := &st.Messages[n-1]
	if last.Role != components.RoleAssistant {
		return
	}
	last.IsStreaming = false
	last.ModelName = st.Model
	if a.opts.Store != nil && messageID != "" {
		if msg, err := a.opts.Store.GetMessage(a.ctx, messageID); err == nil && msg != nil {
			last.OutputTokens = msg.OutputTokens
			last.CostUSD = msg.CostUSD
			last.DurationMs = msg.DurationMs
			if !msg.CreatedAt.IsZero() {
				last.Timestamp = msg.CreatedAt
			}
		}
	}
}

// appendSubagentTool appends a streaming tool entry to the matching
// sub-agent's transcript.
func (a *App) appendSubagentTool(subSessionID, toolName, toolUseID, target string) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	st.Messages = append(st.Messages, uiMessage{
		Role:        components.RoleTool,
		ToolName:    toolName,
		ToolUseID:   toolUseID,
		Target:      target,
		Raw:         "(running…)",
		IsStreaming: true,
	})
}

// finishSubagentTool finalises the most recent streaming tool entry
// for a sub-agent, mirroring updateLastTool but scoped.
func (a *App) finishSubagentTool(subSessionID string, e bus.ToolCallCompleted) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	for i := len(st.Messages) - 1; i >= 0; i-- {
		msg := &st.Messages[i]
		if msg.Role != components.RoleTool || !msg.IsStreaming {
			continue
		}
		if msg.ToolName != e.ToolName {
			continue
		}
		msg.IsStreaming = false
		if e.Err != "" {
			msg.IsError = true
			msg.Raw = e.Err
		} else {
			msg.Raw = string(e.Result)
		}
		return
	}
	out := string(e.Result)
	if e.Err != "" {
		out = e.Err
	}
	st.Messages = append(st.Messages, uiMessage{
		Role:      components.RoleTool,
		ToolName:  e.ToolName,
		ToolUseID: e.ToolUseID,
		Raw:       out,
		IsError:   e.Err != "",
	})
}

// appendSubagentToolProgress appends a progress line from a streaming tool
// call to the matching sub-agent's tool row, mirroring appendToolProgress but
// scoped to the sub-agent transcript.
func (a *App) appendSubagentToolProgress(e bus.ToolCallProgress) {
	st, ok := a.subagents[e.SessionID]
	if !ok {
		return
	}
	for i := len(st.Messages) - 1; i >= 0; i-- {
		msg := &st.Messages[i]
		if msg.Role != components.RoleTool || !msg.IsStreaming {
			continue
		}
		if msg.ToolUseID != e.ToolUseID {
			continue
		}
		if msg.Raw == "(running…)" || msg.Raw == "" {
			msg.Raw = e.Line
		} else {
			msg.Raw += "\n" + e.Line
		}
		return
	}
}

// updateSubagentCost updates a sub-agent's running cost & token totals
// from a bus.CostUpdated event.
func (a *App) updateSubagentCost(subSessionID string, e bus.CostUpdated) {
	st, ok := a.subagents[subSessionID]
	if !ok {
		return
	}
	st.Cost = e.DollarsTotal
	st.InputTokens = e.InputTokens
	st.OutputTokens = e.OutputTokens
}

// gitBranch returns the current git branch for the project directory.
// Delegates to the state package which caches the result per-session.
func (a *App) gitBranch() string {
	if a.opts.ProjectDir == "" {
		return ""
	}
	return appstate.GitBranch(a.opts.ProjectDir)
}

// foregroundTranscriptID returns a stable key identifying which transcript is
// currently being rendered. When viewing a subagent, this is the subagent's
// SubSessionID; otherwise it is the root session ID (foreground stack bottom).
// Used as the outer key into a.expandedThinking.
func (a *App) foregroundTranscriptID() string {
	foreID := a.foregroundID()
	rootID := a.rootSessionID()
	if foreID != rootID && foreID != "" {
		return foreID // subagent transcript
	}
	return rootID // root session transcript
}

// expandedThinkingFor returns the per-transcript map[int]bool for transcriptID,
// creating it if absent. The caller must not hold a stale reference across
// operations that could replace the outer map.
func (a *App) expandedThinkingFor(transcriptID string) map[int]bool {
	if m, ok := a.expandedThinking[transcriptID]; ok {
		return m
	}
	m := make(map[int]bool)
	a.expandedThinking[transcriptID] = m
	return m
}

// refreshTodosCache loads the foreground session's todo list from the store
// into todosCache.  Called when the foreground session changes or when a
