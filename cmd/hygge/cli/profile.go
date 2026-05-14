package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/state"
)

// newProfileCmd builds the `hygge profile` subcommand group.
func newProfileCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "profile",
		Short: "Manage configuration profiles",
	}
	root.AddCommand(newProfileListCmd(), newProfileUseCmd(), newProfileShowCmd())
	return root
}

// newProfileListCmd builds `hygge profile list`.
func newProfileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available profiles, marking the active one",
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

			profilesDir := profilesDirFromOpts(rt)
			entries, err := os.ReadDir(profilesDir)
			if err != nil {
				if os.IsNotExist(err) {
					writeln(out(cmd), "(no profiles directory; create one at ~/.config/hygge/profiles/)")
					return nil
				}
				return fmt.Errorf("cli: read profiles dir: %w", err)
			}

			var names []string
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				name := e.Name()
				if !strings.HasSuffix(name, ".toml") {
					continue
				}
				names = append(names, strings.TrimSuffix(name, ".toml"))
			}
			sort.Strings(names)

			active := rt.State.ActiveProfile
			if active == "" {
				active = "default"
			}

			if len(names) == 0 {
				writeln(out(cmd), "(no profiles found)")
				return nil
			}

			for _, n := range names {
				marker := "  "
				if n == active {
					marker = "* "
				}
				writeln(out(cmd), marker+n)
			}
			return nil
		},
	}
}

// newProfileUseCmd builds `hygge profile use <name>`.
func newProfileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Set the active profile and persist it to state.json",
		Args:  cobra.ExactArgs(1),
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

			name := args[0]
			if err := state.SetActiveProfile(name, rt.StateOpts); err != nil {
				return fmt.Errorf("cli: set active profile: %w", err)
			}
			printf(out(cmd), "active profile set to %q\n", name)
			return nil
		},
	}
}

// newProfileShowCmd builds `hygge profile show [name]`.
func newProfileShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show [name]",
		Short: "Show the resolved Config for the named profile (or active)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profileName := ""
			if len(args) == 1 {
				profileName = args[0]
			}
			rt, err := bootstrap(context.Background(), bootstrapOptions{
				ConfigFile:      rootFlags.ConfigFile,
				ProfileName:     profileName,
				Pwd:             rootFlags.Pwd,
				ProviderFactory: stubProviderFactory,
			})
			if err != nil {
				return err
			}
			defer func() { _ = rt.Close() }()

			cfg := rt.Config
			printf(out(cmd), "profile: %s\n", cfg.Profile)
			printf(out(cmd), "model.provider = %q\n", cfg.Model.Provider)
			printf(out(cmd), "model.name     = %q\n", cfg.Model.Name)
			printf(out(cmd), "theme.name     = %q\n", cfg.Theme.Name)
			printf(out(cmd), "permission:\n")
			printf(out(cmd), "  file_read_outside_pwd = %q\n", cfg.Permission.FileReadOutsidePwd)
			printf(out(cmd), "  file_write            = %q\n", cfg.Permission.FileWrite)
			printf(out(cmd), "  shell                 = %q\n", cfg.Permission.Shell)
			printf(out(cmd), "  network               = %q\n", cfg.Permission.Network)
			return nil
		},
	}
}

// profilesDirFromOpts reconstructs the profiles directory for the
// current bootstrap.  We read it from the resolved overrides if present
// and otherwise fall back to the standard XDG location.  The runtime
// itself does not currently expose the resolved xdgConfig directly, so
// we derive it the same way bootstrap did.
func profilesDirFromOpts(rt *appRuntime) string {
	// Prefer the test override when set; mirrors the resolution order in
	// bootstrap above.
	xdgConfig := ""
	if testOverrides != nil {
		xdgConfig = testOverrides.XDGConfigHome
	}
	if xdgConfig == "" {
		if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && v != "" {
			xdgConfig = v
		} else {
			xdgConfig = filepath.Join(homeDirFromRuntime(rt), ".config")
		}
	}
	return filepath.Join(xdgConfig, "hygge", "profiles")
}

// homeDirFromRuntime returns the resolved HomeDir for the current
// bootstrap.  Mirrors bootstrap's order.
func homeDirFromRuntime(_ *appRuntime) string {
	if testOverrides != nil && testOverrides.HomeDir != "" {
		return testOverrides.HomeDir
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}
