package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// SubagentState is the rendering view of one in-flight or completed
// sub-agent invocation.  The App owns the source of truth; this struct
// is a snapshot the message-list renders.
//
// Lifecycle:
//
//   - Created when bus.SubagentStarted arrives.  StartedAt set, EndedAt
//     zero, Messages empty, Expanded false.
//   - Updated as the sub-session's events flow through (text deltas,
//     tool calls, cost updates).  Caller appends to Messages and
//     overwrites Cost / InputTokens / OutputTokens.
//   - Finalised when bus.SubagentCompleted arrives.  EndedAt is set;
//     HitIterLimit toggles the header to "failed".
type SubagentState struct {
	// SubSessionID is the sub-session's id; the map key in App.
	SubSessionID string

	// ParentSessionID is the dispatching session.  Used to filter
	// events when the foreground session changes (a sub-agent of
	// session A should not render in session B's view).
	ParentSessionID string

	// ParentMessageID is the parent's task tool_use_id.  The
	// message-list uses this to find the UIMessage to anchor the
	// nested block under.  Empty when the dispatcher did not
	// supply one.
	ParentMessageID string

	// Type is the sub-agent type name (e.g. "general").
	Type string

	// Description is the short mission label.  Echoed under the
	// header line in italics.
	Description string

	// Model is the resolved provider/model string, used as the
	// label in the collapsed header.
	Model string

	// StartedAt and EndedAt bound the wall-clock elapsed time.
	// EndedAt is the zero value while the sub-agent is still
	// running.
	StartedAt time.Time
	EndedAt   time.Time

	// HitIterLimit is true after a Completed event reports the
	// sub-agent loop hit its iteration cap.  Renders as
	// "failed (iteration limit)" instead of "done".
	HitIterLimit bool

	// Cost / InputTokens / OutputTokens are running totals tagged
	// to the sub-session.  Stage C derives these from
	// bus.CostUpdated events that arrive with the sub-session id;
	// the Completed event's totals override them at the end.
	Cost         float64
	InputTokens  int64
	OutputTokens int64

	// Messages is the streaming buffer of nested sub-messages,
	// in chronological order.  The component renders them with
	// a left gutter when Expanded.
	Messages []UIMessage

	// Expanded toggles between the collapsed one-line header and
	// the full nested transcript view.  Default false (collapsed).
	Expanded bool
}

// IsRunning is true while no Completed event has arrived yet.  Used
// by the App to drive the elapsed-time tick.
func (s *SubagentState) IsRunning() bool {
	return s != nil && s.EndedAt.IsZero()
}

// SubagentBlock renders a single nested sub-agent state.  Width is
// the available column count for the parent message list; Theme is
// the active theme; Now is the wall-clock to use for elapsed-time
// math while the state is still running.
type SubagentBlock struct {
	State *SubagentState
	Width int
	Theme *theme.Theme
	Now   time.Time
}

// View renders the block.  Returns the empty string when State is
// nil.  Layout:
//
//	▸ task[<type>] · <model> · <state> · <elapsed> · <tokens> · <cost>
//	  "<description>"
//
// When Expanded, the description is followed by the indented sub-
// messages joined with a gutter line.
func (b SubagentBlock) View() string {
	if b.State == nil {
		return ""
	}
	muted := b.muted()

	chevron := "▸"
	if b.State.Expanded {
		chevron = "▾"
	}

	state := "running"
	switch {
	case b.State.HitIterLimit:
		state = "failed (iteration limit)"
	case !b.State.EndedAt.IsZero():
		state = "done"
	}

	header := fmt.Sprintf(
		"%s task[%s] · %s · %s · %s · %s · %s",
		chevron,
		b.State.Type,
		b.State.Model,
		state,
		formatElapsed(b.elapsed()),
		formatSubagentTokens(b.State.InputTokens+b.State.OutputTokens),
		formatDollars(b.State.Cost),
	)
	headerStyled := b.headerStyle().Render(header)

	var out strings.Builder
	out.WriteString(headerStyled)

	if b.State.Description != "" {
		out.WriteString("\n  ")
		out.WriteString(muted.Italic(true).Render(`"` + b.State.Description + `"`))
	}

	if b.State.Expanded {
		out.WriteString("\n")
		out.WriteString(b.renderTranscript())
	}

	return out.String()
}

// renderTranscript renders the indented sub-messages.  Each sub-
// message gets a leading "│ " gutter so the nesting reads like a
// GitHub thread.  Empty when no messages have arrived yet.
func (b SubagentBlock) renderTranscript() string {
	muted := b.muted()
	gutter := muted.Render("│")

	if len(b.State.Messages) == 0 {
		// Show a placeholder so the expanded state looks alive
		// even before the first delta arrives.
		return gutter + " " + muted.Italic(true).Render("(no output yet…)")
	}

	var rows []string
	rows = append(rows, gutter)

	for _, m := range b.State.Messages {
		body := strings.TrimRight(renderUIMessageBody(m), "\n")
		if body == "" {
			continue
		}
		// One-line header for the sub-message role.
		var roleLine string
		switch m.Role {
		case RoleAssistant:
			roleLine = muted.Render("assistant:")
		case RoleTool:
			label := "tool " + m.ToolName
			if m.Target != "" {
				label += ": " + m.Target
			}
			if m.IsError {
				label += " — error"
			}
			roleLine = muted.Render(label)
		case RoleUser:
			roleLine = muted.Render("user:")
		case RoleSystem:
			roleLine = muted.Render("system:")
		default:
			roleLine = muted.Render(string(m.Role) + ":")
		}
		rows = append(rows, gutter+" "+roleLine)
		// Indent every body line so the gutter persists.
		for _, line := range strings.Split(body, "\n") {
			rows = append(rows, gutter+" "+line)
		}
		rows = append(rows, gutter)
	}
	// Drop the trailing empty gutter row for tidy output.
	if len(rows) > 0 && rows[len(rows)-1] == gutter {
		rows = rows[:len(rows)-1]
	}
	return strings.Join(rows, "\n")
}

// renderUIMessageBody picks the right field to display for one nested
// sub-message.  Mirrors the primary message renderer but kept simple:
// no markdown rendering in the nested view -- the gutter would
// fight glamour's own indentation anyway.
func renderUIMessageBody(m UIMessage) string {
	if !m.IsStreaming && m.FinalMarkdown != "" {
		return m.FinalMarkdown
	}
	return m.Raw
}

// elapsed returns the wall-clock duration the sub-agent has been
// running (or its final duration once complete).
func (b SubagentBlock) elapsed() time.Duration {
	end := b.State.EndedAt
	if end.IsZero() {
		end = b.Now
		if end.IsZero() {
			end = time.Now()
		}
	}
	d := end.Sub(b.State.StartedAt)
	if d < 0 {
		return 0
	}
	return d
}

// headerStyle returns the lipgloss style for the chevron + label line.
// Running states use the accent atom (alive); completed states use the
// muted atom (settled background detail).
func (b SubagentBlock) headerStyle() lipgloss.Style {
	if b.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	if b.State.IsRunning() {
		return b.Theme.Style(theme.AtomAccent).Bold(true)
	}
	if b.State.HitIterLimit {
		return b.Theme.Style(theme.AtomError).Bold(true)
	}
	return b.Theme.Style(theme.AtomMuted).Bold(true)
}

// muted returns the muted body style used for the gutter and the
// description quote.
func (b SubagentBlock) muted() lipgloss.Style {
	if b.Theme == nil {
		return lipgloss.NewStyle().Faint(true)
	}
	return b.Theme.Style(theme.AtomMuted)
}

// formatElapsed renders a duration compactly: 4.2s, 1m12s, 2h05m.
// Keeps the header line short so the description still fits at
// typical terminal widths.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Second {
		ms := d.Milliseconds()
		return fmt.Sprintf("%dms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		m := int(d / time.Minute)
		s := int((d % time.Minute) / time.Second)
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	return fmt.Sprintf("%dh%02dm", h, m)
}

// formatSubagentTokens renders integer token counts as 0, 24, 1.2k,
// 5.8k, 1.2M with the "tokens" suffix used in the nested header.
// Distinct from footer.formatTokens which uses different rounding
// and no suffix.
func formatSubagentTokens(n int64) string {
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

// formatDollars renders a USD amount with four decimal places (we
// surface sub-cent precision because sub-agents are often very
// short-lived and the rounded value would look identically zero
// for too many of them).
func formatDollars(d float64) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("$%.4f", d)
}
