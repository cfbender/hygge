package openrouter

import "github.com/cfbender/hygge/internal/provider"

// Models returns the static catalog of OpenRouter models this shim
// advertises.
//
// Static catalog is a curated subset of OpenRouter's full model list.
// Users can pass any other model name via `[model] name = ...` in config
// — OpenRouter resolves arbitrary "<vendor>/<model>" strings server-side
// against its live catalog.  This list exists to give the user
// discoverable defaults spanning the major upstream vendors; it is not
// an exhaustive enumeration.
//
// Capability flags (SupportsTools / SupportsImages) are conservative
// best-effort values for v0.2: true for the flagship Anthropic, OpenAI
// and Google entries; mixed for the open-weight and DeepSeek models.
// The authoritative source for pricing and capability flags is the
// models.dev catalog integration scheduled for the v0.2 polish pass —
// these numbers are placeholders that catalog will override.
//
// Adding entries: pick well-known "<vendor>/<model>" IDs only, no
// invented version numbers.  Anything experimental belongs in user
// override (`hygge config set model.name ...`) rather than this list.
func Models() []provider.Model {
	return []provider.Model{
		{
			Name:           "anthropic/claude-sonnet-4-5",
			ContextWindow:  200_000,
			MaxOutput:      8_192,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "anthropic/claude-opus-4-5",
			ContextWindow:  200_000,
			MaxOutput:      8_192,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "openai/gpt-5",
			ContextWindow:  200_000,
			MaxOutput:      16_384,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "openai/gpt-4o",
			ContextWindow:  128_000,
			MaxOutput:      16_384,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "google/gemini-2.5-pro",
			ContextWindow:  1_000_000,
			MaxOutput:      8_192,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "meta-llama/llama-3.3-70b-instruct",
			ContextWindow:  128_000,
			MaxOutput:      8_192,
			SupportsTools:  true,
			SupportsImages: false,
		},
		{
			Name:           "mistralai/mistral-large-2411",
			ContextWindow:  128_000,
			MaxOutput:      8_192,
			SupportsTools:  true,
			SupportsImages: false,
		},
		{
			Name:           "deepseek/deepseek-chat",
			ContextWindow:  128_000,
			MaxOutput:      8_192,
			SupportsTools:  true,
			SupportsImages: false,
		},
	}
}
