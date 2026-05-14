package openai

import "github.com/cfbender/hygge/internal/provider"

// Models returns the static catalog of OpenAI models this shim advertises.
//
// The catalog is deliberately small for v0.2 — it covers the current
// flagship model line plus the cost-optimised "mini" variant.  Context
// window and max-output figures are conservative published values; the
// authoritative source for pricing and capability flags is the models.dev
// catalog integration scheduled for the v0.2 polish pass.
//
// Adding entries: pick well-known model IDs only, no invented version
// numbers.  Anything experimental or preview-tier belongs in a user
// override (`hygge config set model.name ...`) rather than this list.
func Models() []provider.Model {
	return []provider.Model{
		{
			Name:           "gpt-5",
			ContextWindow:  200_000,
			MaxOutput:      16_384,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "gpt-4o",
			ContextWindow:  128_000,
			MaxOutput:      16_384,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "gpt-4o-mini",
			ContextWindow:  128_000,
			MaxOutput:      16_384,
			SupportsTools:  true,
			SupportsImages: true,
		},
	}
}
