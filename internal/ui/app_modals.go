package ui

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
)

func (a *App) updateInputFocus() {
	a.input.Focused = !a.anyOverlayOpen()
}

func (a *App) anyOverlayOpen() bool {
	a.syncPermissionOverlay()
	a.syncQuestionOverlay()
	return a.overlays.Open()
}

func (a *App) openOverlay(kind overlayKind) {
	a.overlays.Push(kind)
	a.syncActiveModal()
}

func (a *App) closeOverlay(kind overlayKind) {
	a.overlays.Remove(kind)
	a.syncActiveModal()
	a.updateInputFocus()
}

func (a *App) syncPermissionOverlay() {
	if len(a.pendingPerms) > 0 {
		a.overlays.Push(overlayPermission)
		return
	}
	a.overlays.Remove(overlayPermission)
}

func (a *App) syncQuestionOverlay() {
	if len(a.pendingQuestions) > 0 {
		a.clampQuestionSelection()
		a.overlays.Push(overlayQuestion)
		return
	}
	a.questionSelectedIndex = 0
	a.overlays.Remove(overlayQuestion)
}

func (a *App) clampQuestionSelection() {
	if len(a.pendingQuestions) == 0 || len(a.pendingQuestions[0].Options) == 0 {
		a.questionSelectedIndex = 0
		return
	}
	if a.questionSelectedIndex < 0 {
		a.questionSelectedIndex = 0
		return
	}
	maxIndex := len(a.pendingQuestions[0].Options) - 1
	if a.questionSelectedIndex > maxIndex {
		a.questionSelectedIndex = maxIndex
	}
}

func (a *App) syncActiveModal() {
	a.activeModal = ""
	for i := len(a.overlays.entries) - 1; i >= 0; i-- {
		switch a.overlays.entries[i] {
		case overlayHelp, overlaySessions, overlayMemory, overlayMemoryRemember, overlayMemoryForget, overlayCompactConfirm, overlayModel, overlayAPIKey, overlayTheme, overlayOnboarding:
			a.activeModal = string(a.overlays.entries[i])
			return
		}
	}
}

type apiKeySaveResult struct {
	provider string
	err      error
}

type themeSwitchResult struct {
	name    string
	theme   *styles.Styles
	err     error
	saveErr error
}

func (a *App) themeNames() []string {
	if len(a.opts.ThemeNames) > 0 {
		out := make([]string, len(a.opts.ThemeNames))
		copy(out, a.opts.ThemeNames)
		return out
	}
	return []string{"shell"}
}

func currentThemeName(t *styles.Styles) string {
	if t == nil || t.Name == "" {
		return "shell"
	}
	return t.Name
}

func (a *App) switchThemeCmd(name string) tea.Cmd {
	return func() tea.Msg {
		var th *styles.Styles
		if a.opts.LoadTheme != nil {
			loaded, err := a.opts.LoadTheme(a.ctx, name)
			if err != nil {
				return themeSwitchResult{name: name, err: err}
			}
			th = loaded
		} else if name == currentThemeName(a.opts.Theme) || name == "shell" {
			th = styles.DefaultTheme()
		} else {
			return themeSwitchResult{name: name, err: fmt.Errorf("unknown theme %q", name)}
		}
		if a.opts.SaveTheme != nil {
			if err := a.opts.SaveTheme(a.ctx, name); err != nil {
				return themeSwitchResult{name: name, theme: th, saveErr: err}
			}
		}
		return themeSwitchResult{name: name, theme: th}
	}
}

func (a *App) handleThemeModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.ThemeKey{Name: k.String(), Runes: []rune(k.Text)}
	switch k.String() {
	case "up", "down", "enter", "esc", "ctrl+n", "ctrl+p":
		sk.Name = k.String()
	case "backspace", "delete":
		sk.Name = "backspace"
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}
	updated, msg := a.themeModal.HandleKey(sk)
	a.themeModal = updated
	switch m := msg.(type) {
	case components.CloseThemeModal:
		a.closeOverlay(overlayTheme)
	case components.SelectThemeAction:
		a.closeOverlay(overlayTheme)
		return a, a.switchThemeCmd(m.Name)
	}
	return a, nil
}

func (a *App) handleAPIKeyModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.APIKeyKey{Name: k.String(), Runes: []rune(k.Text)}
	if k.String() == "backspace" || k.String() == "delete" {
		sk.Name = "backspace"
	}
	updated, msg := a.apiKeyModal.HandleKey(sk)
	a.apiKeyModal = updated
	switch m := msg.(type) {
	case components.CloseAPIKeyModal:
		a.closeOverlay(overlayAPIKey)
	case components.SaveAPIKeyAction:
		a.closeOverlay(overlayAPIKey)
		return a, a.saveAPIKeyCmd(m.Provider, m.APIKey)
	}
	return a, nil
}

func (a *App) saveAPIKeyCmd(providerName, apiKey string) tea.Cmd {
	return func() tea.Msg {
		if a.opts.SaveAPIKey != nil {
			if err := a.opts.SaveAPIKey(a.ctx, providerName, apiKey); err != nil {
				return apiKeySaveResult{provider: providerName, err: err}
			}
		}
		if a.opts.SwitchModel != nil && providerName == a.opts.ModelProvider && a.opts.ModelName != "" {
			if err := a.opts.SwitchModel(a.ctx, a.opts.ModelProvider, a.opts.ModelName, a.ActiveModeName()); err != nil {
				return apiKeySaveResult{provider: providerName, err: err}
			}
		}
		return apiKeySaveResult{provider: providerName}
	}
}

func (a *App) handleOnboardingPaste(m tea.PasteMsg) (tea.Model, tea.Cmd) {
	content := normalizePasteContent(m.Content)
	if content == "" {
		return a, nil
	}
	return a.applyOnboardingKey(components.OnboardingKey{Name: "paste", Runes: []rune(content)})
}

func (a *App) handleOnboardingKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.OnboardingKey{Name: k.String(), Runes: []rune(k.Text)}
	switch k.String() {
	case "up", "down", "enter", "esc", "ctrl+n", "ctrl+p", "backspace", "delete", "ctrl+u":
		if k.String() == "delete" {
			sk.Name = "backspace"
		}
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}
	return a.applyOnboardingKey(sk)
}

func (a *App) applyOnboardingKey(sk components.OnboardingKey) (tea.Model, tea.Cmd) {
	updated, msg := a.onboardingWizard.HandleKey(sk)
	if updated.ProviderName != a.onboardingWizard.ProviderName {
		updated.Models = a.onboardingModelIDs(updated.ProviderName)
	}
	a.onboardingWizard = updated
	switch m := msg.(type) {
	case components.OnboardingClose:
		// Onboarding is required for normal chat, so Esc exits instead of exposing
		// an unusable empty session.
		return a, tea.Quit
	case components.OnboardingGeneratePrompt:
		return a, a.generateOnboardingPromptCmd(m)
	case components.OnboardingSaveResult:
		a.onboardingWizard.Saving = true
		a.onboardingWizard.SaveError = ""
		return a, a.saveOnboardingCmd(m)
	}
	return a, nil
}

func (a *App) generateOnboardingPromptCmd(req components.OnboardingGeneratePrompt) tea.Cmd {
	return func() tea.Msg {
		if a.opts.GeneratePrompt == nil {
			return components.OnboardingGeneratedPromptReady{ForSubagent: req.ForSubagent, Err: errors.New("prompt generation is unavailable")}
		}
		prompt, err := a.opts.GeneratePrompt(a.ctx, req.ProviderName, req.ModelName, req.APIKey, req.Idea)
		return components.OnboardingGeneratedPromptReady{Prompt: prompt, Err: err, ForSubagent: req.ForSubagent}
	}
}

func (a *App) saveOnboardingCmd(req components.OnboardingSaveResult) tea.Cmd {
	return func() tea.Msg {
		if a.opts.SaveOnboardingResult == nil {
			return components.OnboardingSaved{Err: errors.New("onboarding save is unavailable")}
		}
		keys := make(map[string]string, len(a.onboardingWizard.ProviderKeys))
		maps.Copy(keys, a.onboardingWizard.ProviderKeys)
		result := OnboardingResult{
			ProviderName:    req.ProviderName,
			ProviderAPIKey:  req.ProviderAPIKey,
			ProviderAPIKeys: keys,
			Mode:            req.Mode,
			Subagents:       req.Subagents,
		}
		if err := a.opts.SaveOnboardingResult(a.ctx, result); err != nil {
			return components.OnboardingSaved{Err: err}
		}
		a.opts.ModelProvider = req.Mode.Provider
		a.opts.ModelName = req.Mode.Model
		a.opts.Modes = []config.ModeConfig{req.Mode}
		return components.OnboardingSaved{}
	}
}

func (a *App) onboardingModelIDs(providerName string) []string {
	if a.opts.Catalog == nil || a.opts.Catalog.Source() == nil {
		return nil
	}
	entries := a.opts.Catalog.Source().Models(providerName)
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) != "" {
			out = append(out, entry.ID)
		}
	}
	return out
}

func (a *App) catalogModelOptions() []components.ModelOption {
	if a.opts.Catalog == nil || a.opts.Catalog.Source() == nil {
		return nil
	}
	src := a.opts.Catalog.Source()
	providers := src.Providers()
	configured := a.configuredModelProviders()
	out := make([]components.ModelOption, 0)
	seen := make(map[string]bool)
	for _, providerID := range providers {
		if len(configured) > 0 && !configured[providerID] {
			continue
		}
		for _, entry := range src.Models(providerID) {
			out = append(out, components.ModelOption{Provider: providerID, Entry: entry})
			seen[providerID] = true
		}
	}
	if provider := strings.TrimSpace(a.opts.ModelProvider); provider != "" && !seen[provider] {
		if model := strings.TrimSpace(a.opts.ModelName); model != "" {
			out = append(out, components.ConfiguredModelOption(provider, model))
		}
	}
	return out
}

func (a *App) configuredModelProviders() map[string]bool {
	configured := make(map[string]bool)
	for _, provider := range a.opts.AuthConfiguredProviders {
		if provider := strings.TrimSpace(provider); provider != "" {
			configured[provider] = true
		}
	}
	if provider := strings.TrimSpace(a.opts.ModelProvider); provider != "" {
		configured[provider] = true
	}
	for _, mode := range a.opts.Modes {
		if provider := strings.TrimSpace(mode.Provider); provider != "" {
			configured[provider] = true
		}
	}
	return configured
}

func (a *App) handleModelModalKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	sk := components.ModelKey{Name: k.String(), Runes: []rune(k.Text)}
	switch k.String() {
	case "up", "down", "enter", "esc", "ctrl+n", "ctrl+p":
		sk.Name = k.String()
	case "backspace", "delete":
		sk.Name = "backspace"
	default:
		if len(k.Text) == 1 {
			sk.Name = k.Text
		}
	}
	updated, msg := a.modelModal.HandleKey(sk)
	a.modelModal = updated
	if msg == nil {
		return a, nil
	}
	switch m := msg.(type) {
	case components.CloseModelModal:
		a.closeOverlay(overlayModel)
		return a, nil
	case components.SelectModelAction:
		a.closeOverlay(overlayModel)
		return a, a.switchModelCmd(m.Provider, m.Model)
	}
	return a, nil
}

func (a *App) renderHelpOverlay(width, height int) string {
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2)
	if a.opts.Theme != nil {
		bs := a.opts.Theme.Style(styles.AtomModalBorder)
		border = border.BorderForeground(bs.GetForeground())
		modal := a.opts.Theme.Style(styles.AtomModalBg)
		if modal.GetBackground() != nil {
			border = border.Background(modal.GetBackground())
		}
	}
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle()
	if a.opts.Theme != nil {
		primary = a.opts.Theme.Style(styles.AtomPrimary).Bold(true)
		muted = a.opts.Theme.Style(styles.AtomMuted)
	}
	body := primary.Render("Help") + "\n\n" +
		"Type / to open command completions.\n" +
		"Use ↑/↓ to navigate completions and Enter to accept.\n\n" +
		"Common commands:\n" +
		"  /sessions  open the session picker\n" +
		"  /compact   compact recent context\n" +
		"  /clear     clear the visible transcript\n\n" +
		muted.Render("[esc] close   [q] close")
	box := border.Render(body)
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box)
}

// --- Compaction modal integration -----------------------------------------
