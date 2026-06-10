package ui

import (
	"log/slog"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
)

// hydrateMessagesFromStore loads persisted history for sid and assigns it to
// a.messages.  It returns a tea.Cmd that renders markdown for all hydrated
// messages in a background goroutine (markdownBatchMsg), so the first paint
// never blocks on glamour.  When there is nothing to render (empty session,
// no store, etc.) nil is returned.
func (a *App) hydrateMessagesFromStore(sid string) tea.Cmd {
	if a.opts.Store == nil || sid == "" {
		return nil
	}
	visited := make(map[string]struct{})
	msgs := a.hydrateSessionMessages(sid, visited)
	a.messages = msgs
	a.invalidateMsgCache()
	return a.renderMarkdownBatchTailFirstCmd(0, len(msgs))
}

// markdownRenderableRole reports whether role is one that the async markdown
// batch renderer should process (RoleUser or RoleAssistant).
func markdownRenderableRole(role components.MessageRole) bool {
	return role == components.RoleUser || role == components.RoleAssistant
}

// renderMarkdownBatchCmd returns a tea.Cmd that glamour-renders messages in
// a.messages[startIdx:endIdx] whose Role is RoleUser or RoleAssistant and
// whose Raw is non-empty.  It constructs a fresh renderer (independent of
// a.renderer) at the current msgColW so there is no shared-state data race.
//
// Results are keyed by MessageID when available (safe against index shifts).
// Messages without a MessageID fall back to an index+rawSnap guard so they
// are only applied if the message at that index still has the same Raw.
//
// When startIdx >= endIdx or the slice is empty the function returns nil.
func (a *App) renderMarkdownBatchCmd(startIdx, endIdx int) tea.Cmd {
	if startIdx >= endIdx {
		return nil
	}
	// Snapshot only what the goroutine needs; no App pointer is captured.
	type renderSpec struct {
		idx       int
		messageID string
		raw       string
		role      components.MessageRole
	}
	specs := make([]renderSpec, 0, endIdx-startIdx)
	for i := startIdx; i < endIdx; i++ {
		m := a.messages[i]
		if !markdownRenderableRole(m.Role) || m.Raw == "" || m.IsStreaming {
			continue
		}
		specs = append(specs, renderSpec{
			idx:       i,
			messageID: m.MessageID,
			raw:       m.Raw,
			role:      m.Role,
		})
	}
	if len(specs) == 0 {
		return nil
	}
	theme := a.opts.Theme
	width := a.msgColW
	if width <= 0 {
		width = 80
	}
	return func() tea.Msg {
		r, err := newRenderer(theme, width)
		if err != nil {
			// Renderer creation failed: return empty result so Update skips
			// gracefully (FinalMarkdown stays empty → P1 plain-text fallback).
			return markdownBatchMsg{
				rendered: make(map[string]string),
				width:    width,
			}
		}
		rendered := make(map[string]string, len(specs))
		var fallback []markdownBatchFallback
		for _, spec := range specs {
			out := renderMarkdown(r, spec.raw)
			if out == "" {
				continue
			}
			if spec.messageID != "" {
				rendered[spec.messageID] = out
			} else {
				fallback = append(fallback, markdownBatchFallback{
					idx:     spec.idx,
					rawSnap: spec.raw,
					out:     out,
				})
			}
		}
		return markdownBatchMsg{rendered: rendered, fallback: fallback, width: width}
	}
}

// renderMarkdownBatchTailFirstCmd issues two Cmds when the message slice is
// large enough: the tail (last tailN messages) first so the visible end of the
// chat upgrades immediately, followed by the remainder.  When the total count
// is <= tailN a single Cmd is returned (no split needed).
//
// tailN is the number of messages to prioritize from the tail.
const markdownBatchTailN = 30

func (a *App) renderMarkdownBatchTailFirstCmd(startIdx, endIdx int) tea.Cmd {
	total := endIdx - startIdx
	if total <= 0 {
		return nil
	}
	tailStart := endIdx - markdownBatchTailN
	if tailStart <= startIdx {
		// Small enough to handle in one batch.
		return a.renderMarkdownBatchCmd(startIdx, endIdx)
	}
	// Issue tail first, then the preceding head.
	tailCmd := a.renderMarkdownBatchCmd(tailStart, endIdx)
	headCmd := a.renderMarkdownBatchCmd(startIdx, tailStart)
	if tailCmd == nil && headCmd == nil {
		return nil
	}
	if tailCmd == nil {
		return headCmd
	}
	if headCmd == nil {
		return tailCmd
	}
	return tea.Batch(tailCmd, headCmd)
}

func (a *App) hydrateTodoSummary(sid string) {
	a.todoIncomplete = 0
	a.todoInProgress = 0
	a.todosCache = nil
	if a.opts.Store == nil || sid == "" {
		return
	}
	items, summary, err := a.opts.Store.GetSessionTodos(a.ctx, sid)
	if err != nil {
		slog.Warn("ui: hydrateTodoSummary: failed to load todos", "session_id", sid, "err", err)
		return
	}
	a.todoIncomplete = summary.Incomplete
	a.todoInProgress = summary.InProgress
	if len(items) == 0 {
		return
	}
	out := make([]components.SidebarTodo, 0, len(items))
	for _, it := range items {
		out = append(out, components.SidebarTodo{
			Title:  it.Content,
			Status: components.SidebarTodoStatus(it.Status),
		})
	}
	a.todosCache = out
}

// hydrateSessionMessages is the recursive implementation shared by
// hydrateMessagesFromStore (primary session) and subagent reconstruction.
// visited guards against cycles (impossible today but defensive).
// It returns a []uiMessage slice for the session's conversation.
//
// For KindSubagent sessions, only messages directly owned by the session
// are loaded (no fork-chain walking), because subagents start with a
// fresh history independent of their parent.
func (a *App) hydrateSessionMessages(sid string, visited map[string]struct{}) []uiMessage {
	if _, seen := visited[sid]; seen {
		slog.Warn("ui: hydrateSessionMessages: cycle detected, skipping",
			"session_id", sid)
		return nil
	}
	visited[sid] = struct{}{}

	// Look up session kind to decide which message query to use.
	// For subagent sessions: load messages directly (no fork-chain).
	// For primary/fork sessions: load via MessagesForSession (walks fork chain).
	var (
		storeMsgs []*session.Message
		err       error
	)
	if sess, lookupErr := a.opts.Store.GetSession(a.ctx, sid); lookupErr == nil &&
		sess.Kind == session.KindSubagent {
		storeMsgs, err = a.opts.Store.MessagesDirectForSession(a.ctx, sid)
	} else {
		storeMsgs, err = a.opts.Store.MessagesForSession(a.ctx, sid)
	}
	if err != nil {
		slog.Warn("ui: hydrateSessionMessages: failed to load history",
			"session_id", sid, "err", err)
		return nil
	}

	// Load all markers so we can inject them in order.
	var markers []*session.Marker
	if markList, err := a.opts.Store.ListMarkersForSession(a.ctx, sid); err == nil {
		markers = markList
	} else {
		slog.Warn("ui: hydrateSessionMessages: failed to load markers",
			"session_id", sid, "err", err)
	}

	// Build a set of message ids that are marker cut-off points.
	// key: beforeMessageID → marker.  Multiple markers may share no
	// common message ids since each compaction advances the cursor.
	// We walk markers in chronological order (oldest first) and inject
	// each one immediately before the message it cuts off at.
	markerByBefore := make(map[string]*session.Marker, len(markers))
	for _, mk := range markers {
		markerByBefore[mk.BeforeMessageID] = mk
	}

	// Pass 1: build a result index keyed by ToolUseID from all RoleTool rows.
	// This handles the common persistence shape where PartToolUse lives inside
	// an assistant message and PartToolResult lives in a separate tool message.
	toolResults, toolUseIDs := buildToolResultIndex(storeMsgs)

	// Pass 2: build the flat message list, passing the result index so that
	// assistant messages can inline results for their PartToolUse parts.
	out := make([]uiMessage, 0, len(storeMsgs))
	for _, m := range storeMsgs {
		// Inject marker banner before this message if one targets it.
		if mk, ok := markerByBefore[m.ID]; ok {
			out = append(out, uiMessage{
				Role:              components.RoleMarker,
				MarkerSummary:     mk.Summary,
				MarkerTokensSaved: mk.InputTokensSaved,
				Timestamp:         mk.CreatedAt,
			})
		}
		entries := uiEntriesFromStoreMessage(m, toolResults, toolUseIDs)
		for i := range entries {
			if entries[i].Role == components.RoleTool && entries[i].Target != "" && toolDisplaysPath(entries[i].ToolName) {
				entries[i].Target = relativePathFromPwd(entries[i].Target, a.opts.ProjectDir)
			}
		}
		// Wire AgentType and ModelName on assistant entries so bubbles render correctly.
		// VisibleRaw is streaming-only; do not set it on hydrated messages.
		// FinalMarkdown is left empty and filled asynchronously by markdownBatchTailFirstCmd.
		for i := range entries {
			switch entries[i].Role {
			case components.RoleAssistant:
				entries[i].AgentType = a.ActiveModeName()
				entries[i].ModelName = a.opts.ModelName
				entries[i].ModeColor = a.activeModeColor()
			}
		}
		out = append(out, entries...)
	}

	// Handle markers whose BeforeMessageID no longer matches any message
	// (e.g. the message was deleted or the chain was rebased).  Insert them
	// chronologically by marker CreatedAt instead of appending at the end, so
	// multi-compaction histories remain stable even when a cut-off message is
	// no longer present in the hydrated message chain.
	injectedIDs := make(map[string]struct{}, len(storeMsgs))
	for _, m := range storeMsgs {
		injectedIDs[m.ID] = struct{}{}
	}
	for _, mk := range markers {
		if _, found := injectedIDs[mk.BeforeMessageID]; found {
			continue
		}
		markerMsg := uiMessage{
			Role:              components.RoleMarker,
			MarkerSummary:     mk.Summary,
			MarkerTokensSaved: mk.InputTokensSaved,
			Timestamp:         mk.CreatedAt,
		}
		insertAt := len(out)
		if !mk.CreatedAt.IsZero() {
			for i, msg := range out {
				if msg.Timestamp.IsZero() {
					continue
				}
				if mk.CreatedAt.Before(msg.Timestamp) || mk.CreatedAt.Equal(msg.Timestamp) {
					insertAt = i
					break
				}
			}
		}
		out = append(out, uiMessage{})
		copy(out[insertAt+1:], out[insertAt:])
		out[insertAt] = markerMsg
	}

	// Reconstruct subagent state for `task` tool messages.
	// We list all KindSubagent sessions for this parent once, then match
	// them to task tool UIMessages by exact ParentToolUseID.
	a.reconstructSubagentState(sid, out, visited)

	return out
}

// buildToolResultIndex scans all store messages and returns:
//   - results: PartToolResult parts keyed by ToolUseID (from any RoleTool row).
//   - toolUseIDs: set of ToolIDs that appeared in PartToolUse parts of
//     RoleAssistant messages.
//
// This supports the common persistence shape where PartToolUse lives inside
// an assistant message and PartToolResult lives in a separate tool message.
// If a ToolUseID appears in more than one result (should not happen in
// practice), last-write wins and a warning is logged.
func buildToolResultIndex(msgs []*session.Message) (results map[string]session.Part, toolUseIDs map[string]struct{}) {
	results = make(map[string]session.Part)
	toolUseIDs = make(map[string]struct{})
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartToolResult:
				if _, exists := results[p.ToolUseID]; exists {
					slog.Warn("ui: buildToolResultIndex: duplicate tool_use_id; last writer wins",
						"tool_use_id", p.ToolUseID)
				}
				results[p.ToolUseID] = p
			case session.PartToolUse:
				if m.Role == session.RoleAssistant {
					toolUseIDs[p.ToolID] = struct{}{}
				}
			}
		}
	}
	return results, toolUseIDs
}

// uiEntriesFromStoreMessage converts a persisted *session.Message into zero
// or more uiMessages for the App's message buffer.  Multiple entries can be
// returned from a single store row when the message has text and tool-use
// parts (entries are emitted in part order within the turn).
//
// toolResults is the cross-message result index built by buildToolResultIndex.
// toolUseIDs is the set of ToolIDs that appeared in PartToolUse parts of
// assistant messages, also from buildToolResultIndex.
// Both must not be nil; pass empty maps for the legacy combined-row path.
//
// Phase 2 change: PartThinking parts are now collapsed into the assistant
// uiMessage's Thinking field instead of being emitted as separate rows.
// An assistant message that has only PartToolUse parts (no text, no
// thinking) emits no uiMessage so the bubble is not rendered empty.
func uiEntriesFromStoreMessage(m *session.Message, toolResults map[string]session.Part, toolUseIDs map[string]struct{}) []uiMessage {
	if m == nil {
		return nil
	}
	switch m.Role {
	case session.RoleUser:
		text := firstTextPart(m.Parts)
		if text == "" {
			return nil
		}
		ts := m.CreatedAt
		return []uiMessage{{Role: components.RoleUser, Raw: text, Timestamp: ts, MessageID: m.ID}}

	case session.RoleAssistant:
		// Collect all PartThinking texts (joined with "\n\n").
		var thinkingParts []string
		for _, p := range m.Parts {
			if p.Kind == session.PartThinking && p.Text != "" {
				thinkingParts = append(thinkingParts, p.Text)
			}
		}
		thinking := strings.Join(thinkingParts, "\n\n")

		// Accumulate consecutive PartText parts into a single assistant entry.
		var textBuf strings.Builder
		var toolEntries []uiMessage
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartThinking:
				// Already handled above; skip here.
			case session.PartText:
				textBuf.WriteString(p.Text)
			case session.PartToolUse:
				// Flush any accumulated text before emitting the tool row so
				// the ordering (text before tool) is preserved.
				// (text is captured in textBuf; tool entries are separate)
				target := extractTarget(p.ToolInput)
				raw := ""
				isError := false
				if res, ok := toolResults[p.ToolID]; ok {
					raw = res.Content
					isError = res.IsError
				}
				// Hydrated tool entries with a result are always completed or errored.
				// Entries without a matching result are orphaned (tool_use with no
				// tool_result — interrupted run); use ToolStatusUnknown so no status
				// text is rendered.  Well-formed sessions should not produce orphans.
				hydratedStatus := components.ToolStatusUnknown
				if _, hasResult := toolResults[p.ToolID]; hasResult {
					if isError {
						hydratedStatus = components.ToolStatusError
					} else {
						hydratedStatus = components.ToolStatusCompleted
					}
				}
				toolEntries = append(toolEntries, uiMessage{
					Role:      components.RoleTool,
					ToolName:  p.ToolName,
					ToolUseID: p.ToolID,
					Target:    target,
					ToolArgs:  p.ToolInput,
					Raw:       raw,
					IsError:   isError,
					Status:    hydratedStatus,
				})
			}
		}
		rawText := textBuf.String()

		// Skip entirely if no text and no thinking (tool-only assistant turn).
		if thinking == "" && rawText == "" {
			return toolEntries
		}

		// Emit one assistant uiMessage with thinking + text, then tool entries.
		// MessageID is stamped so duplicate-guard checks in flushAssistantStream
		// can detect this message has already been hydrated.
		assistantMsg := uiMessage{
			Role:         components.RoleAssistant,
			Raw:          rawText,
			Thinking:     thinking,
			Timestamp:    m.CreatedAt,
			OutputTokens: m.OutputTokens,
			CostUSD:      m.CostUSD,
			DurationMs:   m.DurationMs,
			MessageID:    m.ID,
		}
		return append([]uiMessage{assistantMsg}, toolEntries...)

	case session.RoleTool:
		// Check whether this row contains any PartToolUse (legacy combined-row
		// shape).  If so, pair each PartToolUse with its inline PartToolResult.
		// If not (the common result-only shape produced by the current
		// persistence model), emit nothing — results were already inlined by
		// the assistant turn handling above.
		hasUse := false
		for _, p := range m.Parts {
			if p.Kind == session.PartToolUse {
				hasUse = true
				break
			}
		}
		if !hasUse {
			// Result-only row: warn on truly orphaned results whose ToolUseID
			// did not appear in any assistant message's PartToolUse part.
			for _, p := range m.Parts {
				if p.Kind == session.PartToolResult {
					if _, found := toolUseIDs[p.ToolUseID]; !found {
						slog.Warn("ui: uiEntriesFromStoreMessage: orphaned tool_result (no matching tool_use in any assistant message)",
							"tool_use_id", p.ToolUseID)
					}
				}
			}
			return nil
		}
		// Legacy combined-row: pair each PartToolUse with its inline result.
		inlineResults := make(map[string]session.Part, len(m.Parts))
		for _, p := range m.Parts {
			if p.Kind == session.PartToolResult {
				inlineResults[p.ToolUseID] = p
			}
		}
		var entries []uiMessage
		for _, p := range m.Parts {
			if p.Kind != session.PartToolUse {
				continue
			}
			target := extractTarget(p.ToolInput)
			raw := ""
			isError := false
			if res, ok := inlineResults[p.ToolID]; ok {
				raw = res.Content
				isError = res.IsError
			}
			// Legacy combined-row: always completed or errored (no in-flight state).
			legacyStatus := components.ToolStatusCompleted
			if isError {
				legacyStatus = components.ToolStatusError
			}
			entries = append(entries, uiMessage{
				Role:      components.RoleTool,
				ToolName:  p.ToolName,
				ToolUseID: p.ToolID,
				Target:    target,
				Raw:       raw,
				IsError:   isError,
				Status:    legacyStatus,
			})
		}
		return entries

	case session.RoleSystem:
		text := firstTextPart(m.Parts)
		if text == "" {
			return nil
		}
		return []uiMessage{{Role: components.RoleSystem, Raw: text}}

	default:
		return nil
	}
}

// uiEntryFromStoreMessage is a compatibility shim retained for test
// coverage of the single-entry path.  It delegates to
// uiEntriesFromStoreMessage with an empty result index (covers the legacy
// combined-row shape where both PartToolUse and PartToolResult appear in
// the same message row) and returns the first entry, or (zero, false) when
// empty.
func uiEntryFromStoreMessage(m *session.Message) (uiMessage, bool) {
	empty := map[string]session.Part{}
	emptyIDs := map[string]struct{}{}
	entries := uiEntriesFromStoreMessage(m, empty, emptyIDs)
	if len(entries) == 0 {
		return uiMessage{}, false
	}
	return entries[0], true
}

// firstTextPart returns the Text of the first PartText part in parts, or "".
func firstTextPart(parts []session.Part) string {
	for _, p := range parts {
		if p.Kind == session.PartText {
			return p.Text
		}
	}
	return ""
}

// applyDeleteSession soft-deletes a session.  If it was the foreground,
// the App switches to the most recent other primary session (creating a
