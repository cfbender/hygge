package cli

import (
	"context"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/cfbender/hygge/internal/mcp"
)

// newMCPCmd builds the `hygge mcp` subcommand group.
func newMCPCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "mcp",
		Short: "Inspect and probe configured MCP servers",
	}
	root.AddCommand(
		newMCPAddCmd(),
		newMCPListCmd(),
		newMCPPingCmd(),
		newMCPToolsCmd(),
		newMCPDoctorCmd(),
	)
	return root
}

// newMCPListCmd: `hygge mcp list` shows the configured servers and
// their boot-time status.
func newMCPListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers and their status",
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

			if len(rt.MCPStatuses) == 0 {
				writeln(out(cmd), "(no MCP servers configured)")
				return nil
			}
			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "NAME\tTRANSPORT\tSTATUS\tTOOLS\tSOURCE\tCOMMAND")
			for _, s := range rt.MCPStatuses {
				printf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n",
					s.Name, s.Transport, statusLabel(s), s.ToolCount, s.Source,
					orDash(s.CommandLabel),
				)
			}
			return tw.Flush()
		},
	}
}

func statusLabel(s MCPServerStatus) string {
	if !s.Enabled {
		return "disabled"
	}
	if s.Ready {
		return "ready"
	}
	if s.Error != "" {
		return "failed"
	}
	return "unknown"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// newMCPPingCmd: `hygge mcp ping <name>` spawns a temporary client and
// confirms the server responds.
func newMCPPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ping <name>",
		Short: "Initialize and ping a configured MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfgs, err := loadMCPConfigs()
			if err != nil {
				return err
			}
			var target *mcp.ServerConfig
			for i := range cfgs {
				if cfgs[i].Name == name {
					target = &cfgs[i]
					break
				}
			}
			if target == nil {
				return die(cmd, "no MCP server named %q (try `hygge mcp list`)", name)
			}
			if !target.Enabled {
				return die(cmd, "MCP server %q is disabled in %s", name, target.Path)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			var transport mcp.Transport
			switch target.Transport {
			case "sse":
				transport = mcp.NewSSE(mcp.SSEOptions{
					ServerURL:  target.URL,
					Headers:    target.Headers,
					ServerName: target.Name,
				})
			case "http":
				transport = mcp.NewStreamable(mcp.StreamableOptions{
					ServerURL:               target.URL,
					Headers:                 target.Headers,
					ServerName:              target.Name,
					OpenNotificationsStream: true,
				})
			default: // "stdio"
				transport = mcp.NewStdio(mcp.StdioOptions{
					Command: target.Command,
					Args:    target.Args,
					Env:     target.Env,
					Dir:     target.Dir,
				})
			}
			client := mcp.New(mcp.ClientOptions{
				Transport:     transport,
				Name:          target.Name,
				ClientName:    "hygge",
				ClientVersion: Version,
			})
			defer func() { _ = client.Close() }()

			start := time.Now()
			info, err := client.Initialize(ctx)
			if err != nil {
				return die(cmd, "initialize %s: %v", name, err)
			}
			initDur := time.Since(start)
			pingStart := time.Now()
			if err := client.Ping(ctx); err != nil {
				return die(cmd, "ping %s: %v", name, err)
			}
			pingDur := time.Since(pingStart)
			printf(out(cmd), "%s ready (%s %s) — init %s, ping %s\n",
				name, info.ServerInfo.Name, info.ServerInfo.Version,
				initDur.Round(time.Millisecond), pingDur.Round(time.Millisecond))
			return nil
		},
	}
}

// newMCPToolsCmd: `hygge mcp tools [server]` lists the tools the
// connected servers advertise.
func newMCPToolsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tools [server]",
		Short: "List tools advertised by configured MCP servers",
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

			var filter string
			if len(args) == 1 {
				filter = args[0]
			}

			// rt.MCPClients holds only the ready clients; pair them
			// up by Name for filtering.
			byName := make(map[string]*mcp.Client, len(rt.MCPClients))
			for _, c := range rt.MCPClients {
				byName[c.Name()] = c
			}
			if filter != "" {
				if _, ok := byName[filter]; !ok {
					return die(cmd, "no ready MCP server named %q (see `hygge mcp list`)", filter)
				}
			}

			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "SERVER\tTOOL\tDESCRIPTION")
			for _, c := range rt.MCPClients {
				if filter != "" && c.Name() != filter {
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defs, err := c.ListTools(ctx)
				cancel()
				if err != nil {
					printf(tw, "%s\t-\t(tools/list failed: %v)\n", c.Name(), err)
					continue
				}
				if len(defs) == 0 {
					printf(tw, "%s\t-\t(no tools advertised)\n", c.Name())
					continue
				}
				for _, def := range defs {
					printf(tw, "%s\t%s\t%s\n",
						c.Name(), def.Name, truncateInline(def.Description, 60))
				}
			}
			return tw.Flush()
		},
	}
}

// newMCPDoctorCmd: `hygge mcp doctor` walks every mcp.toml hygge can
// find, validates each, and reports issues.
func newMCPDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Validate every discovered mcp.toml and report issues",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := mcpHomeDir()
			xdg := mcpXDGConfig()
			pwd := mcpPwd()
			paths := []string{
				filepath.Join(home, ".agents", "mcp.toml"),
				filepath.Join(xdg, "hygge", "mcp.toml"),
			}
			if pwd != "" {
				paths = append(paths,
					filepath.Join(pwd, ".agents", "mcp.toml"),
					filepath.Join(pwd, ".hygge", "mcp.toml"),
				)
			}

			tw := tabwriter.NewWriter(out(cmd), 0, 0, 2, ' ', 0)
			writeln(tw, "STATUS\tPATH\tDETAIL")
			problems := 0
			anyFound := false
			for _, p := range paths {
				if _, err := os.Stat(p); err != nil {
					if os.IsNotExist(err) {
						printf(tw, "absent\t%s\t-\n", abbreviatePath(p, home))
						continue
					}
					printf(tw, "error\t%s\t%v\n", abbreviatePath(p, home), err)
					problems++
					continue
				}
				anyFound = true
				configs, err := loadMCPConfigsFromPath(p)
				if err != nil {
					printf(tw, "invalid\t%s\t%v\n", abbreviatePath(p, home), err)
					problems++
					continue
				}
				printf(tw, "ok\t%s\t%d server(s)\n", abbreviatePath(p, home), len(configs))
			}
			if err := tw.Flush(); err != nil {
				return err
			}
			if !anyFound {
				printf(out(cmd), "\nNo mcp.toml files found. See `hygge --help` for the search path.\n")
				return nil
			}
			if problems == 0 {
				writeln(out(cmd), "\nno problems detected")
			} else {
				printf(out(cmd), "\n%d issue(s) detected\n", problems)
			}
			return nil
		},
	}
}

// loadMCPConfigs is a thin wrapper that honours testOverrides.  Used
// by `hygge mcp ping` to look up a server without going through full
// bootstrap.
func loadMCPConfigs() ([]mcp.ServerConfig, error) {
	return mcp.LoadConfigs(mcp.LoadOptions{
		HomeDir:       mcpHomeDir(),
		XDGConfigHome: mcpXDGConfig(),
		Pwd:           mcpPwd(),
	})
}

// loadMCPConfigsFromPath parses ONE mcp.toml file and returns its
// servers.  Used by `mcp doctor` to give per-file diagnostics.
func loadMCPConfigsFromPath(path string) ([]mcp.ServerConfig, error) {
	// LoadConfigs walks every layer; for doctor we want each file in
	// isolation.  We synthesise a HomeDir / Pwd pointing at a tmpdir
	// so nothing else is found, and point one of the layers at the
	// file's actual location by adjusting Pwd.  Simpler approach:
	// use a dedicated single-file decoder by piggy-backing on
	// LoadConfigs with carefully-chosen roots is fiddly.  Instead we
	// use a small helper that just runs the same validation logic
	// directly via LoadConfigs with a Pwd that contains a .agents
	// (or .hygge) directory at the path layer.
	//
	// Easier: just load every layer and filter to this path.  This
	// is doctor — performance doesn't matter.
	all, err := loadMCPConfigs()
	if err != nil {
		return nil, err
	}
	var out []mcp.ServerConfig
	for _, c := range all {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out, nil
}

func mcpHomeDir() string {
	if testOverrides != nil && testOverrides.HomeDir != "" {
		return testOverrides.HomeDir
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

func mcpXDGConfig() string {
	if testOverrides != nil && testOverrides.XDGConfigHome != "" {
		return testOverrides.XDGConfigHome
	}
	if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && v != "" {
		return v
	}
	return filepath.Join(mcpHomeDir(), ".config")
}

func mcpPwd() string {
	if testOverrides != nil && testOverrides.Pwd != "" {
		return testOverrides.Pwd
	}
	if rootFlags.Pwd != "" {
		return rootFlags.Pwd
	}
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}
