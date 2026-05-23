package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"charm.land/fantasy"
	fanthropic "charm.land/fantasy/providers/anthropic"
	fgoogle "charm.land/fantasy/providers/google"
	fopenai "charm.land/fantasy/providers/openai"
	fopenaicompat "charm.land/fantasy/providers/openaicompat"
	fopenrouter "charm.land/fantasy/providers/openrouter"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/provider"
	orpkg "github.com/cfbender/hygge/internal/provider/openrouter"
)

// ProviderResolution is the Phase 2 bridge result for constructing a
// fantasy-backed model while preserving metadata the existing runtime expects.
type ProviderResolution struct {
	Provider fantasy.Provider
	Model    fantasy.LanguageModel
	Metadata provider.Model
}

// ProviderBuildOptions carries optional per-build overrides for
// [ResolveProviderModel].  The zero value applies no extra configuration.
type ProviderBuildOptions struct {
	// OpenRouterSessionCache, when non-nil, wires an HTTP transport into the
	// OpenRouter provider that injects x-session-id on every chat request.
	// The session ID is read from the request context (set via
	// [orpkg.ContextWithSessionID]); the cache resolves it to the root
	// session ID before injection.  Only applied when providerID == "openrouter".
	OpenRouterSessionCache *orpkg.RootIDCache
}

// ResolveProviderModel builds a fantasy provider/model pair for the given
// hygge provider id and model id.
func ResolveProviderModel(ctx context.Context, providerID, modelID string, opts map[string]any, cat *catalog.Catalog) (ProviderResolution, error) {
	return ResolveProviderModelWith(ctx, providerID, modelID, opts, cat, ProviderBuildOptions{})
}

// ResolveProviderModelWith is like [ResolveProviderModel] but accepts
// optional per-build configuration via [ProviderBuildOptions].
func ResolveProviderModelWith(ctx context.Context, providerID, modelID string, opts map[string]any, cat *catalog.Catalog, buildOpts ProviderBuildOptions) (ProviderResolution, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return ProviderResolution{}, fmt.Errorf("llm: provider id is required")
	}
	if modelID == "" {
		return ProviderResolution{}, fmt.Errorf("llm: model id is required")
	}

	apiKey, err := resolveAPIKey(providerID, opts, cat)
	if err != nil {
		return ProviderResolution{}, err
	}
	if apiKey == "" {
		return ProviderResolution{}, fmt.Errorf("%w: missing api_key for provider %q", provider.ErrAuth, providerID)
	}

	fp, err := newFantasyProvider(providerID, apiKey, opts, buildOpts, cat)
	if err != nil {
		return ProviderResolution{}, err
	}
	lm, err := fp.LanguageModel(ctx, modelID)
	if err != nil {
		return ProviderResolution{}, fmt.Errorf("llm: construct language model %q/%q: %w", providerID, modelID, err)
	}

	return ProviderResolution{
		Provider: fp,
		Model:    lm,
		Metadata: modelMetadata(providerID, modelID, cat),
	}, nil
}

func newFantasyProvider(providerID, apiKey string, opts map[string]any, buildOpts ProviderBuildOptions, cat *catalog.Catalog) (fantasy.Provider, error) {
	baseURL := stringOpt(opts, "base_url")
	headers := headersOpt(opts)
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	switch providerID {
	case "anthropic":
		fopts := []fanthropic.Option{fanthropic.WithAPIKey(apiKey)}
		if baseURL != "" {
			fopts = append(fopts, fanthropic.WithBaseURL(baseURL))
		}
		if len(headers) > 0 {
			fopts = append(fopts, fanthropic.WithHeaders(headers))
		}
		return fanthropic.New(fopts...)
	case "openai":
		fopts := []fopenai.Option{fopenai.WithAPIKey(apiKey)}
		if isOAuth(opts) {
			// Codex OAuth: use the Responses API format (required by the
			// Codex endpoint) and rewrite URLs to the Codex endpoint.
			codexURL, _ := url.Parse(auth.CodexAPIEndpoint())
			fopts = append(fopts,
				fopenai.WithUseResponsesAPI(),
				fopenai.WithHTTPClient(&codexRewriter{
					target: codexURL,
					inner:  http.DefaultClient,
				}),
			)
			headers["originator"] = "hygge"
			if v := stringOpt(opts, "account_id"); v != "" {
				headers["ChatGPT-Account-Id"] = v
			}
		} else if baseURL != "" {
			fopts = append(fopts, fopenai.WithBaseURL(baseURL))
		}
		if len(headers) > 0 {
			fopts = append(fopts, fopenai.WithHeaders(headers))
		}
		return fopenai.New(fopts...)
	case "gemini":
		fopts := []fgoogle.Option{fgoogle.WithName("gemini"), fgoogle.WithGeminiAPIKey(apiKey)}
		if baseURL != "" {
			fopts = append(fopts, fgoogle.WithBaseURL(baseURL))
		}
		if len(headers) > 0 {
			fopts = append(fopts, fgoogle.WithHeaders(headers))
		}
		return fgoogle.New(fopts...)
	case "openrouter":
		if v := stringOptAllowEmpty(opts, "http_referer", "https://github.com/cfbender/hygge"); v != "" {
			headers["HTTP-Referer"] = v
		}
		if v := stringOptAllowEmpty(opts, "x_title", "hygge"); v != "" {
			headers["X-Title"] = v
		}
		fopts := []fopenrouter.Option{fopenrouter.WithAPIKey(apiKey)}
		if len(headers) > 0 {
			fopts = append(fopts, fopenrouter.WithHeaders(headers))
		}
		// Wire the x-session-id transport when a session cache is provided.
		// The transport reads the session ID from the request context
		// (injected by the agent turn runner via orpkg.ContextWithSessionID)
		// and resolves it to the root session ID before setting the header.
		if buildOpts.OpenRouterSessionCache != nil {
			fopts = append(fopts, fopenrouter.WithHTTPClient(&http.Client{
				Transport: &orpkg.SessionHeaderTransport{
					Inner: http.DefaultTransport,
					Cache: buildOpts.OpenRouterSessionCache,
				},
			}))
		}
		if baseURL != "" {
			// openrouter provider defaults its base URL; override through the
			// compat provider when an explicit base_url is requested.
			// fopenaicompat supports WithHTTPClient, so the session-header
			// transport is wired here too when a cache is provided.
			compatOpts := []fopenaicompat.Option{
				fopenaicompat.WithName("openrouter"),
				fopenaicompat.WithAPIKey(apiKey),
				fopenaicompat.WithBaseURL(baseURL),
				fopenaicompat.WithHeaders(headers),
			}
			if buildOpts.OpenRouterSessionCache != nil {
				compatOpts = append(compatOpts, fopenaicompat.WithHTTPClient(&http.Client{
					Transport: &orpkg.SessionHeaderTransport{
						Inner: http.DefaultTransport,
						Cache: buildOpts.OpenRouterSessionCache,
					},
				}))
			}
			return fopenaicompat.New(compatOpts...)
		}
		return fopenrouter.New(fopts...)
	default:
		if baseURL == "" {
			// Try to find the provider's API endpoint from the catalog.
			// This enables openai-compat Catwalk providers (e.g. opencode-go)
			// to work without the user supplying a base_url explicitly.
			if cat != nil {
				if pm, ok := cat.LookupProvider(providerID); ok && pm.APIEndpoint != "" {
					baseURL = pm.APIEndpoint
					// Merge provider-level default_headers from catalog into the
					// request headers, not overwriting user-supplied values.
					for k, v := range pm.DefaultHeaders {
						if _, exists := headers[k]; !exists {
							headers[k] = v
						}
					}
				}
			}
		}
		if baseURL == "" {
			return nil, fmt.Errorf("llm: unsupported provider %q (base_url required for compat provider)", providerID)
		}
		return fopenaicompat.New(
			fopenaicompat.WithName(providerID),
			fopenaicompat.WithAPIKey(apiKey),
			fopenaicompat.WithBaseURL(baseURL),
			fopenaicompat.WithHeaders(headers),
		)
	}
}

func modelMetadata(providerID, modelID string, cat *catalog.Catalog) provider.Model {
	if cat != nil {
		if e, ok := cat.Lookup(providerID, modelID); ok {
			return provider.Model{
				Name:              modelID,
				ContextWindow:     e.Limit.ContextWindow,
				MaxOutput:         e.Limit.MaxOutput,
				SupportsTools:     e.Capabilities.ToolCalling,
				SupportsImages:    e.Capabilities.InputImages || e.Capabilities.Attachment,
				SupportsReasoning: e.Capabilities.Reasoning,
			}
		}
	}
	return provider.Model{Name: modelID}
}

func resolveAPIKey(providerID string, opts map[string]any, cat *catalog.Catalog) (string, error) {
	if v, ok := opts["api_key"]; ok {
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			return "", nil
		}
		if strings.HasPrefix(s, "op://") {
			return "", fmt.Errorf("%w: %s", provider.ErrAuthOpRefUnsupported, s)
		}
		if after, ok0 := strings.CutPrefix(s, "$"); ok0 {
			envName := after
			if envName == "" {
				return "", fmt.Errorf("%w: empty env reference", provider.ErrAuth)
			}
			if ev := os.Getenv(envName); ev != "" {
				return ev, nil
			}
			return "", fmt.Errorf("%w: env %s referenced by api_key is empty", provider.ErrAuth, envName)
		}
		return s, nil
	}
	if envName := defaultAPIKeyEnv(providerID); envName != "" {
		if ev := os.Getenv(envName); ev != "" {
			return ev, nil
		}
	}
	// Fall back to the catalog's APIKeyRef for this provider.
	// Only used when there is no hardcoded env mapping for the provider.
	if cat != nil {
		if pm, ok := cat.LookupProvider(providerID); ok && pm.APIKeyRef != "" {
			if after, ok0 := strings.CutPrefix(pm.APIKeyRef, "$"); ok0 {
				if after != "" {
					if ev := os.Getenv(after); ev != "" {
						return ev, nil
					}
				}
			}
		}
	}
	return "", nil
}

func defaultAPIKeyEnv(providerID string) string {
	switch providerID {
	case "anthropic":
		return "ANTHROPIC_API_KEY"
	case "openai":
		return "OPENAI_API_KEY"
	case "openrouter":
		return "OPENROUTER_API_KEY"
	case "gemini":
		return "GOOGLE_API_KEY"
	default:
		return ""
	}
}

// codexRewriter is an HTTP client wrapper that rewrites request URLs to the
// Codex API endpoint and ensures the request body has an "instructions" field
// (required by Codex). Fantasy puts the system prompt into the input array
// but Codex requires it in the top-level instructions field.
type codexRewriter struct {
	target *url.URL
	inner  *http.Client
}

func (c *codexRewriter) Do(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	if strings.Contains(path, "/responses") || strings.Contains(path, "/chat/completions") {
		req.URL.Scheme = c.target.Scheme
		req.URL.Host = c.target.Host
		req.URL.Path = c.target.Path

		// Codex requires "instructions" at the top level. Fantasy sends
		// the system prompt as a system/developer message in the input
		// array. Extract it and promote to instructions.
		if req.Body != nil {
			bodyBytes, err := io.ReadAll(req.Body)
			_ = req.Body.Close()
			if err == nil {
				bodyBytes = codexPromoteInstructions(bodyBytes)
				req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
				req.ContentLength = int64(len(bodyBytes))
			}
		}
	}
	return c.inner.Do(req) //nolint:gosec // URL is rewritten to a known constant (codexAPIEndpoint)
}

// codexPromoteInstructions extracts system/developer messages from the input
// array and promotes them to the top-level "instructions" field.
func codexPromoteInstructions(body []byte) []byte {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return body
	}

	// If instructions already set, leave it alone.
	if raw, ok := obj["instructions"]; ok && len(raw) > 0 && string(raw) != `""` && string(raw) != "null" {
		return body
	}

	// Look through the input array for system/developer messages.
	inputRaw, ok := obj["input"]
	if !ok {
		return body
	}
	var input []json.RawMessage
	if err := json.Unmarshal(inputRaw, &input); err != nil {
		return body
	}

	var instructions []string
	var filtered []json.RawMessage
	for _, item := range input {
		var msg struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal(item, &msg); err == nil &&
			(msg.Role == "system" || msg.Role == "developer") && msg.Content != "" {
			instructions = append(instructions, msg.Content)
			continue
		}
		filtered = append(filtered, item)
	}

	if len(instructions) == 0 {
		return body
	}

	instructionText := strings.Join(instructions, "\n\n")
	instrJSON, _ := json.Marshal(instructionText)
	obj["instructions"] = instrJSON

	if len(filtered) > 0 {
		filteredJSON, _ := json.Marshal(filtered)
		obj["input"] = filteredJSON
	} else {
		delete(obj, "input")
	}

	result, err := json.Marshal(obj)
	if err != nil {
		return body
	}
	return result
}

func isOAuth(opts map[string]any) bool {
	if opts == nil {
		return false
	}
	v, ok := opts["oauth"]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func stringOpt(opts map[string]any, key string) string {
	if opts == nil {
		return ""
	}
	if raw, ok := opts[key]; ok {
		if s, ok := raw.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func stringOptAllowEmpty(opts map[string]any, key, def string) string {
	if opts == nil {
		return def
	}
	raw, ok := opts[key]
	if !ok {
		return def
	}
	s, ok := raw.(string)
	if !ok {
		return def
	}
	return strings.TrimSpace(s)
}

func headersOpt(opts map[string]any) map[string]string {
	headers := map[string]string{}
	copyHeaders(headers, opts, "headers")
	copyHeaders(headers, opts, "default_headers")
	copyHeaders(headers, opts, "extra_headers")
	return headers
}

func copyHeaders(dst map[string]string, opts map[string]any, key string) {
	if opts == nil {
		return
	}
	raw, ok := opts[key]
	if !ok {
		return
	}
	switch h := raw.(type) {
	case map[string]string:
		for k, v := range h {
			if strings.TrimSpace(k) != "" {
				dst[k] = v
			}
		}
	case map[string]any:
		for k, v := range h {
			if strings.TrimSpace(k) == "" {
				continue
			}
			if s, ok := v.(string); ok {
				dst[k] = s
			}
		}
	}
}
