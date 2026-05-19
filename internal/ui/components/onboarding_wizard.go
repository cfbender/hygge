package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// OnboardingStep enumerates the wizard steps.
type OnboardingStep int

// Wizard step constants.
const (
	OnboardStepWelcome OnboardingStep = iota
	OnboardStepAPIKey
	OnboardStepProviderMore
	OnboardStepPickModel
	OnboardStepModeName
	OnboardStepModeIdea
	OnboardStepPromptReview
	OnboardStepSubagentOffer
	OnboardStepSubagentName
	OnboardStepSubagentIdea
	OnboardStepSubagentPromptReview
	OnboardStepDone
)

// OnboardingSubagentDraft holds wizard state for a single subagent being built.
type OnboardingSubagentDraft struct {
	Name   string
	Idea   string
	Prompt string
}

// OnboardingWizard is the onboarding wizard component.
// It is a pure state machine: HandleKey advances state and emits messages;
// View renders the current step.
type OnboardingWizard struct {
	Width, Height int
	Theme         *theme.Theme
	Providers     []string // known provider names

	Step OnboardingStep

	// step 0/1: provider + API key
	ProviderCursor      int
	ProviderName        string
	APIKey              string
	ProviderKeys        map[string]string
	ConfiguredProviders map[string]bool

	// step 2: model pick
	Models       []string // filled after provider is confirmed
	ModelCursor  int
	ModelQuery   string
	ModelName    string
	ModelNameRaw string // typed entry

	// step 3: mode name
	ModeName string

	// step 4: mode behavior idea
	ModeIdea string

	// step 5: generated prompt for mode
	ModePrompt    string
	PromptLoading bool
	PromptLoadErr string
	PromptEditing bool
	PromptEditBuf string

	// subagent wizard
	SubagentDrafts  []OnboardingSubagentDraft
	CurrentSubagent OnboardingSubagentDraft
	SubagentLoading bool
	SubagentLoadErr string
	SubagentEditing bool
	SubagentEditBuf string

	// general typing buffer (single-line fields)
	inputBuf string

	// toast message shown at the bottom (transient error or notice)
	toast string

	// saving tracks whether the final save is in progress
	Saving    bool
	SaveError string
}

// OnboardingKey is the wizard-local key event.
type OnboardingKey struct {
	Name  string
	Runes []rune
}

// OnboardingMsg is emitted when the wizard needs the App to act.
type OnboardingMsg interface{ onboardingMsg() }

// OnboardingClose is emitted when the user presses Esc on the welcome step.
type OnboardingClose struct{}

// OnboardingGeneratePrompt asks the App to call GeneratePrompt asynchronously
// (provider/model are ready in wizard state).
type OnboardingGeneratePrompt struct {
	ProviderName string
	ModelName    string
	APIKey       string
	Idea         string
	ForSubagent  bool
}

// OnboardingGeneratedPromptReady is sent back to the wizard by the App after
// generation completes.  Err is non-nil on failure.
type OnboardingGeneratedPromptReady struct {
	Prompt      string
	Err         error
	ForSubagent bool
}

// OnboardingSaveResult asks the App to persist the final wizard output.
type OnboardingSaveResult struct {
	ProviderName   string
	ProviderAPIKey string
	Mode           config.ModeConfig
	Subagents      []OnboardingSubagentDraft
}

// OnboardingSaved is sent back to the wizard when the App has finished
// persisting the result (Err non-nil on failure).
type OnboardingSaved struct{ Err error }

func (OnboardingClose) onboardingMsg()                {}
func (OnboardingGeneratePrompt) onboardingMsg()       {}
func (OnboardingGeneratedPromptReady) onboardingMsg() {}
func (OnboardingSaveResult) onboardingMsg()           {}
func (OnboardingSaved) onboardingMsg()                {}

// HandleKey advances wizard state, returns updated wizard and optional message.
func (w OnboardingWizard) HandleKey(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	w.toast = ""

	switch w.Step {
	case OnboardStepWelcome:
		return w.handleWelcome(k)
	case OnboardStepAPIKey:
		return w.handleAPIKey(k)
	case OnboardStepProviderMore:
		return w.handleProviderMore(k)
	case OnboardStepPickModel:
		return w.handlePickModel(k)
	case OnboardStepModeName:
		return w.handleModeName(k)
	case OnboardStepModeIdea:
		return w.handleModeIdea(k)
	case OnboardStepPromptReview:
		return w.handlePromptReview(k)
	case OnboardStepSubagentOffer:
		return w.handleSubagentOffer(k)
	case OnboardStepSubagentName:
		return w.handleSubagentName(k)
	case OnboardStepSubagentIdea:
		return w.handleSubagentIdea(k)
	case OnboardStepSubagentPromptReview:
		return w.handleSubagentPromptReview(k)
	case OnboardStepDone:
		// nothing; waiting for save
	}
	return w, nil
}

// ApplyGeneratedPrompt feeds a completed prompt-generation result back.
func (w OnboardingWizard) ApplyGeneratedPrompt(msg OnboardingGeneratedPromptReady) OnboardingWizard {
	if msg.ForSubagent {
		w.SubagentLoading = false
		if msg.Err != nil {
			w.SubagentLoadErr = msg.Err.Error()
			w.CurrentSubagent.Prompt = ""
		} else {
			w.SubagentLoadErr = ""
			w.CurrentSubagent.Prompt = msg.Prompt
		}
	} else {
		w.PromptLoading = false
		if msg.Err != nil {
			w.PromptLoadErr = msg.Err.Error()
			w.ModePrompt = ""
		} else {
			w.PromptLoadErr = ""
			w.ModePrompt = msg.Prompt
		}
	}
	return w
}

// ------- step handlers -------------------------------------------------------

func (w OnboardingWizard) handleWelcome(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		return w, OnboardingClose{}
	case "up", "ctrl+p":
		if w.ProviderCursor > 0 {
			w.ProviderCursor--
		}
	case "down", "ctrl+n":
		if w.ProviderCursor < len(w.Providers)-1 {
			w.ProviderCursor++
		}
	case "enter":
		if len(w.Providers) == 0 {
			return w, nil
		}
		w.ProviderName = w.Providers[w.ProviderCursor]
		w.APIKey = ""
		w.inputBuf = ""
		if w.hasConfiguredProvider(w.ProviderName) {
			w.ModelQuery = ""
			w.ModelCursor = 0
			w.Step = OnboardStepPickModel
			return w, nil
		}
		w.Step = OnboardStepAPIKey
	}
	return w, nil
}

func (w OnboardingWizard) handleAPIKey(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		w.Step = OnboardStepWelcome
	case "enter":
		key := strings.TrimSpace(w.inputBuf)
		if key == "" {
			w.toast = "API key cannot be empty"
			return w, nil
		}
		w.APIKey = key
		if w.ProviderKeys == nil {
			w.ProviderKeys = map[string]string{}
		}
		w.ProviderKeys[w.ProviderName] = key
		w.inputBuf = ""
		w.Step = OnboardStepProviderMore
	case "backspace":
		if w.inputBuf != "" {
			r := []rune(w.inputBuf)
			w.inputBuf = string(r[:len(r)-1])
		}
	case "ctrl+u":
		w.inputBuf = ""
	default:
		if len(k.Runes) > 0 {
			w.inputBuf += string(k.Runes)
		}
	}
	return w, nil
}

func (w OnboardingWizard) handleProviderMore(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	configured := w.configuredProviderCount()
	switch k.Name {
	case "y":
		w.Step = OnboardStepWelcome
		w.inputBuf = ""
	case "n", "enter":
		if configured == 0 {
			w.toast = "Configure at least one provider first"
			return w, nil
		}
		w.ModelQuery = ""
		w.ModelCursor = 0
		w.Step = OnboardStepPickModel
	case "esc":
		if w.hasConfiguredProvider(w.ProviderName) {
			w.Step = OnboardStepWelcome
		} else {
			w.Step = OnboardStepAPIKey
		}
	}
	return w, nil
}

func (w OnboardingWizard) hasConfiguredProvider(providerName string) bool {
	if w.ConfiguredProviders == nil {
		return false
	}
	return w.ConfiguredProviders[providerName]
}

func (w OnboardingWizard) configuredProviderCount() int {
	configured := len(w.ConfiguredProviders)
	for name := range w.ProviderKeys {
		if !w.ConfiguredProviders[name] {
			configured++
		}
	}
	return configured
}

func (w OnboardingWizard) filteredModels() []string {
	q := strings.ToLower(strings.TrimSpace(w.ModelQuery))
	if q == "" {
		return w.Models
	}
	out := make([]string, 0, len(w.Models))
	for _, m := range w.Models {
		if strings.Contains(strings.ToLower(m), q) {
			out = append(out, m)
		}
	}
	return out
}

func (w OnboardingWizard) handlePickModel(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	filtered := w.filteredModels()
	switch k.Name {
	case "esc":
		if w.hasConfiguredProvider(w.ProviderName) {
			w.Step = OnboardStepWelcome
		} else {
			w.Step = OnboardStepAPIKey
		}
		w.inputBuf = ""
	case "up", "ctrl+p":
		if w.ModelCursor > 0 {
			w.ModelCursor--
		}
	case "down", "ctrl+n":
		if len(filtered) > 0 && w.ModelCursor < len(filtered)-1 {
			w.ModelCursor++
		}
	case "enter":
		// Allow free-text entry if nothing in the list or query matches exactly.
		choice := strings.TrimSpace(w.ModelQuery)
		if len(filtered) > 0 && w.ModelCursor < len(filtered) {
			choice = filtered[w.ModelCursor]
		}
		if choice == "" {
			w.toast = "Model name cannot be empty"
			return w, nil
		}
		w.ModelName = choice
		w.inputBuf = ""
		w.Step = OnboardStepModeName
	case "backspace":
		if w.ModelQuery != "" {
			r := []rune(w.ModelQuery)
			w.ModelQuery = string(r[:len(r)-1])
			w.ModelCursor = 0
		}
	default:
		if len(k.Runes) > 0 {
			w.ModelQuery += string(k.Runes)
			w.ModelCursor = 0
		}
	}
	return w, nil
}

func (w OnboardingWizard) handleModeName(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		w.Step = OnboardStepPickModel
	case "enter":
		name := strings.TrimSpace(w.inputBuf)
		if name == "" {
			name = "General"
		}
		w.ModeName = name
		w.inputBuf = ""
		w.Step = OnboardStepModeIdea
	case "backspace":
		if w.inputBuf != "" {
			r := []rune(w.inputBuf)
			w.inputBuf = string(r[:len(r)-1])
		}
	default:
		if len(k.Runes) > 0 {
			w.inputBuf += string(k.Runes)
		}
	}
	return w, nil
}

func (w OnboardingWizard) handleModeIdea(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		w.Step = OnboardStepModeName
		w.inputBuf = w.ModeName
	case "enter":
		idea := strings.TrimSpace(w.inputBuf)
		if idea == "" {
			// Skip prompt generation; use an empty prompt.
			w.ModePrompt = ""
			w.ModeIdea = ""
			w.inputBuf = ""
			w.Step = OnboardStepPromptReview
			return w, nil
		}
		w.ModeIdea = idea
		w.inputBuf = ""
		w.PromptLoading = true
		w.PromptLoadErr = ""
		w.ModePrompt = ""
		w.Step = OnboardStepPromptReview
		return w, OnboardingGeneratePrompt{
			ProviderName: w.ProviderName,
			ModelName:    w.ModelName,
			APIKey:       w.APIKey,
			Idea:         idea,
			ForSubagent:  false,
		}
	case "backspace":
		if w.inputBuf != "" {
			r := []rune(w.inputBuf)
			w.inputBuf = string(r[:len(r)-1])
		}
	default:
		if len(k.Runes) > 0 {
			w.inputBuf += string(k.Runes)
		}
	}
	return w, nil
}

func (w OnboardingWizard) handlePromptReview(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	if w.PromptLoading {
		// Let 'e' open edit mode even while loading; 's' skips; otherwise consume.
		if k.Name == "s" {
			w.PromptLoading = false
			w.ModePrompt = ""
			w.PromptLoadErr = ""
			w.Step = OnboardStepSubagentOffer
		}
		return w, nil
	}
	if w.PromptEditing {
		return w.handlePromptEdit(k, false)
	}
	switch k.Name {
	case "e":
		w.PromptEditing = true
		w.PromptEditBuf = w.ModePrompt
	case "enter", "y":
		w.Step = OnboardStepSubagentOffer
	case "r":
		// Retry generation if we have an idea.
		if w.ModeIdea != "" {
			w.PromptLoading = true
			w.PromptLoadErr = ""
			return w, OnboardingGeneratePrompt{
				ProviderName: w.ProviderName,
				ModelName:    w.ModelName,
				APIKey:       w.APIKey,
				Idea:         w.ModeIdea,
				ForSubagent:  false,
			}
		}
		w.toast = "No idea to regenerate from — press 'e' to edit manually"
	case "s":
		// Skip prompt entirely.
		w.ModePrompt = ""
		w.Step = OnboardStepSubagentOffer
	case "esc":
		w.Step = OnboardStepModeIdea
		w.inputBuf = w.ModeIdea
	}
	return w, nil
}

func (w OnboardingWizard) handlePromptEdit(k OnboardingKey, forSubagent bool) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		w.PromptEditing = false
		w.SubagentEditing = false
		// Discard edits.
	case "enter":
		if forSubagent {
			w.CurrentSubagent.Prompt = w.SubagentEditBuf
			w.SubagentEditing = false
		} else {
			w.ModePrompt = w.PromptEditBuf
			w.PromptEditing = false
		}
	case "backspace":
		if forSubagent {
			if w.SubagentEditBuf != "" {
				r := []rune(w.SubagentEditBuf)
				w.SubagentEditBuf = string(r[:len(r)-1])
			}
		} else {
			if w.PromptEditBuf != "" {
				r := []rune(w.PromptEditBuf)
				w.PromptEditBuf = string(r[:len(r)-1])
			}
		}
	default:
		if len(k.Runes) > 0 {
			if forSubagent {
				w.SubagentEditBuf += string(k.Runes)
			} else {
				w.PromptEditBuf += string(k.Runes)
			}
		}
	}
	return w, nil
}

func (w OnboardingWizard) handleSubagentOffer(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "y", "enter":
		w.CurrentSubagent = OnboardingSubagentDraft{}
		w.inputBuf = ""
		w.Step = OnboardStepSubagentName
	case "n", "esc":
		w.Step = OnboardStepDone
		return w, w.buildSaveResult()
	}
	return w, nil
}

func (w OnboardingWizard) handleSubagentName(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		w.Step = OnboardStepSubagentOffer
	case "enter":
		name := strings.TrimSpace(w.inputBuf)
		if name == "" {
			w.toast = "Subagent name cannot be empty"
			return w, nil
		}
		// Validate: lowercase identifier-like.
		for _, r := range name {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '_' {
				w.toast = "Name must be lowercase letters, digits, and underscores only"
				return w, nil
			}
		}
		w.CurrentSubagent.Name = name
		w.inputBuf = ""
		w.Step = OnboardStepSubagentIdea
	case "backspace":
		if w.inputBuf != "" {
			r := []rune(w.inputBuf)
			w.inputBuf = string(r[:len(r)-1])
		}
	default:
		if len(k.Runes) > 0 {
			w.inputBuf += string(k.Runes)
		}
	}
	return w, nil
}

func (w OnboardingWizard) handleSubagentIdea(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	switch k.Name {
	case "esc":
		w.Step = OnboardStepSubagentName
		w.inputBuf = w.CurrentSubagent.Name
	case "enter":
		idea := strings.TrimSpace(w.inputBuf)
		w.CurrentSubagent.Idea = idea
		w.inputBuf = ""
		w.SubagentLoading = idea != ""
		w.SubagentLoadErr = ""
		w.CurrentSubagent.Prompt = ""
		w.Step = OnboardStepSubagentPromptReview
		if idea != "" {
			return w, OnboardingGeneratePrompt{
				ProviderName: w.ProviderName,
				ModelName:    w.ModelName,
				APIKey:       w.APIKey,
				Idea:         idea,
				ForSubagent:  true,
			}
		}
	case "backspace":
		if w.inputBuf != "" {
			r := []rune(w.inputBuf)
			w.inputBuf = string(r[:len(r)-1])
		}
	default:
		if len(k.Runes) > 0 {
			w.inputBuf += string(k.Runes)
		}
	}
	return w, nil
}

func (w OnboardingWizard) handleSubagentPromptReview(k OnboardingKey) (OnboardingWizard, OnboardingMsg) {
	if w.SubagentLoading {
		if k.Name == "s" {
			w.SubagentLoading = false
			w.CurrentSubagent.Prompt = ""
		}
		return w, nil
	}
	if w.SubagentEditing {
		return w.handlePromptEdit(k, true)
	}
	switch k.Name {
	case "e":
		w.SubagentEditing = true
		w.SubagentEditBuf = w.CurrentSubagent.Prompt
	case "enter", "y":
		// Accept subagent, offer another.
		w.SubagentDrafts = append(w.SubagentDrafts, w.CurrentSubagent)
		w.CurrentSubagent = OnboardingSubagentDraft{}
		w.Step = OnboardStepSubagentOffer
	case "r":
		if w.CurrentSubagent.Idea != "" {
			w.SubagentLoading = true
			w.SubagentLoadErr = ""
			return w, OnboardingGeneratePrompt{
				ProviderName: w.ProviderName,
				ModelName:    w.ModelName,
				APIKey:       w.APIKey,
				Idea:         w.CurrentSubagent.Idea,
				ForSubagent:  true,
			}
		}
		w.toast = "No idea to regenerate from — press 'e' to edit manually"
	case "s":
		w.CurrentSubagent.Prompt = ""
		w.SubagentDrafts = append(w.SubagentDrafts, w.CurrentSubagent)
		w.CurrentSubagent = OnboardingSubagentDraft{}
		w.Step = OnboardStepSubagentOffer
	case "esc":
		w.Step = OnboardStepSubagentIdea
		w.inputBuf = w.CurrentSubagent.Idea
	}
	return w, nil
}

func (w OnboardingWizard) buildSaveResult() OnboardingSaveResult {
	mode := config.ModeConfig{
		Name:        w.ModeName,
		Provider:    w.ProviderName,
		Model:       w.ModelName,
		Prompt:      w.ModePrompt,
		Description: "Created during onboarding",
	}
	if mode.Name == "" {
		mode.Name = "General"
	}
	agents := make([]OnboardingSubagentDraft, len(w.SubagentDrafts))
	copy(agents, w.SubagentDrafts)
	return OnboardingSaveResult{
		ProviderName:   w.ProviderName,
		ProviderAPIKey: w.APIKey,
		Mode:           mode,
		Subagents:      agents,
	}
}

// ------- View ---------------------------------------------------------------

// View renders the wizard overlay.
func (w OnboardingWizard) View() string {
	width, height := w.Width, w.Height
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 30
	}

	maxW := minInt(width-8, 90)
	border := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(1, 2).Width(maxW)
	primary := lipgloss.NewStyle().Bold(true)
	muted := lipgloss.NewStyle().Faint(true)
	accent := lipgloss.NewStyle().Bold(true)
	warn := lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	if w.Theme != nil {
		border = border.BorderForeground(w.Theme.Style(theme.AtomModalBorder).GetForeground())
		primary = w.Theme.Style(theme.AtomPrimary).Bold(true)
		muted = w.Theme.Style(theme.AtomMuted)
		accent = w.Theme.Style(theme.AtomAccent).Bold(true)
		warn = w.Theme.Style(theme.AtomWarn)
	}

	var body strings.Builder

	switch w.Step {
	case OnboardStepWelcome:
		body.WriteString(primary.Render("Welcome to Hygge  ✦") + "\n")
		body.WriteString(muted.Render("Choose a provider to get started.") + "\n\n")
		for i, p := range w.Providers {
			line := fmt.Sprintf("  %-14s", p)
			if i == w.ProviderCursor {
				line = accent.Render("› " + strings.TrimPrefix(line, "  "))
			}
			body.WriteString(line + "\n")
		}
		body.WriteString("\n" + muted.Render("↑/↓ navigate   enter select   esc exit"))

	case OnboardStepAPIKey:
		body.WriteString(primary.Render("API Key for "+w.ProviderName) + "\n")
		body.WriteString(muted.Render("Stored in your config file; not logged.") + "\n\n")
		masked := strings.Repeat("•", len([]rune(w.inputBuf)))
		fmt.Fprintf(&body, "Key: %s\n", masked)
		if w.toast != "" {
			body.WriteString("\n" + warn.Render(w.toast) + "\n")
		}
		body.WriteString("\n" + muted.Render("enter confirm   backspace edit   ctrl+u clear   esc back"))

	case OnboardStepProviderMore:
		body.WriteString(primary.Render("Provider saved") + "\n")
		body.WriteString(muted.Render("Configure as many providers as you want before creating your first mode.") + "\n\n")
		fmt.Fprintf(&body, "Configured providers: %d\n", w.configuredProviderCount())
		body.WriteString("\n" + muted.Render("y add another provider   n/enter continue to model   esc back"))

	case OnboardStepPickModel:
		body.WriteString(primary.Render("Choose a model for "+w.ProviderName) + "\n")
		body.WriteString(muted.Render("Type to filter, or enter a model name directly.") + "\n\n")
		fmt.Fprintf(&body, "Filter: %s\n", w.ModelQuery)
		filtered := w.filteredModels()
		if len(filtered) == 0 && w.ModelQuery != "" {
			body.WriteString(muted.Render("  No matches — press enter to use your typed name") + "\n")
		} else {
			limit := minInt(len(filtered), maxInt(4, height-14))
			start := 0
			if w.ModelCursor >= limit {
				start = w.ModelCursor - limit + 1
			}
			for i := start; i < len(filtered) && i < start+limit; i++ {
				m := filtered[i]
				if i == w.ModelCursor {
					body.WriteString(accent.Render("› "+m) + "\n")
				} else {
					body.WriteString("  " + m + "\n")
				}
			}
		}
		if w.toast != "" {
			body.WriteString("\n" + warn.Render(w.toast) + "\n")
		}
		body.WriteString("\n" + muted.Render("↑/↓ navigate   enter select   esc back"))

	case OnboardStepModeName:
		body.WriteString(primary.Render("Name this mode") + "\n")
		body.WriteString(muted.Render("A short label shown in the mode picker. Default: General") + "\n\n")
		fmt.Fprintf(&body, "Name: %s\n", w.inputBuf)
		body.WriteString("\n" + muted.Render("enter confirm (empty = General)   backspace edit   esc back"))

	case OnboardStepModeIdea:
		body.WriteString(primary.Render("Describe this mode's behavior") + "\n")
		body.WriteString(muted.Render("One sentence: what should this mode help you do?") + "\n")
		body.WriteString(muted.Render("Leave empty to skip prompt generation.") + "\n\n")
		fmt.Fprintf(&body, "Idea: %s\n", w.inputBuf)
		body.WriteString("\n" + muted.Render("enter generate (or skip if empty)   esc back"))

	case OnboardStepPromptReview:
		body.WriteString(primary.Render("System prompt for mode: "+w.ModeName) + "\n\n")
		if w.PromptLoading {
			body.WriteString(accent.Render("⟳ Generating prompt…") + "\n\n")
			body.WriteString(muted.Render("s skip generation"))
		} else if w.PromptEditing {
			body.WriteString(muted.Render("Editing (enter save, esc discard):") + "\n")
			printPromptPreview(&body, w.PromptEditBuf, maxW-8, 8, muted)
			body.WriteString("\n" + muted.Render("enter save   esc discard"))
		} else {
			if w.PromptLoadErr != "" {
				body.WriteString(warn.Render("Generation failed: "+w.PromptLoadErr) + "\n\n")
			}
			if w.ModePrompt == "" {
				body.WriteString(muted.Render("(no prompt — mode inherits the default system prompt)") + "\n")
			} else {
				printPromptPreview(&body, w.ModePrompt, maxW-8, 8, muted)
			}
			body.WriteString("\n" + muted.Render("enter/y accept   e edit   r retry   s skip   esc back"))
		}

	case OnboardStepSubagentOffer:
		body.WriteString(primary.Render("Add a subagent?") + "\n")
		body.WriteString(muted.Render("Subagents are specialized agents the LLM can delegate tasks to.") + "\n")
		if len(w.SubagentDrafts) > 0 {
			fmt.Fprintf(&body, "\nAdded so far: ")
			names := make([]string, len(w.SubagentDrafts))
			for i, sa := range w.SubagentDrafts {
				names[i] = sa.Name
			}
			body.WriteString(accent.Render(strings.Join(names, ", ")) + "\n")
		}
		body.WriteString("\n" + muted.Render("y/enter yes   n/esc finish setup"))

	case OnboardStepSubagentName:
		body.WriteString(primary.Render("Subagent name") + "\n")
		body.WriteString(muted.Render("Lowercase letters, digits, underscores (e.g. search_agent)") + "\n\n")
		fmt.Fprintf(&body, "Name: %s\n", w.inputBuf)
		if w.toast != "" {
			body.WriteString("\n" + warn.Render(w.toast) + "\n")
		}
		body.WriteString("\n" + muted.Render("enter confirm   backspace edit   esc back"))

	case OnboardStepSubagentIdea:
		body.WriteString(primary.Render("Describe subagent: "+w.CurrentSubagent.Name) + "\n")
		body.WriteString(muted.Render("One sentence: what should this subagent do?") + "\n")
		body.WriteString(muted.Render("Leave empty to skip prompt generation.") + "\n\n")
		fmt.Fprintf(&body, "Idea: %s\n", w.inputBuf)
		body.WriteString("\n" + muted.Render("enter generate (or skip if empty)   esc back"))

	case OnboardStepSubagentPromptReview:
		body.WriteString(primary.Render("System prompt for subagent: "+w.CurrentSubagent.Name) + "\n\n")
		if w.SubagentLoading {
			body.WriteString(accent.Render("⟳ Generating prompt…") + "\n\n")
			body.WriteString(muted.Render("s skip generation"))
		} else if w.SubagentEditing {
			body.WriteString(muted.Render("Editing (enter save, esc discard):") + "\n")
			printPromptPreview(&body, w.SubagentEditBuf, maxW-8, 8, muted)
			body.WriteString("\n" + muted.Render("enter save   esc discard"))
		} else {
			if w.SubagentLoadErr != "" {
				body.WriteString(warn.Render("Generation failed: "+w.SubagentLoadErr) + "\n\n")
			}
			if w.CurrentSubagent.Prompt == "" {
				body.WriteString(muted.Render("(no prompt — subagent inherits the default system prompt)") + "\n")
			} else {
				printPromptPreview(&body, w.CurrentSubagent.Prompt, maxW-8, 8, muted)
			}
			body.WriteString("\n" + muted.Render("enter/y accept   e edit   r retry   s skip (no prompt)   esc back"))
		}

	case OnboardStepDone:
		if w.SaveError != "" {
			body.WriteString(primary.Render("Setup error") + "\n\n")
			body.WriteString(warn.Render(w.SaveError) + "\n\n")
			body.WriteString(muted.Render("Configuration may be partially saved. Restart Hygge to retry."))
		} else if w.Saving {
			body.WriteString(accent.Render("⟳ Saving configuration…"))
		} else {
			body.WriteString(primary.Render("Setup complete  ✦") + "\n\n")
			body.WriteString(muted.Render("Hygge is configured and ready.") + "\n\n")
			body.WriteString(muted.Render("Press any key to start chatting."))
		}
	}

	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, border.Render(body.String()))
}

// printPromptPreview writes up to maxLines lines of prompt (truncated) into b.
func printPromptPreview(b *strings.Builder, prompt string, maxW, maxLines int, muted lipgloss.Style) {
	if maxW <= 0 {
		maxW = 60
	}
	lines := strings.Split(prompt, "\n")
	for i, line := range lines {
		if i >= maxLines {
			b.WriteString(muted.Render(fmt.Sprintf("  … (%d more lines)", len(lines)-maxLines)) + "\n")
			break
		}
		if len(line) > maxW {
			line = line[:maxW-1] + "…"
		}
		b.WriteString("  " + line + "\n")
	}
}
