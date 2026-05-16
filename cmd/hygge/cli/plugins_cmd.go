package cli

import (
	"context"
	"slices"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/plugin"
)

// newPluginsCmd builds the `hygge plugins` subcommand group.
func newPluginsCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "plugins",
		Short: "Manage hygge plugins (install, update, remove, list, show)",
	}
	root.AddCommand(
		newPluginsListCmd(),
		newPluginsShowCmd(),
		newPluginsInstallCmd(),
		newPluginsRemoveCmd(),
		newPluginsUpdateCmd(),
	)
	return root
}

// newPluginsListCmd builds `hygge plugins list`.
func newPluginsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed plugins",
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

			if rt.Plugins == nil {
				writeln(out(cmd), "(no plugins registry)")
				return nil
			}

			all := rt.Plugins.List()
			if len(all) == 0 {
				writeln(out(cmd), "(no plugins installed)")
				return nil
			}
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "NAME\tSOURCE\tVERSION\tSTATUS")
			for _, p := range all {
				m := p.Manifest()
				ver := m.Version
				if ver == "" {
					ver = "-"
				}
				printf(tw, "%s\t%s\t%s\t%s\n",
					p.Name(),
					truncateInline(p.Source(), 50),
					ver,
					"loaded",
				)
			}
			return tw.Flush()
		},
	}
}

// newPluginsShowCmd builds `hygge plugins show <name>`.
func newPluginsShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <name>",
		Short: "Print details for an installed plugin",
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

			if rt.Plugins == nil {
				return die(cmd, "no plugins registry")
			}

			p, ok := rt.Plugins.Get(args[0])
			if !ok {
				return die(cmd, "plugin %q not found", args[0])
			}
			m := p.Manifest()
			src, _ := rt.Plugins.Source(p.Name())

			printf(out(cmd), "Name:        %s\n", p.Name())
			printf(out(cmd), "Source:      %s\n", p.Source())
			printf(out(cmd), "Version:     %s\n", m.Version)
			printf(out(cmd), "Description: %s\n", m.Description)
			printf(out(cmd), "Entrypoint:  %s\n", m.Entrypoint)
			printf(out(cmd), "Synthesised: %v\n", m.Synthesised())
			printf(out(cmd), "CacheDir:    %s\n", rt.PluginPM.CacheDir(src))
			return nil
		},
	}
}

// newPluginsInstallCmd builds `hygge plugins install <source>`.
func newPluginsInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <source>",
		Short: "Install a plugin from a source URI",
		Long: `Install a plugin from a source URI and add it to config.toml.

Supported source formats:

  github:USER/REPO             default branch
  github:USER/REPO@v1.2.3      tag or commit sha
  github:USER/REPO#main        explicit branch
  local:/abs/path              local directory (development)
  local:~/path/to/plugin       local directory with ~ expansion

Example:

  hygge plugins install github:cfbender/hygge-policy-guard
  hygge plugins install local:~/code/my-plugin`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sourceURI := args[0]

			// Validate the source URI before touching config.
			if _, err := plugin.ParseSource(sourceURI); err != nil {
				return die(cmd, "invalid source URI: %s", err)
			}

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

			// Check if already installed.
			if slices.Contains(rt.Config.Plugins.Sources, sourceURI) {
				return die(cmd, "plugin %q is already installed", sourceURI)
			}

			ctx := context.Background()
			if rt.Plugins == nil {
				return die(cmd, "plugins registry unavailable")
			}

			// Load the plugin.
			if err := rt.Plugins.Install(ctx, sourceURI); err != nil {
				return die(cmd, "install failed: %s", err)
			}

			// Rewrite config.toml to add the source.
			if err := addPluginSource(rt, sourceURI); err != nil {
				return die(cmd, "config update failed: %s", err)
			}

			printf(out(cmd), "installed plugin from %s\n", sourceURI)
			return nil
		},
	}
}

// newPluginsRemoveCmd builds `hygge plugins remove <name>`.
func newPluginsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove an installed plugin",
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

			if rt.Plugins == nil {
				return die(cmd, "plugins registry unavailable")
			}

			src, ok := rt.Plugins.Source(name)
			if !ok {
				return die(cmd, "plugin %q not found", name)
			}

			ctx := context.Background()
			if err := rt.Plugins.Remove(ctx, name); err != nil {
				return die(cmd, "remove failed: %s", err)
			}

			// Rewrite config.toml to remove the source.
			if err := removePluginSource(rt, src.Raw); err != nil {
				return die(cmd, "config update failed: %s", err)
			}

			printf(out(cmd), "removed plugin %q\n", name)
			return nil
		},
	}
}

// newPluginsUpdateCmd builds `hygge plugins update [<name>]`.
func newPluginsUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update [<name>]",
		Short: "Update one or all plugins to their latest version",
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

			if rt.Plugins == nil {
				return die(cmd, "plugins registry unavailable")
			}

			ctx := context.Background()

			if len(args) == 1 {
				name := args[0]
				if err := rt.Plugins.Update(ctx, name); err != nil {
					return die(cmd, "update %q failed: %s", name, err)
				}
				printf(out(cmd), "updated plugin %q\n", name)
				return nil
			}

			// Update all.
			all := rt.Plugins.List()
			if len(all) == 0 {
				writeln(out(cmd), "(no plugins to update)")
				return nil
			}
			var updateErr error
			for _, p := range all {
				if err := rt.Plugins.Update(ctx, p.Name()); err != nil {
					printf(errOut(cmd), "update %q failed: %s\n", p.Name(), err)
					updateErr = err
				} else {
					printf(out(cmd), "updated %q\n", p.Name())
				}
			}
			return updateErr
		},
	}
}

// addPluginSource rewrites config.toml to append a source URI to
// [plugins].sources.  Uses an atomic temp-file + rename.
func addPluginSource(rt *appRuntime, sourceURI string) error {
	return rewritePluginSources(rt, func(sources []string) []string {
		return append(sources, sourceURI)
	})
}

// removePluginSource rewrites config.toml to remove a source URI from
// [plugins].sources.
func removePluginSource(rt *appRuntime, sourceURI string) error {
	return rewritePluginSources(rt, func(sources []string) []string {
		out := sources[:0]
		for _, s := range sources {
			if s != sourceURI {
				out = append(out, s)
			}
		}
		return out
	})
}

// rewritePluginSources reads the user's config.toml, applies fn to the
// plugins.sources list, and writes it back atomically.
func rewritePluginSources(rt *appRuntime, fn func([]string) []string) error {
	// Build the updated sources list.
	current := rt.Config.Plugins.Sources
	updated := fn(append([]string(nil), current...))

	// Build a minimal TOML fragment and call the config rewriter.
	return writePluginSourcesToConfig(rt, updated)
}

// writePluginSourcesToConfig writes the [plugins] sources list to the user
// config file.  This is deliberately minimal: we only touch the
// [plugins].sources array, leaving all other keys intact via raw TOML editing.
func writePluginSourcesToConfig(rt *appRuntime, sources []string) error {
	_, err := config.WritePluginSources(config.WritePluginSourcesOptions{
		HomeDir:       rt.StateOpts.HomeDir,
		XDGConfigHome: rt.XDGConfigHome,
		Pwd:           rt.Pwd,
		Provenance:    rt.Provenance,
	}, sources)
	return err
}
