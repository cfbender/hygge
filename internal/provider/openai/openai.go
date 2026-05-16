// Package openai registers the "openai" provider, configured as a shim
// over internal/provider/openaicompat with OpenAI's defaults.
//
// # Layering
//
// This package's only responsibilities are:
//
//  1. Resolving the API key (opts["api_key"], then OPENAI_API_KEY, then
//     ErrAuth).
//  2. Picking the default base URL (https://api.openai.com/v1).
//  3. Supplying the static model catalog.
//  4. Registering itself with the provider registry under "openai".
//
// All wire-protocol work — request shaping, SSE parsing, tool-call
// accumulation, error classification — lives in openaicompat and is
// shared with every other OpenAI-compatible shim (OpenRouter, Groq,
// DeepSeek, ...).
package openai

import (
	"fmt"
	"os"
	"strings"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/provider/openaicompat"
)

const (
	// defaultBaseURL is OpenAI's production REST endpoint.  Overridable
	// via opts["base_url"] for Azure OpenAI compatibility and for tests.
	defaultBaseURL = "https://api.openai.com/v1"

	// apiKeyEnv is the canonical environment variable OpenAI's SDKs read.
	apiKeyEnv = "OPENAI_API_KEY" //nolint:gosec // env var name, not a credential
)

func init() {
	provider.Register("openai", New)
}

// New constructs an OpenAI provider from the given options map.  Supported
// keys:
//
//   - "api_key" (string): explicit key, or "$ENV" reference.  op:// is
//     reserved for future 1Password CLI integration and currently returns
//     provider.ErrAuthOpRefUnsupported.
//   - "base_url" (string): override the API base URL.  Used for testing
//     and for Azure OpenAI / OpenAI-compatible private gateways.
//
// Returns provider.ErrAuth (wrapped) when no key can be resolved.
func New(opts map[string]any) (provider.Provider, error) {
	key, err := resolveAPIKey(opts)
	if err != nil {
		return nil, err
	}
	baseURL := stringOpt(opts, "base_url", defaultBaseURL)
	return openaicompat.New(openaicompat.Config{
		Name:            "openai",
		BaseURL:         baseURL,
		APIKey:          key,
		Models:          Models(),
		Catalog:         catalogHandle(),
		CatalogProvider: "openai",
	})
}

// resolveAPIKey mirrors the Anthropic shim's precedence chain.  See the
// package doc comment for the full ordering.
func resolveAPIKey(opts map[string]any) (string, error) {
	if raw, ok := opts["api_key"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			if strings.HasPrefix(s, "op://") {
				return "", fmt.Errorf("%w: %s", provider.ErrAuthOpRefUnsupported, s)
			}
			if after, ok0 := strings.CutPrefix(s, "$"); ok0 {
				name := after
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
	return "", fmt.Errorf("%w: no OpenAI API key found; set %s or model.options.api_key", provider.ErrAuth, apiKeyEnv)
}

// stringOpt extracts a string-valued option from opts, falling back to def
// when the key is absent or holds a non-string value.
func stringOpt(opts map[string]any, key, def string) string {
	if raw, ok := opts[key]; ok {
		if s, ok := raw.(string); ok && s != "" {
			return s
		}
	}
	return def
}
