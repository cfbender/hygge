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

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/ui"
)

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

// init binds --resume, --reasoning, --continue, and --new.
// Called from NewRootCmd via wireRunFlags below.
func wireRunFlags(root *cobra.Command) {
	root.Flags().StringVar(&resumeFlag, "resume", "", "resume the most recent session whose id starts with this prefix")
	root.Flags().StringVar(&reasoningFlag, "reasoning", "", "reasoning depth for the run: off | low | medium | high (overrides [model] reasoning)")
	root.Flags().BoolVarP(&continueFlag, "continue", "c", false, "resume the most recent session for the current directory")
	root.Flags().BoolVar(&newFlag, "new", false, "start a fresh session (overrides resume_default = continue)")
}

// runRun is the body of `hygge` (no subcommand).  Bootstraps the
// runtime and launches the TUI.
func runRun(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	if continueFlag && newFlag {
		return die(cmd, "conflicting flags: --continue and --new")
	}

	rt, err := bootstrap(ctx, bootstrapOptions{
		ConfigFile:        rootFlags.ConfigFile,
		ProfileName:       rootFlags.Profile,
		Pwd:               rootFlags.Pwd,
		ReasoningOverride: reasoningFlag,
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
func runTUI(ctx context.Context, _ *cobra.Command, rt *appRuntime, sessionID string, openSessionsModalOnStart bool) error {
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

	app, err := ui.New(ui.AppOptions{
		Bus:           rt.Bus,
		Agent:         rt.Agent,
		Store:         rt.Store,
		Catalog:       rt.Catalog,
		Theme:         rt.Theme,
		SessionID:     sessionID,
		ProjectDir:    rt.Pwd,
		ModelProvider: rt.Config.Model.Provider,
		ModelName:     rt.Config.Model.Name,
		ProfileName:   rt.Config.Profile,
		Reasoning:     resolveReasoning(rt.Config, reasoningFlag),
		Commands:      rt.Commands,
		Version:       Version,
		HomeDir:       homeDir(),
		NerdFonts:     rt.Config.UI.NerdFonts,
		MCPStatuses:   mcpStatuses,
		OnSessionCreated: func(id string) {
			if err := state.AddRecentSession(id, rt.StateOpts); err != nil {
				// State write failure is non-fatal for the running
				// session — log and continue.
				printf(stderr, "hygge: warning: could not record recent session: %v\n", err)
			}
		},
		OpenSessionsModalOnStart: openSessionsModalOnStart,
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

	// Redirect slog to a log file so warnings/errors emitted from the
	// agent loop, provider, tools, etc. are diagnosable even though the
	// TUI owns stderr in alt-screen mode.  Best-effort: a failure to open
	// the file is not fatal — we just keep the default handler.
	if logCloser := setupTUILog(rt); logCloser != nil {
		defer logCloser()
	}

	prog := tea.NewProgram(app,
		tea.WithContext(ctx),
		// Force TrueColor profile so lipgloss/v2 never sends an OSC 11
		// background-color query to the terminal.  Without this, the
		// auto-detect probe (\e]11;?\a) fires at startup and the response
		// (\e]11;rgb:…\a) leaks into stdin, appearing as garbage text in
		// the textarea.  TrueColor is the safest default for a modern
		// terminal; users on older terminals will see colours downsampled
		// by the renderer rather than by us.
		// See: https://github.com/charmbracelet/lipgloss/issues/XXX (upstream v2 tracking issue)
		tea.WithColorProfile(colorprofile.TrueColor),
		// Drop any OSC terminal-response events that slip through bubbletea
		// v2.0.6's input parser as raw KeyPressMsg text.  This is a
		// secondary defence on top of WithColorProfile — some terminals still
		// emit responses to probes fired before the profile override takes
		// effect.  Remove when upstream fixes OSC response parsing.
		// See docs/agents/ui-v2-gotchas.md.
		tea.WithFilter(dropOSCResponses),
	)
	app.SetProgram(prog)

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
// that warnings emitted under bubbletea's alt-screen are recoverable.
// Returns a close function (or nil if redirection failed) that restores
// the previous slog default handler and closes the file.
//
// Log level: reads HYGGE_LOG_LEVEL (case-insensitive).  Accepted values:
// debug, info, warn, error.  When unset the level defaults to debug,
// matching the historical behaviour so existing users see no change.
func setupTUILog(rt *appRuntime) func() {
	dir := rt.StateOpts.XDGStateHome
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
	slog.Info("hygge: tui session started",
		"pwd", rt.Pwd,
		"provider", rt.Config.Model.Provider,
		"model", rt.Config.Model.Name,
		"profile", rt.Config.Profile)
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
