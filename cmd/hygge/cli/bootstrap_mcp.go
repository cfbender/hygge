// Package cli — MCP wiring: config discovery, OAuth header injection,
// transport construction, and best-effort server startup (synchronous
// and async).
package cli

import (
	"context"
	"log/slog"
	"maps"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/mcp"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/tool"
)

// MCPServerStatus summarises the boot-time outcome of one MCP server
// after bootstrap.  Surfaced by `hygge mcp list`.
type MCPServerStatus struct {
	Name      string
	Transport string
	Enabled   bool
	// Ready is true when Initialize + ListTools both succeeded.
	Ready bool
	// Error captures the first failure observed; empty when Ready.
	Error string
	// ToolCount is the number of tools registered from this server.
	ToolCount int
	// Source is the diagnostic source token, e.g. "project/.agents".
	Source string
	// CommandLabel is the command + first arg for display.
	CommandLabel string
}

// mcpBootstrapTimeout caps best-effort MCP discovery during startup. MCP tools
// should never be able to hold the first UI frame hostage; a slow or wedged
// server can still be diagnosed via `hygge mcp list` and fixed independently.
const mcpBootstrapTimeout = 10 * time.Second

type mcpInitResult struct {
	client *mcp.Client
	defs   []mcp.MCPToolDef
	status MCPServerStatus
}

func mcpAuthEnvLookup(opts bootstrapOptions) func(string) string {
	store, err := mcp.LoadAuth(mcpAuthLoadOptionsFromBootstrap(opts))
	if err != nil {
		slog.Warn("cli: failed to load mcp-auth.json; falling back to process environment", "err", err)
		return os.Getenv
	}
	byEnv := map[string]string{}
	owners := map[string]string{}
	servers := make([]string, 0, len(store.Servers))
	for server := range store.Servers {
		servers = append(servers, server)
	}
	sort.Strings(servers)
	for _, server := range servers {
		entry := store.Servers[server]
		headers := make([]string, 0, len(entry.Headers))
		for header := range entry.Headers {
			headers = append(headers, header)
		}
		sort.Strings(headers)
		for _, header := range headers {
			envVar := mcpAuthEnvVar(server, header)
			if owner := owners[envVar]; owner != "" && owner != server {
				slog.Warn("cli: duplicate MCP auth placeholder; keeping first stored value", "env_var", envVar, "first_server", owner, "ignored_server", server)
				continue
			}
			owners[envVar] = server
			byEnv[envVar] = entry.Headers[header]
		}
	}
	return func(key string) string {
		if value, ok := byEnv[key]; ok {
			return value
		}
		return os.Getenv(key)
	}
}

func mcpAuthLoadOptionsFromBootstrap(opts bootstrapOptions) mcp.AuthLoadOptions {
	return mcp.AuthLoadOptions{HomeDir: opts.HomeDir, XDGStateHome: opts.XDGStateHome}
}

func applyMCPOAuth(configs []mcp.ServerConfig, authOpts mcp.AuthLoadOptions, now time.Time) []mcp.ServerConfig {
	return applyMCPOAuthWithRefresh(configs, authOpts, now, true)
}

func applyMCPOAuthLoadOnly(configs []mcp.ServerConfig, authOpts mcp.AuthLoadOptions) []mcp.ServerConfig {
	return applyMCPOAuthWithRefresh(configs, authOpts, time.Time{}, false)
}

func applyMCPOAuthWithRefresh(configs []mcp.ServerConfig, authOpts mcp.AuthLoadOptions, now time.Time, refresh bool) []mcp.ServerConfig {
	store, err := mcp.LoadAuth(authOpts)
	if err != nil {
		slog.Warn("cli: failed to load mcp-auth.json for MCP OAuth; proceeding without OAuth", "err", err)
		return configs
	}
	if refresh && now.IsZero() {
		now = time.Now()
	}
	changed := false
	for i := range configs {
		if !configs[i].Enabled || !configs[i].OAuth.Enabled || (configs[i].Transport != "sse" && configs[i].Transport != "http") {
			continue
		}
		entry, ok := store.GetAuth(configs[i].Name)
		if !ok || (entry.Tokens == nil && entry.OAuth == nil) {
			slog.Warn("cli: MCP server has oauth=true but no OAuth credential in mcp-auth.json", "server", configs[i].Name)
			continue
		}
		if refresh {
			refreshed, err := entry.RefreshOAuth(nil, now)
			if err != nil {
				slog.Warn("cli: failed to refresh MCP OAuth token; using stored token", "server", configs[i].Name, "err", err)
			} else if refreshed {
				if store.Servers == nil {
					store.Servers = map[string]mcp.AuthEntry{}
				}
				store.Servers[configs[i].Name] = entry
				changed = true
			}
		}
		merged := entry.HeadersWithOAuth()
		maps.Copy(merged, configs[i].Headers)
		configs[i].Headers = merged
	}
	if changed {
		for server, entry := range store.Servers {
			if entry.Tokens == nil && entry.OAuth == nil {
				continue
			}
			if err := mcp.SetAuth(server, entry, authOpts); err != nil {
				slog.Warn("cli: failed to persist refreshed MCP OAuth token", "server", server, "err", err)
			}
		}
	}
	return configs
}

// bootstrapMCP loads mcp.toml files, spawns each enabled server, and
// registers its tools.  Returns the live clients, the discovered
// configs (including disabled ones), and a status summary for the
// `hygge mcp list` command.
//
// Failures are non-fatal: every server is independently spawned; one
// crashing does not affect the others.  Errors are recorded in the
// status so the CLI can surface them without blocking startup.
func bootstrapMCP(ctx context.Context, opts bootstrapOptions, xdgConfig string, tools *tool.Registry) ([]*mcp.Client, []mcp.ServerConfig, []MCPServerStatus) {
	configs, err := mcp.LoadConfigs(mcp.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
		EnvLookup:     mcpAuthEnvLookup(opts),
	})
	if err != nil {
		slog.Warn("cli: failed to load mcp.toml; MCP support disabled for this run", "err", err)
		return nil, nil, nil
	}
	if opts.Now != nil {
		configs = applyMCPOAuth(configs, mcpAuthLoadOptionsFromBootstrap(opts), opts.Now())
	} else {
		configs = applyMCPOAuth(configs, mcpAuthLoadOptionsFromBootstrap(opts), time.Now())
	}
	if len(configs) == 0 {
		return nil, nil, nil
	}

	results := make([]mcpInitResult, len(configs))
	var wg sync.WaitGroup

	statuses := make([]MCPServerStatus, 0, len(configs))
	for i, cfg := range configs {
		status := MCPServerStatus{
			Name:      cfg.Name,
			Transport: cfg.Transport,
			Enabled:   cfg.Enabled,
			Source:    cfg.Source.String(),
		}
		if !cfg.Enabled {
			results[i] = mcpInitResult{status: status}
			continue
		}

		wg.Add(1)
		go func(i int, cfg mcp.ServerConfig, status MCPServerStatus) {
			defer wg.Done()
			results[i] = bootstrapMCPServer(ctx, cfg, status, opts)
		}(i, cfg, status)
	}
	wg.Wait()

	var clients []*mcp.Client
	for i, result := range results {
		status := result.status
		if result.client != nil && status.Error == "" {
			registered := 0
			for _, def := range result.defs {
				t := mcp.NewMCPTool(result.client, def, permission.Category(configs[i].PermissionCategory))
				if err := tools.Register(t); err != nil {
					slog.Warn("cli: MCP tool name collision; skipping",
						"server", configs[i].Name, "tool", def.Name, "err", err)
					continue
				}
				registered++
			}
			status.Ready = true
			status.ToolCount = registered
			clients = append(clients, result.client)
		}
		statuses = append(statuses, status)
	}
	return clients, configs, statuses
}

// prepareAsyncMCP does only cheap config parsing and status-row construction.
// Actual server startup is kicked off by appRuntime.StartAsyncMCP after the TUI
// has subscribed to bus events, so no slow MCP process can delay first paint.
func prepareAsyncMCP(opts bootstrapOptions, xdgConfig string) ([]mcp.ServerConfig, []MCPServerStatus) {
	configs, err := mcp.LoadConfigs(mcp.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
		EnvLookup:     mcpAuthEnvLookup(opts),
	})
	if err != nil {
		slog.Warn("cli: failed to load mcp.toml; MCP support disabled for this run", "err", err)
		return nil, nil
	}
	configs = applyMCPOAuthLoadOnly(configs, mcpAuthLoadOptionsFromBootstrap(opts))
	statuses := make([]MCPServerStatus, 0, len(configs))
	for _, cfg := range configs {
		status := MCPServerStatus{
			Name:         cfg.Name,
			Transport:    cfg.Transport,
			Enabled:      cfg.Enabled,
			Source:       cfg.Source.String(),
			CommandLabel: mcpCommandLabel(cfg),
		}
		statuses = append(statuses, status)
	}
	return configs, statuses
}

// StartAsyncMCP launches one best-effort initializer per enabled MCP server.
// It is safe to call more than once; only the first call starts work.
func (r *appRuntime) StartAsyncMCP(ctx context.Context) {
	if r == nil || len(r.mcpAsyncConfigs) == 0 || r.Tools == nil {
		return
	}
	r.mcpMu.Lock()
	if r.mcpAsyncStarted {
		r.mcpMu.Unlock()
		return
	}
	r.mcpAsyncStarted = true
	mcpCtx, cancel := context.WithCancel(ctx)
	r.mcpCancel = cancel
	configs := append([]mcp.ServerConfig(nil), r.mcpAsyncConfigs...)
	authOpts := r.mcpAuthOpts
	nowFunc := r.mcpNow
	r.mcpMu.Unlock()

	if nowFunc != nil {
		configs = applyMCPOAuth(configs, authOpts, nowFunc())
	} else {
		configs = applyMCPOAuth(configs, authOpts, time.Now())
	}

	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		status := MCPServerStatus{
			Name:         cfg.Name,
			Transport:    cfg.Transport,
			Enabled:      cfg.Enabled,
			Source:       cfg.Source.String(),
			CommandLabel: mcpCommandLabel(cfg),
		}
		r.mcpWG.Add(1)
		go func(cfg mcp.ServerConfig, status MCPServerStatus) {
			defer r.mcpWG.Done()
			result := bootstrapMCPServer(mcpCtx, cfg, status, bootstrapOptions{})
			status = result.status
			if result.client != nil && status.Error == "" {
				status.Ready = true
				status.ToolCount = registerMCPTools(r.Tools, result.client, result.defs, permission.Category(cfg.PermissionCategory), cfg.Name)
				r.addMCPClient(result.client)
			}
			r.publishMCPStatus(status)
		}(cfg, status)
	}
}

func (r *appRuntime) addMCPClient(client *mcp.Client) {
	r.mcpMu.Lock()
	defer r.mcpMu.Unlock()
	r.MCPClients = append(r.MCPClients, client)
}

func (r *appRuntime) publishMCPStatus(status MCPServerStatus) {
	r.mcpMu.Lock()
	for i := range r.MCPStatuses {
		if r.MCPStatuses[i].Name == status.Name {
			r.MCPStatuses[i] = status
			r.mcpMu.Unlock()
			publishMCPStatus(r.Bus, status)
			return
		}
	}
	r.MCPStatuses = append(r.MCPStatuses, status)
	r.mcpMu.Unlock()
	publishMCPStatus(r.Bus, status)
}

func publishMCPStatus(b *bus.Bus, status MCPServerStatus) {
	if b == nil {
		return
	}
	bus.Publish(b, bus.MCPStatusUpdated{
		Name:      status.Name,
		Transport: status.Transport,
		Enabled:   status.Enabled,
		Ready:     status.Ready,
		Error:     status.Error,
		ToolCount: status.ToolCount,
		Source:    status.Source,
		At:        time.Now(),
	})
}

func registerMCPTools(tools *tool.Registry, client *mcp.Client, defs []mcp.MCPToolDef, category permission.Category, serverName string) int {
	registered := 0
	for _, def := range defs {
		t := mcp.NewMCPTool(client, def, category)
		if err := tools.Register(t); err != nil {
			slog.Warn("cli: MCP tool name collision; skipping",
				"server", serverName, "tool", def.Name, "err", err)
			continue
		}
		registered++
	}
	return registered
}

func bootstrapMCPServer(ctx context.Context, cfg mcp.ServerConfig, status MCPServerStatus, opts bootstrapOptions) mcpInitResult {
	transport := newMCPTransport(cfg)
	status.CommandLabel = transport.ServerLabel()
	client := mcp.New(mcp.ClientOptions{
		Transport:     transport,
		Name:          cfg.Name,
		ClientName:    "hygge",
		ClientVersion: Version,
		Now:           opts.Now,
	})
	serverCtx, cancel := context.WithTimeout(ctx, mcpBootstrapTimeout)
	defer cancel()
	if _, err := client.Initialize(serverCtx); err != nil {
		slog.Warn("cli: MCP server failed to initialize", "name", cfg.Name, "err", err)
		status.Error = err.Error()
		_ = client.Close()
		return mcpInitResult{status: status}
	}
	defs, err := client.ListTools(serverCtx)
	if err != nil {
		slog.Warn("cli: MCP tools/list failed", "name", cfg.Name, "err", err)
		status.Error = err.Error()
		_ = client.Close()
		return mcpInitResult{status: status}
	}
	return mcpInitResult{client: client, defs: defs, status: status}
}

func mcpCommandLabel(cfg mcp.ServerConfig) string {
	return newMCPTransport(cfg).ServerLabel()
}

func newMCPTransport(cfg mcp.ServerConfig) mcp.Transport {
	switch cfg.Transport {
	case "sse":
		return mcp.NewSSE(mcp.SSEOptions{
			ServerURL:  cfg.URL,
			Headers:    cfg.Headers,
			ServerName: cfg.Name,
		})
	case "http":
		return mcp.NewStreamable(mcp.StreamableOptions{
			ServerURL:               cfg.URL,
			Headers:                 cfg.Headers,
			ServerName:              cfg.Name,
			OpenNotificationsStream: true,
		})
	default: // "stdio"
		return mcp.NewStdio(mcp.StdioOptions{
			Command: cfg.Command,
			Args:    cfg.Args,
			Env:     cfg.Env,
			Dir:     cfg.Dir,
		})
	}
}
