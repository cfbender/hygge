package ui

import (
	"context"
	"fmt"
	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/config"
)

// ActiveMode returns the currently active mode config, or nil if no modes
// are configured.
func (a *App) ActiveMode() *config.ModeConfig {
	if len(a.opts.Modes) == 0 || a.modeIndex < 0 {
		return nil
	}
	return &a.opts.Modes[a.modeIndex]
}

// ActiveModeName returns the display name of the current mode, or "" if
// no modes are configured.
func (a *App) ActiveModeName() string {
	if m := a.ActiveMode(); m != nil {
		return m.Name
	}
	return ""
}

// cycleMode advances to the next mode and switches the model. Returns a
// tea.Cmd that performs the async model switch, or nil if no modes exist.
func (a *App) cycleMode() tea.Cmd {
	if len(a.opts.Modes) < 2 {
		return nil
	}

	a.modeIndex = (a.modeIndex + 1) % len(a.opts.Modes)
	mode := a.opts.Modes[a.modeIndex]

	// Resolve provider and model — empty inherits from base config.
	providerName := mode.Provider
	if providerName == "" {
		providerName = a.opts.ModelProvider
	}
	modelName := mode.Model
	if modelName == "" {
		modelName = a.opts.ModelName
	}

	// Update reasoning display.
	if mode.Reasoning != "" {
		a.opts.Reasoning.Effort = mode.Reasoning
	}

	// Update the displayed model/provider.
	a.opts.ModelName = modelName
	a.opts.ModelProvider = providerName

	// Invalidate the message cache so the footer updates.
	a.invalidateMsgCache()

	// Show toast and switch model.
	toastCmd := a.showToast("Mode Switched", "Switched to "+mode.Name)
	if a.opts.SwitchModel != nil {
		return tea.Batch(toastCmd, a.switchModeCmd(providerName, modelName, mode.Name))
	}
	return toastCmd
}

type modeSwitchResult struct {
	name string
	err  error
}

func (a *App) switchModeCmd(providerName, modelName, modeName string) tea.Cmd {
	return func() tea.Msg {
		if err := a.opts.SwitchModel(context.Background(), providerName, modelName); err != nil {
			return modeSwitchResult{name: modeName, err: err}
		}
		return modeSwitchResult{name: modeName}
	}
}

// initModes sets up the initial mode index. Called from New().
func (a *App) initModes() {
	if len(a.opts.Modes) == 0 {
		a.modeIndex = -1
		return
	}
	a.modeIndex = 0

	// Apply the first mode's overrides so the initial state is correct.
	mode := a.opts.Modes[0]
	if mode.Provider != "" {
		a.opts.ModelProvider = mode.Provider
	}
	if mode.Model != "" {
		a.opts.ModelName = mode.Model
	}
	if mode.Reasoning != "" {
		a.opts.Reasoning.Effort = mode.Reasoning
	}
}

// activeModeColor returns the accent color for the active mode, or nil.
func (a *App) activeModeColor() color.Color {
	m := a.ActiveMode()
	if m == nil || m.Color == "" {
		return nil
	}
	return lipgloss.Color(m.Color)
}

// modeNames returns all mode names for display.
func (a *App) modeNames() []string {
	names := make([]string, len(a.opts.Modes))
	for i, m := range a.opts.Modes {
		names[i] = m.Name
	}
	return names
}

// formatModeIndicator returns a string like "smart · rush · deep" with the
// active mode highlighted.
func (a *App) formatModeIndicator() string {
	if len(a.opts.Modes) < 2 {
		return ""
	}

	s := a.styles
	var parts []string
	for i, mode := range a.opts.Modes {
		if i == a.modeIndex {
			if s != nil {
				parts = append(parts, s.Header.Accent.Render(mode.Name))
			} else {
				parts = append(parts, fmt.Sprintf("[%s]", mode.Name))
			}
		} else {
			if s != nil {
				parts = append(parts, s.Header.Muted.Render(mode.Name))
			} else {
				parts = append(parts, mode.Name)
			}
		}
	}

	sep := " · "
	if s != nil {
		sep = s.Header.Separator.Render(" · ")
	}

	result := ""
	for i, p := range parts {
		if i > 0 {
			result += sep
		}
		result += p
	}
	return result
}
