package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/ui"
)

// resumeFlag is the value bound to the root command's --resume flag.
// We use a package var (rather than a closure) so NewRootCmd can wire it
// into the persistent flag set above any subcommand.
var resumeFlag string

// init binds --resume.  Called from NewRootCmd via wireRunFlags below.
func wireRunFlags(root *cobra.Command) {
	root.Flags().StringVar(&resumeFlag, "resume", "", "resume the most recent session whose id starts with this prefix")
}

// runRun is the body of `hygge` (no subcommand).  Bootstraps the
// runtime and launches the TUI.
func runRun(cmd *cobra.Command, _ []string) error {
	ctx := context.Background()

	rt, err := bootstrap(ctx, bootstrapOptions{
		ConfigFile:  rootFlags.ConfigFile,
		ProfileName: rootFlags.Profile,
		Pwd:         rootFlags.Pwd,
	})
	if err != nil {
		return err
	}
	defer func() { _ = rt.Close() }()

	var sid string
	if resumeFlag != "" {
		sid, err = findSessionByPrefix(ctx, rt, resumeFlag, false)
		if err != nil {
			return die(cmd, "%s", err)
		}
	}

	return runTUI(ctx, cmd, rt, sid)
}

// runTUI builds the App and runs it inside a tea.Program.  Shared
// between `hygge` (no subcommand) and `hygge resume`.  When testing
// (i.e. testOverrides.SkipTea is true) this returns immediately after
// constructing the App so the bootstrap path is exercised without
// touching a TTY.
func runTUI(ctx context.Context, _ *cobra.Command, rt *appRuntime, sessionID string) error {
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
		OnSessionCreated: func(id string) {
			if err := state.AddRecentSession(id, rt.StateOpts); err != nil {
				// State write failure is non-fatal for the running
				// session — log and continue.
				printf(stderr, "hygge: warning: could not record recent session: %v\n", err)
			}
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

	prog := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithContext(ctx))

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
