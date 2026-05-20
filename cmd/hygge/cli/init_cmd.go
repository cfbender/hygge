package cli

import (
	"bufio"
	"context"
	"embed"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/config"
)

//go:embed initstyles/*/*.md
var initStylePrompts embed.FS

var errInitCancelled = errors.New("init cancelled")

type initOptions struct {
	provider string
	model    string
}

type initStyle struct {
	Name        string
	Description string
	Modes       []initModeDefault
	Subagents   []initSubagentDefault
}

type initModeDefault struct {
	Name        string
	Description string
	PromptFile  string
	Reasoning   string
	Color       string
}

type initSubagentDefault struct {
	Name        string
	Description string
	PromptFile  string
}

func newInitCmd() *cobra.Command {
	opts := initOptions{}
	cmd := &cobra.Command{
		Use:   "init [style]",
		Short: "Initialize Hygge modes and subagents",
		Long: `Initialize Hygge modes and subagents.

Available styles: general, amp, opencode. If no style is supplied, Hygge checks
for configured provider auth and prompts you to pick a style. The generated
mode prompts are written as files under $XDG_CONFIG_HOME/hygge/prompts so they
can be edited and extended later.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			style := ""
			if len(args) > 0 {
				style = args[0]
			}
			return runInit(cmd, opts, style)
		},
	}
	cmd.Flags().StringVar(&opts.provider, "provider", "", "provider to assign to generated modes/subagents")
	cmd.Flags().StringVar(&opts.model, "model", "", "model to assign to generated modes/subagents")
	return cmd
}

func runInit(cmd *cobra.Command, opts initOptions, styleName string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	rt, err := bootstrap(ctx, bootstrapOptions{
		ConfigFile:      rootFlags.ConfigFile,
		ProfileName:     rootFlags.Profile,
		Pwd:             rootFlags.Pwd,
		ProviderFactory: stubProviderFactory,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()

	reader := bufio.NewReader(cmd.InOrStdin())
	isTTY := term.IsTerminal(int(os.Stdin.Fd()))
	styles := availableInitStyles()
	style, err := resolveInitStyle(cmd, reader, isTTY, styles, styleName)
	if err != nil {
		return err
	}

	configured := authConfiguredProviders(rt.StateOpts)
	if len(configured) == 0 {
		return die(cmd, "no providers are configured or authenticated; run `hygge provider auth <provider>` first")
	}

	providerName := strings.TrimSpace(opts.provider)
	if providerName == "" {
		providerName, err = pickInitProvider(cmd, reader, isTTY, configured)
		if err != nil {
			return err
		}
	}
	if providerName == "" {
		return die(cmd, "provider is required (pass --provider or run interactively)")
	}
	if !containsString(configured, providerName) {
		return die(cmd, "provider %q is not configured or authenticated (run `hygge provider auth %s`)", providerName, providerName)
	}

	modelName := strings.TrimSpace(opts.model)
	if modelName == "" {
		cat := rt.Catalog.Source()
		if cat == nil {
			return die(cmd, "model is required (pass --model; no catalog available to pick from)")
		}
		modelName, err = pickModel(cmd, reader, isTTY, cat, providerName)
		if err != nil {
			return err
		}
	}
	if modelName == "" {
		return die(cmd, "model is required (pass --model or run interactively)")
	}

	resolved, err := materializeInitStyle(rt.XDGConfigHome, style, providerName, modelName)
	if err != nil {
		return err
	}
	configPath, err := config.WriteInitStyle(config.WriteInitStyleOptions{
		HomeDir:       rt.StateOpts.HomeDir,
		XDGConfigHome: rt.XDGConfigHome,
		Pwd:           rt.Pwd,
		Provenance:    rt.Provenance,
	}, resolved)
	if err != nil {
		return err
	}

	printInitSuccess(cmd, style, providerName, modelName, configPath, filepath.Join(rt.XDGConfigHome, "hygge", "prompts", style.Name), len(resolved.Subagents) > 0, filepath.Join(rt.XDGConfigHome, "hygge", "subagents.toml"))
	return nil
}

func availableInitStyles() []initStyle {
	return []initStyle{
		{
			Name:        "general",
			Description: "Single general engineering mode with no subagents",
			Modes: []initModeDefault{{
				Name:        "general",
				Description: "General engineering work",
				PromptFile:  "general/general.md",
				Color:       "#7c83ff",
			}},
		},
		{
			Name:        "amp",
			Description: "Orchestrated modes and specialist subagents",
			Modes: []initModeDefault{
				{Name: "smart", Description: "Balanced orchestrator for most engineering work", PromptFile: "amp/smart.md", Color: "#7c83ff"},
				{Name: "rush", Description: "Tiny low-risk changes and quick answers", PromptFile: "amp/rush.md", Color: "#22c55e"},
				{Name: "deep", Description: "Deliberate planning for complex or risky work", PromptFile: "amp/deep.md", Reasoning: "high", Color: "#a855f7"},
			},
			Subagents: []initSubagentDefault{
				{Name: "search", Description: "Read-only codebase discovery with file:line citations", PromptFile: "amp/search.md"},
				{Name: "librarian", Description: "External documentation and dependency research", PromptFile: "amp/librarian.md"},
				{Name: "carpenter", Description: "Focused implementation from an approved objective", PromptFile: "amp/carpenter.md"},
				{Name: "oracle", Description: "Senior advice for hard decisions and repeated failures", PromptFile: "amp/oracle.md"},
			},
		},
		{
			Name:        "opencode",
			Description: "Build/plan primary modes plus general/explore/scout subagents",
			Modes: []initModeDefault{
				{Name: "build", Description: "Default development mode with full coding workflow", PromptFile: "opencode/build.md", Color: "#22c55e"},
				{Name: "plan", Description: "Planning and analysis before changes", PromptFile: "opencode/plan.md", Reasoning: "low", Color: "#f59e0b"},
			},
			Subagents: []initSubagentDefault{
				{Name: "general", Description: "General-purpose subagent for complex multi-step tasks", PromptFile: "opencode/general.md"},
				{Name: "explore", Description: "Fast read-only codebase exploration", PromptFile: "opencode/explore.md"},
				{Name: "scout", Description: "External docs and dependency research", PromptFile: "opencode/scout.md"},
			},
		},
	}
}

func resolveInitStyle(cmd *cobra.Command, reader *bufio.Reader, isTTY bool, styles []initStyle, name string) (initStyle, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if name != "" {
		for _, style := range styles {
			if style.Name == name {
				return style, nil
			}
		}
		return initStyle{}, die(cmd, "unknown init style %q (available: %s)", name, strings.Join(initStyleNames(styles), ", "))
	}
	if isTTY {
		return pickInitStyleInteractive(cmd, styles)
	}
	return readInitStyleFromLine(cmd, reader, styles)
}

func readInitStyleFromLine(cmd *cobra.Command, reader *bufio.Reader, styles []initStyle) (initStyle, error) {
	line, err := readLineWithContext(cmd.Context(), reader)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeln(out(cmd), initMutedStyle().Render("Cancelled."))
			return initStyle{}, errInitCancelled
		}
		return initStyle{}, fmt.Errorf("read init style: %w", err)
	}
	choice := strings.ToLower(strings.TrimSpace(line))
	if choice == "" {
		return initStyle{}, die(cmd, "init style is required in non-interactive mode (choose one of: %s)", strings.Join(initStyleNames(styles), ", "))
	}
	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(styles) {
			return initStyle{}, fmt.Errorf("selection %d out of range [1, %d]", n, len(styles))
		}
		return styles[n-1], nil
	}
	return resolveInitStyle(cmd, reader, false, styles, choice)
}

func pickInitProvider(cmd *cobra.Command, reader *bufio.Reader, isTTY bool, providers []string) (string, error) {
	if len(providers) == 1 {
		return providers[0], nil
	}
	if isTTY {
		return pickInitProviderInteractive(cmd, providers)
	}
	return readInitProviderFromLine(cmd, reader, providers)
}

func readInitProviderFromLine(cmd *cobra.Command, reader *bufio.Reader, providers []string) (string, error) {
	line, err := readLineWithContext(cmd.Context(), reader)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			writeln(out(cmd), initMutedStyle().Render("Cancelled."))
			return "", errInitCancelled
		}
		return "", fmt.Errorf("read provider selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(providers) {
			return "", fmt.Errorf("selection %d out of range [1, %d]", n, len(providers))
		}
		return providers[n-1], nil
	}
	return choice, nil
}

func readLineWithContext(ctx context.Context, reader *bufio.Reader) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	type result struct {
		line string
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		line, err := reader.ReadString('\n')
		ch <- result{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		return res.line, res.err
	}
}

func pickInitStyleInteractive(cmd *cobra.Command, styles []initStyle) (initStyle, error) {
	items := make([]list.Item, 0, len(styles))
	for _, style := range styles {
		items = append(items, initStyleSelectItem{style: style})
	}
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(initAccentColor())
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(initMutedColor())

	l := list.New(items, delegate, 76, min(12, max(8, len(items)+5)))
	l.Title = initTitleStyle().Render("Initialize Hygge")
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.SetShowHelp(true)
	l.KeyMap.ForceQuit.SetHelp("ctrl+c", "cancel")
	l.AdditionalShortHelpKeys = func() []key.Binding { return nil }

	m := initStyleSelectModel{list: l}
	p := tea.NewProgram(m, tea.WithInput(cmd.InOrStdin()), tea.WithOutput(out(cmd)), tea.WithContext(cmd.Context()))
	finalModel, err := p.Run()
	if err != nil {
		return initStyle{}, fmt.Errorf("init style picker: %w", err)
	}
	selected, ok := finalModel.(initStyleSelectModel)
	if !ok || selected.cancelled || selected.choice.Name == "" {
		writeln(out(cmd), initMutedStyle().Render("Cancelled."))
		return initStyle{}, errInitCancelled
	}
	return selected.choice, nil
}

type initStyleSelectItem struct{ style initStyle }

func (i initStyleSelectItem) FilterValue() string { return i.style.Name }
func (i initStyleSelectItem) Title() string       { return i.style.Name }
func (i initStyleSelectItem) Description() string { return i.style.Description }

type initStyleSelectModel struct {
	list      list.Model
	choice    initStyle
	cancelled bool
}

func (m initStyleSelectModel) Init() tea.Cmd { return nil }

func (m initStyleSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.Keystroke() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if item, ok := m.list.SelectedItem().(initStyleSelectItem); ok {
				m.choice = item.style
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m initStyleSelectModel) View() tea.View { return tea.NewView(m.list.View()) }

func pickInitProviderInteractive(cmd *cobra.Command, providers []string) (string, error) {
	items := make([]list.Item, 0, len(providers))
	for _, provider := range providers {
		items = append(items, initProviderSelectItem{name: provider})
	}
	delegate := list.NewDefaultDelegate()
	delegate.SetSpacing(0)
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.Foreground(initAccentColor())
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.Foreground(initMutedColor())

	l := list.New(items, delegate, 76, min(14, max(8, len(items)+5)))
	l.Title = initTitleStyle().Render("Choose a configured provider")
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)
	l.SetShowHelp(true)
	l.KeyMap.ForceQuit.SetHelp("ctrl+c", "cancel")
	l.AdditionalShortHelpKeys = func() []key.Binding { return nil }

	m := initProviderSelectModel{list: l}
	p := tea.NewProgram(m, tea.WithInput(cmd.InOrStdin()), tea.WithOutput(out(cmd)), tea.WithContext(cmd.Context()))
	finalModel, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("init provider picker: %w", err)
	}
	selected, ok := finalModel.(initProviderSelectModel)
	if !ok || selected.cancelled || selected.choice == "" {
		writeln(out(cmd), initMutedStyle().Render("Cancelled."))
		return "", errInitCancelled
	}
	return selected.choice, nil
}

type initProviderSelectItem struct{ name string }

func (i initProviderSelectItem) FilterValue() string { return i.name }
func (i initProviderSelectItem) Title() string       { return i.name }
func (i initProviderSelectItem) Description() string {
	if env := providerEnvVar(i.name); env != "" {
		return "configured via auth store or " + env
	}
	return "configured provider"
}

type initProviderSelectModel struct {
	list      list.Model
	choice    string
	cancelled bool
}

func (m initProviderSelectModel) Init() tea.Cmd { return nil }

func (m initProviderSelectModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		switch keyMsg.Keystroke() {
		case "ctrl+c", "esc":
			m.cancelled = true
			return m, tea.Quit
		case "enter":
			if item, ok := m.list.SelectedItem().(initProviderSelectItem); ok {
				m.choice = item.name
				return m, tea.Quit
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m initProviderSelectModel) View() tea.View { return tea.NewView(m.list.View()) }

func printInitSuccess(cmd *cobra.Command, style initStyle, providerName, modelName, configPath, promptDir string, wroteSubagents bool, subagentsPath string) {
	styles := initCLIStyles()
	writeln(out(cmd), styles.Title.Render("✓ Hygge initialized"))
	printf(out(cmd), "%s %s %s\n", styles.Label.Render("Style"), styles.Value.Render(style.Name), styles.Muted.Render(style.Description))
	printf(out(cmd), "%s %s\n", styles.Label.Render("Model"), styles.Value.Render(providerName+"/"+modelName))
	printf(out(cmd), "%s %s\n", styles.Label.Render("Config"), styles.Path.Render(configPath))
	printf(out(cmd), "%s %s\n", styles.Label.Render("Prompts"), styles.Path.Render(promptDir))
	if wroteSubagents {
		printf(out(cmd), "%s %s\n", styles.Label.Render("Subagents"), styles.Path.Render(subagentsPath))
	}
	writeln(out(cmd), styles.Muted.Render("Edit the generated prompt files any time to tune the style."))
}

type initStyles struct {
	Title lipgloss.Style
	Label lipgloss.Style
	Value lipgloss.Style
	Path  lipgloss.Style
	Muted lipgloss.Style
}

func initCLIStyles() initStyles {
	return initStyles{
		Title: lipgloss.NewStyle().Bold(true).Foreground(initSuccessColor()),
		Label: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#38BDF8")).Width(10),
		Value: lipgloss.NewStyle().Foreground(lipgloss.Color("#E5E7EB")),
		Path:  lipgloss.NewStyle().Foreground(lipgloss.Color("#A78BFA")),
		Muted: initMutedStyle(),
	}
}

func initTitleStyle() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(initAccentColor())
}
func initMutedStyle() lipgloss.Style { return lipgloss.NewStyle().Foreground(initMutedColor()) }
func initAccentColor() color.Color   { return lipgloss.Color("#A78BFA") }
func initMutedColor() color.Color    { return lipgloss.Color("#9CA3AF") }
func initSuccessColor() color.Color  { return lipgloss.Color("#22C55E") }

func materializeInitStyle(xdgConfigHome string, style initStyle, providerName, modelName string) (config.InitStyleConfig, error) {
	promptDir := filepath.Join(xdgConfigHome, "hygge", "prompts", style.Name)
	if err := os.MkdirAll(promptDir, 0o700); err != nil {
		return config.InitStyleConfig{}, fmt.Errorf("init: create prompts dir: %w", err)
	}
	outStyle := config.InitStyleConfig{}
	for _, mode := range style.Modes {
		promptRef, err := writeInitPrompt(promptDir, mode.PromptFile)
		if err != nil {
			return config.InitStyleConfig{}, err
		}
		outStyle.Modes = append(outStyle.Modes, config.ModeConfig{
			Name:        mode.Name,
			Provider:    providerName,
			Model:       modelName,
			Reasoning:   mode.Reasoning,
			Prompt:      promptRef,
			Description: mode.Description,
			Color:       mode.Color,
		})
	}
	modelRef := providerName + "/" + modelName
	for _, sub := range style.Subagents {
		promptRef, err := writeInitPrompt(promptDir, sub.PromptFile)
		if err != nil {
			return config.InitStyleConfig{}, err
		}
		outStyle.Subagents = append(outStyle.Subagents, config.OnboardingSubagent{
			Name:        sub.Name,
			Description: sub.Description,
			Prompt:      promptRef,
			Model:       modelRef,
		})
	}
	return outStyle, nil
}

func writeInitPrompt(promptDir, embeddedName string) (string, error) {
	data, err := fs.ReadFile(initStylePrompts, "initstyles/"+embeddedName)
	if err != nil {
		return "", fmt.Errorf("init: read embedded prompt %s: %w", embeddedName, err)
	}
	name := filepath.Base(embeddedName)
	target := filepath.Join(promptDir, name)
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return "", fmt.Errorf("init: write prompt %s: %w", target, err)
	}
	return "file:" + filepath.ToSlash(filepath.Join("prompts", filepath.Base(promptDir), name)), nil
}

func initStyleNames(styles []initStyle) []string {
	names := make([]string, 0, len(styles))
	for _, style := range styles {
		names = append(names, style.Name)
	}
	return names
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}
