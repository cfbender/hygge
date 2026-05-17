package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		newPluginsTypesCmd(),
		newPluginsDevCmd(),
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

// newPluginsTypesCmd builds `hygge plugins types` helper commands for LuaLS.
func newPluginsTypesCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "types",
		Short: "Install Lua plugin editor type stubs",
	}

	var force bool
	install := &cobra.Command{
		Use:   "install [dir]",
		Short: "Install LuaLS definitions into a plugin project",
		Long: `Install Hygge's LuaLS/LuaCATS definition file into a plugin project.

The command writes .hygge/types/hygge.lua and creates .luarc.json when it does
not already exist. Use --force to replace an existing .luarc.json with Hygge's
default LuaLS settings.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolvePluginDevDir(args)
			if err != nil {
				return die(cmd, "%s", err)
			}
			result, err := installPluginTypes(dir, force)
			if err != nil {
				return die(cmd, "install plugin types: %s", err)
			}

			printf(out(cmd), "installed LuaLS types: %s\n", result.TypesPath)
			if result.LuaRCWritten {
				printf(out(cmd), "wrote LuaLS config: %s\n", result.LuaRCPath)
			} else {
				printf(out(cmd), "left existing LuaLS config unchanged: %s\n", result.LuaRCPath)
			}
			return nil
		},
	}
	install.Flags().BoolVar(&force, "force", false, "replace an existing .luarc.json")

	root.AddCommand(install)
	return root
}

// newPluginsDevCmd builds `hygge plugins dev` helper commands.
func newPluginsDevCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "dev",
		Short: "Scaffold Lua plugin development files",
	}

	var force bool
	initCmd := &cobra.Command{
		Use:   "init [dir]",
		Short: "Initialize a local Lua plugin project",
		Long: `Initialize a local Lua plugin project without requiring the Hygge source tree.

The command creates plugin.lua, plugin.toml, .hygge/types/hygge.lua, and
.luarc.json. Existing files are preserved unless --force is set.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir, err := resolvePluginDevDir(args)
			if err != nil {
				return die(cmd, "%s", err)
			}
			if err := os.MkdirAll(dir, 0o750); err != nil {
				return die(cmd, "create plugin directory: %s", err)
			}

			if err := writePluginScaffold(dir, force); err != nil {
				return die(cmd, "initialize plugin scaffold: %s", err)
			}
			result, err := installPluginTypes(dir, force)
			if err != nil {
				return die(cmd, "install plugin types: %s", err)
			}

			printf(out(cmd), "initialized plugin project: %s\n", dir)
			printf(out(cmd), "installed LuaLS types: %s\n", result.TypesPath)
			if !result.LuaRCWritten {
				printf(out(cmd), "left existing LuaLS config unchanged: %s\n", result.LuaRCPath)
			}
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, "replace existing scaffold files and .luarc.json")

	root.AddCommand(initCmd)
	return root
}

type pluginTypesInstallResult struct {
	TypesPath    string
	LuaRCPath    string
	LuaRCWritten bool
}

func resolvePluginDevDir(args []string) (string, error) {
	dir := ""
	if len(args) > 0 {
		dir = args[0]
	}
	if dir == "" {
		dir = rootFlags.Pwd
	}
	if dir == "" && testOverrides != nil {
		dir = testOverrides.Pwd
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = wd
	}
	if !filepath.IsAbs(dir) {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		dir = abs
	}
	return dir, nil
}

func installPluginTypes(dir string, force bool) (pluginTypesInstallResult, error) {
	typesDir := filepath.Join(dir, ".hygge", "types")
	if err := os.MkdirAll(typesDir, 0o750); err != nil {
		return pluginTypesInstallResult{}, err
	}

	typesPath := filepath.Join(typesDir, "hygge.lua")
	if err := os.WriteFile(typesPath, plugin.LuaLSTypeStub(), 0o600); err != nil {
		return pluginTypesInstallResult{}, err
	}

	luarcPath := filepath.Join(dir, ".luarc.json")
	wroteLuaRC, err := writeFileUnlessExists(luarcPath, defaultLuaRC(), force)
	if err != nil {
		return pluginTypesInstallResult{}, err
	}

	return pluginTypesInstallResult{
		TypesPath:    typesPath,
		LuaRCPath:    luarcPath,
		LuaRCWritten: wroteLuaRC,
	}, nil
}

func writePluginScaffold(dir string, force bool) error {
	if _, err := writeFileUnlessExists(filepath.Join(dir, "plugin.toml"), []byte(defaultPluginManifest(dir)), force); err != nil {
		return err
	}
	if _, err := writeFileUnlessExists(filepath.Join(dir, "plugin.lua"), []byte(defaultPluginLua()), force); err != nil {
		return err
	}
	return nil
}

func writeFileUnlessExists(path string, data []byte, force bool) (bool, error) {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return false, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

func defaultLuaRC() []byte {
	settings := map[string]any{
		"runtime.version":           "Lua 5.1",
		"diagnostics.globals":       []string{"hygge"},
		"workspace.library":         []string{".hygge/types"},
		"workspace.checkThirdParty": false,
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func defaultPluginManifest(dir string) string {
	return `name = "` + defaultPluginName(dir) + `"
version = "0.1.0"
description = "A Hygge Lua plugin"
entrypoint = "plugin.lua"
`
}

func defaultPluginLua() string {
	return `-- Hygge Lua plugin entrypoint.

hygge.register_tool {
    name = "hello_world",
    description = "Returns a friendly greeting.",
    input_schema = {
        type = "object",
        properties = {
            name = { type = "string" },
        },
        required = { "name" },
        additionalProperties = false,
    },
    execute = function(ctx, input)
        local who = "World"
        if input and input.name then
            who = input.name
        end
        return { content = "Hello, " .. who .. "!" }
    end,
}
`
}

func defaultPluginName(dir string) string {
	name := strings.ToLower(filepath.Base(filepath.Clean(dir)))
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_'
		if valid {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	outName := strings.Trim(b.String(), "-")
	if outName == "" {
		return "plugin"
	}
	first := outName[0]
	if first < 'a' || first > 'z' {
		return "plugin-" + outName
	}
	return outName
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
