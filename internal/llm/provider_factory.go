package llm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"charm.land/fantasy"
	fanthropic "charm.land/fantasy/providers/anthropic"
	fopenai "charm.land/fantasy/providers/openai"
	fopenaicompat "charm.land/fantasy/providers/openaicompat"
	fopenrouter "charm.land/fantasy/providers/openrouter"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/provider"
)

// ProviderResolution is the Phase 2 bridge result for constructing a
// fantasy-backed model while preserving metadata the existing runtime expects.
type ProviderResolution struct {
	Provider fantasy.Provider
	Model    fantasy.LanguageModel
	Metadata provider.Model
}

// ResolveProviderModel builds a fantasy provider/model pair for the given
// hygge provider id and model id.
func ResolveProviderModel(ctx context.Context, providerID, modelID string, opts map[string]any, cat *catalog.Catalog) (ProviderResolution, error) {
	providerID = strings.ToLower(strings.TrimSpace(providerID))
	modelID = strings.TrimSpace(modelID)
	if providerID == "" {
		return ProviderResolution{}, fmt.Errorf("llm: provider id is required")
	}
	if modelID == "" {
		return ProviderResolution{}, fmt.Errorf("llm: model id is required")
	}

	apiKey, err := resolveAPIKey(providerID, opts)
	if err != nil {
		return ProviderResolution{}, err
	}
	if apiKey == "" {
		return ProviderResolution{}, fmt.Errorf("%w: missing api_key for provider %q", provider.ErrAuth, providerID)
	}

	fp, err := newFantasyProvider(providerID, apiKey, opts)
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

func newFantasyProvider(providerID, apiKey string, opts map[string]any) (fantasy.Provider, error) {
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
		if baseURL != "" {
			fopts = append(fopts, fopenai.WithBaseURL(baseURL))
		}
		if len(headers) > 0 {
			fopts = append(fopts, fopenai.WithHeaders(headers))
		}
		return fopenai.New(fopts...)
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
		if baseURL != "" {
			// openrouter provider defaults base URL; override through compat provider when explicit base_url is requested.
			return fopenaicompat.New(
				fopenaicompat.WithName("openrouter"),
				fopenaicompat.WithAPIKey(apiKey),
				fopenaicompat.WithBaseURL(baseURL),
				fopenaicompat.WithHeaders(headers),
			)
		}
		return fopenrouter.New(fopts...)
	default:
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

func resolveAPIKey(providerID string, opts map[string]any) (string, error) {
	if v, ok := opts["api_key"]; ok {
		s, _ := v.(string)
		s = strings.TrimSpace(s)
		if s == "" {
			return "", nil
		}
		if strings.HasPrefix(s, "op://") {
			return "", fmt.Errorf("%w: %s", provider.ErrAuthOpRefUnsupported, s)
		}
		if strings.HasPrefix(s, "$") {
			envName := strings.TrimPrefix(s, "$")
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
	default:
		return ""
	}
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
