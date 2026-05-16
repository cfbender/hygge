package components

import (
	"math/rand/v2"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/ui/styles"
	"github.com/cfbender/hygge/internal/ui/theme"
)

const (
	inputMinBoxHeight = 3
	inputMaxBoxHeight = 8
	inputFrameHeight  = 2
	inputMinTextRows  = inputMinBoxHeight - inputFrameHeight
	inputMaxTextRows  = inputMaxBoxHeight - inputFrameHeight
)

// readyPlaceholders are shown when the agent is idle.
var readyPlaceholders = []string{
	"Ready!",
	"Ready...",
	"Ready?",
	"Ready for instructions",
	"What's on your mind?",
	"Listening...",
}

// workingPlaceholders are shown while the agent is processing.
var workingPlaceholders = []string{
	"Thinking…",
	"Working…",
	"Brrrrr…",
	"Prrrrrrrr…",
	"Processing…",
	"Reasoning…",
}

// Input wraps a bubbles textarea with dynamic height, custom prompts,
// and theme-aware styling.
//
// Keybind contract:
//   - Enter submits (handled by the App, not the textarea).
//   - Shift+Enter and Alt+Enter insert a newline.
//   - Ctrl+C, Ctrl+L handled by the App.
type Input struct {
	Textarea textarea.Model
	Styles   *styles.Styles
	Theme    *theme.Theme // kept for gradual migration
	Focused  bool
	prevH    int // track height changes for layout recalc
}

// NewInput builds a configured textarea with dynamic height and custom prompts.
func NewInput(t *theme.Theme) *Input {
	ta := textarea.New()
	ta.Placeholder = randomPlaceholder(readyPlaceholders)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0

	// Dynamic height: grows with content, bounded so the styled input box is
	// 3–8 terminal rows including its border.
	ta.DynamicHeight = true
	ta.MinHeight = inputMinTextRows
	ta.MaxHeight = inputMaxTextRows
	ta.MaxContentHeight = 10_000
	ta.SetHeight(inputMinTextRows)

	// Apply theme-aware styles.
	if t != nil {
		muted := t.Style(theme.AtomMuted)
		s := ta.Styles()
		s.Focused.Placeholder = muted
		s.Blurred.Placeholder = muted
		ta.SetStyles(s)
	}

	ta.Focus()

	return &Input{
		Textarea: ta,
		Theme:    t,
		Focused:  true,
		prevH:    inputMinTextRows,
	}
}

// SetStyles applies the theme style system.
func (i *Input) SetStyles(s *styles.Styles) {
	i.Styles = s
	ta := i.Textarea.Styles()
	ta.Focused = s.Editor.Textarea.Focused
	ta.Blurred = s.Editor.Textarea.Blurred
	ta.Cursor = s.Editor.Textarea.Cursor
	i.Textarea.SetStyles(ta)
}

// SetBusy switches the placeholder based on agent state.
func (i *Input) SetBusy(busy bool, suffix string) {
	if busy {
		i.Textarea.Placeholder = randomPlaceholder(workingPlaceholders) + suffix
	} else {
		i.Textarea.Placeholder = randomPlaceholder(readyPlaceholders)
	}
}

// SetWidth resizes the underlying textarea, accounting for border and padding.
func (i *Input) SetWidth(w int) {
	frame := 0
	if i.Styles != nil {
		frame = 4 // border (1) + padding (1) on each side
	}
	inner := w - frame
	if inner < 1 {
		inner = 1
	}
	i.Textarea.SetWidth(inner)
}

// HeightChanged reports whether the textarea height changed since the last
// check. Call this after Update to know if layout needs recalculation.
func (i *Input) HeightChanged() bool {
	h := i.Textarea.Height()
	if h != i.prevH {
		i.prevH = h
		return true
	}
	return false
}

// View renders the input area with a themed border.
func (i *Input) View() string {
	content := i.Textarea.View()
	if i.Styles == nil {
		return content
	}

	style := lipgloss.NewStyle().Padding(0, 1)

	if i.Focused {
		style = style.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(i.Styles.Dialog.TitleGradFrom)
	} else {
		style = style.
			Border(lipgloss.RoundedBorder()).
			BorderForeground(i.Styles.Section.Line.GetForeground())
	}

	return style.Render(content)
}

// Value returns the current input text.
func (i *Input) Value() string { return i.Textarea.Value() }

// Reset clears the input.
func (i *Input) Reset() { i.Textarea.Reset() }

func randomPlaceholder(pool []string) string {
	return pool[rand.IntN(len(pool))] //nolint:gosec // cosmetic placeholder, not security-sensitive
}
