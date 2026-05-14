package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// defaultBaseURL is the canonical models.dev catalog host.  The full URL
// fetched is baseURL + "/api.json".
const defaultBaseURL = "https://models.dev"

// defaultTTL is the freshness window for cached catalog snapshots.  After
// this much time has passed since FetchedAt, LookUp will attempt a refresh
// on the next call.
const defaultTTL = 24 * time.Hour

// defaultHTTPTimeout caps the HTTP request when CatalogOptions.HTTPClient
// is nil.  Tests inject their own client (typically with no timeout, since
// httptest is synchronous).
const defaultHTTPTimeout = 10 * time.Second

// CatalogOptions configures a [Catalog].  The zero value is valid and
// produces a Catalog that fetches from the real models.dev, caches to
// $XDG_STATE_HOME/hygge/models_catalog.json, uses a 24-hour TTL, and uses
// time.Now for freshness checks.
type CatalogOptions struct {
	// HTTPClient is the client used for live fetches.  If nil, a client
	// with a 10-second total-request timeout is used.
	HTTPClient *http.Client

	// BaseURL overrides the models.dev host.  Empty means
	// "https://models.dev".  Tests typically point this at an httptest
	// server's URL.
	BaseURL string

	// CachePath overrides the on-disk cache file path.  Empty means
	// $XDG_STATE_HOME/hygge/models_catalog.json (with the same XDG
	// fallback rules used elsewhere in hygge).
	CachePath string

	// TTL is the freshness window for cached snapshots.  Zero means 24h.
	TTL time.Duration

	// Now is an injectable clock for testing.  Nil means time.Now.
	Now func() time.Time
}

// Snapshot is a parsed catalog plus the time it was fetched.  It is the
// in-memory and on-disk representation; the wire format from models.dev is
// normalized into this shape on parse.
//
// Providers maps provider id ("anthropic", "openai", ...) to a map of
// model id to [Pricing].
type Snapshot struct {
	FetchedAt time.Time                     `json:"fetched_at"`
	Providers map[string]map[string]Pricing `json:"providers"`
}

// Catalog provides Pricing lookups with caching and fallback.  A zero
// Catalog is not usable — construct one with [NewCatalog].
//
// Catalog is safe for concurrent use.  Concurrent LookUps that need to
// fetch coalesce into a single HTTP request (single-flight semantics).
type Catalog struct {
	httpClient *http.Client
	baseURL    string
	cachePath  string
	ttl        time.Duration
	now        func() time.Time

	mu       sync.Mutex
	snapshot *Snapshot // in-memory snapshot; nil until first LookUp/Refresh

	// fetchMu serialises concurrent live fetches.  When a goroutine
	// starts a fetch it sets inflight to a fresh channel under mu; later
	// arrivals find the channel non-nil and wait on it instead of
	// issuing their own request.
	inflight chan struct{}

	// warnedMissing tracks (provider, model) pairs we've already warned
	// about for "live catalog miss, falling back to hard-coded".  This
	// prevents log spam under high-volume LookUps.
	warnedMissing map[string]struct{}
}

// NewCatalog builds a Catalog.  Does not perform any I/O — neither the
// disk cache nor the network is touched until the first [Catalog.LookUp]
// or [Catalog.Refresh].
func NewCatalog(opts CatalogOptions) *Catalog {
	c := &Catalog{
		httpClient:    opts.HTTPClient,
		baseURL:       opts.BaseURL,
		cachePath:     opts.CachePath,
		ttl:           opts.TTL,
		now:           opts.Now,
		warnedMissing: make(map[string]struct{}),
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if c.baseURL == "" {
		c.baseURL = defaultBaseURL
	}
	if c.ttl <= 0 {
		c.ttl = defaultTTL
	}
	if c.now == nil {
		c.now = time.Now
	}
	return c
}

// LookUp returns Pricing for (provider, model).
//
// Resolution order:
//
//  1. In-memory snapshot, if it contains the model and is within TTL.
//  2. On-disk cache, if it contains the model and is within TTL — also
//     promotes the snapshot into memory.
//  3. Live fetch from models.dev.  On success the snapshot is written to
//     disk and promoted into memory.
//  4. Hard-coded fallback catalog.
//
// The returned bool is true when the Pricing came from a fresh in-memory
// snapshot, a fresh disk cache, or a successful live fetch.  It is false
// when the Pricing came from a stale snapshot/cache or from the fallback
// table — callers may use this to flag "best-effort" cost numbers in the
// UI.
//
// If every source fails (or none has an entry for the requested model),
// LookUp returns [ErrModelNotPriced].
//
// LookUp never panics, even with a corrupt cache file, malformed JSON
// from the upstream catalog, or unexpected upstream types — those
// scenarios all degrade to the next step in the cascade.
func (c *Catalog) LookUp(ctx context.Context, provider, model string) (Pricing, bool, error) {
	// 1. Try in-memory snapshot first (cheapest).
	if p, ok, fresh := c.lookupInSnapshot(provider, model, true); ok {
		return p, fresh, nil
	}

	// 2. Try disk cache.  loadDiskCache treats parse errors and missing
	//    files as "no cache" with a slog.Warn on corruption.
	if c.snapshot == nil {
		if snap := c.loadDiskCacheLocked(); snap != nil {
			c.mu.Lock()
			c.snapshot = snap
			c.mu.Unlock()
			if p, ok, fresh := c.lookupInSnapshot(provider, model, true); ok {
				return p, fresh, nil
			}
		}
	}

	// 3. Live fetch.  Single-flight: at most one goroutine fetches; the
	//    rest wait on the inflight channel.
	if err := c.ensureFresh(ctx); err != nil {
		// Live fetch failed.  If we have ANY snapshot (stale or fresh
		// in-memory after step 1's freshness re-check), try it first
		// even if stale — stale data beats fallback.
		if p, ok, _ := c.lookupInSnapshot(provider, model, false); ok {
			// Mark as not-fresh: live fetch failed AND we are
			// serving from cache.  We treat the data as fresh
			// only when both (a) the cache is within TTL AND (b)
			// we did not just fail a refresh that should have
			// touched it.  Since ensureFresh failed, we know the
			// cache is older than TTL (or we wouldn't have tried
			// to refresh), so the freshness flag must be false.
			return p, false, nil
		}
		// No usable snapshot at all.  Try fallback.
		if fb, ok := lookupFallback(provider, model); ok {
			return fb, false, nil
		}
		return Pricing{}, false, fmt.Errorf("%w: %s/%s (live fetch failed: %w)", ErrModelNotPriced, provider, model, err)
	}

	// 4. Fetch succeeded — try the now-current snapshot.
	if p, ok, fresh := c.lookupInSnapshot(provider, model, true); ok {
		return p, fresh, nil
	}

	// Live catalog parsed fine but does not contain the requested model.
	if fb, ok := lookupFallback(provider, model); ok {
		c.warnOnce(provider, model)
		return fb, false, nil
	}
	return Pricing{}, false, fmt.Errorf("%w: %s/%s (not in live catalog or fallback)", ErrModelNotPriced, provider, model)
}

// Refresh forces a live fetch and overwrites both the in-memory snapshot
// and the on-disk cache.  Used by `hygge models refresh` (not yet wired in
// v0.1).  Refresh participates in the same single-flight pool as LookUp,
// so concurrent Refresh+LookUp calls collapse to one HTTP request.
func (c *Catalog) Refresh(ctx context.Context) error {
	snap, err := c.singleFlightFetch(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.snapshot = snap
	c.mu.Unlock()
	if writeErr := c.writeDiskCache(snap); writeErr != nil {
		// A failed disk write is not fatal for Refresh — the live
		// snapshot is in memory and serves subsequent LookUps.  Log
		// and continue.
		slog.Warn("cost: failed to write catalog cache", "path", c.resolveCachePath(), "err", writeErr)
	}
	return nil
}

// lookupInSnapshot inspects the in-memory snapshot for (provider, model).
// requireFresh controls whether a stale snapshot counts as a hit:
//   - true  : only return ok if the snapshot is within TTL.
//   - false : return ok for any snapshot that contains the model.
//
// The returned fresh bool reflects the actual freshness (independent of
// the requireFresh gate).
func (c *Catalog) lookupInSnapshot(provider, model string, requireFresh bool) (Pricing, bool, bool) {
	c.mu.Lock()
	snap := c.snapshot
	c.mu.Unlock()

	if snap == nil {
		return Pricing{}, false, false
	}
	fresh := c.now().Sub(snap.FetchedAt) <= c.ttl
	if requireFresh && !fresh {
		return Pricing{}, false, false
	}
	mods, ok := snap.Providers[provider]
	if !ok {
		return Pricing{}, false, fresh
	}
	if p, ok := mods[model]; ok {
		// Stamp the caller-asked-for model id on the returned Pricing
		// (the snapshot's value already does this for live data).
		p.Model = model
		p.Provider = provider
		return p, true, fresh
	}
	return Pricing{}, false, fresh
}

// ensureFresh kicks off a fetch if the in-memory snapshot is missing or
// stale.  Uses single-flight: only one fetch is in flight at a time, and
// late arrivals block until it completes.
func (c *Catalog) ensureFresh(ctx context.Context) error {
	c.mu.Lock()
	if c.snapshot != nil && c.now().Sub(c.snapshot.FetchedAt) <= c.ttl {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	snap, err := c.singleFlightFetch(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.snapshot = snap
	c.mu.Unlock()
	if writeErr := c.writeDiskCache(snap); writeErr != nil {
		slog.Warn("cost: failed to write catalog cache", "path", c.resolveCachePath(), "err", writeErr)
	}
	return nil
}

// singleFlightFetch issues a fetch if none is in flight, otherwise waits
// for the in-flight one and returns the current snapshot.  The leader
// goroutine performs the fetch and stores the result; waiters re-read the
// snapshot under the lock after the leader signals completion.
func (c *Catalog) singleFlightFetch(ctx context.Context) (*Snapshot, error) {
	c.mu.Lock()
	if c.inflight != nil {
		// A fetch is in flight — wait for it.
		ch := c.inflight
		c.mu.Unlock()
		select {
		case <-ch:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		c.mu.Lock()
		snap := c.snapshot
		c.mu.Unlock()
		if snap == nil {
			return nil, errors.New("cost: leader fetch failed")
		}
		return snap, nil
	}
	// We are the leader.
	c.inflight = make(chan struct{})
	c.mu.Unlock()

	snap, err := fetchAndParse(ctx, c.httpClient, c.baseURL, c.now())

	c.mu.Lock()
	if err == nil {
		c.snapshot = snap
	}
	ch := c.inflight
	c.inflight = nil
	c.mu.Unlock()
	close(ch)

	if err != nil {
		return nil, err
	}
	return snap, nil
}

// loadDiskCacheLocked reads and parses the on-disk cache file.  Returns
// nil on any failure (missing file, corrupt JSON, etc.) and emits a
// slog.Warn for corruption.  Does NOT mutate c.snapshot — callers do that
// themselves under c.mu.
//
// The "Locked" suffix is a misnomer left over from an earlier draft; this
// method does its own short-lived file I/O and takes no lock itself.  Kept
// for symmetry with similar helpers.
func (c *Catalog) loadDiskCacheLocked() *Snapshot {
	path := c.resolveCachePath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // intentional: XDG state path
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("cost: failed to read catalog cache", "path", path, "err", err)
		}
		return nil
	}
	if len(data) == 0 {
		slog.Warn("cost: catalog cache is empty", "path", path)
		return nil
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		slog.Warn("cost: catalog cache is corrupt; will refetch", "path", path, "err", err)
		return nil
	}
	if snap.Providers == nil {
		// Treat as empty rather than crashing on later map access.
		snap.Providers = map[string]map[string]Pricing{}
	}
	return &snap
}

// writeDiskCache atomically writes the snapshot to disk.  Uses the same
// temp-file-plus-rename pattern as internal/state.  A failure here is not
// fatal: the snapshot is already in memory and the next process restart
// will simply re-fetch.
func (c *Catalog) writeDiskCache(snap *Snapshot) error {
	path := c.resolveCachePath()
	if path == "" {
		return errors.New("cost: cannot resolve cache path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cost: create dir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return fmt.Errorf("cost: marshal snapshot: %w", err)
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // 0o600 intentional
	if err != nil {
		return fmt.Errorf("cost: open tmp %s: %w", tmp, err)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()
	if writeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cost: write tmp: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cost: sync tmp: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cost: close tmp: %w", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cost: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// resolveCachePath returns the absolute path of the on-disk cache file.
// Honors CatalogOptions.CachePath if set, else falls back to
// $XDG_STATE_HOME/hygge/models_catalog.json (with the same XDG fallback
// rules used in internal/state).
func (c *Catalog) resolveCachePath() string {
	if c.cachePath != "" {
		return c.cachePath
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "hygge", "models_catalog.json")
}

// warnOnce emits a slog.Warn the first time we observe a (provider, model)
// pair that is missing from the live catalog but available in the
// fallback.  Subsequent observations are silent.
func (c *Catalog) warnOnce(provider, model string) {
	key := provider + "/" + model
	c.mu.Lock()
	if _, seen := c.warnedMissing[key]; seen {
		c.mu.Unlock()
		return
	}
	c.warnedMissing[key] = struct{}{}
	c.mu.Unlock()
	slog.Warn("cost: model missing from live catalog; using fallback pricing",
		"provider", provider, "model", model)
}
