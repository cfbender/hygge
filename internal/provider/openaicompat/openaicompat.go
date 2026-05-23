// Package openaicompat implements the provider.Provider interface against
// any HTTP API that speaks OpenAI's Chat Completions wire protocol.
//
// # One adapter, many providers
//
// OpenAI's Chat Completions API is the de-facto standard for non-Anthropic
// LLM providers.  OpenRouter, Groq, Mistral, DeepSeek, Together, xAI,
// Cerebras and others implement it bit-for-bit.  The differences are
// confined to:
//
//  1. Base URL.
//  2. Auth header (almost always Authorization: Bearer ...).
//  3. Static model catalog.
//  4. Optional extra headers (OpenRouter wants HTTP-Referer and X-Title).
//  5. Minor body quirks (some providers reject stream_options, some want
//     vendor-prefixed model names).
//
// This package provides one shared adapter parameterised by those knobs.
// Per-provider shim packages (internal/provider/openrouter, ...) configure
// the adapter and register themselves with the provider registry.  The
// internal/provider/openai shim was removed; OpenAI streaming is now
// handled by Fantasy (charm.land/fantasy).  Adding a new shim is intended to
// be ~30 lines.
//
// # Layering
//
// The shared adapter NEVER does environment lookup or op:// resolution.
// API key resolution is the shim's responsibility — the shim hands a
// literal APIKey string to Config.  This keeps the adapter pure and lets
// each shim define its own precedence chain (e.g. shim_API_KEY vs
// OPENAI_API_KEY).
//
// # Streaming
//
// /chat/completions is invoked with stream=true and stream_options.include_usage=true
// (the latter is suppressible for providers that reject it).  The adapter
// consumes the resulting SSE stream, accumulates tool-call argument
// fragments keyed by tool_calls[].index, and emits provider.Event values.
//
// # Boundaries
//
// This package depends only on internal/provider, internal/session, and
// the standard library.  It must not import internal/store, internal/agent,
// or internal/cost.
package openaicompat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/provider"
)

const (
	streamPath = "/chat/completions"

	// defaultTimeout is the HTTP client timeout when Config.HTTPClient is
	// nil.  Stream responses can be slow (a long reasoning turn can spend
	// minutes on the server) so we pick a generous ten minutes.
	defaultTimeout = 10 * time.Minute
)

// Config configures a Provider built around an OpenAI-compatible HTTP API.
// Fields without an explicit default in the per-field comment are required.
type Config struct {
	// Name is what Provider.Name() returns ("openai", "openrouter", ...).
	// Required.
	Name string

	// BaseURL is the API root (e.g. "https://api.openai.com/v1").
	// /chat/completions is appended.  Required.
	BaseURL string

	// APIKey is the resolved bearer credential.  The shim is responsible
	// for env-var lookup and op:// resolution; this adapter never touches
	// the environment.  Required.
	APIKey string

	// AuthHeader defaults to "Authorization".  Override only when the
	// provider uses a non-standard header (rare).
	AuthHeader string

	// AuthScheme is prepended to APIKey before being set on AuthHeader.
	// Defaults to "Bearer ".  Set to "" to send the raw key (e.g. when
	// AuthHeader is "x-api-key").
	AuthScheme string

	// ExtraHeaders are sent on every request.  Used for OpenRouter's
	// HTTP-Referer / X-Title attribution, for example.
	ExtraHeaders map[string]string

	// Models is the static catalog this provider exposes via ListModels.
	// May be an empty slice but not nil.  Required.
	Models []provider.Model

	// HTTPClient is optional.  When nil, a client with the package's
	// default 10-minute timeout is constructed.
	HTTPClient *http.Client

	// IncludeUsage, when non-nil and true, sets stream_options.include_usage
	// on the request so the trailing SSE chunk carries token usage.  Set
	// to a false pointer to omit stream_options entirely (some providers
	// reject the field).  Nil defaults to true.
	IncludeUsage *bool

	// DefaultMaxTokens is the value sent when provider.Request.MaxTokens
	// is zero.  Zero here means "omit max_tokens" — let the server pick.
	DefaultMaxTokens int

	// Catalog, when non-nil, is consulted by reasoning-model detection
	// before falling back to the legacy name-prefix heuristic.  The
	// catalog is the authoritative source for capability metadata
	// (sourced from Catwalk); the prefix matcher is the fallback
	// for models the catalog hasn't been refreshed for.
	//
	// CatalogProvider is the provider id used for catalog Lookups.
	// When empty, [Config.Name] is used.  This matters for adapters
	// that ride on top of compat (e.g. openrouter) where the
	// adapter's Name and the catalog's provider id differ from the
	// upstream vendor that owns the reasoning capability — see
	// canonicaliseModelID below for the namespaced-id stripping
	// applied before catalog lookup.
	Catalog         *catalog.Catalog
	CatalogProvider string

	// Now is an injectable clock for tests and diagnostics.  Defaults to
	// time.Now.
	Now func() time.Time
}

// adapter is the concrete provider.Provider implementation.
type adapter struct {
	cfg Config
}

// New constructs a Provider from Config.  Returns an error if any required
// field is missing.
func New(cfg Config) (provider.Provider, error) {
	if cfg.Name == "" {
		return nil, errors.New("openaicompat: Config.Name is required")
	}
	if cfg.BaseURL == "" {
		return nil, errors.New("openaicompat: Config.BaseURL is required")
	}
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("%w: openaicompat: Config.APIKey is required", provider.ErrAuth)
	}
	if cfg.Models == nil {
		return nil, errors.New("openaicompat: Config.Models is required (may be empty slice)")
	}
	if cfg.AuthHeader == "" {
		cfg.AuthHeader = "Authorization"
	}
	if cfg.AuthScheme == "" && !authSchemeExplicitlyEmpty(cfg) {
		cfg.AuthScheme = "Bearer "
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultTimeout}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &adapter{cfg: cfg}, nil
}

// authSchemeExplicitlyEmpty distinguishes "unset, use default Bearer" from
// "explicitly want raw key".  Go's zero value of string is ambiguous; this
// helper preserves the documented contract that an empty AuthScheme defaults
// to "Bearer " EXCEPT when the caller has also set a non-default
// AuthHeader, which implies they're intentionally bypassing Bearer auth.
func authSchemeExplicitlyEmpty(cfg Config) bool {
	return cfg.AuthScheme == "" && cfg.AuthHeader != "" && cfg.AuthHeader != "Authorization"
}

// Name implements provider.Provider.
func (a *adapter) Name() string { return a.cfg.Name }

// Stream implements provider.Provider.
func (a *adapter) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body, err := a.buildRequestBody(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := a.newRequest(ctx, http.MethodPost, streamPath, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := a.cfg.HTTPClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s: stream request: %w", a.cfg.Name, classifyTransportError(err))
	}
	if resp.StatusCode/100 != 2 {
		return nil, a.readHTTPError(resp)
	}

	out := make(chan provider.Event, streamEventBufSize)
	go parseStream(ctx, a.cfg.Name, resp.Body, out)
	return out, nil
}

// CountTokens implements provider.Provider.
//
// OpenAI does not expose a first-party token-count endpoint.  Returning
// (0, nil) is the documented v0.2 behaviour; a tiktoken-style estimator
// is a v0.3 concern.
func (a *adapter) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}

// ListModels implements provider.Provider.  Returns the static catalog
// provided in Config verbatim — no upstream call.
func (a *adapter) ListModels(_ context.Context) ([]provider.Model, error) {
	return a.cfg.Models, nil
}

// buildRequestBody serialises a provider.Request into the Chat Completions
// JSON body.
func (a *adapter) buildRequestBody(req provider.Request) ([]byte, error) {
	if req.ModelName == "" {
		return nil, fmt.Errorf("%w: model_name is required", provider.ErrInvalidRequest)
	}
	wireMsgs, err := toWireMessages(req.System, req.Messages)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", provider.ErrInvalidRequest, err)
	}

	body := chatRequest{
		Model:    req.ModelName,
		Messages: wireMsgs,
		Stream:   true,
	}

	if tools := toWireTools(req.Tools); len(tools) > 0 {
		body.Tools = tools
		body.ToolChoice = "auto"
	}

	reasoning := a.isReasoningModel(req.ModelName)

	// Reasoning-class OpenAI models reject `temperature` and
	// `max_tokens` outright.  Route them through the
	// reasoning-specific fields and omit both legacy fields entirely
	// (a zero is not the same as absent for these models).
	if reasoning {
		switch {
		case req.MaxTokens > 0:
			mt := req.MaxTokens
			body.MaxCompletionTokens = &mt
		case a.cfg.DefaultMaxTokens > 0:
			mt := a.cfg.DefaultMaxTokens
			body.MaxCompletionTokens = &mt
		}
		if req.Reasoning.IsOn() {
			switch req.Reasoning.Effort {
			case "low", "medium", "high":
				body.ReasoningEffort = req.Reasoning.Effort
			}
		}
	} else {
		if req.Temperature > 0 {
			t := req.Temperature
			body.Temperature = &t
		}
		switch {
		case req.MaxTokens > 0:
			mt := req.MaxTokens
			body.MaxTokens = &mt
		case a.cfg.DefaultMaxTokens > 0:
			mt := a.cfg.DefaultMaxTokens
			body.MaxTokens = &mt
		}
	}

	if a.includeUsage() {
		body.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	return encodeJSON(body)
}

// isReasoningModel reports whether the model id refers to a
// reasoning-class OpenAI model that requires the reasoning request
// shape (max_completion_tokens, no temperature, reasoning_effort).
//
// Two-tier detection:
//
//  1. When a [*catalog.Catalog] is wired into Config.Catalog, look up
//     the model in the catalog and use its [Capabilities.Reasoning]
//     flag. This is the authoritative source — when Catwalk advertises
//     reasoning, hygge believes it.
//  2. Otherwise (or when the catalog has no entry for the model), fall
//     back to the hardcoded name-prefix matcher.  This keeps detection
//     working for brand-new ids the catalog hasn't been refreshed for.
//
// Provider-prefixed forms ("openai/o3-mini", "openrouter/openai/o3")
// are handled by stripping any leading "<vendor>/" segments before
// the lookup or prefix check.
func (a *adapter) isReasoningModel(modelID string) bool {
	if modelID == "" {
		return false
	}
	// Catalog tier.  We try the configured CatalogProvider first;
	// when that misses we also try "openai" because openrouter-style
	// ids ("openai/o3-mini") carry their upstream vendor in the id
	// itself.  See canonicaliseModelID for the stripping rules.
	if a.cfg.Catalog != nil {
		bare := canonicaliseModelID(modelID)
		providerID := a.cfg.CatalogProvider
		if providerID == "" {
			providerID = a.cfg.Name
		}
		if providerID != "" {
			if e, ok := a.cfg.Catalog.Lookup(providerID, bare); ok {
				return e.Capabilities.Reasoning
			}
		}
		// Second chance: try "openai" directly.  Most reasoning
		// models are OpenAI's, and ids like "openai/o3-mini" should
		// resolve cleanly when the catalog has them under that
		// provider regardless of which adapter is asking.
		if providerID != "openai" {
			if e, ok := a.cfg.Catalog.Lookup("openai", bare); ok {
				return e.Capabilities.Reasoning
			}
		}
	}
	return matchesReasoningPrefix(modelID)
}

// canonicaliseModelID strips any leading "<vendor>/" segments and
// lowercases the result.  Used to translate ids like
// "openrouter/openai/o3-mini" into "o3-mini" before a catalog lookup
// or prefix check.
func canonicaliseModelID(modelID string) string {
	id := strings.ToLower(modelID)
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	return id
}

// matchesReasoningPrefix is the legacy heuristic kept as a fallback
// when the catalog has no entry for a model.  Matches, case-insensitive
// on the prefix:
//
//   - o1, o1-* (e.g. o1-mini)
//   - o3, o3-* (e.g. o3-mini)
//   - o4-*    (e.g. o4-mini; bare "o4" is not yet a real model)
//   - reasoning-*  (future-proof escape hatch for explicitly-tagged
//     gateway models)
//
// Provider-prefixed forms ("openai/o3-mini", "openrouter/openai/o3")
// are handled via canonicaliseModelID upstream.
func matchesReasoningPrefix(modelID string) bool {
	if modelID == "" {
		return false
	}
	id := canonicaliseModelID(modelID)
	switch {
	case id == "o1", strings.HasPrefix(id, "o1-"):
		return true
	case id == "o3", strings.HasPrefix(id, "o3-"):
		return true
	case strings.HasPrefix(id, "o4-"):
		return true
	case strings.HasPrefix(id, "reasoning-"):
		return true
	default:
		return false
	}
}

// includeUsage resolves the IncludeUsage tri-state (nil = default true,
// non-nil = explicit value).
func (a *adapter) includeUsage() bool {
	if a.cfg.IncludeUsage == nil {
		return true
	}
	return *a.cfg.IncludeUsage
}

// newRequest constructs an authenticated HTTP request with the standard
// OpenAI-compatible headers attached.
func (a *adapter) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	url := a.cfg.BaseURL + path
	r, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%s: build request: %w", a.cfg.Name, err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(a.cfg.AuthHeader, a.cfg.AuthScheme+a.cfg.APIKey)
	for k, v := range a.cfg.ExtraHeaders {
		r.Header.Set(k, v)
	}
	return r, nil
}

// classifyTransportError wraps low-level HTTP transport errors as
// ErrTransient, except for context errors which propagate verbatim so
// callers can branch on ctx.Err().
func classifyTransportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", provider.ErrTransient, err)
}
