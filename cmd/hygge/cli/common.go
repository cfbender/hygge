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
	"time"

	"github.com/cfbender/hygge/internal/agent"
	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	_ "github.com/cfbender/hygge/internal/provider/anthropic" // self-register
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/tool"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// runtime is the wired graph of every component the CLI needs.  Returned
// from bootstrap.  Callers must defer Close to release the SQLite handle
// and unblock the agent's per-session locks.
type appRuntime struct {
	Config     *config.Config
	Provenance config.Provenance
	State      *state.State
	StateOpts  state.LoadOptions
	Bus        *bus.Bus
	Store      *store.Store
	Provider   provider.Provider
	Permission *permission.Engine
	Tools      *tool.Registry
	Catalog    *cost.Catalog
	Agent      *agent.Agent
	Theme      *theme.Theme
	Pwd        string
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
	modelOpts, err := resolveProviderOptions(cfg, stateOpts)
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

	tools := tool.Default()
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

	sysPrompt := opts.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = defaultSystemPrompt
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
	})
	if err != nil {
		permEngine.Close()
		_ = stOpen.Close()
		b.Close()
		return nil, fmt.Errorf("cli: build agent: %w", err)
	}

	return &appRuntime{
		Config:     cfg,
		Provenance: prov,
		State:      st,
		StateOpts:  stateOpts,
		Bus:        b,
		Store:      stOpen,
		Provider:   prv,
		Permission: permEngine,
		Tools:      tools,
		Catalog:    catalog,
		Agent:      ag,
		Theme:      thm,
		Pwd:        opts.Pwd,
	}, nil
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

// resolveProviderOptions composes the options map passed to the
// provider factory.  Order of precedence:
//
//  1. config model.options as-is (an explicit api_key wins).
//  2. environment variable (deferred to the adapter — we leave
//     opts["api_key"] absent so the adapter's own env fallback runs).
//  3. credential store entry of type CredAPIKey (injected as
//     opts["api_key"]).
//
// CredOAuth entries are skipped with a warning — the OAuth flow is
// scaffolded but not yet wired end-to-end.
func resolveProviderOptions(cfg *config.Config, stateOpts state.LoadOptions) (map[string]any, error) {
	// Start with a shallow copy of cfg.Model.Options so we never
	// mutate the loaded config struct.
	merged := make(map[string]any, len(cfg.Model.Options)+1)
	for k, v := range cfg.Model.Options {
		merged[k] = v
	}

	// 1) Explicit config override wins.
	if v, ok := merged["api_key"]; ok {
		if s, ok := v.(string); ok && s != "" {
			slog.Debug("cli: api key from config", "provider", cfg.Model.Provider, "key", maskKey(s))
			return merged, nil
		}
	}

	// 2) Environment variable: defer to the adapter.  If the canonical
	//    env var is set we deliberately do not inject from the store,
	//    so the env fallback chain in the adapter runs unchanged.
	if envName := providerEnvVar(cfg.Model.Provider); envName != "" {
		if v, ok := os.LookupEnv(envName); ok && v != "" {
			slog.Debug("cli: api key from env", "provider", cfg.Model.Provider, "var", envName, "key", maskKey(v))
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
	cred, ok := store.Get(cfg.Model.Provider)
	if !ok {
		return merged, nil
	}
	switch cred.Type {
	case auth.CredAPIKey:
		if cred.APIKey == "" {
			slog.Warn("cli: auth store entry has empty api_key; skipping",
				"provider", cfg.Model.Provider)
			return merged, nil
		}
		merged["api_key"] = cred.APIKey
		slog.Debug("cli: api key from auth store", "provider", cfg.Model.Provider, "key", maskKey(cred.APIKey))
	case auth.CredOAuth:
		slog.Warn("cli: auth store has OAuth credential but OAuth is not yet wired; falling back to adapter defaults",
			"provider", cfg.Model.Provider)
	default:
		slog.Warn("cli: auth store entry has unknown credential type; skipping",
			"provider", cfg.Model.Provider, "type", cred.Type)
	}
	return merged, nil
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
