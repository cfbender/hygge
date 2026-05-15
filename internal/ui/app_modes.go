package ui

import (
	"context"
	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/config"
)

// ActiveMode returns the currently active mode config. Always returns
// non-nil because Modes is guaranteed to have at least one entry after
// config loading.
func (a *App) ActiveMode() *config.ModeConfig {
	return &a.opts.Modes[a.modeIndex]
}

// ActiveModeName returns the display name of the current mode.
func (a *App) ActiveModeName() string {
	return a.ActiveMode().Name
}

// cycleMode advances to the next mode and switches the model. Returns a
// tea.Cmd that performs the async model switch, or nil if only one mode.
func (a *App) cycleMode() tea.Cmd {
	if len(a.opts.Modes) < 2 {
		return nil
	}

	a.modeIndex = (a.modeIndex + 1) % len(a.opts.Modes)
	mode := a.opts.Modes[a.modeIndex]

	// Update reasoning display.
	if mode.Reasoning != "" {
		a.opts.Reasoning.Effort = mode.Reasoning
	}

	// Update the displayed model/provider.
	a.opts.ModelName = mode.Model
	a.opts.ModelProvider = mode.Provider

	// Invalidate the message cache so the footer updates.
	a.invalidateMsgCache()

	// Show toast and switch model.
	toastCmd := a.showToast("Mode Switched", "Switched to "+mode.Name)
	if a.opts.SwitchModel != nil {
		return tea.Batch(toastCmd, a.switchModeCmd(mode.Provider, mode.Model, mode.Name))
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
// If Modes is empty (e.g. in tests that bypass config.Load), a default
// mode is synthesized from ModelProvider/ModelName.
func (a *App) initModes() {
	if len(a.opts.Modes) == 0 {
		a.opts.Modes = []config.ModeConfig{{
			Name:     "General",
			Provider: a.opts.ModelProvider,
			Model:    a.opts.ModelName,
		}}
	}
	a.modeIndex = 0

	// Ensure display fields match the first mode.
	mode := a.opts.Modes[0]
	a.opts.ModelProvider = mode.Provider
	a.opts.ModelName = mode.Model
	if mode.Reasoning != "" {
		a.opts.Reasoning.Effort = mode.Reasoning
	}
}

// activeModeColor returns the accent color for the active mode, or nil.
func (a *App) activeModeColor() color.Color {
	m := a.ActiveMode()
	if m.Color == "" {
		return nil
	}
	return lipgloss.Color(m.Color)
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
			style := lipgloss.NewStyle().Bold(true)
			if mode.Color != "" {
				style = style.Foreground(lipgloss.Color(mode.Color))
			} else if s != nil {
				style = s.Header.Accent
			}
			parts = append(parts, style.Render(mode.Name))
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
