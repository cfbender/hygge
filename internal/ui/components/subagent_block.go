package components

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/components/anim"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// SubagentState is the rendering view of one in-flight or completed
// sub-agent invocation.  The App owns the source of truth; this struct
// is a snapshot the message-list renders.
//
// Lifecycle:
//
//   - Created when bus.SubagentStarted arrives.  StartedAt set, EndedAt
//     zero, Messages empty.
//   - Updated as the sub-session's events flow through (text deltas,
//     tool calls, cost updates).  Caller appends to Messages and
//     overwrites Cost / InputTokens / OutputTokens.
//   - Finalised when bus.SubagentCompleted arrives. EndedAt is set.
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

	// Description is the short mission label shown in the heading.
	Description string

	// Model is the resolved provider/model string shown in the compact block
	// and detail view accessed via Ctrl+G.
	Model string

	// StartedAt and EndedAt bound the wall-clock elapsed time.
	// EndedAt is the zero value while the sub-agent is still running.
	StartedAt time.Time
	EndedAt   time.Time

	// Cost / InputTokens / OutputTokens are running totals tagged
	// to the sub-session.  Stage C derives these from
	// bus.CostUpdated events that arrive with the sub-session id;
	// the Completed event's totals override them at the end.
	Cost         float64
	InputTokens  int64
	OutputTokens int64

	// Messages is the streaming buffer of nested sub-messages,
	// in chronological order.  Used to derive the tool-count
	// subtitle and the latest-tool hint in the running state.
	Messages []UIMessage

	// Expanded is retained for Ctrl+T toggle compatibility.
	// In the compact layout it controls whether the transcript view
	// is shown when the user follows into the subagent via Ctrl+G.
	Expanded bool
}

// IsRunning is true while no Completed event has arrived yet.  Used
// by the App to drive the elapsed-time tick.
func (s *SubagentState) IsRunning() bool {
	return s != nil && s.EndedAt.IsZero()
}

// SubagentBlock renders a single nested sub-agent state in the compact
// heading + subtitle + hint layout.
//
// Layout (rendered inside a distinct bubble by wrapSubagentBubble):
//
//	{Type} Subagent — {Description}
//	{subtitle}
//
//	ctrl+g  view subagent
//
// Width is the available column count for the parent message list; Theme
// is the active theme; Now is the wall-clock used for elapsed-time math
// while the state is still running.  Anim is the optional animation
// component for the running state (nil renders a static placeholder).
type SubagentBlock struct {
	State   *SubagentState
	Width   int
	Theme   *styles.Styles
	Now     time.Time
	Anim    *anim.Anim
	Hovered bool
}

// View renders the compact block.  Returns the empty string when State is nil.
// The output has no leading │ gutter — the surrounding bubble border provides
// the visual containment.
func (b SubagentBlock) View() string {
	if b.State == nil {
		return ""
	}

	// Heading: "{Type} Subagent — {Description}" or "{Type} Subagent"
	heading := b.heading()

	// Subtitle: state-dependent one-liner.
	subtitle := b.subtitle()

	// Hint line.
	hint := b.hintLine()

	var out strings.Builder
	out.WriteString(heading)
	out.WriteString("\n" + subtitle)
	out.WriteString("\n")
	out.WriteString("\n" + hint)
	return out.String()
}

// heading builds "{TypeTitleCase} Subagent — {Description}" truncated to fit.
func (b SubagentBlock) heading() string {
	typeName := titleCase(b.State.Type)
	if typeName == "" {
		typeName = "Subagent"
	}
	base := typeName + " Subagent"

	if b.State.Description == "" {
		return b.headingStyle().Render(base)
	}

	full := base + " \u2014 " + b.State.Description // em dash

	// Only truncate when a meaningful width constraint is provided.
	if b.Width > 0 {
		// Available width: terminal width minus outer bubble border (2 runes) and
		// a small margin.
		avail := b.Width - 2
		if utf8.RuneCountInString(full) > avail && avail >= 5 {
			// Truncate description with ellipsis.
			emDashPrefix := base + " \u2014 "
			prefixLen := utf8.RuneCountInString(emDashPrefix)
			descAllowed := avail - prefixLen - 1 // 1 for ellipsis
			if descAllowed < 1 {
				return b.headingStyle().Render(base)
			}
			descRunes := []rune(b.State.Description)
			if len(descRunes) > descAllowed {
				descRunes = descRunes[:descAllowed]
			}
			return b.headingStyle().Render(emDashPrefix + string(descRunes) + "\u2026")
		}
	}

	return b.headingStyle().Render(full)
}

// subtitle renders the state-appropriate second line.
func (b SubagentBlock) subtitle() string {
	switch {
	case b.State.IsRunning():
		// RUNNING state: anim + latest tool label.
		animStr := b.animStr()
		label := b.latestToolLabel()
		return b.muted().Render(animStr + " " + label)

	default:
		// DONE state.
		toolCount := b.toolCallCount()
		parts := []string{fmt.Sprintf("%d toolcalls", toolCount)}
		if b.State.Model != "" {
			parts = append(parts, b.State.Model)
		}
		if b.State.OutputTokens > 0 {
			parts = append(parts, fmt.Sprintf("%d tokens", b.State.OutputTokens))
		}
		if b.State.Cost > 0 {
			parts = append(parts, formatDollars(b.State.Cost))
		}
		elapsed := b.elapsed()
		if elapsed > 0 {
			parts = append(parts, formatElapsed(elapsed))
		}
		return b.muted().Render(strings.Join(parts, " \u00b7 "))
	}
}

// animStr returns either the anim's current Render() or a static placeholder.
func (b SubagentBlock) animStr() string {
	if b.Anim != nil {
		return b.Anim.Render()
	}
	// Static fallback: 8-cell block when no anim is wired.
	return "░▒▓█▓▒░·"
}

// latestToolLabel returns "{toolname} {target}" for the last RoleTool entry,
// or "working…" when no tool calls have arrived yet.
func (b SubagentBlock) latestToolLabel() string {
	for i := len(b.State.Messages) - 1; i >= 0; i-- {
		m := b.State.Messages[i]
		if m.Role != RoleTool {
			continue
		}
		label := m.ToolName
		if m.Target != "" {
			label += " " + m.Target
		}
		return label
	}
	return "working\u2026" // "working…"
}

// toolCallCount returns the number of RoleTool entries in Messages.
func (b SubagentBlock) toolCallCount() int {
	n := 0
	for _, m := range b.State.Messages {
		if m.Role == RoleTool {
			n++
		}
	}
	return n
}

// hintLine renders "ctrl+g  view subagent" with a styled keybind.
func (b SubagentBlock) hintLine() string {
	muted := b.muted()
	key := b.keyStyle().Render("ctrl+g")
	desc := muted.Render("  view subagent")
	return key + desc
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

// headingStyle returns a bold style for the heading line.
func (b SubagentBlock) headingStyle() lipgloss.Style {
	if b.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	if b.State.IsRunning() {
		return b.Theme.Style(styles.AtomAccent).Bold(true)
	}
	return b.Theme.Style(styles.AtomPrimary).Bold(true)
}

// muted returns the muted style for gutter lines and subtitles.
// When hovered, uses a brighter style to indicate clickability.
func (b SubagentBlock) muted() lipgloss.Style {
	if b.Theme == nil {
		if b.Hovered {
			return lipgloss.NewStyle()
		}
		return lipgloss.NewStyle().Faint(true)
	}
	if b.Hovered {
		return b.Theme.Style(styles.AtomPrimary)
	}
	return b.Theme.Style(styles.AtomMuted)
}

// keyStyle returns a slightly-highlighted style for keybinding text.
func (b SubagentBlock) keyStyle() lipgloss.Style {
	if b.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	if b.Hovered {
		return b.Theme.Style(styles.AtomPrimary).Bold(true)
	}
	return b.Theme.Style(styles.AtomMuted).Bold(true)
}

// titleCase uppercases the first letter of s (ASCII only).
func titleCase(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	if r[0] >= 'a' && r[0] <= 'z' {
		r[0] = r[0] - 'a' + 'A'
	}
	return string(r)
}

// formatElapsed renders a duration compactly.
// Spec: <seconds>s for <60s (e.g. "4.2s"), "<m>m <s>s" for >= 60s.
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

// formatDollars renders a USD amount with four decimal places.
func formatDollars(d float64) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("$%.4f", d)
}

// formatSubagentTokens renders integer token counts compactly.
// Kept for backward compatibility; not used in the compact block.
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
