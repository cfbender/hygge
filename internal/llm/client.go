// Package llm is the catwalk client wrapper and model-resolution layer.
//
// # Scope
//
// This package provides two things:
//
//  1. A [Client] that wraps the catwalk catalog.  On construction it
//     loads the catwalk embedded snapshot immediately (so there is
//     always offline-capable model metadata), then fires a background
//     ETag-gated refresh from the live catwalk service.  Internally it
//     delegates to [catalog.Catalog].
//
//  2. A [ResolveProviderModel] helper in provider_factory.go that constructs
//     fantasy.LanguageModel values from a provider/model pair.
//
// # Boundaries
//
// This package may import the catalog layer, Fantasy provider adapters, and
// narrow internal support packages needed for provider/model resolution (for
// example auth and provider metadata).  It must not import internal/agent,
// internal/store, or internal/cost.
package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cfbender/hygge/internal/catalog"
)

// Client is a thin wrapper around [catalog.Catalog].  It loads the
// catwalk embedded snapshot on construction and optionally performs a
// live ETag-gated refresh in the background.
//
// Use [NewWithOptions] to construct one; the zero value is not usable.
type Client struct {
	cat *catalog.Catalog
}

// Options configures a [Client].  The zero value yields sensible
// production defaults.
type Options struct {
	// StateDir is the directory for the on-disk snapshot cache.
	// Empty defaults to the XDG state directory (see catalog.LoadOptions).
	StateDir string

	// BaseURL overrides the catwalk service URL.  Empty defaults to
	// the catalog package default (the public catwalk API).
	// Tests point this at an httptest server.
	BaseURL string

	// HTTPClient overrides the HTTP client used for live fetches.
	// Nil uses a client with the catalog package's default timeout.
	HTTPClient *http.Client

	// BackgroundRefresh enables the on-startup goroutine that fetches
	// a fresh snapshot when the disk cache is stale.  Defaults to true.
	// Set to false in tests for determinism.
	BackgroundRefresh *bool

	// RefreshInterval, when positive, starts a periodic ticker that
	// refreshes the catalog at that cadence.  Zero means no periodic
	// refresh.
	RefreshInterval time.Duration

	// Now is an injectable clock for tests.  Nil uses time.Now.
	Now func() time.Time
}

// NewWithOptions constructs a [Client] with the given options.
func NewWithOptions(opts Options) (*Client, error) {
	loadOpts := catalog.LoadOptions{
		StateDir:          opts.StateDir,
		BaseURL:           opts.BaseURL,
		HTTPClient:        opts.HTTPClient,
		BackgroundRefresh: opts.BackgroundRefresh,
		RefreshInterval:   opts.RefreshInterval,
		Now:               opts.Now,
	}
	cat, err := catalog.Load(loadOpts)
	if err != nil {
		return nil, fmt.Errorf("llm: load catalog: %w", err)
	}
	return &Client{cat: cat}, nil
}

// Resolve returns the [catalog.Entry] for (providerID, modelID).
// Returns ok=false when no entry is found.  Both arguments are
// case-insensitive (delegated to catalog.Catalog.Lookup).
func (c *Client) Resolve(providerID, modelID string) (catalog.Entry, bool) {
	return c.cat.Lookup(providerID, modelID)
}

// Refresh forces a live fetch and updates the in-memory and on-disk
// snapshot.  If the catwalk service responds 304 Not Modified, Refresh
// returns nil — the snapshot is already current.
func (c *Client) Refresh(ctx context.Context) error {
	if _, err := c.cat.Refresh(ctx); err != nil {
		return fmt.Errorf("llm: catalog refresh: %w", err)
	}
	return nil
}

// Close stops any periodic-refresh ticker goroutine.  Should be
// deferred by long-lived callers (e.g. the CLI runtime).
func (c *Client) Close() error {
	return c.cat.Close()
}

// EmbeddedProviders returns a summary of the providers available in the
// embedded snapshot.  Useful for startup diagnostics.
func (c *Client) EmbeddedProviders() []string {
	if c.cat == nil {
		return nil
	}
	loaded := c.cat.Loaded()
	if loaded.Source == "" {
		return nil
	}
	return c.cat.Providers()
}
