package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/cfbender/hygge/internal/ui/theme"
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
	// RoleThinking renders assistant reasoning content as always-visible dim
	// italic text preceding the assistant's response.  No expand/collapse.
	RoleThinking MessageRole = "thinking"
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
	Messages      []UIMessage
	Subagents     map[string]*SubagentState
	// Now is the wall-clock to use for elapsed-time math inside
	// nested SubagentBlocks.  Zero means time.Now (production
	// path); tests override it for deterministic output.
	Now time.Time
}

// View renders all messages joined with a blank line between them.
func (m MessageList) View() string {
	if len(m.Messages) == 0 {
		muted := m.muted()
		return muted.Render("(no messages yet — type a prompt below)")
	}
	collapseLimit := m.CollapseLines
	if collapseLimit <= 0 {
		collapseLimit = 8
	}
	var parts []string
	for _, msg := range m.Messages {
		parts = append(parts, m.renderOne(msg, collapseLimit))
	}
	return strings.Join(parts, "\n\n")
}

// renderOne renders a single message with its gutter, plus any nested
// subagent block bound to it.
func (m MessageList) renderOne(msg UIMessage, collapseLimit int) string {
	// RoleThinking: always-visible dim italic text, no gutter header line.
	if msg.Role == RoleThinking {
		var style lipgloss.Style
		if m.Theme != nil {
			style = m.Theme.Style(theme.AtomMuted).Faint(true).Italic(true)
		} else {
			style = lipgloss.NewStyle().Faint(true).Italic(true)
		}
		body := strings.TrimRight(msg.Raw, "\n")
		return style.Render(body)
	}

	// RoleMarker: prominent banner-style compaction section break.
	if msg.Role == RoleMarker {
		return m.renderMarker(msg)
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

// or "" when no block applies.
func (m MessageList) nestedFor(msg UIMessage) string {
	if msg.SubagentID == "" || m.Subagents == nil {
		return ""
	}
	state, ok := m.Subagents[msg.SubagentID]
	if !ok || state == nil {
		return ""
	}
	block := SubagentBlock{
		State: state,
		Width: m.Width,
		Theme: m.Theme,
		Now:   m.Now,
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
	case RoleThinking:
		return m.Theme.Style(theme.AtomMuted).Faint(true).Italic(true)
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
