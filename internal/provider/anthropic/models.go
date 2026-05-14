package anthropic

import (
	"sync"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/provider"
)

// catalogMu guards access to packageCatalog so SetCatalog from the CLI
// bootstrap is safe to race against ongoing ListModels calls.
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

// Models returns the model list this adapter advertises.  When a
// catalog is wired, models are derived from it; the hardcoded
// defaultModels below are returned only when the catalog has no
// entries for the "anthropic" provider id.
//
// The hardcoded list is a "minimum guaranteed set" so the TUI can
// still pick a model when running fully offline against a bare repo
// (no disk catalog, no embedded catalog — which shouldn't happen, but
// belt-and-braces).
func Models() []provider.Model {
	c := catalogHandle()
	if c == nil {
		return defaultModels()
	}
	entries := c.Models("anthropic")
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
// wired (or the catalog has no anthropic entries).  Mirrors the v0.1
// hardcoded list verbatim.
func defaultModels() []provider.Model {
	return []provider.Model{
		{
			Name:              "claude-sonnet-4-5",
			ContextWindow:     200_000,
			MaxOutput:         8192,
			SupportsTools:     true,
			SupportsImages:    true,
			SupportsReasoning: true,
		},
		{
			Name:              "claude-opus-4-5",
			ContextWindow:     200_000,
			MaxOutput:         8192,
			SupportsTools:     true,
			SupportsImages:    true,
			SupportsReasoning: true,
		},
		{
			Name:              "claude-haiku-4-5",
			ContextWindow:     200_000,
			MaxOutput:         8192,
			SupportsTools:     true,
			SupportsImages:    true,
			SupportsReasoning: true,
		},
	}
}
