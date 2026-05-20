// Package components contains the bubbletea sub-views that compose the App.
//
// Each file in this package exports one component as a simple struct with a
// View(...) method.  The components are intentionally NOT tea.Model
// implementations: the App owns the state machine; components are pure
// presentation layers.
package components

import (
	"encoding/json"
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/components/anim"
	"github.com/cfbender/hygge/internal/ui/components/bubble"
	"github.com/cfbender/hygge/internal/ui/styles"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// ToolStatus is the execution lifecycle state of a single tool call.
// Only meaningful on UIMessage entries where Role == RoleTool.
type ToolStatus int

const (
	// ToolStatusUnknown is the zero value; no status text is rendered.
	// Used for hydrated orphan tool-use entries (tool_use with no matching
	// result — interrupted run). Should not occur in well-formed sessions.
	ToolStatusUnknown ToolStatus = iota
	// ToolStatusPending means the agent requested the tool call; no permission
	// decision has been asked yet. Rarely visible — transitions quickly.
	ToolStatusPending
	// ToolStatusAwaitingPermission means the permission engine published a
	// PermissionAsked event; the user has not yet responded. The modal is
	// visible; the inline row shows "Requesting permission…".
	ToolStatusAwaitingPermission
	// ToolStatusRunning means permission was granted (or not required); the tool
	// is executing. The inline row shows "Waiting for tool response…".
	ToolStatusRunning
	// ToolStatusCompleted means the tool finished without error. No status text.
	ToolStatusCompleted
	// ToolStatusError means the tool finished with an error. Inline row shows "error".
	ToolStatusError
	// ToolStatusCancelled means permission was denied or the user cancelled.
	// Inline row shows "cancelled".
	ToolStatusCancelled
)

// MessageRole is the participant role for a rendered message.
type MessageRole string

// Recognised roles for rendering purposes.  Mirrors session.Role but kept
// separate so the components package does not import session.
const (
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
	RoleSystem    MessageRole = "system"
	// RoleMarker renders a prominent banner-style section break produced
	// by a compaction event.  It shows the summary and tokens-saved count.
	RoleMarker MessageRole = "marker"
)

// UIMessage is one entry in the conversation view.
//
// FinalMarkdown is the glamour-rendered output. Assistant messages populate it
// while streaming and again on completion so the body does not reflow when the
// stream finalizes.
type UIMessage struct {
	Role      MessageRole
	ToolName  string // populated for RoleTool
	ToolUseID string // optional provider-assigned tool_use_id; lets the
	// view correlate this message with a SubagentState
	Target        string          // optional tool target hint (path/cmd)
	ToolArgs      json.RawMessage // raw tool arguments for rich rendering
	Raw           string          // raw text (streaming buffer or plain content)
	FinalMarkdown string          // cached glamour output once streaming completes
	IsStreaming   bool
	IsError       bool // tool result error flag
	// Status is the execution lifecycle state for RoleTool messages.
	// Set by handleBusEvent transitions; hydrated from persisted state.
	Status ToolStatus
	// SubagentID is the SubSessionID of a sub-agent dispatched by this
	// message.  When non-empty and the matching SubagentState is in
	// MessageList.Subagents, the view renders a nested block under this
	// message.  Set on the parent `task` tool UIMessage when
	// bus.SubagentStarted arrives.
	SubagentID string

	// MarkerSummary is the post-compaction context summary, populated on
	// RoleMarker messages.
	MarkerSummary string
	// MarkerTokensSaved is the number of input tokens saved by the
	// compaction, populated on RoleMarker messages.
	MarkerTokensSaved int64

	// Timestamp is the wall-clock time the message was created.
	// Populated for RoleUser and RoleAssistant messages.
	Timestamp time.Time

	// Thinking holds the assistant's reasoning content (inline thinking).
	// Populated for RoleAssistant messages that carry thinking blocks.
	// Rendered in muted italic style at the top of the assistant bubble.
	Thinking string

	// OutputTokens is the number of output tokens for an assistant message.
	// Zero while streaming or when the provider did not report usage.
	OutputTokens int64

	// CostUSD is the per-message cost in USD for an assistant message.
	// Zero while streaming or when cost data is unavailable.
	CostUSD float64

	// DurationMs is the wall-clock elapsed milliseconds for an assistant message.
	// Zero while streaming.
	DurationMs int64

	// AgentType is the agent identity label for the assistant bubble header-left.
	// Defaults to "General" when empty.
	AgentType string

	// ModelName is the model name for the assistant bubble header-right metadata.
	ModelName string

	// ModeColor is the per-mode accent color for the bubble border/header.
	// When non-nil, overrides the theme's default agent border color.
	ModeColor color.Color

	// SubagentColor is a deterministic accent color for subagent bubbles.
	// Derived from the subagent type name. When non-nil, used for both
	// the sidebar bar and header text.
	SubagentColor color.Color

	// VisibleRaw is the streaming assistant text that has been revealed by the
	// typing animation. Raw keeps the full accumulated provider text; VisibleRaw
	// lags behind it while IsStreaming is true so new text can animate in.
	VisibleRaw string
}

// MessageList renders the conversation history.
//
// Width is the terminal width; the gutter (`▌user`, etc.) is prepended to the
// first line of each message.  Tool result blocks are collapsed to the first
// CollapseLines lines, with a hint when the rest is hidden.
//
// Subagents, when populated, is a map from sub-session id to the rendering
// state of an in-flight or completed sub-agent.  Messages whose SubagentID
// is a key in this map get a nested SubagentBlock rendered under them.
type MessageList struct {
	Width         int
	CollapseLines int // 0 → 8 (tool result collapse threshold)
	Theme         *theme.Theme
	Styles        *styles.Styles
	Messages      []UIMessage
	Subagents     map[string]*SubagentState
	// AnimFor, when non-nil, maps SubSessionID to the running Anim for
	// that sub-agent.  Passed through to SubagentBlock so the running
	// state can display the animated spinner.
	AnimFor map[string]*anim.Anim
	// Now is the wall-clock to use for elapsed-time math inside
	// nested SubagentBlocks.  Zero means time.Now (production
	// path); tests override it for deterministic output.
	Now time.Time

	// HoverSubagentID is the subagent currently under the mouse cursor.
	// When set, that subagent's bubble renders with highlight styling.
	HoverSubagentID string

	// ExpandedTools is the set of ToolUseIDs whose output is fully expanded
	// (not truncated). Nil means all tools are collapsed.
	ExpandedTools map[string]bool
}

// now returns the reference time for relative timestamps.
func (m MessageList) now() time.Time {
	if m.Now.IsZero() {
		return time.Now()
	}
	return m.Now
}

// relativeTimestamp formats a timestamp relative to now:
//   - Same hour: "3 minutes ago"
//   - Same day, different hour: "4:50 PM"
//   - Different day: "01/15/2026 - 4:50 PM"
func relativeTimestamp(t, now time.Time) string {
	t = t.Local()
	now = now.Local()

	tY, tM, tD := t.Date()
	nY, nM, nD := now.Date()

	if tY == nY && tM == nM && tD == nD {
		// Same day.
		mins := int(now.Sub(t).Minutes())
		if mins < 1 {
			return "just now"
		}
		if mins == 1 {
			return "1 minute ago"
		}
		if mins < 60 {
			return fmt.Sprintf("%d minutes ago", mins)
		}
		// Over an hour ago — show time only.
		return t.Format("3:04 PM")
	}
	// Different day — full timestamp.
	return t.Format("01/02/2006 - 3:04 PM")
}

// renderChunkKind labels the kind of a render chunk built by the View pre-pass.
type renderChunkKind int

const (
	chunkSingle    renderChunkKind = iota // one UIMessage → rendered by renderOne
	chunkToolGroup                        // consecutive non-task RoleTool entries → grouped bubble
)

// renderChunk is one item in the pre-pass output slice.
type renderChunk struct {
	kind   renderChunkKind
	single UIMessage   // valid when kind == chunkSingle
	group  []UIMessage // valid when kind == chunkToolGroup
}

// emptyStateMaxWidth is the column width of the empty-state panel used when
// no actual width is known.
const emptyStateMaxWidth = 80

// renderEmptyState returns no chat content. The full idle splash is owned by
// the App because it embeds the real prompt input; once the user starts typing,
// the empty chat area should stay blank instead of showing a second splash.
func (m MessageList) renderEmptyState() string {
	return ""
}

// thinkingMaxLines is the maximum number of lines to show in the thinking
// section before appending a truncation indicator.
const thinkingMaxLines = 8

// truncateThinking caps thinking text to thinkingMaxLines lines.
// When truncation occurs a faint "… +N more lines (thinking)" indicator is
// appended.  Returns the original string unchanged when it fits.
func (m MessageList) truncateThinking(thinking string) string {
	lines := strings.Split(thinking, "\n")
	if len(lines) <= thinkingMaxLines {
		return thinking
	}
	visible := lines[:thinkingMaxLines-1]
	extra := len(lines) - (thinkingMaxLines - 1)

	indicator := "… +" + itoa(extra) + " more lines (thinking)"
	var indicatorStyle lipgloss.Style
	if m.Theme != nil {
		indicatorStyle = m.Theme.Style(theme.AtomBubbleBodyMuted).Faint(true)
	} else {
		indicatorStyle = lipgloss.NewStyle().Faint(true)
	}
	return strings.Join(visible, "\n") + "\n" + indicatorStyle.Render(indicator)
}

// ToolHitZone maps a range of content lines to a tool use ID for click-to-expand.
type ToolHitZone struct {
	StartLine int
	EndLine   int
	ToolUseID string
	partIndex int
}

// SubagentHitZone maps a range of content lines to a subagent session ID.
type SubagentHitZone struct {
	StartLine    int // inclusive, relative to message list content
	EndLine      int // exclusive
	SubSessionID string
	partIndex    int // internal: index into parts array during construction
}

// View renders all messages joined with a blank line between them.
// The pre-pass groups consecutive compact RoleTool entries into a single
// tool-calls bubble. Bash, edit, and write tools render as standalone tool
// blocks so expandable output and large file diffs have their own hit zone.
func (m MessageList) View() string {
	content, _, _ := m.ViewWithHitZones()
	return content
}

// ViewWithHitZones renders all messages and returns both the rendered content
// and the line ranges of clickable subagent bubbles.
func (m MessageList) ViewWithHitZones() (string, []SubagentHitZone, []ToolHitZone) {
	if len(m.Messages) == 0 {
		return m.renderEmptyState(), nil, nil
	}
	collapseLimit := m.CollapseLines
	if collapseLimit <= 0 {
		collapseLimit = 8
	}

	chunks := m.buildChunks()

	var parts []string
	var zones []SubagentHitZone
	var toolZones []ToolHitZone

	for _, chunk := range chunks {
		var text string
		var subID string
		switch chunk.kind {
		case chunkToolGroup:
			text = m.renderToolGroup(chunk.group)
			// Track standalone tool blocks for click-to-expand.
			for _, msg := range chunk.group {
				if msg.ToolUseID != "" && isExpandableToolBlock(msg) {
					toolZones = append(toolZones, ToolHitZone{
						ToolUseID: msg.ToolUseID,
						partIndex: len(parts),
					})
				}
			}
		default:
			text = m.renderOne(chunk.single, collapseLimit)
			if chunk.single.Role == RoleTool && chunk.single.ToolName == "subagent" && chunk.single.SubagentID != "" {
				subID = chunk.single.SubagentID
			}
		}
		if text == "" {
			continue
		}
		parts = append(parts, text)
		if subID != "" {
			zones = append(zones, SubagentHitZone{
				SubSessionID: subID,
				partIndex:    len(parts) - 1,
			})
		}
	}

	joined := strings.Join(parts, "\n\n")

	// Walk the joined string to compute actual line offsets for each zone.
	if len(zones) > 0 || len(toolZones) > 0 {
		line := 0
		for i, part := range parts {
			partLines := strings.Count(part, "\n") + 1
			for zi := range zones {
				if zones[zi].partIndex == i {
					zones[zi].StartLine = line
					zones[zi].EndLine = line + partLines
				}
			}
			for zi := range toolZones {
				if toolZones[zi].partIndex == i {
					toolZones[zi].StartLine = line
					toolZones[zi].EndLine = line + partLines
				}
			}
			line += partLines
			if i < len(parts)-1 {
				line++
			}
		}
	}

	return joined, zones, toolZones
}

// buildChunks walks m.Messages and produces a slice of renderChunks.
// Consecutive compact RoleTool entries are folded into a chunkToolGroup.
// Bash, edit, and write tools become one-item chunkToolGroups, and subagent
// tool calls plus all other roles become chunkSingle entries.
func (m MessageList) buildChunks() []renderChunk {
	chunks := make([]renderChunk, 0, len(m.Messages))
	i := 0
	for i < len(m.Messages) {
		msg := m.Messages[i]
		if isStandaloneToolBlock(msg) {
			chunks = append(chunks, renderChunk{
				kind:  chunkToolGroup,
				group: m.Messages[i : i+1],
			})
			i++
		} else if isCompactToolBlock(msg) {
			// Collect run of consecutive compact tool calls.
			j := i + 1
			for j < len(m.Messages) && isCompactToolBlock(m.Messages[j]) {
				j++
			}
			chunks = append(chunks, renderChunk{
				kind:  chunkToolGroup,
				group: m.Messages[i:j],
			})
			i = j
		} else {
			chunks = append(chunks, renderChunk{
				kind:   chunkSingle,
				single: msg,
			})
			i++
		}
	}
	return chunks
}

// isCompactToolBlock reports whether msg is a RoleTool entry that can share a
// grouped tool bubble with adjacent compact tools.
func isCompactToolBlock(msg UIMessage) bool {
	return msg.Role == RoleTool && msg.ToolName != "subagent" && !isStandaloneToolBlock(msg)
}

// isStandaloneToolBlock reports whether a tool should always render in its own
// tool bubble. These tools can carry expandable output or file diffs where a
// shared block makes click targeting ambiguous.
func isStandaloneToolBlock(msg UIMessage) bool {
	if msg.Role != RoleTool {
		return false
	}
	switch strings.ToLower(msg.ToolName) {
	case "bash", "edit", "write":
		return true
	default:
		return false
	}
}

func isExpandableToolBlock(msg UIMessage) bool {
	switch strings.ToLower(msg.ToolName) {
	case "bash", "edit", "write":
		return true
	default:
		return false
	}
}

// renderOne renders a single message with its gutter, plus any nested
// subagent block bound to it.
func (m MessageList) renderOne(msg UIMessage, collapseLimit int) string {
	// RoleUser: right-aligned chat bubble with timestamp header.
	if msg.Role == RoleUser {
		return m.renderUserBubble(msg)
	}

	// RoleAssistant: left-aligned chat bubble with agent/model/metadata header
	// and optional inline thinking above the response body.
	if msg.Role == RoleAssistant {
		return m.renderAssistantBubble(msg)
	}

	// RoleMarker: prominent banner-style compaction section break.
	if msg.Role == RoleMarker {
		return m.renderMarker(msg)
	}

	// subagent tool call with a bound subagent: wrap the SubagentBlock in a
	// distinct bubble container.  No "▌tool: subagent" gutter row.
	if msg.Role == RoleTool && msg.ToolName == "subagent" && msg.SubagentID != "" {
		if nested := m.nestedFor(msg); nested != "" {
			return m.wrapSubagentBubble(nested, msg.SubagentID == m.HoverSubagentID)
		}
		// SubagentID set but no matching state yet (edge case during hydration):
		// fall through to the normal gutter render so nothing is lost.
	}

	gutter := m.gutter(msg)

	body := msg.Raw
	if !msg.IsStreaming && msg.FinalMarkdown != "" {
		body = msg.FinalMarkdown
	}
	body = strings.TrimRight(body, "\n")

	// Tool result truncation: only applies when the message is a tool with
	// non-streaming, plain content (no markdown).
	if msg.Role == RoleTool {
		expanded := m.ExpandedTools != nil && m.ExpandedTools[msg.ToolUseID]
		if !expanded {
			body = m.collapseToolBody(body, collapseLimit)
		} else {
			body = m.muted().Render(body)
		}
	}

	rendered := gutter + "\n" + body

	if nested := m.nestedFor(msg); nested != "" {
		rendered += "\n" + nested
	}
	return rendered
}

// renderUserBubble renders a RoleUser message as a right-aligned chat bubble.
func (m MessageList) renderUserBubble(msg UIMessage) string {
	width := m.Width
	if width <= 0 {
		width = 80
	}

	// BubbleWidth: 80% of available width.  No min/max cap — the caller
	// provides a sensible width (≥30).  Edge-case fallback for very narrow
	// terminals: leave at least 4 cells of gutter.
	bubbleW := int(float64(width) * 0.80)
	if width < 30 {
		bubbleW = width - 4
	}
	if bubbleW < 1 {
		bubbleW = 1
	}

	// Body: prefer FinalMarkdown when not streaming; Raw otherwise.
	body := msg.Raw
	if !msg.IsStreaming && msg.FinalMarkdown != "" {
		body = msg.FinalMarkdown
	}
	body = strings.TrimRight(body, "\n")
	if m.Theme != nil {
		body = HighlightMentions(body, m.Theme.Style(theme.AtomAccent))
	}

	// Header-right: relative timestamp.
	headerRight := ""
	if !msg.Timestamp.IsZero() {
		headerRight = relativeTimestamp(msg.Timestamp, m.now())
	}

	var accentColor color.Color
	if m.Styles != nil && m.Styles.UserAccent != nil {
		accentColor = m.Styles.UserAccent
	} else if m.Theme != nil {
		fg := m.Theme.Style(theme.AtomBubbleUserBorder).GetForeground()
		if _, isNoColor := fg.(lipgloss.NoColor); fg != nil && !isNoColor {
			accentColor = fg
		}
	}

	b := bubble.Bubble{
		Width:           width,
		BubbleWidth:     bubbleW,
		Alignment:       bubble.AlignRight,
		HeaderLeft:      "",
		HeaderRight:     headerRight,
		Body:            body,
		Theme:           m.Theme,
		AccentColor:     accentColor,
		BackgroundColor: m.bubbleBackgroundColor(),
		SubStyle:        bubble.StyleNormal,
	}
	return b.View()
}

// renderAssistantBubble renders a RoleAssistant message as a left-aligned
// chat bubble with optional inline thinking in muted italic style.
// Returns "" when both Thinking and body are empty (skips empty bubbles).
func (m MessageList) renderAssistantBubble(msg UIMessage) string {
	width := m.Width
	if width <= 0 {
		width = 80
	}

	// BubbleWidth: 80% of available width.  No min/max cap — same logic as
	// renderUserBubble.
	bubbleW := int(float64(width) * 0.80)
	if width < 30 {
		bubbleW = width - 4
	}
	if bubbleW < 1 {
		bubbleW = 1
	}

	// Body: prefer rendered markdown when available, including during streaming,
	// so completion does not cause a visible raw→glamour layout jump. Streaming
	// messages render FinalMarkdown from VisibleRaw, not the full buffered Raw.
	rawBody := msg.Raw
	if msg.IsStreaming {
		rawBody = msg.VisibleRaw
	}
	if msg.FinalMarkdown != "" {
		rawBody = msg.FinalMarkdown
	}
	rawBody = strings.TrimRight(rawBody, "\n")
	thinking := strings.TrimRight(msg.Thinking, "\n")

	// Skip empty-bubble case: assistant turn with only tool_use, no text/thinking.
	if thinking == "" && rawBody == "" {
		return ""
	}

	// Compose body: thinking (muted italic) + blank line + response text.
	var bodyParts []string
	if thinking != "" {
		// Apply max-height cap before rendering.
		thinking = m.truncateThinking(thinking)
		// Render thinking in muted italic style.
		var thinkStyle lipgloss.Style
		if m.Theme != nil {
			thinkStyle = m.Theme.Style(theme.AtomBubbleBodyMuted).Italic(true)
		} else {
			thinkStyle = lipgloss.NewStyle().Faint(true).Italic(true)
		}
		bodyParts = append(bodyParts, thinkStyle.Render(thinking))
	}
	if rawBody != "" {
		bodyParts = append(bodyParts, rawBody)
	}
	body := strings.Join(bodyParts, "\n\n")

	// Header-left: agent type.
	agentType := msg.AgentType
	if agentType == "" {
		agentType = "General"
	}

	// Header-right: model · tokens · cost · duration (omit during streaming).
	var headerRightParts []string
	modelName := msg.ModelName
	if modelName != "" {
		headerRightParts = append(headerRightParts, modelName)
	}
	if !msg.IsStreaming {
		if msg.OutputTokens > 0 {
			headerRightParts = append(headerRightParts, fmt.Sprintf("%d tokens", msg.OutputTokens))
		}
		if msg.CostUSD > 0 {
			headerRightParts = append(headerRightParts, fmt.Sprintf("$%.4f", msg.CostUSD))
		}
		if msg.DurationMs > 0 {
			if msg.DurationMs >= 1000 {
				headerRightParts = append(headerRightParts, fmt.Sprintf("%ds", msg.DurationMs/1000))
			} else {
				headerRightParts = append(headerRightParts, fmt.Sprintf("%dms", msg.DurationMs))
			}
		}
	}
	headerRight := strings.Join(headerRightParts, " · ")

	accentColor := m.agentAccentColor(msg)

	b := bubble.Bubble{
		Width:           width,
		BubbleWidth:     bubbleW,
		Alignment:       bubble.AlignLeft,
		HeaderLeft:      agentType,
		HeaderRight:     headerRight,
		Body:            body,
		Theme:           m.Theme,
		AccentColor:     accentColor,
		BackgroundColor: m.bubbleBackgroundColor(),
		SubStyle:        bubble.StyleNormal,
		HeaderLeftColor: accentColor,
	}
	return b.View()
}

// renderMarker renders a RoleMarker message as a prominent banner-style
// compaction section break.  Shows the tokens saved and the full summary text.
func (m MessageList) renderMarker(msg UIMessage) string {
	width := m.Width
	if width <= 0 {
		width = emptyStateMaxWidth
	}
	innerW := max(width-4, 1) // rounded border (2) + horizontal padding (2)
	if msg.IsStreaming {
		return m.renderWorkingMarker(msg, innerW)
	}

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerW + 2)
	labelStyle := lipgloss.NewStyle().Bold(true)
	bodyStyle := lipgloss.NewStyle().Width(innerW)
	if m.Theme != nil {
		borderStyle = borderStyle.
			BorderForeground(m.Theme.Style(theme.AtomWarn).GetForeground())
		labelStyle = m.Theme.Style(theme.AtomWarn).Bold(true)
		bodyStyle = m.Theme.Style(theme.AtomMuted).Width(innerW)
	}

	header := fmt.Sprintf("── compacted · %s saved ──", formatTokensSaved(msg.MarkerTokensSaved))
	if lipgloss.Width(header) > innerW {
		header = truncate(header, innerW)
	}
	body := msg.MarkerSummary
	if body == "" {
		body = "(no summary)"
	}

	inner := labelStyle.Render(header) + "\n" + bodyStyle.Render(body)
	return borderStyle.Render(inner)
}

func (m MessageList) renderWorkingMarker(msg UIMessage, innerW int) string {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1).
		Width(innerW + 2)
	labelStyle := lipgloss.NewStyle().Bold(true)
	bodyStyle := lipgloss.NewStyle().Width(innerW)
	animStyle := lipgloss.NewStyle().Width(innerW)
	if m.Theme != nil {
		borderStyle = borderStyle.BorderForeground(m.Theme.Style(theme.AtomAccent).GetForeground())
		labelStyle = m.Theme.Style(theme.AtomAccent).Bold(true)
		bodyStyle = m.Theme.Style(theme.AtomMuted).Width(innerW)
		animStyle = m.Theme.Style(theme.AtomWarn).Width(innerW)
	}

	header := "── compaction · crunching ──"
	if lipgloss.Width(header) > innerW {
		header = truncate(header, innerW)
	}
	body := msg.MarkerSummary
	if body == "" {
		body = "Crunching conversation history into a compact context summary…"
	}
	frame := msg.Raw
	if frame == "" {
		frame = "▰▰▰▱▱▱"
	}
	inner := labelStyle.Render(header) + "\n" + bodyStyle.Render(body) + "\n" + animStyle.Render(frame)
	return borderStyle.Render(inner)
}

// formatTokensSaved renders an integer token count compactly for the
// compaction marker banner (e.g. 0, 1.2k, 5.8M).
func formatTokensSaved(n int64) string {
	if n < 0 {
		n = 0
	}
	switch {
	case n < 1000:
		return fmt.Sprintf("%d tokens", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk tokens", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM tokens", float64(n)/1_000_000)
	}
}

// toolStatusText returns the inline status label for a tool row, or "" when
// no text should be shown (Pending, Completed, Unknown).
func toolStatusText(s ToolStatus, t *theme.Theme) string {
	var muted, errStyle lipgloss.Style
	if t != nil {
		muted = t.Style(theme.AtomBubbleBodyMuted).Faint(true).Italic(true)
		errStyle = t.Style(theme.AtomError).Faint(true)
	} else {
		muted = lipgloss.NewStyle().Faint(true).Italic(true)
		errStyle = lipgloss.NewStyle().Faint(true)
	}
	switch s {
	case ToolStatusAwaitingPermission:
		return muted.Render("Requesting permission…")
	case ToolStatusRunning:
		return muted.Render("Waiting for tool response…")
	case ToolStatusError:
		return errStyle.Render("error")
	case ToolStatusCancelled:
		return muted.Render("cancelled")
	default:
		// Pending, Completed, Unknown — no status text.
		return ""
	}
}

// renderToolGroup renders a group of consecutive non-task tool calls as a
// single distinct side-bar bubble. Each tool call occupies one body row:
//
//	· {ToolName} {Target}   [status text]
func (m MessageList) renderToolGroup(items []UIMessage) string {
	if len(items) == 0 {
		return ""
	}

	width := m.Width
	if width <= 0 {
		width = 80
	}
	bubbleW := m.toolBubbleWidth(width)

	// Content width = bubble width minus the side bar and horizontal padding.
	innerW := max(bubbleW-3, 1)

	// Build body: one line per tool call.
	nameStyle := lipgloss.NewStyle()
	if m.Theme != nil {
		nameStyle = m.Theme.Style(theme.AtomPrimary)
	}
	targetStyle := m.muted()

	muted := m.muted()
	collapseLimit := 4

	var rows []string
	for _, msg := range items {
		label := toolGroupLabel(msg, nameStyle, targetStyle)

		statusTxt := toolStatusText(msg.Status, m.Theme)
		if statusTxt == "" && msg.IsError {
			if m.Theme != nil {
				statusTxt = m.Theme.Style(theme.AtomError).Faint(true).Render("error")
			} else {
				statusTxt = lipgloss.NewStyle().Faint(true).Render("error")
			}
		}
		if statusTxt != "" {
			label += " " + statusTxt
		}

		if lipgloss.Width(label) > innerW {
			label = truncateTarget(label, innerW)
		}
		rows = append(rows, label)

		if diff := m.toolDiffPreview(msg, innerW); diff != "" {
			rows = append(rows, "")
			rows = append(rows, diff)
			continue
		}

		// Bash: show command on its own line + collapsed output.
		if msg.ToolName == "bash" || msg.ToolName == "Bash" {
			if cmd := msg.Target; cmd != "" {
				rows = append(rows, "")
				rows = append(rows, muted.Render("$ "+cmd))
			}
			if msg.Raw != "" && msg.Raw != "(running…)" {
				expanded := m.ExpandedTools != nil && m.ExpandedTools[msg.ToolUseID]
				bodyLines := strings.Split(strings.TrimRight(msg.Raw, "\n"), "\n")
				rows = append(rows, "")
				if looksLikeDiff(msg.Raw) {
					maxLines := defaultDiffPreviewLines
					if expanded {
						maxLines = 10_000
					}
					rows = append(rows, DiffView{Raw: msg.Raw, Width: innerW, Theme: m.Theme, MaxLines: maxLines}.View())
					continue
				}
				if expanded {
					for _, line := range bodyLines {
						rows = append(rows, muted.Render(line))
					}
				} else if len(bodyLines) > collapseLimit {
					for _, line := range bodyLines[:collapseLimit] {
						rows = append(rows, muted.Render(line))
					}
					rows = append(rows, muted.Render("…"))
					rows = append(rows, "")
					rows = append(rows, muted.Italic(true).Render("Click to expand"))
				} else {
					for _, line := range bodyLines {
						rows = append(rows, muted.Render(line))
					}
				}
			}
		}
	}
	body := strings.Join(rows, "\n")

	var accentColor color.Color
	if m.Theme != nil {
		fg := m.Theme.Style(theme.AtomBubbleBorderDistinct).GetForeground()
		if _, isNoColor := fg.(lipgloss.NoColor); fg != nil && !isNoColor {
			accentColor = fg
		}
	}

	b := bubble.Bubble{
		Width:           width,
		BubbleWidth:     bubbleW,
		Alignment:       bubble.AlignLeft,
		HeaderLeft:      "",
		HeaderRight:     "",
		Body:            "\n" + body,
		Theme:           m.Theme,
		AccentColor:     accentColor,
		BackgroundColor: m.bubbleBackgroundColor(),
		SubStyle:        bubble.StyleDistinct,
	}
	return b.View()
}

func (m MessageList) toolDiffPreview(msg UIMessage, width int) string {
	if msg.IsError {
		return ""
	}
	if msg.ToolName == "bash" || msg.ToolName == "Bash" {
		return ""
	}
	if !msg.IsStreaming && looksLikeDiff(msg.Raw) {
		expanded := m.ExpandedTools != nil && m.ExpandedTools[msg.ToolUseID]
		maxLines := defaultDiffPreviewLines
		if expanded {
			maxLines = 10_000
		}
		diff := DiffView{Raw: msg.Raw, Width: width, Theme: m.Theme, MaxLines: maxLines}
		out := diff.View()
		if !expanded && diff.IsTruncated() {
			out += "\n" + m.muted().Italic(true).Render("Click to expand")
		}
		return out
	}
	return ""
}

// wrapSubagentBubble wraps existing SubagentBlock content in a distinct side-bar bubble.
// When hovered is true, the accent bar brightens to indicate clickability.
func (m MessageList) wrapSubagentBubble(body string, hovered bool) string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	bubbleW := m.toolBubbleWidth(width)

	var accentColor color.Color
	if m.Theme != nil {
		fg := m.Theme.Style(theme.AtomBubbleBorderDistinct).GetForeground()
		if _, isNoColor := fg.(lipgloss.NoColor); fg != nil && !isNoColor {
			accentColor = fg
		}
	}

	subStyle := bubble.StyleDistinct
	if hovered {
		// Switch from faint/distinct to normal style on hover and use
		// a brighter accent to signal clickability.
		subStyle = bubble.StyleNormal
		if m.Styles != nil {
			accentColor = m.Styles.WorkingLabelColor
		}
	}

	b := bubble.Bubble{
		Width:           width,
		BubbleWidth:     bubbleW,
		Alignment:       bubble.AlignLeft,
		HeaderLeft:      "",
		HeaderRight:     "",
		Body:            "\n" + body, // top padding (bottom handled by composeInner)
		Theme:           m.Theme,
		AccentColor:     accentColor,
		BackgroundColor: m.bubbleBackgroundColor(),
		SubStyle:        subStyle,
	}
	return b.View()
}

// toolBubbleWidth returns the bubble width for tool-group and subagent bubbles:
// 80% of available width.  Edge-case fallback: width-4 when terminal is very narrow.
func (m MessageList) toolBubbleWidth(width int) int {
	w := int(float64(width) * 0.80)
	if width < 30 {
		w = width - 4
	}
	if w < 1 {
		w = 1
	}
	return w
}

// truncateTarget truncates a path/command string to avail rune characters,
// appending "…" at the end when truncation occurs.
func truncateTarget(s string, avail int) string {
	runes := []rune(s)
	if len(runes) <= avail {
		return s
	}
	if avail <= 1 {
		return "…"
	}
	return string(runes[:avail-1]) + "…"
}
func (m MessageList) nestedFor(msg UIMessage) string {
	if msg.SubagentID == "" || m.Subagents == nil {
		return ""
	}
	state, ok := m.Subagents[msg.SubagentID]
	if !ok || state == nil {
		return ""
	}
	var an *anim.Anim
	if m.AnimFor != nil {
		an = m.AnimFor[msg.SubagentID]
	}
	block := SubagentBlock{
		State:   state,
		Width:   m.Width,
		Theme:   m.Theme,
		Now:     m.Now,
		Anim:    an,
		Hovered: msg.SubagentID == m.HoverSubagentID,
	}
	return block.View()
}

// gutter renders a rich tool header line in the style of opencode:
//
//	✱ Grep "pattern" in path (N matches)
//	✱ Bash $ command
//	✱ Read path (N lines)
//	✱ Edit path
//	✱ Glob pattern (N files)
func (m MessageList) gutter(msg UIMessage) string {
	if msg.Role != RoleTool {
		label := "▌" + string(msg.Role)
		style := m.roleStyle(msg.Role)
		return style.Render(label)
	}

	label := "✱ " + capitalize(msg.ToolName)
	args := toolArgsMap(msg.ToolArgs)

	switch msg.ToolName {
	case "grep", "Grep":
		if pat, ok := args["pattern"]; ok {
			label += fmt.Sprintf(" %q", pat)
		}
		if path, ok := args["path"]; ok {
			label += " in " + path
		}
		if !msg.IsStreaming && !msg.IsError && msg.Raw != "" {
			n := strings.Count(msg.Raw, "\n") + 1
			label += fmt.Sprintf(" (%d %s)", n, plural(n, "match", "matches"))
		}
	case "bash", "Bash":
		// Command shown on its own line in the bubble body, not inline.
	case "read", "Read":
		if msg.Target != "" {
			label += " " + msg.Target
		}
		if !msg.IsStreaming && !msg.IsError && msg.Raw != "" {
			n := strings.Count(msg.Raw, "\n") + 1
			label += fmt.Sprintf(" (%d %s)", n, plural(n, "line", "lines"))
		}
	case "glob", "Glob":
		if pat, ok := args["pattern"]; ok {
			label += " " + pat
		}
		if !msg.IsStreaming && !msg.IsError && msg.Raw != "" {
			n := strings.Count(msg.Raw, "\n") + 1
			label += fmt.Sprintf(" (%d %s)", n, plural(n, "file", "files"))
		}
	case "skill", "Skill":
		if name, ok := args["name"]; ok {
			label += " " + name
		}
	case "edit", "Edit", "write", "Write":
		if msg.Target != "" {
			label += " " + msg.Target
		}
	default:
		if msg.Target != "" {
			label += " " + msg.Target
		}
	}

	if msg.IsError {
		label += " — error"
	}

	style := m.roleStyle(msg.Role)
	return style.Render(label)
}

// toolGroupLabel builds a rich label for a tool call inside a tool group bubble.
func toolGroupLabel(msg UIMessage, nameStyle, targetStyle lipgloss.Style) string {
	args := toolArgsMap(msg.ToolArgs)
	name := nameStyle.Render(capitalize(msg.ToolName))

	switch msg.ToolName {
	case "grep", "Grep":
		pat := ""
		if v, ok := args["pattern"]; ok {
			pat = fmt.Sprintf("%q", v)
		}
		path := ""
		if v, ok := args["path"]; ok {
			path = v
		}
		label := "✱ " + name
		if pat != "" {
			label += " " + targetStyle.Render(pat)
		}
		if path != "" {
			label += " in " + targetStyle.Render(path)
		}
		if !msg.IsStreaming && !msg.IsError && msg.Raw != "" {
			n := strings.Count(msg.Raw, "\n") + 1
			label += targetStyle.Render(fmt.Sprintf(" (%d %s)", n, plural(n, "match", "matches")))
		}
		return label
	case "bash", "Bash":
		return "✱ " + name
	case "read", "Read":
		label := "✱ " + name
		if msg.Target != "" {
			label += " " + targetStyle.Render(msg.Target)
		}
		if !msg.IsStreaming && !msg.IsError && msg.Raw != "" {
			n := strings.Count(msg.Raw, "\n") + 1
			label += targetStyle.Render(fmt.Sprintf(" (%d %s)", n, plural(n, "line", "lines")))
		}
		return label
	case "glob", "Glob":
		label := "✱ " + name
		if pat, ok := args["pattern"]; ok {
			label += " " + targetStyle.Render(pat)
		}
		if !msg.IsStreaming && !msg.IsError && msg.Raw != "" {
			n := strings.Count(msg.Raw, "\n") + 1
			label += targetStyle.Render(fmt.Sprintf(" (%d %s)", n, plural(n, "file", "files")))
		}
		return label
	case "skill", "Skill":
		label := "✱ " + name
		if skillName, ok := args["name"]; ok {
			label += " " + targetStyle.Render(skillName)
		}
		return label
	default:
		label := "✱ " + name
		if msg.Target != "" {
			label += " " + targetStyle.Render(msg.Target)
		}
		return label
	}
}

// toolArgsMap decodes raw tool JSON args into a string map for display.
func toolArgsMap(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// agentAccentColor returns the accent color for an assistant bubble.
// Per-mode color takes priority, then subagent color, then theme atom.
func (m MessageList) agentAccentColor(msg UIMessage) color.Color {
	if msg.ModeColor != nil {
		return msg.ModeColor
	}
	if msg.SubagentColor != nil {
		return msg.SubagentColor
	}
	if m.Theme != nil {
		fg := m.Theme.Style(theme.AtomBubbleAgentBorder).GetForeground()
		if _, isNoColor := fg.(lipgloss.NoColor); fg != nil && !isNoColor {
			return fg
		}
	}
	return nil
}

// ColorForSubagentType returns a deterministic color derived from a
// subagent type name. Uses a curated palette of distinct ANSI-256 colors
// that are visible on both dark and light backgrounds.
func ColorForSubagentType(typeName string) color.Color {
	// Curated palette of visually distinct colors (ANSI 256).
	palette := []string{
		"#E06C75", // red
		"#61AFEF", // blue
		"#C678DD", // purple
		"#56B6C2", // cyan
		"#E5C07B", // yellow
		"#98C379", // green
		"#D19A66", // orange
		"#BE5046", // dark red
		"#7EC8E3", // light blue
		"#C991E1", // lavender
	}
	var h uint32
	for _, c := range typeName {
		h = h*31 + uint32(c) //nolint:gosec // overflow is intentional for hash distribution
	}
	return lipgloss.Color(palette[h%uint32(len(palette))]) //nolint:gosec // len(palette) is a small constant
}

func (m MessageList) bubbleBackgroundColor() color.Color {
	if m.Styles != nil {
		return m.Styles.BubbleBg
	}
	if m.Theme == nil {
		return nil
	}
	bg := m.Theme.Style(theme.AtomBubbleBg).GetBackground()
	if _, isNoColor := bg.(lipgloss.NoColor); bg == nil || isNoColor {
		return nil
	}
	return bg
}

// roleStyle returns the lipgloss style for a role gutter.
func (m MessageList) roleStyle(role MessageRole) lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	switch role {
	case RoleUser:
		return m.Theme.Style(theme.AtomPrimary).Bold(true)
	case RoleAssistant:
		return m.Theme.Style(theme.AtomAccent).Bold(true)
	case RoleTool:
		return m.Theme.Style(theme.AtomMuted).Bold(true)
	default:
		return m.Theme.Style(theme.AtomMuted)
	}
}

// muted returns the muted body style for placeholders and tool-content text.
func (m MessageList) muted() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Faint(true)
	}
	return m.Theme.Style(theme.AtomMuted)
}

// collapseToolBody truncates a tool body to the first N lines and appends a
// "[+K more lines, press space to expand]" hint when truncated.  Expansion is
// a v0.2 concern — v0.1 only shows the hint.
func (m MessageList) collapseToolBody(body string, limit int) string {
	muted := m.muted()
	lines := strings.Split(body, "\n")
	if len(lines) <= limit {
		return muted.Render(body)
	}
	head := strings.Join(lines[:limit], "\n")
	hint := muted.Italic(true).Render(
		"[+" + itoa(len(lines)-limit) + " more lines — Ctrl+E to expand]",
	)
	return muted.Render(head) + "\n" + hint
}

// itoa is a tiny strconv.Itoa shim to avoid an extra import.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func plural(n int, singular, pluralForm string) string {
	if n == 1 {
		return singular
	}
	return pluralForm
}
