package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// CompactionModal renders the confirmation dialog shown when the user runs
// /compact.  It displays the message count, context usage, and a brief
// explanation before asking the user to confirm or cancel.
//
// Keybinds (handled by the App, not here):
//
//	y / Y  — confirm compaction
//	n / N  — cancel
//	esc    — cancel
type CompactionModal struct {
	Width  int
	Height int
	Theme  *theme.Theme
	Toast  string // optional transient line (e.g. warning text)

	// SessionID is the foreground session being compacted.
	SessionID string

	// MessageCount is the number of messages since the last compaction marker.
	// When < 4 the modal disables the [y] key and shows a "nothing to compact"
	// notice.
	MessageCount int

	// ContextPct is the current context-window usage as a percentage (0–100).
	ContextPct float64

	// ContextWindow is the model's maximum context size in tokens.  Shown in
	// the modal body.
	ContextWindow int64
}

// NothingToCompact reports whether there are fewer than 4 messages since the
// last marker.  When true the [y] key is disabled.
func (m CompactionModal) NothingToCompact() bool {
	return m.MessageCount < 4
}

// View renders the modal centred in a Width×Height box.
func (m CompactionModal) View() string {
	width := m.Width
	if width <= 0 {
		width = 80
	}
	height := m.Height
	if height <= 0 {
		height = 24
	}
	box := m.renderBox()
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

func (m CompactionModal) renderBox() string {
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(1, 2)
	if m.Theme != nil {
		bs := m.Theme.Style(theme.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := m.Theme.Style(theme.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}

	var b strings.Builder

	title := m.bold().Render("Compact session?")
	b.WriteString(title)
	b.WriteString("\n\n")

	if m.NothingToCompact() {
		b.WriteString(m.warnStyle().Render("Nothing to compact — fewer than 4 messages since the last marker."))
		b.WriteString("\n\n")
		b.WriteString(m.muted().Render("[n] cancel   [esc] cancel"))
	} else {
		b.WriteString("This will summarise the ")
		b.WriteString(m.bold().Render(fmt.Sprintf("%d messages", m.MessageCount)))
		b.WriteString(" since the\nlast compaction marker into a 2-3 paragraph\nsummary that the model will see in place of\nthose messages.")
		b.WriteString("\n\n")
		if m.ContextWindow > 0 {
			b.WriteString(m.field("Current context usage",
				fmt.Sprintf("%.0f%% of %s tokens", m.ContextPct, formatWindowTokens(m.ContextWindow))))
		} else {
			b.WriteString(m.field("Current context usage",
				fmt.Sprintf("%.0f%%", m.ContextPct)))
		}
		b.WriteString("\n")
		b.WriteString(m.field("Messages to compact", fmt.Sprintf("%d", m.MessageCount)))
		b.WriteString("\n\n")
		b.WriteString(m.warnStyle().Render("Compaction is destructive — message-level detail"))
		b.WriteString("\n")
		b.WriteString(m.warnStyle().Render("is replaced by the summary."))
		b.WriteString("\n\n")
		b.WriteString(m.muted().Render("[y] compact   [n] cancel   [esc] cancel"))
	}

	if m.Toast != "" {
		b.WriteString("\n\n")
		b.WriteString(m.warnStyle().Render(m.Toast))
	}

	return border.Render(b.String())
}

func (m CompactionModal) field(label, value string) string {
	muted := m.muted()
	return muted.Render(label+": ") + value
}

func (m CompactionModal) bold() lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle().Bold(true)
	}
	return m.Theme.Style(theme.AtomPrimary).Bold(true)
}

func (m CompactionModal) warnStyle() lipgloss.Style {
	return m.style(theme.AtomWarn)
}

func (m CompactionModal) muted() lipgloss.Style {
	return m.style(theme.AtomMuted)
}

func (m CompactionModal) style(a theme.Atom) lipgloss.Style {
	if m.Theme == nil {
		return lipgloss.NewStyle()
	}
	return m.Theme.Style(a)
}

// formatWindowTokens formats a token count into a human-readable string with
// commas (e.g. 200000 → "200,000").
func formatWindowTokens(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out strings.Builder
	start := len(s) % 3
	if start > 0 {
		out.WriteString(s[:start])
	}
	for i := start; i < len(s); i += 3 {
		if i > 0 {
			out.WriteString(",")
		}
		out.WriteString(s[i : i+3])
	}
	return out.String()
}
