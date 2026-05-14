// Package cli — `hygge provider` subcommand group.
//
// Three subcommands manage the per-machine credential store:
//
//   - `hygge provider auth [name]` — interactively (or via stdin) save
//     an API key for a named provider.
//   - `hygge provider list` — print the stored credentials with masked
//     keys.
//   - `hygge provider remove <name>` — delete a stored credential.
//
// Credentials are persisted by [internal/auth] at
// $XDG_STATE_HOME/hygge/auth.json (mode 0600).  See that package for
// the storage contract.
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/cfbender/hygge/internal/auth"
)

// newProviderCmd builds the `hygge provider` subcommand group.
func newProviderCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "provider",
		Short: "Manage per-machine provider credentials",
		Long: `Manage per-machine provider credentials.

Credentials live at $XDG_STATE_HOME/hygge/auth.json (mode 0600).  They
are intentionally separate from the human-edited TOML config so the
config can be checked into a dotfiles repository safely.`,
	}
	root.AddCommand(
		newProviderAuthCmd(),
		newProviderListCmd(),
		newProviderRemoveCmd(),
	)
	return root
}

// newProviderAuthCmd builds `hygge provider auth [name]`.
//
// Interactive flow:
//   - if name is omitted, print the known-provider list and prompt for
//     a number-or-name selection.
//   - prompt for auth method (1=API key, 2=OAuth-not-yet-supported).
//   - for API key: read once without echo via term.ReadPassword.
//
// Non-interactive flow (stdin is not a TTY):
//   - read a single line from stdin as the key.  Used by tests and
//     by `echo $KEY | hygge provider auth anthropic`.
func newProviderAuthCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "auth [name]",
		Short: "Save an API key for a provider",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			authOpts := auth.LoadOptions{
				HomeDir:      rt.StateOpts.HomeDir,
				XDGStateHome: rt.StateOpts.XDGStateHome,
			}

			isTTY := term.IsTerminal(int(os.Stdin.Fd()))
			in := cmd.InOrStdin()
			reader := bufio.NewReader(in)

			// 1. Resolve the provider name.
			var name string
			if len(args) == 1 {
				name = strings.TrimSpace(args[0])
			}
			if name == "" {
				name, err = pickProvider(cmd, reader, isTTY)
				if err != nil {
					return err
				}
			}
			if name == "" {
				return die(cmd, "provider name is required")
			}

			// 2. Pick auth method.  Default is API key.  Non-TTY
			// stdin always implies API key (the only flow that
			// doesn't need interactive input).
			method := "1"
			if isTTY {
				printf(out(cmd), "Auth method for %s:\n  1) API key (paste)\n  2) OAuth (not yet supported)\nPick (1 or 2): ", name)
				line, _ := reader.ReadString('\n')
				if v := strings.TrimSpace(line); v != "" {
					method = v
				}
			}

			switch method {
			case "1", "api_key", "api-key", "key":
				return runProviderAuthAPIKey(cmd, reader, isTTY, name, authOpts)
			case "2", "oauth":
				printf(out(cmd),
					"OAuth flow for %s is not yet implemented. Use the API-key flow (option 1) for now.\n",
					name)
				return nil
			default:
				return die(cmd, "unknown auth method %q", method)
			}
		},
	}
}

// runProviderAuthAPIKey handles the "paste an API key" branch of
// `hygge provider auth`.
func runProviderAuthAPIKey(cmd *cobra.Command, reader *bufio.Reader, isTTY bool, name string, authOpts auth.LoadOptions) error {
	var key string
	if isTTY {
		printf(out(cmd), "Paste your %s API key (hidden input, press Enter when done): ", name)
		// term.ReadPassword reads directly from the FD — there's no
		// way to redirect it to a *bufio.Reader, so non-TTY callers
		// must use the stdin-pipe branch below.
		raw, err := term.ReadPassword(int(os.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("read password: %w", err)
		}
		// term.ReadPassword does not echo a newline; print one so
		// subsequent output starts on its own line.
		writeln(out(cmd))
		key = strings.TrimSpace(string(raw))
	} else {
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return fmt.Errorf("read stdin: %w", err)
		}
		key = strings.TrimSpace(line)
	}

	if key == "" {
		return die(cmd, "empty API key — aborted")
	}

	cred := auth.Credential{
		Type:    auth.CredAPIKey,
		APIKey:  key,
		AddedAt: time.Now(),
	}
	if err := auth.Set(name, cred, authOpts); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}
	printf(out(cmd), "Saved credential for %s. (%s)\n", name, maskKey(key))
	return nil
}

// pickProvider prints the known-provider list and reads a selection
// (number or name).  Returns the resolved provider name.
func pickProvider(cmd *cobra.Command, reader *bufio.Reader, isTTY bool) (string, error) {
	names := knownProviders()
	if isTTY {
		writeln(out(cmd), "Known providers:")
		for i, n := range names {
			printf(out(cmd), "  %d) %s\n", i+1, n)
		}
		printRaw(out(cmd), "Pick a provider (number or name): ")
	}
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read provider selection: %w", err)
	}
	choice := strings.TrimSpace(line)
	if choice == "" {
		return "", nil
	}
	// Numeric selection.
	if n, convErr := strconv.Atoi(choice); convErr == nil {
		if n < 1 || n > len(names) {
			return "", fmt.Errorf("selection %d out of range [1, %d]", n, len(names))
		}
		return names[n-1], nil
	}
	// Free-form name.  Allow unknown names — the user may target a
	// provider hygge does not yet ship an adapter for.
	return choice, nil
}

// newProviderListCmd builds `hygge provider list`.
func newProviderListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List stored credentials with masked keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			authOpts := auth.LoadOptions{
				HomeDir:      rt.StateOpts.HomeDir,
				XDGStateHome: rt.StateOpts.XDGStateHome,
			}
			store, err := auth.Load(authOpts)
			if err != nil {
				return fmt.Errorf("load auth store: %w", err)
			}
			names := store.List()
			if len(names) == 0 {
				writeln(out(cmd), "(no providers configured — run hygge provider auth)")
				return nil
			}

			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			printf(tw, "NAME\tTYPE\tKEY\tADDED\n")
			for _, n := range names {
				cred, _ := store.Get(n)
				display := "<oauth>"
				if cred.Type == auth.CredAPIKey {
					display = maskKey(cred.APIKey)
				}
				added := cred.AddedAt.Format("2006-01-02")
				printf(tw, "%s\t%s\t%s\t%s\n", n, cred.Type, display, added)
			}
			return tw.Flush()
		},
	}
}

// newProviderRemoveCmd builds `hygge provider remove <name>`.
func newProviderRemoveCmd() *cobra.Command {
	var noConfirm bool
	c := &cobra.Command{
		Use:   "remove <name>",
		Short: "Delete a stored credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     rootFlags.Profile,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			authOpts := auth.LoadOptions{
				HomeDir:      rt.StateOpts.HomeDir,
				XDGStateHome: rt.StateOpts.XDGStateHome,
			}
			store, err := auth.Load(authOpts)
			if err != nil {
				return fmt.Errorf("load auth store: %w", err)
			}
			if _, ok := store.Get(name); !ok {
				printf(out(cmd), "(no credential for %s; nothing to do)\n", name)
				return nil
			}

			if !noConfirm {
				printf(out(cmd), "Remove credential for %s? [y/N]: ", name)
				reader := bufio.NewReader(cmd.InOrStdin())
				line, _ := reader.ReadString('\n')
				ans := strings.ToLower(strings.TrimSpace(line))
				if ans != "y" && ans != "yes" {
					writeln(out(cmd), "Cancelled.")
					return nil
				}
			}

			if err := auth.Remove(name, authOpts); err != nil {
				return fmt.Errorf("remove credential: %w", err)
			}
			printf(out(cmd), "Removed credential for %s\n", name)
			return nil
		},
	}
	c.Flags().BoolVarP(&noConfirm, "no-confirm", "f", false, "skip the confirmation prompt")
	return c
}

// providerKnownContains reports whether name is in the known-providers
// list.  Kept as a small helper so tests can assert against the same
// list the picker uses.
func providerKnownContains(name string) bool {
	return slices.Contains(knownProviders(), name)
}
