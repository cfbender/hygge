package cost

// fallbackPricing returns a copy of the hard-coded Pricing table.  Returning
// a fresh map on each call prevents callers from accidentally mutating
// shared state.
//
// Snapshot prices as of catalog seeding.  Live catalog at models.dev
// supersedes these when available.  Numbers reflect Anthropic's published
// list pricing for these models (USD per 1M tokens) at the time this file
// was written; refresh via `hygge models refresh` (forthcoming) once the
// live fetch path is wired to a CLI command.
func fallbackPricing() map[string]map[string]Pricing {
	return map[string]map[string]Pricing{
		"anthropic": {
			"claude-sonnet-4-5": {
				Provider:          "anthropic",
				Model:             "claude-sonnet-4-5",
				InputPerMTok:      3.00,
				OutputPerMTok:     15.00,
				CacheReadPerMTok:  0.30,
				CacheWritePerMTok: 3.75,
			},
			"claude-opus-4-5": {
				Provider:          "anthropic",
				Model:             "claude-opus-4-5",
				InputPerMTok:      15.00,
				OutputPerMTok:     75.00,
				CacheReadPerMTok:  1.50,
				CacheWritePerMTok: 18.75,
			},
			"claude-haiku-4-5": {
				Provider:          "anthropic",
				Model:             "claude-haiku-4-5",
				InputPerMTok:      1.00,
				OutputPerMTok:     5.00,
				CacheReadPerMTok:  0.10,
				CacheWritePerMTok: 1.25,
			},
		},
	}
}

// lookupFallback finds Pricing for (provider, model) in the hard-coded
// table.  Returns the zero Pricing and false if absent.  The caller is
// responsible for stamping UpdatedAt (fallback entries leave it as the zero
// time so freshness checks always treat fallback as stale).
func lookupFallback(provider, model string) (Pricing, bool) {
	tbl := fallbackPricing()
	mods, ok := tbl[provider]
	if !ok {
		return Pricing{}, false
	}
	p, ok := mods[model]
	if !ok {
		return Pricing{}, false
	}
	return p, true
}
