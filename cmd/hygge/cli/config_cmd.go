package cli

import (
	"context"
	"sort"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/config"
)

// newConfigCmd builds the `hygge config` subcommand group.
func newConfigCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "config",
		Short: "Inspect the merged configuration",
	}
	root.AddCommand(newConfigExplainCmd())
	return root
}

// newConfigExplainCmd builds `hygge config explain [key]`.
//
// With no argument, prints every effective key with its winning source.
// With a key argument, prints the full provenance chain for that key
// using the config package's Explain helper.
func newConfigExplainCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "explain [key]",
		Short: "Show effective config values and their sources",
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

			if len(args) == 1 {
				explained, _, err := config.Explain(rt.Provenance, rt.Config, args[0])
				if err != nil {
					return err
				}
				printRaw(out(cmd), explained)
				return nil
			}

			// Print every effective key with the winning source.
			keys := allKnownKeys(rt.Config)
			sort.Strings(keys)
			for _, k := range keys {
				explained, _, err := config.Explain(rt.Provenance, rt.Config, k)
				if err != nil {
					// A key that resolves to its hard-coded default may
					// not appear in provenance; skip silently.
					continue
				}
				printRaw(out(cmd), explained)
			}
			return nil
		},
	}
}

// allKnownKeys enumerates the v0.1 leaf keys we know how to explain.
// model.options is treated as a single key — its inner contents are
// per-provider and don't have stable provenance entries.
func allKnownKeys(_ *config.Config) []string {
	return []string{
		"model.provider",
		"model.name",
		"permission.file_read_outside_pwd",
		"permission.file_write",
		"permission.shell",
		"permission.network",
		"theme.name",
	}
}
