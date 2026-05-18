package cost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/cfbender/hygge/internal/catalog"
)

// CatalogOptions configures a [Catalog].  The zero value is valid and
// produces a Catalog backed by the package-level default
// [*catalog.Catalog] (created lazily on first use).
//
// Construction has two modes:
//
//   - When [CatalogOptions.Catalog] is non-nil, the cost catalog wraps
//     it directly.  This is the production wiring done by
//     cmd/hygge/cli/common.go: a single [*catalog.Catalog] is shared
//     by the cost lookups and every provider's model list.
//
//   - When [CatalogOptions.Catalog] is nil, a private
//     [*catalog.Catalog] is constructed from the remaining fields
//     (BaseURL, CachePath, HTTPClient, Now, TTL) so legacy callers and
//     tests continue to work without explicit wiring.  The remaining
//     fields are interpreted in the obvious way; see the field
//     comments.
type CatalogOptions struct {
	// Catalog is the underlying metadata catalog.  When non-nil the
	// other fields are ignored.
	Catalog *catalog.Catalog

	// HTTPClient is the client used for live fetches when Catalog is
	// nil.  Nil uses an http.Client with a 15-second timeout.
	HTTPClient *http.Client

	// BaseURL overrides the Catwalk host when Catalog is nil.
	// Empty falls back to catalog.DefaultBaseURL.  Tests typically
	// point this at an httptest server.
	BaseURL string

	// CachePath overrides the on-disk cache path when Catalog is nil.
	// Empty falls back to $XDG_STATE_HOME/hygge/catalog.json (the new
	// catalog filename — note this differs from the legacy
	// "models_catalog.json" used in v0.1.x; callers that need the
	// legacy path must set CachePath explicitly).
	CachePath string

	// TTL is preserved for source-compat with v0.1 callers but is no
	// longer honoured: the underlying catalog uses its own staleness
	// policy (catalog.DefaultMaxStaleness).  Setting this to a
	// positive value is silently ignored.
	TTL time.Duration

	// Now is an injectable clock for tests.  Nil uses time.Now.
	Now func() time.Time
}

// Catalog is the cost-resolution facade.  It wraps a [*catalog.Catalog]
// so existing internal/agent and internal/subagent code can keep using
// the same LookUp API.
//
// Safe for concurrent use.
type Catalog struct {
	src *catalog.Catalog
	now func() time.Time
}

// NewCatalog builds a Catalog.  Does not perform any I/O at
// construction time; the first LookUp triggers a network fetch IF the
// underlying catalog is stale and background refresh succeeded.
func NewCatalog(opts CatalogOptions) *Catalog {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	src := opts.Catalog
	if src == nil {
		// Build a private catalog using the legacy options.  Tests
		// that point BaseURL at an httptest server should also pass
		// CachePath into a temp dir so the disk cache is hermetic.
		stateDir := ""
		if opts.CachePath != "" {
			// CachePath in v0.1 pointed at a file; we extract its
			// parent dir so the catalog stores at that location.
			stateDir = filepathDir(opts.CachePath)
		}
		c, err := catalog.Load(catalog.LoadOptions{
			StateDir:          stateDir,
			HTTPClient:        opts.HTTPClient,
			BaseURL:           opts.BaseURL,
			Now:               opts.Now,
			BackgroundRefresh: falsePtr(),
		})
		if err != nil {
			// Catalog.Load returns an error only for catastrophic
			// situations (embedded snapshot itself malformed) —
			// degrade to a Catalog that always misses rather than
			// panicking.
			return &Catalog{src: nil, now: opts.Now}
		}
		src = c
	}
	return &Catalog{src: src, now: opts.Now}
}

// LookUp returns Pricing for (provider, model).
//
// Cascade:
//   - Hit the wrapped catalog.  When it has an entry, translate the
//     catalog.Cost into a cost.Pricing and return (Pricing, true, nil).
//   - When the catalog has no entry, return (zero Pricing, false,
//     [ErrModelNotPriced]).
//
// The returned bool reports whether the result came from a fresh
// snapshot.  "Fresh" here means "the catalog's current snapshot is at
// most catalog.DefaultMaxStaleness old".  Stale-but-present snapshots
// still serve a hit, but with fresh=false so callers can flag
// best-effort cost numbers in the UI.
//
// LookUp never panics, even with a corrupt cache file, malformed JSON,
// or unexpected upstream types — those scenarios all degrade to the
// next step in the cascade owned by the underlying [*catalog.Catalog].
func (c *Catalog) LookUp(_ context.Context, provider, model string) (Pricing, bool, error) {
	if c == nil || c.src == nil {
		return Pricing{}, false, fmt.Errorf("%w: %s/%s (no catalog wired)", ErrModelNotPriced, provider, model)
	}
	e, ok := c.src.Lookup(provider, model)
	if !ok {
		return Pricing{}, false, fmt.Errorf("%w: %s/%s", ErrModelNotPriced, provider, model)
	}
	loaded := c.src.Loaded()
	fresh := !loaded.FetchedAt.IsZero() && c.now().Sub(loaded.FetchedAt) <= catalog.DefaultMaxStaleness && loaded.Source != catalog.SourceEmbedded
	p := Pricing{
		Provider:          provider,
		Model:             model,
		InputPerMTok:      e.Cost.Input,
		OutputPerMTok:     e.Cost.Output,
		CacheReadPerMTok:  e.Cost.CacheRead,
		CacheWritePerMTok: e.Cost.CacheWrite,
		UpdatedAt:         loaded.FetchedAt,
	}
	return p, fresh, nil
}

// Refresh forces a live fetch and overwrites the on-disk cache.  Errors
// from the underlying catalog are returned verbatim.
func (c *Catalog) Refresh(ctx context.Context) error {
	if c == nil || c.src == nil {
		return errors.New("cost: catalog not initialised")
	}
	if _, err := c.src.Refresh(ctx); err != nil {
		return err
	}
	return nil
}

// Source returns the underlying [*catalog.Catalog] handle.  Useful for
// callers (e.g. the `hygge catalog refresh` command) that want to talk
// to the catalog directly without going through the cost wrapper.
// Returns nil when the cost Catalog was constructed without a
// catalog and the lazy construction failed.
func (c *Catalog) Source() *catalog.Catalog {
	if c == nil {
		return nil
	}
	return c.src
}

// falsePtr returns a *bool pointing at false.  Local helper to avoid
// the noise of inline addr-of expressions.
func falsePtr() *bool { v := false; return &v }

// filepathDir is a tiny wrapper to avoid an explicit import in
// the cost package's options struct.  Pulled out so the cost package
// keeps its previous import set unchanged for unrelated code (lint
// won't flag an unused import if Catalog is the only consumer).
func filepathDir(p string) string {
	// We intentionally don't import path/filepath here; the cost
	// package historically only depended on a minimal stdlib set.
	// Use a hand-rolled basename split that handles both unix and
	// the legacy v0.1 cache-path string.
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[:i]
		}
	}
	return "."
}
