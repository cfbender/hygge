package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Version is the human-facing version string surfaced by CLI and TUI chrome.
const Version = "0.15.1"

// globalFlags holds values bound to root-level persistent flags.  A
// single struct keeps subcommand bodies free of cobra-Flag plumbing.
type globalFlags struct {
	Profile    string
	ConfigFile string
	Pwd        string
}

// rootFlags is the package-scoped instance bound by NewRootCmd.  Tests
// build their own *cobra.Command via NewRootCmd so they can assert on
// captured stdout/stderr and reset state.
var rootFlags globalFlags

// testOverrides is consulted by bootstrap when it's non-nil, so tests
// can redirect HomeDir/XDG paths and inject a provider factory without
// exposing those knobs as CLI flags.  Production builds leave it nil.
var testOverrides *bootstrapOptions

// SetTestOverrides installs override values consulted by every
// bootstrap call.  Pass nil to clear.  Test-only.
func SetTestOverrides(o *bootstrapOptions) { testOverrides = o }

// applyTestOverrides merges fields from testOverrides into o where o's
// values are zero.  This is a one-way override: tests can supply
// HomeDir/XDG values etc. without the CLI flags ever being touched.
func applyTestOverrides(o bootstrapOptions) bootstrapOptions {
	if testOverrides == nil {
		return o
	}
	if o.HomeDir == "" {
		o.HomeDir = testOverrides.HomeDir
	}
	if o.XDGConfigHome == "" {
		o.XDGConfigHome = testOverrides.XDGConfigHome
	}
	if o.XDGStateHome == "" {
		o.XDGStateHome = testOverrides.XDGStateHome
	}
	if o.Pwd == "" {
		o.Pwd = testOverrides.Pwd
	}
	if o.ProviderFactory == nil {
		o.ProviderFactory = testOverrides.ProviderFactory
	}
	if o.FantasyModel == nil {
		o.FantasyModel = testOverrides.FantasyModel
	}
	if o.Now == nil {
		o.Now = testOverrides.Now
	}
	if o.SystemPrompt == "" {
		o.SystemPrompt = testOverrides.SystemPrompt
	}
	if o.CatalogBaseURL == "" {
		o.CatalogBaseURL = testOverrides.CatalogBaseURL
	}
	if o.ReasoningOverride == "" {
		o.ReasoningOverride = testOverrides.ReasoningOverride
	}
	if !o.Yolo {
		o.Yolo = testOverrides.Yolo
	}
	return o
}

// Execute is the standard cobra entry point used by main.go.  Returns the
// process exit code.
func Execute() int {
	cmd := NewRootCmd()
	if err := fang.Execute(
		context.Background(),
		cmd,
		fang.WithColorSchemeFunc(cliFangColorScheme),
		fang.WithVersion(Version),
		fang.WithNotifySignal(os.Interrupt),
	); err != nil {
		return 1
	}
	return 0
}

// NewRootCmd builds the cobra command tree.  Tests build their own root
// per call so flag state and stdout are isolated.
func NewRootCmd() *cobra.Command {
	// Fresh flag struct per build so tests don't leak state between
	// successive Execute() calls.
	rootFlags = globalFlags{}

	root := &cobra.Command{
		Use:   "hygge",
		Short: "hygge — a terminal-based AI coding assistant",
		Long: `hygge is a terminal-based AI coding assistant.

Run hygge with no arguments to launch the TUI.  Use the subcommands to
manage sessions, profiles, configuration, and themes.`,
		SilenceUsage: true,
	}
	installCLIHelp(root)

	root.PersistentFlags().StringVar(&rootFlags.Profile, "profile", "", "name of the profile to use (overrides state.json)")
	root.PersistentFlags().StringVar(&rootFlags.ConfigFile, "config", "", "explicit user config file (advisory; v0.1 falls back to default discovery)")
	root.PersistentFlags().StringVar(&rootFlags.Pwd, "pwd", "", "override the working directory used for config walk-up and session pwd")

	// `--version` on the root command is a convenience that mirrors
	// `hygge version`.  We intercept it here and print the same string
	// the version subcommand would, then short-circuit Execute.
	var showVersion bool
	root.Flags().BoolVar(&showVersion, "version", false, "print version information and exit")
	wireRunFlags(root)
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if showVersion {
			writeln(cmd.OutOrStdout(), versionString())
			return nil
		}
		return runRun(cmd, args)
	}

	root.AddCommand(
		newVersionCmd(),
		newResumeCmd(),
		newSessionsCmd(),
		newProfileCmd(),
		newConfigCmd(),
		newThemeCmd(),
		newProviderCmd(),
		newModelsCmd(),
		newOnboardCmd(),
		newInitCmd(),
		newSkillsCmd(),
		newSubagentsCmd(),
		newCommandsCmd(),
		newContextCmd(),
		newMCPCmd(),
		newCatalogCmd(),
		newHooksCmd(),
		newPluginsCmd(),
		newLogsCmd(),
	)

	return root
}

func installCLIHelp(root *cobra.Command) {
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		printCLIHelp(cmd)
	})
}

func printCLIHelp(cmd *cobra.Command) {
	styles := newCLIStylesFor(out(cmd))
	w := out(cmd)
	if cmd.Long != "" {
		writeln(w, styles.Value.Render(cmd.Long))
	} else if cmd.Short != "" {
		writeln(w, styles.Value.Render(cmd.Short))
	}
	writeln(w)

	writeln(w, styles.Header.Render("Usage:"))
	writeln(w, "  "+styles.Value.Render(cmd.UseLine()))

	if aliases := cmd.Aliases; len(aliases) > 0 {
		writeln(w)
		writeln(w, styles.Header.Render("Aliases:"))
		writeln(w, "  "+styles.Value.Render(strings.Join(aliases, ", ")))
	}

	commands := visibleSubcommands(cmd)
	if len(commands) > 0 {
		writeln(w)
		writeln(w, styles.Header.Render("Available Commands:"))
		for _, child := range commands {
			printf(w, "  %s  %s\n", styles.Accent.Render(cliPadRight(child.Name(), commandNameWidth(commands))), styles.Muted.Render(child.Short))
		}
	}

	flags := cmd.NonInheritedFlags()
	if flags.HasAvailableFlags() {
		writeln(w)
		writeln(w, styles.Header.Render("Flags:"))
		printCLIFlagHelp(w, styles, flags)
	}

	inherited := cmd.InheritedFlags()
	if inherited.HasAvailableFlags() {
		writeln(w)
		writeln(w, styles.Header.Render("Global Flags:"))
		printCLIFlagHelp(w, styles, inherited)
	}

	if cmd.HasAvailableSubCommands() {
		writeln(w)
		printf(w, "Use %s for more information about a command.\n", styles.Value.Render(cmd.CommandPath()+" [command] --help"))
	}
}

func visibleSubcommands(cmd *cobra.Command) []*cobra.Command {
	commands := make([]*cobra.Command, 0, len(cmd.Commands()))
	for _, child := range cmd.Commands() {
		if !child.IsAvailableCommand() && child.Name() != "help" {
			continue
		}
		commands = append(commands, child)
	}
	return commands
}

func commandNameWidth(commands []*cobra.Command) int {
	width := 0
	for _, cmd := range commands {
		if n := len(cmd.Name()); n > width {
			width = n
		}
	}
	return width
}

func printCLIFlagHelp(w io.Writer, styles cliStyles, flags *pflag.FlagSet) {
	rows := make([][2]string, 0)
	width := 0
	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden {
			return
		}
		name := "--" + flag.Name
		if flag.Shorthand != "" {
			name = "-" + flag.Shorthand + ", " + name
		}
		if flag.Value.Type() != "bool" {
			name += " " + flag.Value.Type()
		}
		if len(name) > width {
			width = len(name)
		}
		usage := flag.Usage
		if flag.DefValue != "" && flag.DefValue != "false" {
			usage += fmt.Sprintf(" (default %q)", flag.DefValue)
		}
		rows = append(rows, [2]string{name, usage})
	})
	for _, row := range rows {
		printf(w, "  %s  %s\n", styles.Accent.Render(cliPadRight(row[0], width)), styles.Muted.Render(row[1]))
	}
}

// out returns the writer for the cobra command's standard output.  Tests
// inject a *bytes.Buffer via cmd.SetOut and assert on the captured
// content.
func out(cmd *cobra.Command) io.Writer {
	return cmd.OutOrStdout()
}

// errOut returns the writer for the cobra command's standard error.
func errOut(cmd *cobra.Command) io.Writer {
	return cmd.ErrOrStderr()
}

// printf is a fmt.Fprintf wrapper that drops the return value.  Used by
// CLI bodies that write to cmd-attached writers — the writer is either
// stdout (errors there are not actionable) or a *bytes.Buffer in tests
// (cannot fail).  Centralising the discard keeps subcommand bodies
// readable and keeps errcheck quiet.
func printf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}

// writeln is the fmt.Fprintln equivalent of printf above.
func writeln(w io.Writer, args ...any) {
	_, _ = fmt.Fprintln(w, args...)
}

// printRaw is the fmt.Fprint equivalent of printf above.
func printRaw(w io.Writer, args ...any) {
	_, _ = fmt.Fprint(w, args...)
}

// die writes msg to cmd's stderr and returns a non-nil error so cobra
// records a failure.  Used for "user-facing" errors (e.g. no session
// matched a prefix) where we want a clean message rather than a
// backtrace-style cobra dump.
func die(cmd *cobra.Command, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	writeln(errOut(cmd), msg)
	return fmt.Errorf("%s", msg)
}

// stderr is a small typed wrapper for places where we need to write to
// the process's real stderr rather than the cobra-attached writer
// (e.g. signal handlers).
var stderr io.Writer = os.Stderr
