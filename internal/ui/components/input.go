package components

import (
	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/theme"
)

// Input wraps a bubbles textarea.Model with theming.
//
// The contract for v0.1:
//   - Enter submits (handled by the App, NOT the textarea).
//   - Alt+Enter inserts a newline (the textarea's default for KeyAltEnter).
//   - The App routes KeyEnter to a submit path before forwarding the message
//     to the textarea, so plain Enter never adds a newline.
//
// Other keybinds (Ctrl+C, Ctrl+L) are also handled by the App.
type Input struct {
	Textarea textarea.Model
	Theme    *theme.Theme
	// Focused controls the border color: accent when true, muted when false.
	Focused bool
	// ReadyPlaceholder is shown when the agent is idle.
	ReadyPlaceholder string
	// WorkingPlaceholder is shown while the agent is processing a turn.
	WorkingPlaceholder string
}

// NewInput builds a configured textarea wrapped in Input.
func NewInput(t *theme.Theme) *Input {
	ta := textarea.New()
	ta.Placeholder = "Type a message…"
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // unlimited
	ta.SetHeight(3)
	// Match the rest of the chrome via theme atoms.
	if t != nil {
		muted := t.Style(theme.AtomMuted)
		styles := ta.Styles()
		styles.Focused.Placeholder = muted
		styles.Blurred.Placeholder = muted
		ta.SetStyles(styles)
	}
	ta.Focus()
	return &Input{
		Textarea:         ta,
		Theme:            t,
		Focused:          true,
		ReadyPlaceholder: "Type a message…",
	}
}

// SetBusy switches the textarea placeholder based on whether the agent is
// currently processing a turn.  When busy is true, WorkingPlaceholder is
// used (if non-empty), with suffix appended (e.g. " (2 queued)"); when false
// the placeholder reverts to ReadyPlaceholder.
// The placeholder is only visible when the textarea is empty, so this has no
// visual effect while the user is typing.
func (i *Input) SetBusy(busy bool, suffix string) {
	if busy {
		if i.WorkingPlaceholder != "" {
			i.Textarea.Placeholder = i.WorkingPlaceholder + suffix
		}
	} else {
		i.Textarea.Placeholder = i.ReadyPlaceholder
	}
}

// SetWidth resizes the underlying textarea.  The outer border adds 2 columns
// per side, so the textarea itself is narrowed by 2 (border chars) to keep
// the total within the requested width.
func (i *Input) SetWidth(w int) {
	inner := w - 2 // subtract left+right border columns
	if inner < 1 {
		inner = 1
	}
	i.Textarea.SetWidth(inner)
}

// View renders the textarea wrapped in a rounded border whose color reflects
// focus state: accent (AtomBubbleBorder) when focused, muted when blurred.
func (i *Input) View() string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		Padding(0, 1)
	if i.Theme != nil {
		var borderAtom theme.Atom
		if i.Focused {
			borderAtom = theme.AtomBubbleBorder
		} else {
			borderAtom = theme.AtomMuted
		}
		bs := i.Theme.Style(borderAtom)
		style = style.BorderForeground(bs.GetForeground())
	}
	return style.Render(i.Textarea.View())
}

// Value returns the current input text.
func (i *Input) Value() string { return i.Textarea.Value() }

// Reset clears the input.
func (i *Input) Reset() { i.Textarea.Reset() }
