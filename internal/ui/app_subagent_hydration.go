package ui

import (
	"log/slog"
	"strings"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
)

// reconstructSubagentState finds child subagent sessions for sid and
// populates a.subagents + stamps SubagentID on the parent task UIMessages.
// msgs is the already-built message list for sid (modified in place via
// the a.messages slice reference for the primary session).
func (a *App) reconstructSubagentState(parentSID string, msgs []uiMessage, visited map[string]struct{}) {
	if a.opts.Store == nil {
		return
	}

	// List all KindSubagent sessions for this parent.
	childSessions, err := a.opts.Store.ListSessions(a.ctx, session.ListOpts{
		ParentID: parentSID,
		Kind:     session.KindSubagent,
	})
	if err != nil {
		slog.Warn("ui: reconstructSubagentState: failed to list child sessions",
			"parent_id", parentSID, "err", err)
		return
	}
	if len(childSessions) == 0 {
		return
	}

	// Build a map from ToolUseID → child session. New rows use the explicit
	// parent_tool_use_id column; legacy rows fall back to parsing the slug format
	// that older builds wrote: "<type>: <description> [<toolUseID>]".
	// We also keep an ordered slice of children for fallback matching.
	toolUseToChild := make(map[string]*session.Session, len(childSessions))
	for _, cs := range childSessions {
		toolUseID := cs.ParentToolUseID
		if toolUseID == "" {
			toolUseID = extractToolUseIDFromSlug(cs.Slug)
		}
		if toolUseID != "" {
			toolUseToChild[toolUseID] = cs
		}
	}

	// Walk msgs (the already-built flat list for this parent) and match
	// task tool entries to child sessions.  We work on the caller's slice
	// by walking a.messages when parentSID is the primary session, or
	// directly on msgs otherwise.  Since hydrateSessionMessages returns
	// the slice and the caller either assigns it to a.messages or uses it
	// directly, we pass a pointer-slice approach: stamp SubagentID on the
	// returned slice, which the caller then stores.
	//
	// For the primary session path, msgs and a.messages will be the same
	// slice after hydrateMessagesFromStore assigns them — but we're still
	// building msgs here, so we work on msgs.
	matched := make(map[string]bool, len(childSessions))
	for i := range msgs {
		msg := &msgs[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		cs, ok := toolUseToChild[msg.ToolUseID]
		if !ok || msg.ToolUseID == "" {
			continue
		}
		matched[cs.ID] = true
		a.buildSubagentState(cs, msg, visited)
	}

	// Collect truly unmatched children (those not matched by ToolUseID).
	var unmatchedChildren []*session.Session
	for _, cs := range childSessions {
		if !matched[cs.ID] {
			unmatchedChildren = append(unmatchedChildren, cs)
		}
	}

	// Fallback: match remaining children to unclaimed task messages in
	// order of session creation time.  Walk msgs again for unclaimed task
	// entries.
	childIdx := 0
	for i := range msgs {
		if childIdx >= len(unmatchedChildren) {
			break
		}
		msg := &msgs[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		if msg.SubagentID != "" {
			continue // already claimed
		}
		cs := unmatchedChildren[childIdx]
		childIdx++
		slog.Warn("ui: reconstructSubagentState: falling back to order-based matching",
			"parent_id", parentSID, "child_id", cs.ID)
		a.buildSubagentState(cs, msg, visited)
	}

	// Any remaining unmatched children have no corresponding task message
	// (e.g. the message was deleted).  Register them without an anchor.
	for ; childIdx < len(unmatchedChildren); childIdx++ {
		cs := unmatchedChildren[childIdx]
		childMsgs := a.hydrateSessionMessages(cs.ID, visited)
		state := a.buildSubagentStateFromSession(cs, childMsgs)
		a.subagents[cs.ID] = state
	}
}

// buildSubagentState hydrates the child session cs, creates a SubagentState,
// registers it in a.subagents, and stamps SubagentID on msg.
func (a *App) buildSubagentState(cs *session.Session, msg *uiMessage, visited map[string]struct{}) {
	childMsgs := a.hydrateSessionMessages(cs.ID, visited)
	state := a.buildSubagentStateFromSession(cs, childMsgs)
	a.subagents[cs.ID] = state
	msg.SubagentID = cs.ID
}

// buildSubagentStateFromSession constructs a SubagentState from a session row
// and a pre-hydrated message list.
func (a *App) buildSubagentStateFromSession(cs *session.Session, msgs []uiMessage) *components.SubagentState {
	endedAt := cs.UpdatedAt
	if endedAt.IsZero() {
		endedAt = cs.CreatedAt
	}
	// Parse type/description from slug: "<type>: <description> [toolID]"
	agentType, description := parseTypeDescFromSlug(cs.Slug)

	// Patch assistant messages to use the subagent's type name and color
	// instead of the parent session's active mode (which the generic
	// hydration path stamps).
	subColor := components.ColorForSubagentType(agentType)
	model := cs.Model.Provider + "/" + cs.Model.Name
	for i := range msgs {
		if msgs[i].Role == components.RoleAssistant {
			msgs[i].AgentType = agentType
			msgs[i].ModelName = model
			msgs[i].ModeColor = nil
			msgs[i].SubagentColor = subColor
		}
	}

	state := &components.SubagentState{
		SubSessionID:    cs.ID,
		ParentSessionID: cs.ParentID,
		ParentMessageID: cs.ParentToolUseID,
		Type:            agentType,
		Description:     description,
		Model:           model,
		StartedAt:       cs.CreatedAt,
		EndedAt:         endedAt, // completed on resume
		Cost:            cs.Totals.CostUSD,
		InputTokens:     cs.Totals.InputTokens,
		OutputTokens:    cs.Totals.OutputTokens,
		Messages:        msgs,
		Expanded:        false,
	}
	return state
}

// extractToolUseIDFromSlug parses the ToolUseID from a subagent session slug.
// The slug format produced by buildSlug is: "<type>: <description> [<toolUseID>]"
// or "<type> [<toolUseID>]" when no description.  Returns "" if not present.
func extractToolUseIDFromSlug(slug string) string {
	// Find the last "[" ... "]" bracketed segment.
	last := strings.LastIndex(slug, "[")
	if last < 0 {
		return ""
	}
	rest := slug[last+1:]
	before, _, ok := strings.Cut(rest, "]")
	if !ok {
		return ""
	}
	return before
}

// parseTypeDescFromSlug extracts the type and description from a subagent
// session slug.  Format: "<type>: <description> [<toolUseID>]".
// Returns ("", "") when the slug is empty or doesn't match.
func parseTypeDescFromSlug(slug string) (agentType, description string) {
	// Strip trailing " [toolUseID]" if present.
	if last := strings.LastIndex(slug, " ["); last >= 0 {
		slug = slug[:last]
	}
	// Split on ": " to separate type from description.
	parts := strings.SplitN(slug, ": ", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return slug, ""
}
