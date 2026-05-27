package agent

import (
	"testing"

	"charm.land/fantasy"
)

func TestUsageFromFantasy_OpenRouterSubtractsCachedFromInput(t *testing.T) {
	// Fantasy's OpenRouter provider exports prompt_tokens verbatim as
	// InputTokens, and breaks the cached portion out as CacheReadTokens.
	// Hygge's cost and context-window math (recordUsage,
	// cost.Calculate) treat CacheReadTokens as additive to InputTokens,
	// so without per-provider normalization OpenRouter cached tokens
	// get double-counted in both billing and the context-percent
	// footer. This regression guards the OpenRouter-specific
	// subtraction.
	u := fantasy.Usage{
		InputTokens:     1000, // prompt_tokens (includes cached)
		OutputTokens:    50,
		CacheReadTokens: 800, // cached portion of prompt_tokens
		// OpenRouter does not report cache creation tokens through
		// the chat-completions usage envelope; leaving this at zero
		// matches what the upstream Fantasy hook actually returns.
	}

	got := usageFromFantasy("openrouter", u)

	if got.InputTokens != 200 {
		t.Fatalf("InputTokens: got %d, want 200 (1000 prompt - 800 cached)", got.InputTokens)
	}
	if got.OutputTokens != 50 {
		t.Fatalf("OutputTokens: got %d, want 50", got.OutputTokens)
	}
	if got.CacheReadTokens != 800 {
		t.Fatalf("CacheReadTokens: got %d, want 800", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 0 {
		t.Fatalf("CacheWriteTokens: got %d, want 0", got.CacheWriteTokens)
	}
}

func TestUsageFromFantasy_OpenRouterClampsNegativeInput(t *testing.T) {
	// Defensive: should the upstream ever report CacheReadTokens >
	// InputTokens (transient streaming artefact, e.g. final usage
	// chunk arriving after a partial reset), the subtraction must
	// clamp to zero rather than going negative.
	u := fantasy.Usage{
		InputTokens:     100,
		OutputTokens:    10,
		CacheReadTokens: 500,
	}

	got := usageFromFantasy("openrouter", u)

	if got.InputTokens != 0 {
		t.Fatalf("InputTokens: got %d, want 0 (clamped)", got.InputTokens)
	}
	if got.CacheReadTokens != 500 {
		t.Fatalf("CacheReadTokens: got %d, want 500", got.CacheReadTokens)
	}
}

func TestUsageFromFantasy_AnthropicPassthrough(t *testing.T) {
	// Anthropic-native reports InputTokens EXCLUSIVE of cached tokens
	// by API convention, so the conversion is a pure passthrough. A
	// hypothetical subtraction here would under-bill input tokens.
	u := fantasy.Usage{
		InputTokens:         1000, // already excludes cached
		OutputTokens:        50,
		CacheReadTokens:     800,
		CacheCreationTokens: 200,
	}

	got := usageFromFantasy("anthropic", u)

	if got.InputTokens != 1000 {
		t.Fatalf("InputTokens: got %d, want 1000 (passthrough)", got.InputTokens)
	}
	if got.OutputTokens != 50 {
		t.Fatalf("OutputTokens: got %d, want 50", got.OutputTokens)
	}
	if got.CacheReadTokens != 800 {
		t.Fatalf("CacheReadTokens: got %d, want 800", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 200 {
		t.Fatalf("CacheWriteTokens: got %d, want 200 (from CacheCreationTokens)", got.CacheWriteTokens)
	}
}

func TestUsageFromFantasy_UnknownProviderPassthrough(t *testing.T) {
	// Unknown providers default to passthrough — the conservative
	// choice matches existing behaviour for every non-OpenRouter
	// provider in Hygge today.
	u := fantasy.Usage{
		InputTokens:     500,
		OutputTokens:    25,
		CacheReadTokens: 100,
	}

	got := usageFromFantasy("openai", u)

	if got.InputTokens != 500 {
		t.Fatalf("InputTokens: got %d, want 500 (passthrough for openai; Fantasy openai hook already subtracts)", got.InputTokens)
	}

	got = usageFromFantasy("", u)
	if got.InputTokens != 500 {
		t.Fatalf("InputTokens: got %d, want 500 (passthrough for empty providerID)", got.InputTokens)
	}
}
