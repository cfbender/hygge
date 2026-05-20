package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/llm"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/ui"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// supportsProgressBar reports whether the current terminal can render an
// indeterminate progress bar via the OSC 9;4 escape sequence.  Only a
// known-good subset of terminal emulators support the sequence; on others
// the escape is silently ignored or rendered as garbage.
func supportsProgressBar() bool {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return false
	}
	termProg := os.Getenv("TERM_PROGRAM")
	_, isWindowsTerminal := os.LookupEnv("WT_SESSION")
	return isWindowsTerminal ||
		strings.Contains(strings.ToLower(termProg), "ghostty") ||
		strings.Contains(strings.ToLower(termProg), "iterm2") ||
		strings.Contains(strings.ToLower(termProg), "rio") ||
		strings.Contains(strings.ToLower(termProg), "wezterm") ||
		strings.Contains(strings.ToLower(termProg), "kitty")
	// Note: Zellij forwards TERM_PROGRAM from its host; if the user runs
	// hygge under Zellij in Ghostty/iTerm2, this still works.
}

// resumeFlag is the value bound to the root command's --resume flag.
// We use a package var (rather than a closure) so NewRootCmd can wire it
// into the persistent flag set above any subcommand.
var resumeFlag string

// reasoningFlag is the value bound to the root command's --reasoning
// flag.  Allowed values: "" / "off" / "low" / "medium" / "high".
// Overrides config.Model.Reasoning for the run.  Validated at
// bootstrap; invalid values warn and are reset to "" (no reasoning).
var reasoningFlag string

// continueFlag is set by --continue / -c on bare `hygge`.  When true,
// hygge attempts to resume the most recent session for the current cwd
// instead of starting a fresh one.
var continueFlag bool

// newFlag is set by --new on bare `hygge`.  When true, hygge always
// starts a fresh session even when resume_default = "continue".
var newFlag bool

// yoloFlag is set by --yolo on interactive TUI entry points. When true,
// configurable permission checks are bypassed while secrets remain denied.
var yoloFlag bool

// noConfigAuthFlag starts the TUI as if no user/project config or provider
// credentials exist. It is intended for onboarding/manual testing.
var noConfigAuthFlag bool

// init binds --resume, --reasoning, --continue, and --new.
// Called from NewRootCmd via wireRunFlags below.
func wireRunFlags(root *cobra.Command) {
	noConfigAuthFlag = false
	root.Flags().StringVar(&resumeFlag, "resume", "", "resume the most recent session whose id starts with this prefix")
	root.Flags().StringVar(&reasoningFlag, "reasoning", "", "reasoning depth for the run: off | low | medium | high (overrides [model] reasoning)")
	root.Flags().BoolVarP(&continueFlag, "continue", "c", false, "resume the most recent session for the current directory")
	root.Flags().BoolVar(&newFlag, "new", false, "start a fresh session (overrides resume_default = continue)")
	root.Flags().BoolVar(&yoloFlag, "yolo", false, "allow non-secret tool actions without prompting")
	root.Flags().BoolVar(&noConfigAuthFlag, "no-config-auth", false, "start without loading user/project config or provider auth (for onboarding testing)")
}

// runRun is the body of `hygge` (no subcommand).  Bootstraps the
// runtime and launches the TUI.
func runRun(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	if continueFlag && newFlag {
		return die(cmd, "conflicting flags: --continue and --new")
	}

	// Show an indeterminate progress bar in the terminal dock/taskbar while
	// bootstrap runs (workspace init + lipgloss probe).  This gives immediate
	// visual feedback that hygge is working even before the TUI starts.
	// The bar is reset before tea.NewProgram takes over the screen; if bootstrap
	// fails the defer fires before we return the error.
	if supportsProgressBar() {
		_, _ = fmt.Fprint(os.Stderr, ansi.SetIndeterminateProgressBar)
		defer fmt.Fprint(os.Stderr, ansi.ResetProgressBar) //nolint:errcheck
	}

	rt, err := bootstrap(ctx, bootstrapOptions{
		ConfigFile:        rootFlags.ConfigFile,
		ProfileName:       rootFlags.Profile,
		Pwd:               rootFlags.Pwd,
		ReasoningOverride: reasoningFlag,
		Yolo:              yoloFlag,
		AsyncMCP:          true,
		NoConfigAuth:      noConfigAuthFlag,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()

	// Legacy --resume flag (prefix-match).
	if resumeFlag != "" {
		sid, err := findSessionByPrefix(ctx, rt, resumeFlag, false)
		if err != nil {
			return die(cmd, "%s", err)
		}
		return runTUI(ctx, cmd, rt, sid, false)
	}

	var sid string
	openPicker := false

	switch {
	case continueFlag:
		// --continue: resume the cwd's most recent session or start fresh.
		sid, err = findResumableSession(ctx, rt, rt.Pwd, false)
		if err != nil {
			return fmt.Errorf("cli: find resumable session: %w", err)
		}
		if sid == "" {
			slog.Info("hygge: no session to continue; starting fresh", "pwd", rt.Pwd)
		}

	case newFlag:
		// --new: explicit fresh session; sid stays "".

	case rt.Config.Session.ResumeDefault == "continue":
		sid, err = findResumableSession(ctx, rt, rt.Pwd, false)
		if err != nil {
			return fmt.Errorf("cli: find resumable session: %w", err)
		}
		// Falls through to fresh start if sid == "".

	case rt.Config.Session.ResumeDefault == "ask":
		openPicker = true

	default:
		// "new" (the built-in default) — start fresh; sid stays "".
	}

	return runTUI(ctx, cmd, rt, sid, openPicker)
}

// runTUI builds the App and runs it inside a tea.Program.  Shared
// between `hygge` (no subcommand) and `hygge resume`.  When testing
// (i.e. testOverrides.SkipTea is true) this returns immediately after
// constructing the App so the bootstrap path is exercised without
// touching a TTY.
//
// openSessionsModalOnStart, when true, opens the sessions picker
// immediately after the first render (used by resume_default="ask" and
// `hygge resume` with multiple cwd sessions).
func configuredProvidersForRuntime(rt *appRuntime) []string {
	if rt != nil && rt.NoConfigAuth {
		return nil
	}
	if rt == nil {
		return nil
	}
	return authConfiguredProviders(rt.StateOpts)
}

func runTUI(ctx context.Context, _ *cobra.Command, rt *appRuntime, sessionID string, openSessionsModalOnStart bool) error {
	// Determine whether the wizard must run before regular chat is available.
	// The TUI always opens; onboarding replaces the main view when needed.
	//
	// Onboarding is required when model setup is missing or no provider auth
	// exists. If auth already exists but no config/model has been written yet,
	// the wizard still opens, but AuthConfiguredProviders lets it skip API-key
	// authorization and proceed to model/mode setup.
	needsOnboarding := rt.NoConfigAuth || !hasConfiguredModel(rt.Config, rt.Provenance) || !hasAnyProviderAuth(rt.StateOpts)

	// Map CLI MCPServerStatus → UI SidebarMCPStatus so internal/ui has no
	// dependency on cmd/.
	mcpStatuses := make([]ui.SidebarMCPStatus, 0, len(rt.MCPStatuses))
	for _, s := range rt.MCPStatuses {
		mcpStatuses = append(mcpStatuses, ui.SidebarMCPStatus{
			Name:      s.Name,
			Ready:     s.Ready,
			Error:     s.Error,
			ToolCount: s.ToolCount,
		})
	}
	subagentMentions := make([]ui.MentionSubagent, 0)
	if rt.Subagents != nil {
		for _, t := range rt.Subagents.List() {
			subagentMentions = append(subagentMentions, ui.MentionSubagent{
				Name:        t.Name,
				Description: t.Description,
			})
		}
	}

	app, err := ui.New(ui.AppOptions{
		Bus:                     rt.Bus,
		Agent:                   rt.Agent,
		Store:                   rt.Store,
		Catalog:                 rt.Catalog,
		Theme:                   rt.Theme,
		StyleTheme:              rt.Config.Theme.Name,
		Modes:                   rt.Config.Modes,
		AuthConfiguredProviders: configuredProvidersForRuntime(rt),
		SessionID:               sessionID,
		ProjectDir:              rt.Pwd,
		ModelProvider:           rt.Config.Model.Provider,
		ModelName:               rt.Config.Model.Name,
		ProfileName:             rt.Config.Profile,
		Reasoning:               resolveReasoning(rt.Config, reasoningFlag),
		Yolo:                    rt.Permission != nil && rt.Permission.Yolo(),
		Commands:                rt.Commands,
		Subagents:               subagentMentions,
		Version:                 Version,
		HomeDir:                 homeDir(),
		NerdFonts:               rt.Config.UI.NerdFonts,
		MCPStatuses:             mcpStatuses,
		NeedsOnboarding:         needsOnboarding,
		KnownProviders:          knownProviders(),
		SaveOnboardingResult: func(ictx context.Context, result ui.OnboardingResult) error {
			// 1. Persist every provider API key collected during onboarding.
			providerKeys := result.ProviderAPIKeys
			if len(providerKeys) == 0 && result.ProviderAPIKey != "" {
				providerKeys = map[string]string{result.ProviderName: result.ProviderAPIKey}
			}
			for providerName, apiKey := range providerKeys {
				if apiKey == "" {
					continue
				}
				if err := auth.Set(providerName, auth.Credential{Type: auth.CredAPIKey, APIKey: apiKey, AddedAt: time.Now()}, auth.LoadOptions{HomeDir: rt.StateOpts.HomeDir, XDGStateHome: rt.StateOpts.XDGStateHome}); err != nil {
					return fmt.Errorf("onboarding: save api key for %s: %w", providerName, err)
				}
				if providerName == result.Mode.Provider {
					if rt.Config.Model.Options == nil {
						rt.Config.Model.Options = map[string]any{}
					}
					rt.Config.Model.Options["api_key"] = apiKey
				}
			}
			// 2. Persist mode (model selection + inline prompt) to user config.
			if _, err := config.WriteOnboardingMode(config.WriteOnboardingModeOptions{
				HomeDir:       rt.StateOpts.HomeDir,
				XDGConfigHome: rt.XDGConfigHome,
				Pwd:           rt.Pwd,
				Provenance:    rt.Provenance,
			}, result.Mode); err != nil {
				return fmt.Errorf("onboarding: save mode: %w", err)
			}
			rt.Config.Model.Provider = result.Mode.Provider
			rt.Config.Model.Name = result.Mode.Model
			// Synthesize a modes slice from the onboarding result so the
			// rest of the session has the new mode immediately available.
			rt.Config.Modes = []config.ModeConfig{result.Mode}

			// 3. Persist subagents to user subagents.toml if any were created.
			if len(result.Subagents) > 0 {
				agents := make([]config.OnboardingSubagent, 0, len(result.Subagents))
				for _, draft := range result.Subagents {
					agents = append(agents, config.OnboardingSubagent{
						Name:        draft.Name,
						Description: draft.Idea,
						Prompt:      draft.Prompt,
					})
				}
				if err := config.WriteSubagentsToml(config.WriteSubagentsTomlOptions{
					HomeDir:       rt.StateOpts.HomeDir,
					XDGConfigHome: rt.XDGConfigHome,
				}, agents); err != nil {
					return fmt.Errorf("onboarding: save subagents: %w", err)
				}
			}

			// 4. Refresh the runtime agent to use the new model+prompt.
			modelOpts, err := resolveProviderOptionsFor(result.Mode.Provider, rt.Config, rt.StateOpts)
			if err != nil {
				return fmt.Errorf("onboarding: resolve provider opts: %w", err)
			}
			prv, err := buildProviderForName(result.Mode.Provider, rt.ProviderFactory, modelOpts)
			if err != nil {
				return fmt.Errorf("onboarding: build provider: %w", err)
			}
			resolved, err := llm.ResolveProviderModel(ictx, result.Mode.Provider, result.Mode.Model, modelOpts, rt.catalogSrc)
			if err != nil {
				return fmt.Errorf("onboarding: resolve model: %w", err)
			}
			if err := rt.Agent.SetModel(result.Mode.Provider, result.Mode.Model, prv, resolved.Model); err != nil {
				return fmt.Errorf("onboarding: set model: %w", err)
			}
			if result.Mode.Prompt != "" {
				if err := rt.Agent.SetSystemPrompt(composeModeSystemPrompt(rt.BaseSystemPrompt, result.Mode.Prompt)); err != nil {
					return fmt.Errorf("onboarding: set system prompt: %w", err)
				}
			}
			rt.Provider = prv
			return nil
		},
		GeneratePrompt: func(ictx context.Context, providerName, modelName, apiKey, idea string) (string, error) {
			// Build a one-shot provider with the given credentials to generate a prompt.
			opts := map[string]any{}
			if apiKey != "" {
				opts["api_key"] = apiKey
			}
			prv, err := buildProviderForName(providerName, rt.ProviderFactory, opts)
			if err != nil {
				return "", fmt.Errorf("prompt gen: build provider: %w", err)
			}
			return ui.GenerateSystemPrompt(ictx, prv, modelName, idea)
		},
		OnSessionCreated: func(id string) {
			sessionID = id
			if err := state.AddRecentSession(id, rt.StateOpts); err != nil {
				// State write failure is non-fatal for the running
				// session — log and continue.
				printf(stderr, "hygge: warning: could not record recent session: %v\n", err)
			}
		},
		OpenSessionsModalOnStart: openSessionsModalOnStart,
		SwitchModel: func(ctx context.Context, providerName, modelName, modeName string) error {
			modelOpts, err := resolveProviderOptionsFor(providerName, rt.Config, rt.StateOpts)
			if err != nil {
				return err
			}
			prv, err := buildProviderForName(providerName, rt.ProviderFactory, modelOpts)
			if err != nil {
				return err
			}
			resolved, err := llm.ResolveProviderModel(ctx, providerName, modelName, modelOpts, rt.catalogSrc)
			if err != nil {
				return err
			}
			if err := rt.Agent.SetModel(providerName, modelName, prv, resolved.Model); err != nil {
				return err
			}
			if modeName != "" {
				for i, mode := range rt.Config.Modes {
					if mode.Name == modeName {
						if err := rt.Agent.SetSystemPrompt(composeModeSystemPrompt(rt.BaseSystemPrompt, activeModePrompt(rt.Config, rt.XDGConfigHome, i))); err != nil {
							return err
						}
						break
					}
				}
				if err := rt.Agent.RefreshHookSystemPromptAdditions(ctx, sessionID, modeName); err != nil {
					return err
				}
			}
			rt.Provider = prv
			return nil
		},
		SaveModel: func(_ context.Context, providerName, modelName string) error {
			_, err := config.WriteModelSelection(config.WriteModelOptions{
				HomeDir:       rt.StateOpts.HomeDir,
				XDGConfigHome: rt.XDGConfigHome,
				Pwd:           rt.Pwd,
				Provenance:    rt.Provenance,
			}, providerName, modelName)
			if err == nil {
				rt.Config.Model.Provider = providerName
				rt.Config.Model.Name = modelName
			}
			return err
		},
		SaveAPIKey: func(_ context.Context, providerName, apiKey string) error {
			_, err := config.WriteProviderAPIKey(config.WriteProviderAPIKeyOptions{
				HomeDir:       rt.StateOpts.HomeDir,
				XDGConfigHome: rt.XDGConfigHome,
				Pwd:           rt.Pwd,
				Provenance:    rt.Provenance,
			}, providerName, apiKey)
			if err == nil && providerName == rt.Config.Model.Provider {
				if rt.Config.Model.Options == nil {
					rt.Config.Model.Options = map[string]any{}
				}
				rt.Config.Model.Options["api_key"] = apiKey
			}
			return err
		},
		RememberMemory:                rt.MemoryStore.Remember,
		ForgetMemory:                  rt.MemoryStore.Forget,
		ListMemories:                  rt.MemoryStore.ListMemories,
		ProjectMemoryGitignoreWarning: rt.MemoryStore.ProjectMemoryGitignoreWarning,
		ThemeNames:                    theme.KnownNames(theme.LoadOptions{ConfigHome: rt.XDGConfigHome, HomeDir: rt.StateOpts.HomeDir}),
		LoadTheme: func(_ context.Context, name string) (*theme.Theme, error) {
			return theme.Load(name, theme.LoadOptions{ConfigHome: rt.XDGConfigHome, HomeDir: rt.StateOpts.HomeDir})
		},
		SaveTheme: func(_ context.Context, name string) error {
			_, err := config.WriteThemeSelection(config.WriteThemeSelectionOptions{
				HomeDir:       rt.StateOpts.HomeDir,
				XDGConfigHome: rt.XDGConfigHome,
				Pwd:           rt.Pwd,
				Provenance:    rt.Provenance,
			}, name)
			if err == nil {
				rt.Config.Theme.Name = name
				rt.Theme, _ = theme.Load(name, theme.LoadOptions{ConfigHome: rt.XDGConfigHome, HomeDir: rt.StateOpts.HomeDir})
			}
			return err
		},
		SetYolo: func(_ context.Context, enabled bool) error {
			rt.Permission.SetYolo(enabled)
			return nil
		},
	})
	if err != nil {
		return fmt.Errorf("cli: build ui app: %w", err)
	}
	defer func() { _ = app.Close() }()

	// Tests skip the bubbletea Program entirely — the bootstrap is the
	// thing under test, and a real tea.Program would try to grab the
	// TTY.  testOverrides.SkipTea is consulted here so the same code
	// path covers both modes.
	if testOverrides != nil && testOverrides.SkipTea {
		return nil
	}

	slog.Info("hygge: tui session started",
		"pwd", rt.Pwd,
		"provider", rt.Config.Model.Provider,
		"model", rt.Config.Model.Name,
		"profile", rt.Config.Profile)

	prog := tea.NewProgram(app,
		// Pass the exact environment so bubbletea uses our env for terminal
		// capability detection rather than re-reading os.Environ() internally.
		// Fixes subtle terminal-detection quirks when the environment is
		// manipulated before this point.
		tea.WithEnvironment(os.Environ()),
		tea.WithContext(ctx),
		// Force a concrete color profile so lipgloss/v2 never sends an OSC 11
		// background-color query to the terminal. Without this, the auto-detect
		// probe can leak terminal responses into stdin. Default macOS Terminal
		// advertises 256 colors and renders some true-color sequences poorly, so
		// use ANSI256 there unless COLORTERM explicitly opts into true color.
		tea.WithColorProfile(tuiColorProfile(os.Environ())),
		// Drop any OSC terminal-response events that slip through bubbletea
		// v2.0.6's input parser as raw KeyPressMsg text.  This is a
		// secondary defence on top of WithColorProfile — some terminals still
		// emit responses to probes fired before the profile override takes
		// effect.  Remove when upstream fixes OSC response parsing.
		// See docs/agents/ui-v2-gotchas.md.
		tea.WithFilter(newInputEventFilter()),
	)
	app.SetProgram(prog)
	rt.StartAsyncMCP(ctx)

	// Translate SIGINT/SIGTERM into a clean Quit so deferred Close runs.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	doneCh := make(chan struct{})
	go func() {
		select {
		case <-sigCh:
			prog.Quit()
		case <-doneCh:
		}
	}()
	defer func() {
		signal.Stop(sigCh)
		close(doneCh)
	}()

	if _, err := prog.Run(); err != nil {
		return fmt.Errorf("cli: tea run: %w", err)
	}
	return nil
}

// setupTUILog redirects slog output to $XDG_STATE_HOME/hygge/hygge.log so
// logs emitted during bootstrap and under bubbletea's alt-screen are
// recoverable without leaking to the terminal. Returns a close function (or
// nil if redirection failed) that restores the previous slog default handler
// and closes the file.
//
// Log level: reads HYGGE_LOG_LEVEL (case-insensitive).  Accepted values:
// debug, info, warn, error.  When unset the level defaults to debug,
// matching the historical behaviour so existing users see no change.
func setupTUILog(stateOpts state.LoadOptions) func() {
	dir := stateOpts.XDGStateHome
	if dir == "" {
		// state.LoadOptions falls back to XDG_STATE_HOME or ~/.local/state
		// at load time; if we don't have it here, just skip — the user
		// can still see fatal errors when the program returns.
		return nil
	}
	logDir := filepath.Join(dir, "hygge")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil
	}
	logPath := filepath.Join(logDir, "hygge.log")
	f, err := os.OpenFile(logPath, //nolint:gosec // logPath is a derived path under our state dir
		os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}

	// Resolve log level from HYGGE_LOG_LEVEL; default to debug for
	// backward compatibility (the level was hard-coded to debug before
	// this env var was introduced).
	level := slog.LevelDebug
	if raw := strings.TrimSpace(os.Getenv("HYGGE_LOG_LEVEL")); raw != "" {
		switch strings.ToLower(raw) {
		case "debug":
			level = slog.LevelDebug
		case "info":
			level = slog.LevelInfo
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
		// Unknown values are silently ignored; level stays at debug.
	}

	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(f, &slog.HandlerOptions{Level: level})))
	return func() {
		slog.SetDefault(prev)
		_ = f.Close()
	}
}

// homeDir returns the user's home directory or "" if it cannot be
// determined.  Used by the UI for tilde-collapsing the project path
// in the header bar.
func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return h
}
