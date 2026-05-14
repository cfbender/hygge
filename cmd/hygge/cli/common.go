// Package cli is the dependency-injection root for the hygge binary.
//
// Each subcommand lives in its own file (run.go, resume.go, sessions.go,
// profile.go, config_cmd.go, theme_cmd.go, version.go).  Shared bootstrap
// code that wires every internal/* package together lives here.
//
// # Layering
//
// cmd/hygge/cli is the only place that may import every internal package.
// All other internal packages must remain free of cross-cutting "wire it
// all up" code.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/agent"
	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/mcp"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	_ "github.com/cfbender/hygge/internal/provider/anthropic"  // self-register
	_ "github.com/cfbender/hygge/internal/provider/openai"     // self-register
	_ "github.com/cfbender/hygge/internal/provider/openrouter" // self-register
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/skill"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/subagent"
	"github.com/cfbender/hygge/internal/tool"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// runtime is the wired graph of every component the CLI needs.  Returned
// from bootstrap.  Callers must defer Close to release the SQLite handle
// and unblock the agent's per-session locks.
type appRuntime struct {
	Config         *config.Config
	Provenance     config.Provenance
	State          *state.State
	StateOpts      state.LoadOptions
	Bus            *bus.Bus
	Store          *store.Store
	Provider       provider.Provider
	Permission     *permission.Engine
	Tools          *tool.Registry
	Catalog        *cost.Catalog
	Agent          *agent.Agent
	Theme          *theme.Theme
	Skills         *skill.Registry
	Subagents      *subagent.Registry
	SubagentRunner *subagent.Runner
	AgentsBlocks   []agentsmd.Block
	MCPClients     []*mcp.Client
	MCPConfigs     []mcp.ServerConfig
	MCPStatuses    []MCPServerStatus
	SystemPrompt   string
	Pwd            string
}

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

// bootstrapOptions feeds bootstrap.  Most fields are populated from
// cobra flags; HomeDir / XDGConfigHome / XDGStateHome are present to let
// tests redirect every disk operation into a t.TempDir.
//
// ProviderFactory, when non-nil, replaces the provider.Get(...) lookup so
// tests can inject a scripted fake without registering a global factory.
type bootstrapOptions struct {
	ConfigFile      string
	ProfileName     string
	Pwd             string
	HomeDir         string
	XDGConfigHome   string
	XDGStateHome    string
	ProviderFactory func(opts map[string]any) (provider.Provider, error)
	// Now is an injectable clock for tests; defaults to time.Now.
	Now func() time.Time
	// SystemPrompt overrides the default system prompt.  Tests use this
	// to keep agent traffic predictable; production callers leave it
	// empty so defaultSystemPrompt below is used.
	SystemPrompt string
	// SkipTea, when true, makes runTUI build the App but skip the
	// tea.Program.Run loop.  Test-only.
	SkipTea bool
	// CatalogBaseURL overrides the cost catalog's models.dev host.
	// Tests point this at an httptest server (typically a 500-returning
	// stub) so no live network call is ever made.  Test-only.
	CatalogBaseURL string
}

// defaultSystemPrompt is the v0.1 hardcoded system prompt.  Two sentences.
const defaultSystemPrompt = "You are hygge, a terminal-based AI coding assistant. " +
	"Be concise and use the available tools to read, search, and modify files in the user's working directory."

// Close releases resources held by the runtime.  Idempotent — safe to
// defer in a command body even if construction failed mid-way.
func (r *appRuntime) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	if r.Agent != nil {
		if err := r.Agent.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, c := range r.MCPClients {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.Permission != nil {
		r.Permission.Close()
	}
	if r.Bus != nil {
		r.Bus.Close()
	}
	if r.Store != nil {
		if err := r.Store.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// buildLazyTracker constructs the per-tool-call subdir context
// tracker the agent loop hands every touched path to.  Returns nil
// (lazy loading disabled) when no project root could be discovered
// from pwd, or when no markers exist at all.  Seeds the tracker's
// seen-dir set with every directory whose context was already loaded
// at bootstrap so those files are never re-injected.
func buildLazyTracker(homeDir, pwd string, loaded []agentsmd.Block) *agentsmd.LazyTracker {
	root := discoverProjectRoot(pwd, homeDir)
	if root == "" {
		slog.Warn("cli: lazy context disabled (no project root found)",
			"pwd", pwd)
		return nil
	}
	seenDirs := make([]string, 0, len(loaded))
	for _, b := range loaded {
		if b.Path == "" {
			continue
		}
		seenDirs = append(seenDirs, filepath.Dir(b.Path))
	}
	return agentsmd.NewLazyTracker(homeDir, root, seenDirs)
}

// discoverProjectRoot walks parents of pwd looking for any of the
// project-root markers Load uses (AGENTS.md, CLAUDE.md, .git, .agents,
// .hygge).  Returns "" when no marker is found before $HOME or the
// filesystem root.  Mirrors agentsmd.findProjectRoot deliberately — see
// STATUS.md for why we don't share a helper yet.
func discoverProjectRoot(pwd, homeStop string) string {
	if pwd == "" {
		return ""
	}
	dir := filepath.Clean(pwd)
	homeStop = filepath.Clean(homeStop)
	for {
		if homeStop != "" && dir == homeStop {
			return ""
		}
		if hasProjectMarker(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hasProjectMarker reports whether dir contains any of the
// project-root markers: AGENTS.md / CLAUDE.md, .git, .agents/, .hygge/.
func hasProjectMarker(dir string) bool {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if info, err := os.Stat(filepath.Join(dir, ".agents")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(dir, ".hygge")); err == nil && info.IsDir() {
		return true
	}
	return false
}

// bootstrap builds every component the CLI commands need from a single
// options struct.  Returns a fully-wired runtime that callers Close.
//
// Wiring order matches the dependency graph:
//
//  1. Resolve Pwd.
//  2. Load config (state.json may be consulted for the active profile).
//  3. Load state.
//  4. Build bus.
//  5. Open store at $XDG_STATE_HOME/hygge/sessions.db.
//  6. Build provider (via opts.ProviderFactory or provider.Get).
//  7. Build permission engine.
//  8. Build tool registry (defaults).
//  9. Build cost catalog.
//
// 10. Build theme.
// 11. Build agent.
//
// Callers MUST defer rt.Close().
func bootstrap(ctx context.Context, opts bootstrapOptions) (rt *appRuntime, err error) {
	opts = applyTestOverrides(opts)
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.ConfigFile != "" {
		// TODO(v0.2): treat opts.ConfigFile as an explicit user-config
		// override layer.  For now we warn and fall back to the
		// default discovery path so users aren't surprised by a
		// silent no-op.
		slog.Warn("cli: --config flag not yet implemented; using default discovery path",
			"path", opts.ConfigFile)
	}

	if opts.Pwd == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cli: getwd: %w", err)
		}
		opts.Pwd = wd
	}

	if opts.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("cli: home dir: %w", err)
		}
		opts.HomeDir = home
	}

	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		if v, ok := os.LookupEnv("XDG_CONFIG_HOME"); ok && v != "" {
			xdgConfig = v
		} else {
			xdgConfig = filepath.Join(opts.HomeDir, ".config")
		}
	}
	xdgState := opts.XDGStateHome
	if xdgState == "" {
		if v, ok := os.LookupEnv("XDG_STATE_HOME"); ok && v != "" {
			xdgState = v
		} else {
			xdgState = filepath.Join(opts.HomeDir, ".local", "state")
		}
	}

	stateOpts := state.LoadOptions{HomeDir: opts.HomeDir, XDGStateHome: xdgState}

	// Load the config.  This consults state.json for the active profile
	// when opts.ProfileName is empty, so it must run before we Load the
	// state ourselves.
	cfgEnv := envLookupWithXDG(opts.HomeDir, xdgConfig, xdgState)
	cfg, prov, err := config.Load(ctx, config.LoadOptions{
		Pwd:       opts.Pwd,
		Profile:   opts.ProfileName,
		HomeDir:   opts.HomeDir,
		EnvLookup: cfgEnv,
	})
	if err != nil {
		return nil, fmt.Errorf("cli: load config: %w", err)
	}

	st, err := state.Load(stateOpts)
	if err != nil {
		return nil, fmt.Errorf("cli: load state: %w", err)
	}

	b := bus.New()

	// Ensure the parent directory exists before SQLite tries to open the
	// file — store.Open does not create intermediate directories.
	storePath := filepath.Join(xdgState, "hygge", "sessions.db")
	if err := os.MkdirAll(filepath.Dir(storePath), 0o700); err != nil {
		b.Close()
		return nil, fmt.Errorf("cli: ensure store dir: %w", err)
	}
	stOpen, err := store.Open(ctx, storePath)
	if err != nil {
		b.Close()
		return nil, fmt.Errorf("cli: open store: %w", err)
	}

	// Build the provider.  If a factory was injected use it directly;
	// otherwise look up the registered factory by config name.  Before
	// either path runs, we resolve credentials: precedence is
	//
	//   1. config-level model.options.api_key (explicit user override)
	//   2. $<PROVIDER>_API_KEY environment variable (handled by the
	//      adapter itself; we just step out of the way)
	//   3. auth.json store entry (injected as opts["api_key"])
	//
	// The provider adapter's own resolveAPIKey chain is preserved
	// unchanged; this code is purely about feeding it the right input.
	modelOpts, err := resolveProviderOptionsFor(cfg.Model.Provider, cfg, stateOpts)
	if err != nil {
		_ = stOpen.Close()
		b.Close()
		return nil, err
	}
	prv, err := buildProvider(opts.ProviderFactory, cfg, modelOpts)
	if err != nil {
		_ = stOpen.Close()
		b.Close()
		return nil, err
	}

	permEngine, err := permission.New(permission.EngineOptions{
		Bus:    b,
		Config: cfg,
		State:  stateOpts,
		Clock:  opts.Now,
	})
	if err != nil {
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build permission engine: %w", err)
	}

	catalog := cost.NewCatalog(cost.CatalogOptions{
		Now:     opts.Now,
		BaseURL: opts.CatalogBaseURL,
	})

	thm, err := theme.Load(cfg.Theme.Name, theme.LoadOptions{
		ConfigHome: xdgConfig,
		HomeDir:    opts.HomeDir,
	})
	if err != nil {
		// A missing or malformed user theme should never block the CLI
		// — fall back to the shell theme and warn.
		slog.Warn("cli: theme load failed; falling back to shell theme",
			"name", cfg.Theme.Name, "err", err)
		thm = theme.ShellTheme()
	}

	contextWindow := lookupContextWindow(ctx, prv, cfg.Model.Name)

	// Skill registry and AGENTS.md blocks both feed into the system
	// prompt.  Failures here are non-fatal — they degrade the prompt
	// but should not block the CLI from starting.
	skillReg, err := skill.Load(skill.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
	})
	if err != nil {
		slog.Warn("cli: failed to load skills", "err", err)
		skillReg = &skill.Registry{}
	}
	agentsBlocks, err := agentsmd.Load(agentsmd.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
	})
	if err != nil {
		slog.Warn("cli: failed to load AGENTS.md", "err", err)
		agentsBlocks = nil
	}

	// Construct the lazy per-tool-call subdir context tracker.  The
	// tracker is seeded with every directory we just loaded a block
	// from so the same file is never re-injected.  Failures here
	// only degrade lazy loading; the agent still runs without it.
	lazyTracker := buildLazyTracker(opts.HomeDir, opts.Pwd, agentsBlocks)

	// Build the tool registry now that the skill registry is in hand
	// so the skill tool is registered when (and only when) skills are
	// configured.
	tools := tool.DefaultWith(tool.DefaultOptions{SkillRegistry: skillReg})

	// Sub-agents: the `task` tool dispatches isolated missions to a
	// fresh agent.  We load the type registry (built-in `general` +
	// any TOML-declared additions), construct the Runner, and
	// register the tool ONLY in the orchestrator's tool set.  The
	// runner internally builds a restricted tool registry per
	// invocation that strips `task` -- the recursion guard.
	//
	// DefaultTools is the parent's built-in tool names minus `task`
	// (the tool isn't registered yet at this point, so the list is
	// already correct).  MCP tools register below; they are NOT
	// automatically included in the sub-agent default set so a TOML
	// type that wants them must opt in explicitly.
	defaultSubagentTools := toolNamesFromRegistry(tools)
	subagentReg, err := subagent.Load(subagent.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
		DefaultTools:  defaultSubagentTools,
	})
	if err != nil {
		slog.Warn("cli: failed to load subagents.toml; using built-in types only", "err", err)
		subagentReg, _ = subagent.Load(subagent.LoadOptions{DefaultTools: defaultSubagentTools})
	}

	subRunner, err := subagent.NewRunner(subagent.RunnerOptions{
		Bus:              b,
		Store:            stOpen,
		Provider:         prv,
		Permission:       permEngine,
		Catalog:          catalog,
		Registry:         subagentReg,
		ParentTools:      tools,
		Pwd:              opts.Pwd,
		ContextWindow:    contextWindow,
		ProviderResolver: buildProviderResolver(cfg, stateOpts, prv),
		Now:              opts.Now,
	})
	if err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build subagent runner: %w", err)
	}
	if err := tools.Register(tool.NewTaskTool(subRunner.ToolAdapter())); err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: register task tool: %w", err)
	}

	// MCP servers contribute additional tools.  Loading is best-
	// effort: a misconfigured server warns and is skipped.  The agent
	// still works without any MCP tools.
	mcpClients, mcpConfigs, mcpStatuses := bootstrapMCP(ctx, opts, xdgConfig, tools)

	sysPrompt := opts.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = defaultSystemPrompt
	}
	if extras := skill.BuildSystemPromptAdditions(skillReg); extras != "" {
		sysPrompt += "\n\n" + extras
	}
	if extras := agentsmd.BuildSystemPromptAdditions(agentsBlocks); extras != "" {
		sysPrompt += "\n\n" + extras
	}

	ag, err := agent.New(agent.Options{
		Bus:           b,
		Store:         stOpen,
		Provider:      prv,
		Permission:    permEngine,
		Tools:         tools,
		Catalog:       catalog,
		Pwd:           opts.Pwd,
		ContextWindow: contextWindow,
		SystemPrompt:  sysPrompt,
		Now:           opts.Now,
		LazyContext:   lazyTracker,
	})
	if err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build agent: %w", err)
	}

	return &appRuntime{
		Config:         cfg,
		Provenance:     prov,
		State:          st,
		StateOpts:      stateOpts,
		Bus:            b,
		Store:          stOpen,
		Provider:       prv,
		Permission:     permEngine,
		Tools:          tools,
		Catalog:        catalog,
		Agent:          ag,
		Theme:          thm,
		Skills:         skillReg,
		Subagents:      subagentReg,
		SubagentRunner: subRunner,
		AgentsBlocks:   agentsBlocks,
		MCPClients:     mcpClients,
		MCPConfigs:     mcpConfigs,
		MCPStatuses:    mcpStatuses,
		SystemPrompt:   sysPrompt,
		Pwd:            opts.Pwd,
	}, nil
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
	})
	if err != nil {
		slog.Warn("cli: failed to load mcp.toml; MCP support disabled for this run", "err", err)
		return nil, nil, nil
	}
	if len(configs) == 0 {
		return nil, nil, nil
	}

	var clients []*mcp.Client
	statuses := make([]MCPServerStatus, 0, len(configs))
	for _, cfg := range configs {
		status := MCPServerStatus{
			Name:      cfg.Name,
			Transport: cfg.Transport,
			Enabled:   cfg.Enabled,
			Source:    cfg.Source.String(),
		}
		if !cfg.Enabled {
			statuses = append(statuses, status)
			continue
		}
		transport := mcp.NewStdio(mcp.StdioOptions{
			Command: cfg.Command,
			Args:    cfg.Args,
			Env:     cfg.Env,
			Dir:     cfg.Dir,
		})
		status.CommandLabel = transport.ServerLabel()
		client := mcp.New(mcp.ClientOptions{
			Transport:     transport,
			Name:          cfg.Name,
			ClientName:    "hygge",
			ClientVersion: Version,
			Now:           opts.Now,
		})
		if _, err := client.Initialize(ctx); err != nil {
			slog.Warn("cli: MCP server failed to initialize", "name", cfg.Name, "err", err)
			status.Error = err.Error()
			_ = client.Close()
			statuses = append(statuses, status)
			continue
		}
		defs, err := client.ListTools(ctx)
		if err != nil {
			slog.Warn("cli: MCP tools/list failed", "name", cfg.Name, "err", err)
			status.Error = err.Error()
			_ = client.Close()
			statuses = append(statuses, status)
			continue
		}
		registered := 0
		for _, def := range defs {
			t := mcp.NewMCPTool(client, def, permission.Category(cfg.PermissionCategory))
			if err := tools.Register(t); err != nil {
				slog.Warn("cli: MCP tool name collision; skipping",
					"server", cfg.Name, "tool", def.Name, "err", err)
				continue
			}
			registered++
		}
		status.Ready = true
		status.ToolCount = registered
		statuses = append(statuses, status)
		clients = append(clients, client)
	}
	return clients, configs, statuses
}

// buildProviderResolver returns a [subagent.ProviderResolver] closure
// that constructs (or fetches a cached) provider for a "<provider>/
// <model-id>" reference.  Behaviour:
//
//   - When providerName matches the parent's config, returns the
//     parent provider unchanged — no second construction, no second
//     credential lookup.
//   - Otherwise, constructs the provider via [buildProviderFor],
//     reusing the parent's cfg + stateOpts for credential resolution.
//   - Successfully-constructed providers are cached by name so
//     repeated invocations across many `task` calls share a single
//     instance per provider.
//   - Errors (unknown provider, missing credentials, invalid ref)
//     bubble up to the caller; the Runner surfaces them as Run
//     errors which the task tool renders as IsError results.
//
// The resolver is safe for concurrent use — the cache map is guarded
// by a sync.Mutex.  Provider adapters themselves are required to be
// concurrent-safe; this layer assumes that.
func buildProviderResolver(cfg *config.Config, stateOpts state.LoadOptions, parent provider.Provider) subagent.ProviderResolver {
	var mu sync.Mutex
	cache := map[string]provider.Provider{}
	if parent != nil && cfg != nil && cfg.Model.Provider != "" {
		cache[cfg.Model.Provider] = parent
	}
	return func(_ context.Context, ref string) (provider.Provider, string, error) {
		providerName, modelID, err := subagent.ParseModelRef(ref)
		if err != nil {
			return nil, "", err
		}
		mu.Lock()
		cached, hit := cache[providerName]
		mu.Unlock()
		if hit {
			return cached, modelID, nil
		}
		prv, err := buildProviderFor(providerName, cfg, stateOpts)
		if err != nil {
			return nil, "", err
		}
		mu.Lock()
		// Double-check after racing constructors: keep whichever
		// won the lock; the loser's provider is discarded.
		if existing, ok := cache[providerName]; ok {
			mu.Unlock()
			return existing, modelID, nil
		}
		cache[providerName] = prv
		mu.Unlock()
		return prv, modelID, nil
	}
}

// buildProvider returns the resolved Provider, preferring a caller-supplied
// factory over the global provider registry.  modelOpts is the
// caller-merged options map (config + injected credentials); the
// adapter is opaque to its origin.
func buildProvider(factory func(opts map[string]any) (provider.Provider, error), cfg *config.Config, modelOpts map[string]any) (provider.Provider, error) {
	if factory != nil {
		prv, err := factory(modelOpts)
		if err != nil {
			return nil, fmt.Errorf("cli: build provider (injected): %w", err)
		}
		return prv, nil
	}
	f, err := provider.Get(cfg.Model.Provider)
	if err != nil {
		return nil, fmt.Errorf("cli: lookup provider %q: %w", cfg.Model.Provider, err)
	}
	prv, err := f(modelOpts)
	if err != nil {
		return nil, fmt.Errorf("cli: build provider %q: %w", cfg.Model.Provider, err)
	}
	return prv, nil
}

// resolveProviderOptionsFor composes the options map passed to a
// provider factory for the given providerName.  Order of precedence:
//
//  1. cfg.Model.Options as-is, but ONLY when providerName matches
//     cfg.Model.Provider.  Options scoped to the parent's provider
//     should not leak into an override provider with the same key.
//  2. environment variable (deferred to the adapter — we leave
//     opts["api_key"] absent so the adapter's own env fallback runs).
//  3. credential store entry of type CredAPIKey (injected as
//     opts["api_key"]).
//
// CredOAuth entries are skipped with a warning — the OAuth flow is
// scaffolded but not yet wired end-to-end.
func resolveProviderOptionsFor(providerName string, cfg *config.Config, stateOpts state.LoadOptions) (map[string]any, error) {
	merged := make(map[string]any, 1)
	// 1) Inherit cfg.Model.Options only when this is the parent's
	//    provider.  When a subagent override targets a different
	//    provider, its options come solely from credentials + the
	//    adapter's own defaults.
	if providerName == cfg.Model.Provider {
		for k, v := range cfg.Model.Options {
			merged[k] = v
		}
		if v, ok := merged["api_key"]; ok {
			if s, ok := v.(string); ok && s != "" {
				slog.Debug("cli: api key from config", "provider", providerName, "key", maskKey(s))
				return merged, nil
			}
		}
	}

	// 2) Environment variable: defer to the adapter.  If the canonical
	//    env var is set we deliberately do not inject from the store,
	//    so the env fallback chain in the adapter runs unchanged.
	if envName := providerEnvVar(providerName); envName != "" {
		if v, ok := os.LookupEnv(envName); ok && v != "" {
			slog.Debug("cli: api key from env", "provider", providerName, "var", envName, "key", maskKey(v))
			return merged, nil
		}
	}

	// 3) Auth store.  Load failures here are fatal — a corrupt
	//    auth.json should not be silently ignored.
	authOpts := auth.LoadOptions{
		HomeDir:      stateOpts.HomeDir,
		XDGStateHome: stateOpts.XDGStateHome,
	}
	store, err := auth.Load(authOpts)
	if err != nil {
		return nil, fmt.Errorf("cli: load auth store: %w", err)
	}
	cred, ok := store.Get(providerName)
	if !ok {
		return merged, nil
	}
	switch cred.Type {
	case auth.CredAPIKey:
		if cred.APIKey == "" {
			slog.Warn("cli: auth store entry has empty api_key; skipping",
				"provider", providerName)
			return merged, nil
		}
		merged["api_key"] = cred.APIKey
		slog.Debug("cli: api key from auth store", "provider", providerName, "key", maskKey(cred.APIKey))
	case auth.CredOAuth:
		slog.Warn("cli: auth store has OAuth credential but OAuth is not yet wired; falling back to adapter defaults",
			"provider", providerName)
	default:
		slog.Warn("cli: auth store entry has unknown credential type; skipping",
			"provider", providerName, "type", cred.Type)
	}
	return merged, nil
}

// buildProviderFor constructs a provider by name.  Used by the
// subagent ProviderResolver when a [subagent.Type] pins a provider
// other than the parent's.  The configured cfg + stateOpts are
// re-used for credential resolution so override providers inherit
// the same auth-store + env-var precedence as the parent.
//
// Returns provider.ErrUnknownProvider (wrapped) when no factory has
// been registered under name, or the factory's own error otherwise.
func buildProviderFor(providerName string, cfg *config.Config, stateOpts state.LoadOptions) (provider.Provider, error) {
	if providerName == "" {
		return nil, fmt.Errorf("cli: buildProviderFor: empty provider name")
	}
	opts, err := resolveProviderOptionsFor(providerName, cfg, stateOpts)
	if err != nil {
		return nil, err
	}
	factory, err := provider.Get(providerName)
	if err != nil {
		return nil, fmt.Errorf("cli: lookup provider %q: %w", providerName, err)
	}
	prv, err := factory(opts)
	if err != nil {
		return nil, fmt.Errorf("cli: build provider %q: %w", providerName, err)
	}
	return prv, nil
}

// providerEnvVar returns the canonical environment variable name a
// provider's adapter reads its API key from, or "" if the provider is
// unknown.  The list mirrors the providers we expect to support in
// v0.1 / v0.2; new entries must be added here when a new adapter
// lands.  Hard-coded so the CLI never reads from a surprising
// variable.
func providerEnvVar(name string) string {
	switch name {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "deepseek":
		return "DEEPSEEK_API_KEY"
	case "google", "gemini":
		return "GOOGLE_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	default:
		return ""
	}
}

// knownProviders returns the providers providerEnvVar recognises.
// Used by the `hygge provider auth` picker to enumerate known names
// without duplicating the list.
func knownProviders() []string {
	return []string{
		"anthropic",
		"openai",
		"openrouter",
		"mistral",
		"groq",
		"deepseek",
		"google",
		"xai",
	}
}

// maskKey redacts an API key for logs and printf-style output.
// Strings longer than 8 chars become "<first-3>***<last-4>"; shorter
// strings collapse to "***".  Never returns the raw value.
func maskKey(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:3] + "***" + s[len(s)-4:]
}

// lookupContextWindow asks the provider for its model list and finds the
// configured model's window size.  Returns 0 when the provider cannot
// answer (offline, transient error) — the agent treats 0 as "unknown" and
// skips PctUsed math.
func lookupContextWindow(ctx context.Context, prv provider.Provider, modelName string) int64 {
	if prv == nil {
		return 0
	}
	models, err := prv.ListModels(ctx)
	if err != nil {
		slog.Warn("cli: ListModels failed; context window will be 0", "err", err)
		return 0
	}
	for _, m := range models {
		if m.Name == modelName {
			return m.ContextWindow
		}
	}
	return 0
}

// envLookupWithXDG returns an env-lookup function that overrides the XDG
// vars to the values we resolved (so config.Load sees our hermetic paths
// in tests) and falls back to the real os environment for everything
// else.
func envLookupWithXDG(homeDir, xdgConfig, xdgState string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		switch key {
		case "XDG_CONFIG_HOME":
			return xdgConfig, true
		case "XDG_STATE_HOME":
			return xdgState, true
		case "HOME":
			if homeDir != "" {
				return homeDir, true
			}
		}
		return os.LookupEnv(key)
	}
}

// toolNamesFromRegistry returns the names of every currently
// registered tool, sorted.  Used to seed the sub-agent registry's
// DefaultTools list at bootstrap so the built-in "general" type
// inherits the orchestrator's toolbox (minus `task`, which is added
// later to the same registry but excluded by the runtime's recursion
// guard).
func toolNamesFromRegistry(r *tool.Registry) []string {
	if r == nil {
		return nil
	}
	all := r.All()
	names := make([]string, 0, len(all))
	for _, t := range all {
		names = append(names, t.Name())
	}
	return names
}

// stubProviderFactory builds a provider that satisfies the interface
// without performing any I/O.  It's used by inspection commands
// (`hygge config explain`, `hygge theme show`, `hygge sessions list`)
// where the agent / provider would otherwise demand an API key just to
// print local state.
func stubProviderFactory(_ map[string]any) (provider.Provider, error) {
	return stubProvider{}, nil
}

// stubProvider is a no-network provider used by introspection commands.
type stubProvider struct{}

func (stubProvider) Name() string { return "stub" }
func (stubProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	close(ch)
	return ch, nil
}
func (stubProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) { return 0, nil }
func (stubProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}

// shortID returns the leading 8 chars of id, or id itself if shorter.
// Used by table-style output where the full 26-char ULID would dominate.
func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// abbreviatePath replaces a leading $HOME with "~" and truncates the
// middle of long paths so tables stay readable.
func abbreviatePath(p, home string) string {
	if home != "" && strings.HasPrefix(p, home) {
		p = "~" + strings.TrimPrefix(p, home)
	}
	const maxLen = 40
	if len(p) <= maxLen {
		return p
	}
	// Keep the head and tail with an ellipsis in between.
	const head = 14
	const tail = 23
	return p[:head] + "…" + p[len(p)-tail:]
}

// findSessionByPrefix looks up a session whose id begins with prefix
// (case-insensitive).  Returns an error when no candidate is found.
// Newest match wins implicitly: ListSessions returns rows newest-first,
// and the first match in that order is returned.
func findSessionByPrefix(ctx context.Context, rt *appRuntime, prefix string, includeDeleted bool) (string, error) {
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if prefix == "" {
		return "", fmt.Errorf("cli: empty session prefix")
	}
	sessions, err := rt.Store.ListSessions(ctx, session.ListOpts{
		IncludeDeleted: includeDeleted,
		Limit:          200,
	})
	if err != nil {
		return "", fmt.Errorf("cli: list sessions: %w", err)
	}
	for _, s := range sessions {
		if strings.HasPrefix(strings.ToLower(s.ID), prefix) {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("no session matches %q", prefix)
}
