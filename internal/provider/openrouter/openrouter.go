// Package openrouter registers the "openrouter" provider, configured as a
// shim over internal/provider/openaicompat with OpenRouter's defaults.
//
// # What OpenRouter is
//
// OpenRouter is an HTTPS gateway that fronts many underlying model hosts
// (Anthropic, OpenAI, Meta, Google, Mistral, xAI, DeepSeek, ...) behind a
// single OpenAI-compatible Chat Completions API.  Users authenticate once
// against OpenRouter; OpenRouter routes each request to whichever upstream
// hosts the requested model.  Model names are namespaced as
// "<vendor>/<model>", e.g. "anthropic/claude-sonnet-4-5", "openai/gpt-5",
// "meta-llama/llama-3.3-70b-instruct".
//
// # Layering
//
// This package's responsibilities are intentionally narrow:
//
//  1. Resolve the API key (opts["api_key"], then OPENROUTER_API_KEY, then
//     provider.ErrAuth).
//  2. Pick the default base URL (https://openrouter.ai/api/v1).
//  3. Supply the static model catalog (see models.go).
//  4. Forward the optional HTTP-Referer / X-Title attribution headers so
//     OpenRouter's "Apps using OpenRouter" dashboard groups our traffic
//     correctly.
//  5. Register itself with the provider registry under "openrouter".
//
// All wire-protocol work — request shaping, SSE parsing, tool-call
// accumulation, error classification — lives in openaicompat and is shared
// with every other OpenAI-compatible shim.
package openrouter

import (
	"fmt"
	"os"
	"strings"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/provider/openaicompat"
)

const (
	// defaultBaseURL is OpenRouter's production REST endpoint.  Overridable
	// via opts["base_url"] for tests and for self-hosted gateways.
	defaultBaseURL = "https://openrouter.ai/api/v1"

	// apiKeyEnv is the canonical environment variable for OpenRouter
	// credentials.
	apiKeyEnv = "OPENROUTER_API_KEY" //nolint:gosec // env var name, not a credential

	// defaultHTTPReferer identifies hygge on OpenRouter's analytics
	// dashboards.  Overridable via opts["http_referer"].
	defaultHTTPReferer = "https://github.com/cfbender/hygge"

	// defaultXTitle is the human-readable app name reported to OpenRouter.
	// Overridable via opts["x_title"].
	defaultXTitle = "hygge"
)

func init() {
	provider.Register("openrouter", New)
}

// New constructs an OpenRouter provider from the given options map.
// Supported keys:
//
//   - "api_key" (string): explicit key, or "$ENV" reference.  op:// is
//     reserved for future 1Password CLI integration and currently returns
//     provider.ErrAuthOpRefUnsupported.
//   - "base_url" (string): override the API base URL.  Used for testing
//     and for private gateways that proxy OpenRouter.
//   - "http_referer" (string): override the HTTP-Referer attribution
//     header.  Pass "" to suppress the header entirely.
//   - "x_title" (string): override the X-Title attribution header.  Pass
//     "" to suppress the header entirely.
//
// Returns provider.ErrAuth (wrapped) when no key can be resolved.
func New(opts map[string]any) (provider.Provider, error) {
	key, err := resolveAPIKey(opts)
	if err != nil {
		return nil, err
	}
	baseURL := stringOpt(opts, "base_url", defaultBaseURL)

	// Attribution headers.  Defaults identify hygge so OpenRouter's
	// per-app analytics group our traffic correctly.  Users can override
	// either value, including setting it to the empty string to opt out
	// of attribution entirely.
	httpReferer := stringOptAllowEmpty(opts, "http_referer", defaultHTTPReferer)
	xTitle := stringOptAllowEmpty(opts, "x_title", defaultXTitle)

	extra := map[string]string{}
	if httpReferer != "" {
		extra["HTTP-Referer"] = httpReferer
	}
	if xTitle != "" {
		extra["X-Title"] = xTitle
	}

	return openaicompat.New(openaicompat.Config{
		Name:         "openrouter",
		BaseURL:      baseURL,
		APIKey:       key,
		ExtraHeaders: extra,
		Models:       Models(),
	})
}

// resolveAPIKey mirrors the openai shim's precedence chain, but reads
// OPENROUTER_API_KEY instead of OPENAI_API_KEY.  See the package doc
// comment for the full ordering.
func resolveAPIKey(opts map[string]any) (string, error) {
	if raw, ok := opts["api_key"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			if strings.HasPrefix(s, "op://") {
				return "", fmt.Errorf("%w: %s", provider.ErrAuthOpRefUnsupported, s)
			}
			if strings.HasPrefix(s, "$") {
				name := strings.TrimPrefix(s, "$")
				if v := os.Getenv(name); v != "" {
					return v, nil
				}
				return "", fmt.Errorf("%w: env %s referenced by api_key is empty", provider.ErrAuth, name)
			}
			return s, nil
		}
	}
	if v := os.Getenv(apiKeyEnv); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%w: no OpenRouter API key found; set %s or model.options.api_key", provider.ErrAuth, apiKeyEnv)
}

// stringOpt extracts a string-valued option from opts, falling back to def
// when the key is absent, holds a non-string value, or is the empty
// string.  Use this for required fields where empty is meaningless (e.g.
// a base URL).
func stringOpt(opts map[string]any, key, def string) string {
	if raw, ok := opts[key]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return def
}

// stringOptAllowEmpty is like stringOpt but treats an explicit "" as a
// valid user choice (meaning "suppress this field") rather than falling
// back to the default.  Used for OpenRouter's optional attribution
// headers, where setting `http_referer = ""` in config is a documented
// way to opt out of attribution.
func stringOptAllowEmpty(opts map[string]any, key, def string) string {
	raw, ok := opts[key]
	if !ok {
		return def
	}
	s, ok := raw.(string)
	if !ok {
		return def
	}
	return s
}
