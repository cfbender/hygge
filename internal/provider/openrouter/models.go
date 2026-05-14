package openrouter

import (
	"sync"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/provider"
)

// catalogMu guards access to packageCatalog so SetCatalog from the CLI
// bootstrap is safe to race against ongoing Models calls.
var (
	catalogMu      sync.RWMutex
	packageCatalog *catalog.Catalog
)

// SetCatalog wires a shared [*catalog.Catalog] into this provider
// package.  Called once by cmd/hygge/cli/common.go during bootstrap.
// Passing nil disables catalog-driven model lists (useful for tests
// that want the hardcoded defaults).
func SetCatalog(c *catalog.Catalog) {
	catalogMu.Lock()
	defer catalogMu.Unlock()
	packageCatalog = c
}

// catalogHandle returns the package-level catalog under a read lock.
func catalogHandle() *catalog.Catalog {
	catalogMu.RLock()
	defer catalogMu.RUnlock()
	return packageCatalog
}

// Models returns the model catalog this shim advertises.
//
// When a [*catalog.Catalog] is wired via [SetCatalog], the list is
// derived from the catalog's "openrouter" entries.  When no catalog
// is wired (or it has no OpenRouter entries), a small hardcoded
// curated subset is returned so the TUI keeps working offline.
//
// Users can pass any other model name via `[model] name = ...` in
// config — OpenRouter resolves arbitrary "<vendor>/<model>" strings
// server-side against its live catalog.  The hardcoded list exists
// only to give discoverable defaults spanning the major upstream
// vendors; it is not an exhaustive enumeration.
func Models() []provider.Model {
	c := catalogHandle()
	if c == nil {
		return defaultModels()
	}
	entries := c.Models("openrouter")
	if len(entries) == 0 {
		return defaultModels()
	}
	out := make([]provider.Model, 0, len(entries))
	for _, e := range entries {
		out = append(out, provider.Model{
			Name:              e.ID,
			ContextWindow:     e.Limit.ContextWindow,
			MaxOutput:         e.Limit.MaxOutput,
			SupportsTools:     e.Capabilities.ToolCalling,
			SupportsImages:    e.Capabilities.InputImages,
			SupportsReasoning: e.Capabilities.Reasoning,
		})
	}
	return out
}

// defaultModels is the hardcoded fallback returned when no catalog is
// wired.  Mirrors the v0.1 hardcoded list verbatim.
func defaultModels() []provider.Model {
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
