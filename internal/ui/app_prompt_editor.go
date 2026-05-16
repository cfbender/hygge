package ui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type promptEditorFinishedMsg struct {
	text string
	err  error
}

func (a *App) openPromptEditorCmd() tea.Cmd {
	initial := a.expandPastedInputText(a.input.Value())
	if a.opts.EditPrompt != nil {
		return func() tea.Msg {
			text, err := a.opts.EditPrompt(a.ctx, initial)
			if err != nil {
				return promptEditorFinishedMsg{err: err}
			}
			return promptEditorFinishedMsg{text: normalizeEditedPrompt(text)}
		}
	}

	path, err := writePromptEditorFile(initial)
	if err != nil {
		return func() tea.Msg { return promptEditorFinishedMsg{err: err} }
	}

	cmd := promptEditorCommand(promptEditorName(), path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		defer func() { _ = os.Remove(path) }()
		if err != nil {
			return promptEditorFinishedMsg{err: err}
		}
		// #nosec G304 -- path comes from writePromptEditorFile, which creates it
		// with os.CreateTemp specifically for this editor round-trip.
		data, err := os.ReadFile(path)
		if err != nil {
			return promptEditorFinishedMsg{err: fmt.Errorf("read prompt: %w", err)}
		}
		return promptEditorFinishedMsg{text: normalizeEditedPrompt(string(data))}
	})
}

func (a *App) setEditedPrompt(text string) {
	a.setInputValueAndCursor(text, len([]rune(text)))
	a.pastedInputBlocks = nil
	a.history.Reset()
	a.paletteHighlight = -1
	a.slashPaletteDismissed = false
	a.mentionHighlight = -1
	a.mentionDismissed = false
}

func writePromptEditorFile(initial string) (string, error) {
	f, err := os.CreateTemp("", "hygge-prompt-*.md")
	if err != nil {
		return "", fmt.Errorf("create prompt file: %w", err)
	}
	path := f.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	if _, err := f.WriteString(initial); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("write prompt file: %w", err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close prompt file: %w", err)
	}
	cleanup = false
	return path, nil
}

func promptEditorName() string {
	if editor := strings.TrimSpace(os.Getenv("VISUAL")); editor != "" {
		return editor
	}
	if editor := strings.TrimSpace(os.Getenv("EDITOR")); editor != "" {
		return editor
	}
	return "vi"
}

func promptEditorCommand(editor, path string) *exec.Cmd {
	parts := strings.Fields(editor)
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	args := append(append([]string{}, parts[1:]...), path)
	// #nosec G204 -- launching the user's configured editor is the feature;
	// arguments are passed without a shell and the prompt path is temp-created.
	return exec.Command(parts[0], args...)
}

func normalizeEditedPrompt(text string) string {
	return strings.TrimRight(text, "\r\n")
}
