// Package components contains the bubbletea sub-views that compose the App.
//
// Each file in this package exports one component as a simple struct with a
// View(...) method.  The components are intentionally NOT tea.Model
// implementations: the App owns the state machine; components are pure
// presentation layers.
package components

import (
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
// FinalMarkdown is the glamour-rendered output, populated once the assistant
// stops streaming.  While IsStreaming is true the View renders Raw verbatim
// (no markdown) for snappy incremental updates.
type UIMessage struct {
	Role      MessageRole
	ToolName  string // populated for RoleTool
	ToolUseID string // optional provider-assigned tool_use_id; lets the
	// view correlate this message with a SubagentState
	Target        string // optional tool target hint (path/cmd)
	Raw           string // raw text (streaming buffer or plain content)
	FinalMarkdown string // cached glamour output once streaming completes
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

// renderEmptyState returns the centered welcome message shown when there are
// no messages in the list.
func (m MessageList) renderEmptyState() string {
	width := m.Width
	if width <= 0 {
		width = emptyStateMaxWidth
	}

	var accentStyle, mutedStyle lipgloss.Style
	if m.Theme != nil {
		accentStyle = m.Theme.Style(theme.AtomAccent)
		mutedStyle = m.Theme.Style(theme.AtomMuted)
	} else {
		accentStyle = lipgloss.NewStyle().Faint(true)
		mutedStyle = lipgloss.NewStyle().Faint(true)
	}

	glyph := accentStyle.Bold(true).Render("·hygge·")
	hints := mutedStyle.Render("Type a message to get started.\nctrl+p  commands · ctrl+g  view subagents")

	content := glyph + "\n\n" + hints

	// Center each line horizontally.
	var centeredLines []string
	for _, line := range strings.Split(content, "\n") {
		visW := lipgloss.Width(line)
		pad := (width - visW) / 2
		if pad < 0 {
			pad = 0
		}
		centeredLines = append(centeredLines, strings.Repeat(" ", pad)+line)
	}
	return strings.Join(centeredLines, "\n")
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

// SubagentHitZone maps a range of content lines to a subagent session ID.
type SubagentHitZone struct {
	StartLine    int // inclusive, relative to message list content
	EndLine      int // exclusive
	SubSessionID string
	partIndex    int // internal: index into parts array during construction
}

// View renders all messages joined with a blank line between them.
// The pre-pass groups consecutive non-task RoleTool entries into a single
// tool-calls bubble; task tool calls and all other roles render individually.
func (m MessageList) View() string {
	content, _ := m.ViewWithHitZones()
	return content
}

// ViewWithHitZones renders all messages and returns both the rendered content
// and the line ranges of clickable subagent bubbles.
func (m MessageList) ViewWithHitZones() (string, []SubagentHitZone) {
	if len(m.Messages) == 0 {
		return m.renderEmptyState(), nil
	}
	collapseLimit := m.CollapseLines
	if collapseLimit <= 0 {
		collapseLimit = 8
	}

	chunks := m.buildChunks()

	var parts []string
	var zones []SubagentHitZone

	for _, chunk := range chunks {
		var text string
		var subID string
		switch chunk.kind {
		case chunkToolGroup:
			text = m.renderToolGroup(chunk.group)
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
				// partIndex is filled below after joining
				partIndex: len(parts) - 1,
			})
		}
	}

	joined := strings.Join(parts, "\n\n")

	// Walk the joined string to compute actual line offsets for each zone.
	// Split into lines and find each part boundary.
	if len(zones) > 0 {
		// Build a cumulative line offset table for each part.
		line := 0
		for i, part := range parts {
			partLines := strings.Count(part, "\n") + 1
			for zi := range zones {
				if zones[zi].partIndex == i {
					zones[zi].StartLine = line
					zones[zi].EndLine = line + partLines
				}
			}
			line += partLines
			if i < len(parts)-1 {
				line++ // "\n\n" separator = one blank line
			}
		}
	}

	return joined, zones
}

// buildChunks walks m.Messages and produces a slice of renderChunks.
// Consecutive non-task RoleTool entries are folded into a chunkToolGroup.
// subagent tool calls and all other roles become chunkSingle entries.
func (m MessageList) buildChunks() []renderChunk {
	chunks := make([]renderChunk, 0, len(m.Messages))
	i := 0
	for i < len(m.Messages) {
		msg := m.Messages[i]
		if isNonSubagentTool(msg) {
			// Collect run of consecutive non-subagent tool calls.
			j := i + 1
			for j < len(m.Messages) && isNonSubagentTool(m.Messages[j]) {
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

// isNonSubagentTool reports whether msg is a RoleTool entry that is NOT "subagent".
func isNonSubagentTool(msg UIMessage) bool {
	return msg.Role == RoleTool && msg.ToolName != "subagent"
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
		body = m.collapseToolBody(body, collapseLimit)
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

	// Body: prefer FinalMarkdown when not streaming; Raw otherwise.
	rawBody := msg.Raw
	if !msg.IsStreaming && msg.FinalMarkdown != "" {
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
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	labelStyle := lipgloss.NewStyle().Bold(true)
	bodyStyle := lipgloss.NewStyle()
	if m.Theme != nil {
		borderStyle = borderStyle.
			BorderForeground(m.Theme.Style(theme.AtomWarn).GetForeground())
		labelStyle = m.Theme.Style(theme.AtomWarn).Bold(true)
		bodyStyle = m.Theme.Style(theme.AtomMuted)
	}

	header := fmt.Sprintf("── compacted · %s saved ──", formatTokensSaved(msg.MarkerTokensSaved))
	body := msg.MarkerSummary
	if body == "" {
		body = "(no summary)"
	}

	inner := labelStyle.Render(header) + "\n" + bodyStyle.Render(body)
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
	innerW := bubbleW - 3
	if innerW < 1 {
		innerW = 1
	}

	// Build body: one line per tool call.
	dotStyle := m.muted()
	nameStyle := lipgloss.NewStyle()
	if m.Theme != nil {
		nameStyle = m.Theme.Style(theme.AtomPrimary)
	}
	targetStyle := m.muted()

	var rows []string
	for _, msg := range items {
		dot := dotStyle.Render("·")
		name := nameStyle.Render(msg.ToolName)

		// Plain visible width of "· {name} " prefix (without ANSI).
		prefixVisW := 2 + len(msg.ToolName) + 1 // "· " + name + " "

		// Compute the inline status suffix.  For errored tools that also have
		// a ToolStatus set, prefer the ToolStatus rendering; fall back to the
		// legacy IsError flag when Status is zero/unknown (e.g. hydrated rows
		// that predate the status field).
		statusTxt := toolStatusText(msg.Status, m.Theme)
		if statusTxt == "" && msg.IsError {
			// Legacy path: no Status but IsError is set.
			if m.Theme != nil {
				statusTxt = m.Theme.Style(theme.AtomError).Faint(true).Render("error")
			} else {
				statusTxt = lipgloss.NewStyle().Faint(true).Render("error")
			}
		}

		var row string
		if msg.Target != "" {
			// Truncate target to fit inside innerW: leave room for prefix + status suffix.
			statusLen := lipgloss.Width(statusTxt)
			sepLen := 0
			if statusLen > 0 {
				sepLen = 1 // one space separator
			}
			avail := innerW - prefixVisW - statusLen - sepLen
			if avail < 1 {
				avail = 1
			}
			target := truncateTarget(msg.Target, avail)
			tgt := targetStyle.Render(target)
			row = dot + " " + name + " " + tgt
			if statusTxt != "" {
				row += " " + statusTxt
			}
		} else {
			row = dot + " " + name
			if statusTxt != "" {
				row += " " + statusTxt
			}
		}
		rows = append(rows, row)
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
		Body:            body,
		Theme:           m.Theme,
		AccentColor:     accentColor,
		BackgroundColor: m.bubbleBackgroundColor(),
		SubStyle:        bubble.StyleDistinct,
	}
	return b.View()
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

// gutter renders the `▌user` / `▌assistant` / `▌tool: name (target)` header line.
func (m MessageList) gutter(msg UIMessage) string {
	label := "▌" + string(msg.Role)
	if msg.Role == RoleTool {
		label = "▌tool: " + msg.ToolName
		if msg.Target != "" {
			label += " (" + msg.Target + ")"
		}
		if msg.IsError {
			label += " — error"
		}
	}
	style := m.roleStyle(msg.Role)
	return style.Render(label)
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
		"[+" + itoa(len(lines)-limit) + " more lines, press space to expand]",
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
