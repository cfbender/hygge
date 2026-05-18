package ui

import (
	"fmt"
	"mime"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type pastedInputBlock struct {
	Marker      string
	Content     string
	Attachments []promptAttachment
}

func (a *App) handlePaste(m tea.PasteMsg) (tea.Model, tea.Cmd) {
	if a.anyOverlayOpen() || a.viewingSubagent() {
		return a, nil
	}

	content := normalizePasteContent(m.Content)
	if content == "" {
		return a, nil
	}

	if att, ok := imageAttachmentFromPaste(content); ok {
		a.insertPastedInputBlock(pastedImageMarker(att), "", att)
		a.notice = ""
		return a, nil
	}

	lineCount := pasteLineCount(content)
	if lineCount <= 1 {
		return a.updateInputPaste(content)
	}

	a.insertPastedInputBlock(pastedInputMarker(lineCount), content)
	a.history.Reset()
	a.paletteHighlight = -1
	a.slashPaletteDismissed = false
	a.mentionHighlight = -1
	a.mentionDismissed = false
	return a, nil
}

func (a *App) insertPastedInputBlock(marker, content string, attachments ...promptAttachment) {
	a.input.Textarea.InsertString(marker + " ")
	a.pastedInputBlocks = append(a.pastedInputBlocks, pastedInputBlock{
		Marker:      marker,
		Content:     content,
		Attachments: attachments,
	})
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

func (a *App) handleAtomicPasteEdit(k tea.KeyPressMsg) bool {
	if len(a.pastedInputBlocks) == 0 {
		return false
	}
	offset := a.inputCursorOffset()
	for _, marker := range a.pastedInputMarkerRanges() {
		switch {
		case isInputBackspace(k) && offset > marker.Start && offset <= marker.End:
			a.removePastedInputMarker(marker)
			return true
		case isInputDelete(k) && offset >= marker.Start && offset < marker.End:
			a.removePastedInputMarker(marker)
			return true
		}
	}
	return false
}

func (a *App) keepCursorOutsidePastedMarkers(k tea.KeyPressMsg) {
	if len(a.pastedInputBlocks) == 0 {
		return
	}
	offset := a.inputCursorOffset()
	for _, marker := range a.pastedInputMarkerRanges() {
		if offset <= marker.Start || offset >= marker.End {
			continue
		}
		target := marker.End
		if isInputMoveLeft(k) {
			target = marker.Start
		}
		a.setInputValueAndCursor(a.input.Value(), target)
		return
	}
}

func (a *App) removePastedInputMarker(marker pastedInputMarkerRange) {
	runes := []rune(a.input.Value())
	if marker.Start < 0 || marker.End > len(runes) || marker.Start >= marker.End {
		return
	}
	updated := string(slices.Concat(runes[:marker.Start], runes[marker.End:]))
	a.pastedInputBlocks = slices.Delete(a.pastedInputBlocks, marker.BlockIndex, marker.BlockIndex+1)
	a.setInputValueAndCursor(updated, marker.Start)
	a.history.Reset()
	a.paletteHighlight = -1
	a.slashPaletteDismissed = false
	a.mentionHighlight = -1
	a.mentionDismissed = false
}

type pastedInputMarkerRange struct {
	Start      int
	End        int
	BlockIndex int
}

func (a *App) pastedInputMarkerRanges() []pastedInputMarkerRange {
	text := a.input.Value()
	if text == "" {
		return nil
	}
	type markerCandidate struct {
		Start  int
		End    int
		Marker string
	}
	candidates := make([]markerCandidate, 0, len(a.pastedInputBlocks))
	seenMarkers := make(map[string]struct{}, len(a.pastedInputBlocks))
	for _, block := range a.pastedInputBlocks {
		if block.Marker == "" {
			continue
		}
		if _, ok := seenMarkers[block.Marker]; ok {
			continue
		}
		seenMarkers[block.Marker] = struct{}{}
		search := text
		base := 0
		for {
			idx := strings.Index(search, block.Marker)
			if idx == -1 {
				break
			}
			start := base + utf8.RuneCountInString(search[:idx])
			end := start + utf8.RuneCountInString(block.Marker)
			candidates = append(candidates, markerCandidate{Start: start, End: end, Marker: block.Marker})
			search = search[idx+len(block.Marker):]
			base = end
		}
	}
	slices.SortFunc(candidates, func(a, b markerCandidate) int {
		return a.Start - b.Start
	})

	ranges := make([]pastedInputMarkerRange, 0, len(a.pastedInputBlocks))
	usedBlocks := make([]bool, len(a.pastedInputBlocks))
	for _, candidate := range candidates {
		for blockIndex, block := range a.pastedInputBlocks {
			if usedBlocks[blockIndex] || block.Marker != candidate.Marker {
				continue
			}
			usedBlocks[blockIndex] = true
			ranges = append(ranges, pastedInputMarkerRange{Start: candidate.Start, End: candidate.End, BlockIndex: blockIndex})
			break
		}
	}
	return ranges
}

func (a *App) inputCursorOffset() int {
	value := a.input.Value()
	lines := strings.Split(value, "\n")
	line := min(max(a.input.Textarea.Line(), 0), len(lines)-1)
	offset := 0
	for i := range line {
		offset += utf8.RuneCountInString(lines[i]) + 1
	}
	col := min(max(a.input.Textarea.Column(), 0), utf8.RuneCountInString(lines[line]))
	return offset + col
}

func (a *App) setInputValueAndCursor(value string, offset int) {
	runes := []rune(value)
	offset = min(max(offset, 0), len(runes))
	prefix := string(runes[:offset])
	suffix := string(runes[offset:])
	a.input.Textarea.SetValue(suffix)
	a.input.Textarea.MoveToBegin()
	a.input.Textarea.InsertString(prefix)
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

func (a *App) displayPastedInputText(text string) string {
	if len(a.pastedInputBlocks) == 0 || text == "" {
		return text
	}
	out := text
	for _, block := range a.pastedInputBlocks {
		if block.Marker == "" {
			continue
		}
		replacement := block.Content
		if len(block.Attachments) > 0 {
			replacement = strings.TrimSpace(block.Marker)
		}
		out = strings.Replace(out, block.Marker, replacement, 1)
	}
	return out
}

func (a *App) pastedInputAttachments(text string) []promptAttachment {
	if len(a.pastedInputBlocks) == 0 || text == "" {
		return nil
	}
	var attachments []promptAttachment
	for _, block := range a.pastedInputBlocks {
		if block.Marker == "" || len(block.Attachments) == 0 || !strings.Contains(text, block.Marker) {
			continue
		}
		attachments = appendUniquePromptAttachments(attachments, block.Attachments...)
	}
	return attachments
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

func imageAttachmentFromPaste(content string) (promptAttachment, bool) {
	path := strings.TrimSpace(content)
	if path == "" || strings.ContainsAny(path, "\n\x00") {
		return promptAttachment{}, false
	}
	if u, err := url.Parse(path); err == nil && u.Scheme != "" {
		if u.Scheme != "file" {
			return promptAttachment{}, false
		}
		path = u.Path
	}

	candidates := []string{path}
	if unescaped := unescapePastedPath(path); unescaped != path {
		candidates = append(candidates, unescaped)
	}
	for _, candidate := range candidates {
		if att, ok := loadImageAttachmentCandidate(candidate); ok {
			return att, true
		}
	}
	return promptAttachment{}, false
}

func loadImageAttachmentCandidate(path string) (promptAttachment, bool) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return promptAttachment{}, false
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return promptAttachment{}, false
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(abs)))
	if !strings.HasPrefix(mimeType, "image/") {
		return promptAttachment{}, false
	}
	att, err := loadPromptAttachment(abs)
	if err != nil {
		return promptAttachment{}, false
	}
	return att, true
}

func unescapePastedPath(path string) string {
	if !strings.Contains(path, `\`) {
		return path
	}
	var out strings.Builder
	out.Grow(len(path))
	escaped := false
	for _, r := range path {
		if escaped {
			out.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		out.WriteRune(r)
	}
	if escaped {
		out.WriteRune('\\')
	}
	return out.String()
}

func pastedInputMarker(lineCount int) string {
	label := "lines"
	if lineCount == 1 {
		label = "line"
	}
	return fmt.Sprintf("[Pasted %d %s]", lineCount, label)
}

func pastedImageMarker(att promptAttachment) string {
	name := strings.TrimSpace(att.Name)
	if name == "" {
		name = "image"
	}
	return fmt.Sprintf("[Pasted image: %s]", name)
}

func isInputBackspace(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeyBackspace || k.String() == "backspace" || k.String() == "ctrl+h"
}

func isInputDelete(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeyDelete || k.String() == "delete"
}

func isInputMoveLeft(k tea.KeyPressMsg) bool {
	return k.Code == tea.KeyLeft || k.String() == "left" || k.String() == "ctrl+b"
}

func (a *App) pasteInputMarkerStyle() lipgloss.Style {
	bg := a.activeModeColor()
	if bg == nil && a.styles != nil {
		bg = a.styles.Dialog.TitleGradFrom
	}
	style := lipgloss.NewStyle().Foreground(lipgloss.Color("0"))
	if bg != nil {
		style = style.Background(bg)
	}
	return style
}
