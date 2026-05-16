package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type pastedInputBlock struct {
	Marker  string
	Content string
}

func (a *App) handlePaste(m tea.PasteMsg) (tea.Model, tea.Cmd) {
	if a.anyOverlayOpen() || a.viewingSubagent() {
		return a, nil
	}

	content := normalizePasteContent(m.Content)
	if content == "" {
		return a, nil
	}

	lineCount := pasteLineCount(content)
	if lineCount <= 1 {
		return a.updateInputPaste(content)
	}

	marker := pastedInputMarker(lineCount)
	a.input.Textarea.InsertString(marker)
	a.pastedInputBlocks = append(a.pastedInputBlocks, pastedInputBlock{
		Marker:  marker,
		Content: content,
	})
	a.history.Reset()
	a.paletteHighlight = -1
	a.slashPaletteDismissed = false
	a.mentionHighlight = -1
	a.mentionDismissed = false
	return a, nil
}

func (a *App) updateInputPaste(content string) (tea.Model, tea.Cmd) {
	before := a.input.Value()
	var cmd tea.Cmd
	a.input.Textarea, cmd = a.input.Textarea.Update(tea.PasteMsg{Content: content})
	if a.input.Value() != before {
		a.history.Reset()
		a.paletteHighlight = -1
		a.slashPaletteDismissed = false
		a.mentionHighlight = -1
		a.mentionDismissed = false
	}
	return a, cmd
}

func (a *App) expandPastedInputText(text string) string {
	if len(a.pastedInputBlocks) == 0 || text == "" {
		return text
	}
	out := text
	for _, block := range a.pastedInputBlocks {
		if block.Marker == "" {
			continue
		}
		out = strings.Replace(out, block.Marker, block.Content, 1)
	}
	return out
}

func normalizePasteContent(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

func pasteLineCount(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func pastedInputMarker(lineCount int) string {
	label := "lines"
	if lineCount == 1 {
		label = "line"
	}
	return fmt.Sprintf("[ Pasted %d %s ]", lineCount, label)
}
