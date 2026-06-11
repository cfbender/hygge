// Package cli — provider and model wiring: credential resolution,
// Fantasy model resolution, subagent provider/model resolvers, and the
// no-network stub providers.
package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/llm"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/subagent"
)

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
	if parent != nil && cfg != nil {
		if ap, _ := activeModel(cfg); ap != "" {
			cache[ap] = parent
		}
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

func buildFantasyModelResolver(cfg *config.Config, stateOpts state.LoadOptions, catSrc *catalog.Catalog, _ fantasy.LanguageModel, buildOpts llm.ProviderBuildOptions) subagent.FantasyModelResolver {
	return func(ctx context.Context, providerName, modelID string) (fantasy.LanguageModel, error) {
		opts, err := resolveProviderOptionsFor(providerName, cfg, stateOpts)
		if err != nil {
			return nil, err
		}
		resolved, err := llm.ResolveProviderModelWith(ctx, providerName, modelID, opts, catSrc, buildOpts)
		if err != nil {
			return nil, err
		}
		return resolved.Model, nil
	}
}

// buildNamedStubProvider returns a namedStub for any provider name.
// Fantasy/Catwalk handles all actual LLM calls; the CLI only needs to
// carry provider identity (Name()) and enforce auth semantics for
// providers with a known canonical env var.
//
// Auth rules:
//   - If providerEnvVar returns a non-empty env var, requireAnyKey is
//     called: ErrAuth is returned when neither opts["api_key"] nor the
//     env var is set.  This preserves the bootstrap auth-fallback path
//     (errors.Is(err, provider.ErrAuth) → stubProvider) for providers
//     whose canonical env var the CLI already knows.
//   - For providers with no known env var, no auth check is performed —
//     Fantasy/Catwalk performs validation at runtime during model
//     resolution.
func buildNamedStubProvider(name string, opts map[string]any) (provider.Provider, error) {
	if envVar := providerEnvVar(name); envVar != "" {
		if err := requireAnyKey(opts, envVar); err != nil {
			return nil, err
		}
	}
	return namedStub{name: name}, nil
}

// lookupOrStubProvider consults the global provider registry for name.
// When the provider is registered its factory is called with opts and
// the result is returned.  When the provider is unknown it falls back
// to buildNamedStubProvider so Fantasy/Catwalk can resolve it at
// runtime.  Any error other than ErrUnknownProvider is returned as-is.
func lookupOrStubProvider(name string, opts map[string]any) (provider.Provider, error) {
	f, err := provider.Get(name)
	if err == nil {
		prv, err := f(opts)
		if err != nil {
			return nil, fmt.Errorf("cli: build provider %q: %w", name, err)
		}
		return prv, nil
	}
	if !errors.Is(err, provider.ErrUnknownProvider) {
		return nil, fmt.Errorf("cli: lookup provider %q: %w", name, err)
	}
	return buildNamedStubProvider(name, opts)
}

// buildProvider returns the resolved Provider, preferring a caller-supplied
// factory over the global provider registry.  modelOpts is the
// caller-merged options map (config + injected credentials); the
// adapter is opaque to its origin.
//
// When no factory is injected, lookupOrStubProvider handles the registry
// lookup and namedStub fallback.
func buildProvider(factory func(opts map[string]any) (provider.Provider, error), providerName string, modelOpts map[string]any) (provider.Provider, error) {
	if factory != nil {
		prv, err := factory(modelOpts)
		if err != nil {
			return nil, fmt.Errorf("cli: build provider (injected): %w", err)
		}
		return prv, nil
	}
	prv, err := lookupOrStubProvider(providerName, modelOpts)
	if err != nil {
		return nil, fmt.Errorf("cli: build provider %q: %w", providerName, err)
	}
	return prv, nil
}

func buildProviderForName(providerName string, factory func(opts map[string]any) (provider.Provider, error), modelOpts map[string]any) (provider.Provider, error) {
	if factory != nil {
		prv, err := factory(modelOpts)
		if err != nil {
			return nil, fmt.Errorf("cli: build provider (injected): %w", err)
		}
		return prv, nil
	}
	prv, err := lookupOrStubProvider(providerName, modelOpts)
	if err != nil {
		return nil, fmt.Errorf("cli: build provider %q: %w", providerName, err)
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
	return resolveProviderOptionsForWithAuth(providerName, cfg, stateOpts, true)
}

func resolveProviderOptionsForWithAuth(providerName string, cfg *config.Config, stateOpts state.LoadOptions, allowAuth bool) (map[string]any, error) {
	merged := make(map[string]any, 1)
	// 1) Inherit cfg.Model.Options only when this is the parent's
	//    provider.  When a subagent override targets a different
	//    provider, its options come solely from credentials + the
	//    adapter's own defaults.
	if ap, _ := activeModel(cfg); providerName == ap {
		maps.Copy(merged, cfg.Model.Options)
		if v, ok := merged["api_key"]; ok {
			if s, ok := v.(string); ok && s != "" {
				slog.Debug("cli: api key from config", "provider", providerName, "key", maskKey(s))
				return merged, nil
			}
		}
	}

	if !allowAuth {
		return merged, nil
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
		if cred.AccessToken == "" && cred.RefreshToken == "" {
			slog.Warn("cli: auth store has OAuth credential with no tokens; skipping",
				"provider", providerName)
			return merged, nil
		}
		// Check if token needs refresh.
		if !cred.ExpiresAt.IsZero() && time.Now().After(cred.ExpiresAt) && cred.RefreshToken != "" {
			slog.Info("cli: OAuth token expired, refreshing", "provider", providerName)
			tokens, err := auth.RefreshAccessToken(context.Background(), cred.RefreshToken)
			if err != nil {
				slog.Warn("cli: OAuth token refresh failed; using expired token",
					"provider", providerName, "err", err)
			} else {
				cred.AccessToken = tokens.AccessToken
				if tokens.RefreshToken != "" {
					cred.RefreshToken = tokens.RefreshToken
				}
				cred.ExpiresAt = time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second)
				if id := auth.ExtractAccountID(tokens.AccessToken); id != "" {
					cred.AccountID = id
				}
				// Persist the refreshed token.
				authOpts := auth.LoadOptions{
					HomeDir:      stateOpts.HomeDir,
					XDGStateHome: stateOpts.XDGStateHome,
				}
				if err := auth.Set(providerName, cred, authOpts); err != nil {
					slog.Warn("cli: failed to persist refreshed OAuth token", "err", err)
				}
			}
		}
		merged["api_key"] = cred.AccessToken
		merged["oauth"] = true
		if cred.AccountID != "" {
			merged["account_id"] = cred.AccountID
		}
		slog.Debug("cli: OAuth token from auth store", "provider", providerName)
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
// lookupOrStubProvider handles the registry lookup and namedStub
// fallback for any provider not found in the global registry.
func buildProviderFor(providerName string, cfg *config.Config, stateOpts state.LoadOptions) (provider.Provider, error) {
	if providerName == "" {
		return nil, fmt.Errorf("cli: buildProviderFor: empty provider name")
	}
	opts, err := resolveProviderOptionsFor(providerName, cfg, stateOpts)
	if err != nil {
		return nil, err
	}
	prv, err := lookupOrStubProvider(providerName, opts)
	if err != nil {
		return nil, fmt.Errorf("cli: build provider %q: %w", providerName, err)
	}
	return prv, nil
}

// activeModel returns the provider and model name for the active (first)
// mode. [[modes]] is the canonical source for active provider/model;
// configs without modes have no active model and require onboarding.
func activeModel(cfg *config.Config) (provider, name string) {
	if cfg != nil && len(cfg.Modes) > 0 {
		return cfg.Modes[0].Provider, cfg.Modes[0].Model
	}
	return "", ""
}

// hasConfiguredModel reports whether [[modes]] in the config contain at
// least one entry that came from a real config source (not defaults).
// [[modes]] is the canonical source for provider/model since the
// compatibility layer that copied modes[0] into cfg.Model.Provider/Name
// was removed.  A config with no modes needs onboarding.
func hasConfiguredModel(cfg *config.Config, prov config.Provenance) bool {
	if cfg == nil {
		return false
	}
	if len(cfg.Modes) == 0 {
		return false
	}
	// The "modes" provenance key is set when [[modes]] comes from a real
	// config file.  Individual mode keys (modes.<name>.provider etc.) are
	// an alternative path used by some test helpers.
	if hasRealConfigSource(prov["modes"]) {
		return true
	}
	for _, mode := range cfg.Modes {
		prefix := "modes." + mode.Name
		if hasRealConfigSource(prov[prefix+".provider"]) && hasRealConfigSource(prov[prefix+".model"]) {
			return true
		}
	}
	return false
}

func hasRealConfigSource(sources []config.Source) bool {
	for _, src := range sources {
		switch src.File {
		case "", "<defaults>":
			continue
		default:
			return true
		}
	}
	return false
}

func hasAnyProviderAuth(stateOpts state.LoadOptions) bool {
	return len(authConfiguredProviders(stateOpts)) > 0
}

func authConfiguredProviders(stateOpts state.LoadOptions) []string {
	configured := make(map[string]bool)
	for _, name := range knownProviders() {
		if envName := providerEnvVar(name); envName != "" {
			if v, ok := os.LookupEnv(envName); ok && v != "" {
				configured[name] = true
			}
		}
	}
	store, err := auth.Load(auth.LoadOptions{
		HomeDir:      stateOpts.HomeDir,
		XDGStateHome: stateOpts.XDGStateHome,
	})
	if err != nil {
		slog.Debug("cli: authConfiguredProviders: auth.Load failed", "err", err)
		return sortedProviderNames(configured)
	}
	for _, name := range store.List() {
		if strings.TrimSpace(name) != "" {
			configured[name] = true
		}
	}
	return sortedProviderNames(configured)
}

func sortedProviderNames(providers map[string]bool) []string {
	if len(providers) == 0 {
		return nil
	}
	names := make([]string, 0, len(providers))
	for name := range providers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
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
	case "gemini":
		return "GOOGLE_API_KEY"
	case "xai":
		return "XAI_API_KEY"
	default:
		return ""
	}
}

// knownProviders returns the Catwalk-bundled provider ids used by provider
// pickers. It stays network-free so first-run onboarding works offline.
func knownProviders() []string {
	providers := catalog.EmbeddedProviders()
	if len(providers) > 0 {
		return providers
	}
	return []string{
		"anthropic",
		"openai",
		"openrouter",
		"mistral",
		"groq",
		"deepseek",
		"gemini",
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

// resolveReasoning composes a [provider.Reasoning] from the config's
// model.reasoning / model.reasoning_budget fields and any --reasoning
// CLI override.  Order of precedence: override > config.
//
// Override values outside the allowed set ("off" / "low" / "medium" /
// "high") are ignored with a warning; the config value (or off) wins.
// An override of "off" explicitly clears any reasoning configured at
// the config level — this is the documented way to disable reasoning
// for a single run.
func resolveReasoning(cfg *config.Config, override string) provider.Reasoning {
	effort := ""
	if cfg != nil {
		effort = cfg.Model.Reasoning
	}
	override = strings.ToLower(strings.TrimSpace(override))
	if override != "" {
		switch override {
		case "off", "low", "medium", "high":
			effort = override
		default:
			slog.Warn("cli: invalid --reasoning value, ignoring",
				"value", override)
		}
	}
	r := provider.Reasoning{Effort: effort}
	if cfg != nil {
		r.BudgetTokens = cfg.Model.ReasoningBudget
	}
	return r
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

// namedStub is a no-network provider.Provider that carries a provider
// name and satisfies the interface.  Fantasy/Catwalk uses the name for
// model resolution and performs all actual LLM calls; the stub is never
// expected to handle streaming itself at runtime.
type namedStub struct{ name string }

func (n namedStub) Name() string { return n.name }
func (n namedStub) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	return nil, fmt.Errorf("provider %q: direct Stream call on stub provider (Fantasy/Catwalk should handle this)", n.name)
}
func (n namedStub) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, fmt.Errorf("provider %q: direct CountTokens call on stub provider (Fantasy/Catwalk should handle this)", n.name)
}
func (n namedStub) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, fmt.Errorf("provider %q: direct ListModels call on stub provider (Fantasy/Catwalk should handle this)", n.name)
}

// requireAnyKey returns provider.ErrAuth (wrapped) when neither
// opts["api_key"] nor the canonical environment variable envVar contains
// a non-empty credential.  It is intentionally lenient: any non-empty
// value satisfies the check because the caller (Fantasy) validates the
// key's correctness when it actually makes the request.
func requireAnyKey(opts map[string]any, envVar string) error {
	if v, ok := opts["api_key"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return nil
		}
	}
	if os.Getenv(envVar) != "" {
		return nil
	}
	return fmt.Errorf("%w: no credential found; set %s or run `hygge provider auth`", provider.ErrAuth, envVar)
}
