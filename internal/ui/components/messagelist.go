package components

import (
	"strings"

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
)

// UIMessage is one entry in the conversation view.
//
// FinalMarkdown is the glamour-rendered output, populated once the assistant
// stops streaming.  While IsStreaming is true the View renders Raw verbatim
// (no markdown) for snappy incremental updates.
type UIMessage struct {
	Role          MessageRole
	ToolName      string // populated for RoleTool
	Target        string // optional tool target hint (path/cmd)
	Raw           string // raw text (streaming buffer or plain content)
	FinalMarkdown string // cached glamour output once streaming completes
	IsStreaming   bool
	IsError       bool // tool result error flag
}

// MessageList renders the conversation history.
//
// Width is the terminal width; the gutter (`▌user`, etc.) is prepended to the
// first line of each message.  Tool result blocks are collapsed to the first
// CollapseLines lines, with a hint when the rest is hidden.
type MessageList struct {
	Width         int
	CollapseLines int // 0 → 8 (tool result collapse threshold)
	Theme         *theme.Theme
	Messages      []UIMessage
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

// renderOne renders a single message with its gutter.
func (m MessageList) renderOne(msg UIMessage, collapseLimit int) string {
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

	return gutter + "\n" + body
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
