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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/agent"
	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/llm"
	"github.com/cfbender/hygge/internal/mcp"
	"github.com/cfbender/hygge/internal/memory"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/plugin"
	"github.com/cfbender/hygge/internal/provider"
	openrouterShim "github.com/cfbender/hygge/internal/provider/openrouter"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/skill"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/subagent"
	"github.com/cfbender/hygge/internal/tool"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// runtime is the wired graph of every component the CLI needs.  Returned
// from bootstrap.  Callers must defer Close to release the SQLite handle
// and unblock the agent's per-session locks.
type appRuntime struct {
	Config          *config.Config
	Provenance      config.Provenance
	State           *state.State
	StateOpts       state.LoadOptions
	XDGConfigHome   string
	Bus             *bus.Bus
	Store           *store.Store
	Provider        provider.Provider
	ProviderFactory func(opts map[string]any) (provider.Provider, error)
	Permission      *permission.Engine
	Tools           *tool.Registry
	Catalog         *cost.Catalog
	Agent           *agent.Agent
	MemoryStore     *memory.FileStore
	Theme           *styles.Styles
	Skills          *skill.Registry
	Subagents       *subagent.Registry
	SubagentRunner  *subagent.Runner
	Commands        *command.Registry
	Hooks           *hook.Registry
	AgentsBlocks    []agentsmd.Block
	MCPClients      []*mcp.Client
	MCPConfigs      []mcp.ServerConfig
	MCPStatuses     []MCPServerStatus
	logCloser       func()
	mcpAuthOpts     mcp.AuthLoadOptions
	mcpNow          func() time.Time
	mcpMu           sync.Mutex
	mcpWG           sync.WaitGroup
	mcpCancel       context.CancelFunc
	mcpAsyncConfigs []mcp.ServerConfig
	mcpAsyncStarted bool
	// BaseSystemPrompt is the stable prompt assembled from the default/override,
	// skills, and project context. Mode prompts are composed with it at runtime so
	// switching modes replaces the mode-specific tail instead of accumulating it.
	BaseSystemPrompt string
	SystemPrompt     string
	Pwd              string
	// Plugins is the plugin registry (nil only when registry construction fails).
	Plugins *plugin.Registry
	// PluginPM is the package manager used by the plugins registry.
	// Exposed for CLI commands that need to inspect cache directories.
	PluginPM *plugin.PackageManager
	// DryRun is true when this runtime was bootstrapped without loading
	// user/project config or provider auth.
	DryRun bool
	// catalogSrc is the raw catalog.Catalog closed by Close to stop
	// any periodic-refresh ticker goroutine.
	catalogSrc *catalog.Catalog
	// providerBuildOpts carries per-provider Fantasy construction options that
	// must be reused by one-shot model builds, such as OpenRouter session headers.
	providerBuildOpts llm.ProviderBuildOptions
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
	// CatalogBaseURL overrides the Catwalk catalog host.
	// Tests point this at an httptest server (typically a 500-returning
	// stub) so no live network call is ever made.  Test-only.
	CatalogBaseURL string
	// ReasoningOverride, when non-empty, replaces the config-supplied
	// model.reasoning value for the lifetime of this runtime.  Values
	// outside the allowed set ("off"/"low"/"medium"/"high") are
	// silently ignored — bootstrap warns and falls back to the
	// config value.  Populated from the --reasoning CLI flag.
	ReasoningOverride string
	// AsyncMCP loads configured MCP servers after the TUI has started. This is
	// used by interactive commands so slow external MCP processes never delay the
	// first UI frame; inspection commands leave it false for synchronous status.
	AsyncMCP bool
	// Yolo bypasses configurable permission prompts/default denies while keeping
	// hard-coded secrets denied.
	Yolo bool
	// DryRun ignores user/project config files, HYGGE_* config env vars,
	// provider env vars, and auth.json during bootstrap. It is intended for
	// onboarding/manual testing and still permits saving new onboarding config.
	DryRun bool
	// FantasyModel injects a no-network language model for bootstrap tests. When
	// nil, production resolves the configured provider/model through Fantasy.
	FantasyModel fantasy.LanguageModel
}

// Close releases resources held by the runtime.  Idempotent — safe to
// defer in a command body even if construction failed mid-way.
func (r *appRuntime) Close() error {
	if r == nil {
		return nil
	}
	var firstErr error
	if r.Plugins != nil {
		if err := r.Plugins.Close(context.Background()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.Agent != nil {
		if err := r.Agent.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.mcpCancel != nil {
		r.mcpCancel()
	}
	r.mcpWG.Wait()
	r.mcpMu.Lock()
	mcpClients := append([]*mcp.Client(nil), r.MCPClients...)
	r.mcpMu.Unlock()
	for _, c := range mcpClients {
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
	// Stop the catalog periodic-refresh ticker (no-op when interval was 0).
	if r.catalogSrc != nil {
		if err := r.catalogSrc.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if r.logCloser != nil {
		r.logCloser()
		r.logCloser = nil
	}
	return firstErr
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
	bootstrapStart := time.Now()
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
	logCloser := setupTUILog(stateOpts)
	defer func() {
		if err != nil && logCloser != nil {
			logCloser()
		}
	}()

	// Phase: config load
	t0 := time.Now()
	// Load the config.  This consults state.json for the active profile
	// when opts.ProfileName is empty, so it must run before we Load the
	// state ourselves.
	cfgEnv := envLookupWithXDG(opts.HomeDir, xdgConfig, xdgState)
	cfg, prov, err := config.Load(ctx, config.LoadOptions{
		Pwd:                   opts.Pwd,
		Profile:               opts.ProfileName,
		HomeDir:               opts.HomeDir,
		EnvLookup:             cfgEnv,
		IgnoreExternalSources: opts.DryRun,
	})
	if err != nil {
		return nil, fmt.Errorf("cli: load config: %w", err)
	}
	slog.Debug("bootstrap phase", "phase", "config_load", "elapsed_ms", time.Since(t0).Milliseconds())

	// Phase: state load
	t0 = time.Now()
	st, err := state.Load(stateOpts)
	if err != nil {
		return nil, fmt.Errorf("cli: load state: %w", err)
	}
	slog.Debug("bootstrap phase", "phase", "state_load", "elapsed_ms", time.Since(t0).Milliseconds())

	b := bus.New()

	// Phase: store open + migrations
	t0 = time.Now()
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
	slog.Debug("bootstrap phase", "phase", "store_open", "elapsed_ms", time.Since(t0).Milliseconds())

	// Construct the OpenRouter session-ID cache.  The cache is passed to
	// ResolveProviderModelWith so the OpenRouter HTTP transport injects
	// x-session-id on every chat request.  Resolving the root session ID
	// requires a store look-up, so the cache is built here where stOpen is
	// available and threaded into every fantasy provider construction that
	// may target OpenRouter (main model, title model, subagent model
	// resolver).  For non-OpenRouter providers the cache is unused.
	orBuildOpts := llm.ProviderBuildOptions{
		OpenRouterSessionCache: openrouterShim.NewRootIDCache(func(ctx context.Context, sessionID string) (string, error) {
			return session.ResolveRootSessionID(ctx, stOpen, sessionID)
		}),
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
	activeProvider, activeModelName := activeModel(cfg)
	modelOpts, err := resolveProviderOptionsForWithAuth(activeProvider, cfg, stateOpts, !opts.DryRun)
	if err != nil {
		_ = stOpen.Close()
		b.Close()
		return nil, err
	}
	var prv provider.Provider
	if opts.DryRun {
		prv = stubProvider{}
	} else {
		prv, err = buildProvider(opts.ProviderFactory, activeProvider, modelOpts)
		if err != nil {
			// Missing credentials must not block CLI inspection commands.
			// The TUI entrypoint opens onboarding when no provider auth exists;
			// every other CLI command tolerates a no-op stub. Non-auth failures
			// still surface.
			if errors.Is(err, provider.ErrAuth) {
				slog.Debug("cli: configured provider has no credential; using stub", "provider", activeProvider, "err", err)
				prv = stubProvider{}
			} else {
				_ = stOpen.Close()
				b.Close()
				return nil, err
			}
		}
	}
	permEngine, err := permission.New(permission.EngineOptions{
		Bus:        b,
		Config:     cfg,
		State:      stateOpts,
		ProjectDir: opts.Pwd,
		Clock:      opts.Now,
		Yolo:       opts.Yolo,
	})
	if err != nil {
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build permission engine: %w", err)
	}

	// Phase: catalog load
	t0 = time.Now()
	catSrc := buildCatalog(xdgState, opts, cfg)
	// Wire the shared catalog into each provider package so their
	// Models() lists come from the live snapshot rather than the
	// hardcoded fallbacks.  Passing nil is fine (tests sometimes
	// deliberately do this); the provider shims tolerate it.
	openrouterShim.SetCatalog(catSrc)
	var fantasyResolved llm.ProviderResolution
	if opts.FantasyModel != nil {
		fantasyResolved.Model = opts.FantasyModel
	} else if opts.DryRun || activeProvider == "" {
		// No provider to resolve: DryRun ignores external config, or no
		// [[modes]] have been configured yet (onboarding needed).
		fantasyResolved = llm.ProviderResolution{}
	} else {
		fantasyResolved, err = llm.ResolveProviderModelWith(ctx, activeProvider, activeModelName, modelOpts, catSrc, orBuildOpts)
		if err != nil {
			// Missing credentials are non-fatal at bootstrap so CLI
			// inspection commands work without an API key.  The TUI
			// entrypoint enforces the auth requirement before
			// reaching a state where the model is actually used.
			if errors.Is(err, provider.ErrAuth) {
				slog.Debug("cli: skipping fantasy model resolution; no credential", "provider", activeProvider, "err", err)
				fantasyResolved = llm.ProviderResolution{}
			} else {
				permEngine.Close()
				_ = stOpen.Close()
				b.Close()
				return nil, fmt.Errorf("cli: build fantasy model: %w", err)
			}
		}
	}
	var titleFantasyModel fantasy.LanguageModel
	if opts.FantasyModel == nil && !opts.DryRun && cfg.Model.SmallModel != "" {
		smallProvider := cfg.Model.SmallProvider
		if smallProvider == "" {
			smallProvider = activeProvider
		}
		resolved, err := llm.ResolveProviderModelWith(ctx, smallProvider, cfg.Model.SmallModel, modelOpts, catSrc, orBuildOpts)
		if err != nil {
			slog.Warn("cli: failed to resolve small title model; using active model", "provider", smallProvider, "model", cfg.Model.SmallModel, "err", err)
		} else {
			titleFantasyModel = resolved.Model
		}
	}

	catalog := cost.NewCatalog(cost.CatalogOptions{
		Catalog: catSrc,
		Now:     opts.Now,
	})
	slog.Debug("bootstrap phase", "phase", "catalog_load", "elapsed_ms", time.Since(t0).Milliseconds())

	thm, err := styles.Load(cfg.Theme.Name, styles.LoadOptions{
		ConfigHome: xdgConfig,
		HomeDir:    opts.HomeDir,
	})
	if err != nil {
		// A missing or malformed user theme should never block the CLI
		// — fall back to styles.DefaultTheme() (Claret) and warn.
		slog.Warn("cli: theme load failed; falling back to styles.DefaultTheme()",
			"name", cfg.Theme.Name, "err", err)
		thm = styles.DefaultTheme()
	}

	// Context window is sourced from the Catwalk catalog via the fantasy
	// resolution metadata.  For non-fantasy paths (DryRun, stubProvider,
	// test injections) the value is 0 — the agent treats 0 as "unknown"
	// and skips PctUsed math.
	contextWindow := fantasyResolved.Metadata.ContextWindow
	memoryStore := memory.NewFileStore(memory.FileStoreOptions{ProjectDir: opts.Pwd, HomeDir: opts.HomeDir, XDGConfigHome: xdgConfig, Now: opts.Now})

	// Phase: skills load
	t0 = time.Now()
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
	slog.Debug("bootstrap phase", "phase", "skills_load", "elapsed_ms", time.Since(t0).Milliseconds())

	// Phase: AGENTS.md / subagents load
	t0 = time.Now()
	agentsBlocks, err := agentsmd.Load(agentsmd.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
	})
	if err != nil {
		slog.Warn("cli: failed to load AGENTS.md", "err", err)
		agentsBlocks = nil
	}
	slog.Debug("bootstrap phase", "phase", "agentsmd_load", "elapsed_ms", time.Since(t0).Milliseconds())

	// Construct the lazy per-tool-call subdir context tracker.  The
	// tracker is seeded with every directory we just loaded a block
	// from so the same file is never re-injected.  Failures here
	// only degrade lazy loading; the agent still runs without it.
	lazyTracker := buildLazyTracker(opts.HomeDir, opts.Pwd, agentsBlocks)

	// Phase: tool registry
	t0 = time.Now()
	// Build the tool registry now that the skill registry is in hand
	// so the skill tool is registered when (and only when) skills are
	// configured.
	tools := tool.DefaultWith(tool.DefaultOptions{SkillRegistry: skillReg, TodoStore: stOpen, SessionMemoryStore: stOpen, FileMemoryStore: memoryStore})
	slog.Debug("bootstrap phase", "phase", "tool_registry", "elapsed_ms", time.Since(t0).Milliseconds())

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
		Config:        cfg,
	})
	if err != nil {
		slog.Warn("cli: failed to load subagents.toml; using built-in types only", "err", err)
		subagentReg, _ = subagent.Load(subagent.LoadOptions{DefaultTools: defaultSubagentTools})
	}

	subRunner, err := subagent.NewRunner(subagent.RunnerOptions{
		Bus:                  b,
		Store:                stOpen,
		Provider:             prv,
		Permission:           permEngine,
		Catalog:              catalog,
		Registry:             subagentReg,
		ParentTools:          tools,
		Pwd:                  opts.Pwd,
		ContextWindow:        contextWindow,
		ProviderResolver:     buildProviderResolver(cfg, stateOpts, prv),
		FantasyModelResolver: buildFantasyModelResolver(cfg, stateOpts, catSrc, fantasyResolved.Model, orBuildOpts),
		Now:                  opts.Now,
	})
	if err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build subagent runner: %w", err)
	}
	if err := tools.Register(tool.NewSubagentTool(subRunner.ToolAdapter())); err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: register subagent tool: %w", err)
	}

	// compact tool: registered here (after subagent, before agent construction)
	// using a compactorAdapter that will be wired to the agent once it is built.
	// We use a level of indirection via a pointer-to-pointer so the closure
	// captures the real *agent.Agent after New() returns.
	compactAdapter := &lazyCompactorAdapter{}
	if err := tools.Register(tool.NewCompactTool(compactAdapter)); err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: register compact tool: %w", err)
	}

	// Phase: plugin host init + plugin load
	t0 = time.Now()
	// MCP servers contribute additional tools.  Interactive TUI startup prepares
	// sidebar rows now and defers network/process initialization until after the
	// first frame can render; non-interactive inspection commands keep the old
	// synchronous behaviour so their status output is complete.
	var mcpClients []*mcp.Client
	var mcpConfigs []mcp.ServerConfig
	var mcpStatuses []MCPServerStatus
	if opts.AsyncMCP {
		mcpConfigs, mcpStatuses = prepareAsyncMCP(opts, xdgConfig)
	} else {
		mcpClients, mcpConfigs, mcpStatuses = bootstrapMCP(ctx, opts, xdgConfig, tools)
	}
	slog.Debug("bootstrap phase", "phase", "mcp_load", "elapsed_ms", time.Since(t0).Milliseconds())

	// Slash-command registry: built-in command set plus any
	// TOML-declared prompt templates.  Failures degrade to the
	// built-in set; the TUI is still usable without user
	// commands.toml.
	command.SetVersion(Version)
	cmdReg, err := command.Load(command.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
	})
	if err != nil {
		slog.Warn("cli: failed to load commands.toml; built-ins only", "err", err)
		cmdReg, _ = command.Load(command.LoadOptions{})
	}

	baseSysPrompt := opts.SystemPrompt
	if baseSysPrompt == "" {
		baseSysPrompt = defaultSystemPrompt
	}
	if extras := skill.BuildSystemPromptAdditions(skillReg); extras != "" {
		baseSysPrompt += "\n\n" + extras
	}
	if extras := agentsmd.BuildSystemPromptAdditions(agentsBlocks); extras != "" {
		baseSysPrompt += "\n\n" + extras
	}
	sysPrompt := composeModeSystemPrompt(baseSysPrompt, activeModePrompt(cfg, xdgConfig, 0))

	// Hooks: load from the standard four-layer paths.  Failures here
	// are non-fatal — no hooks is a valid (and safe) configuration.
	hookReg, err := hook.Load(hook.LoadOptions{
		HomeDir:       opts.HomeDir,
		XDGConfigHome: xdgConfig,
		Pwd:           opts.Pwd,
	})
	if err != nil {
		slog.Warn("cli: failed to load hooks.toml; hooks disabled for this run", "err", err)
		hookReg = hook.New()
	}

	// Phase: plugin load
	t0 = time.Now()
	// Plugin registry: always create the host so `hygge plugins install` can
	// load a first plugin before any [plugins].sources entry exists. Load all
	// configured plugins when present; failures are non-fatal per-plugin
	// (LoadAll skips bad ones with a warn).
	var pluginReg *plugin.Registry
	var pluginPM *plugin.PackageManager
	pluginCacheDir := filepath.Join(xdgState, "hygge", "plugins")
	pluginReg, err = plugin.NewRegistry(plugin.RegistryOptions{
		CacheDir:      pluginCacheDir,
		Tools:         tools,
		Hooks:         hookReg,
		Commands:      cmdReg,
		Subagents:     subagentReg,
		Permission:    permEngine,
		PluginConfigs: cfg.RawPluginSettings(),
		ProfileDir:    cfg.ProfileDir,
		Pwd:           opts.Pwd,
	})
	if err != nil {
		slog.Warn("cli: failed to create plugin registry; plugins disabled for this run", "err", err)
	} else {
		pluginPM = pluginReg.PM()
		pluginSources := pluginSourcesWithAutoload(cfg.Plugins.Sources, xdgConfig, opts.Pwd)
		if len(pluginSources) > 0 {
			pluginReg.LoadAll(context.Background(), pluginSources)
		}
	}
	slog.Debug("bootstrap phase", "phase", "plugin_load", "elapsed_ms", time.Since(t0).Milliseconds())

	// Phase: bus init / agent loop wiring
	t0 = time.Now()
	ag, err := agent.New(agent.Options{
		Bus:                    b,
		Store:                  stOpen,
		Provider:               prv,
		FantasyModel:           fantasyResolved.Model,
		TitleFantasyModel:      titleFantasyModel,
		Permission:             permEngine,
		Tools:                  tools,
		Catalog:                catalog,
		Pwd:                    opts.Pwd,
		ContextWindow:          contextWindow,
		CompactionThresholdPct: cfg.Compaction.ThresholdPct,
		SystemPrompt:           sysPrompt,
		Now:                    opts.Now,
		LazyContext:            lazyTracker,
		MemoryLoader:           memoryStore,
		Reasoning:              resolveReasoning(cfg, opts.ReasoningOverride),
		Hooks:                  hookReg,
		// TurnContextDecorator injects the current session ID into the
		// request context so the OpenRouter HTTP transport can read it and
		// set x-session-id on every chat request.  The decorator is always
		// wired; it is a no-op for non-OpenRouter providers because their
		// transports do not consult the context value.
		TurnContextDecorator: openrouterShim.ContextWithSessionID,
	})
	if err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build agent: %w", err)
	}
	// Wire the compact tool's adapter to the real agent now that it exists.
	compactAdapter.set(ag)
	slog.Debug("bootstrap phase", "phase", "agent_init", "elapsed_ms", time.Since(t0).Milliseconds())

	// Wire InjectMessage into the plugin registry now that the agent
	// is available.  The late-bind avoids a circular dependency between
	// plugin.Registry and agent.Agent.
	if pluginReg != nil {
		pluginReg.SetInjectMessage(func(ctx context.Context, sessionID, role, content string) error {
			return ag.InjectMessage(ctx, "plugin", sessionID, role, content)
		})
	}

	slog.Debug("bootstrap complete", "elapsed_ms", time.Since(bootstrapStart).Milliseconds())

	rt = &appRuntime{
		Config:            cfg,
		Provenance:        prov,
		State:             st,
		StateOpts:         stateOpts,
		XDGConfigHome:     xdgConfig,
		Bus:               b,
		Store:             stOpen,
		Provider:          prv,
		ProviderFactory:   opts.ProviderFactory,
		Permission:        permEngine,
		Tools:             tools,
		Catalog:           catalog,
		Agent:             ag,
		MemoryStore:       memoryStore,
		Theme:             thm,
		Skills:            skillReg,
		Subagents:         subagentReg,
		SubagentRunner:    subRunner,
		Commands:          cmdReg,
		Hooks:             hookReg,
		AgentsBlocks:      agentsBlocks,
		MCPClients:        mcpClients,
		MCPConfigs:        mcpConfigs,
		MCPStatuses:       mcpStatuses,
		mcpAsyncConfigs:   mcpConfigs,
		mcpAuthOpts:       mcpAuthLoadOptionsFromBootstrap(opts),
		mcpNow:            opts.Now,
		BaseSystemPrompt:  baseSysPrompt,
		SystemPrompt:      sysPrompt,
		Pwd:               opts.Pwd,
		Plugins:           pluginReg,
		PluginPM:          pluginPM,
		DryRun:            opts.DryRun,
		catalogSrc:        catSrc,
		providerBuildOpts: orBuildOpts,
		logCloser:         logCloser,
	}
	return rt, nil
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

// findResumableSession returns the most recent non-deleted primary session
// for resumption.  When projectDir is non-empty, the search is scoped to
// that directory.  When allowAny is true, the project filter is ignored.
// Returns ("", nil) when no eligible session exists.
func findResumableSession(ctx context.Context, rt *appRuntime, projectDir string, allowAny bool) (string, error) {
	opts := session.ListOpts{
		Kind:  session.KindPrimary,
		Limit: 1,
	}
	if !allowAny && projectDir != "" {
		opts.ProjectDir = projectDir
	}
	sessions, err := rt.Store.ListSessions(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("cli: list sessions: %w", err)
	}
	if len(sessions) == 0 {
		return "", nil
	}
	return sessions[0].ID, nil
}

// lowerTrim returns s lowercased and with surrounding whitespace removed.
func lowerTrim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// hasLowerPrefix reports whether strings.ToLower(id) starts with lower.
func hasLowerPrefix(id, lower string) bool {
	return strings.HasPrefix(strings.ToLower(id), lower)
}

// buildCatalog constructs the shared [*catalog.Catalog] used by both
// the cost lookups and every provider's model list.
//
// The catalog is loaded with background refresh enabled in production
// (so a stale disk snapshot self-heals on the next run) but disabled
// in tests (so output is deterministic).  Tests can also redirect the
// catalog at an httptest server via [bootstrapOptions.CatalogBaseURL].
//
// Returns nil only when [catalog.Load] catastrophically fails — which
// in practice means the binary's embedded snapshot is malformed.  Each
// provider shim already tolerates nil.
func buildCatalog(xdgState string, opts bootstrapOptions, cfg *config.Config) *catalog.Catalog {
	stateDir := filepath.Join(xdgState, "hygge")
	// Tests sometimes provide a Now func; honor it so disk-snapshot
	// freshness checks are deterministic.
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	// Tests point CatalogBaseURL at an httptest server.  We also
	// disable the background refresh in that case so the test
	// surface stays deterministic.
	bg := opts.CatalogBaseURL == ""

	var refreshInterval time.Duration
	if cfg != nil {
		refreshInterval = cfg.Catalog.RefreshIntervalDuration()
	}

	loadOpts := catalog.LoadOptions{
		StateDir:          stateDir,
		BaseURL:           opts.CatalogBaseURL,
		Now:               now,
		BackgroundRefresh: &bg,
		RefreshInterval:   refreshInterval,
	}
	c, err := catalog.Load(loadOpts)
	if err != nil {
		slog.Warn("cli: catalog.Load failed; model lists will fall back to hardcoded defaults",
			"err", err)
		return nil
	}
	return c
}

// lazyCompactorAdapter is a thread-safe shim that satisfies [tool.Compactor]
// and bridges the bootstrap chicken-and-egg: the compact tool must be
// registered before the agent is constructed (so the agent inherits the full
// tool set), but the agent does not yet exist at that point.
//
// set() is called once, immediately after agent.New() returns, to wire the
// concrete *agent.Agent.  All Compact calls that arrive before set() return
// an error (which should never happen in practice since the agent is set
// before any turn begins).
type lazyCompactorAdapter struct {
	mu sync.RWMutex
	ag *agent.Agent
}

func (a *lazyCompactorAdapter) set(ag *agent.Agent) {
	a.mu.Lock()
	a.ag = ag
	a.mu.Unlock()
}

// Compact implements [tool.Compactor].  It drops the returned *session.Marker
// (the tool layer does not surface it) and adapts agent.Agent.Compact's
// signature to the narrow interface the compact tool needs.
func (a *lazyCompactorAdapter) Compact(ctx context.Context, sessionID string) error {
	a.mu.RLock()
	ag := a.ag
	a.mu.RUnlock()
	if ag == nil {
		return fmt.Errorf("compact: agent not yet wired")
	}
	_, err := ag.Compact(ctx, sessionID)
	if errors.Is(err, agent.ErrNothingToCompact) {
		return compactNothingToCompactError{err: err}
	}
	return err
}

type compactNothingToCompactError struct {
	err error
}

func (e compactNothingToCompactError) Error() string { return e.err.Error() }
func (e compactNothingToCompactError) Unwrap() error { return e.err }
func (e compactNothingToCompactError) IsNothingToCompact() bool {
	return true
}
