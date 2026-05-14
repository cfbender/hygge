package cost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixtureServer returns an httptest server that serves a small canned
// catalog and an atomic counter of how many times /api.json was hit.
func fixtureServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	body := `{
      "anthropic": {
        "id": "anthropic",
        "models": {
          "claude-sonnet-4-5": {
            "cost": {"input": 3.0, "output": 15.0, "cache_read": 0.3, "cache_write": 3.75}
          },
          "claude-zenith-9": {
            "cost": {"input": 0.5, "output": 2.5}
          }
        }
      }
    }`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestLookUp_LiveFetchPopulatesAndPersistsCache(t *testing.T) {
	t.Parallel()

	srv, hits := fixtureServer(t)
	cachePath := tempCachePath(t)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  cachePath,
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true on first live fetch, got false")
	}
	if p.InputPerMTok != 3.0 || p.OutputPerMTok != 15.0 {
		t.Errorf("pricing mismatch: %+v", p)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected 1 HTTP hit, got %d", got)
	}

	// Disk cache must exist with correct content.
	data, err := os.ReadFile(cachePath) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read cache: %v", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("cache JSON: %v", err)
	}
	if _, ok := snap.Providers["anthropic"]["claude-sonnet-4-5"]; !ok {
		t.Errorf("cache missing sonnet entry: %+v", snap)
	}
}

func TestLookUp_InMemoryCacheServesSecondCall(t *testing.T) {
	t.Parallel()

	srv, hits := fixtureServer(t)
	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	for i := 0; i < 3; i++ {
		_, _, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
		if err != nil {
			t.Fatalf("LookUp %d: %v", i, err)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected exactly 1 HTTP hit (memory cache), got %d", got)
	}
}

func TestLookUp_FreshDiskCacheNoHTTP(t *testing.T) {
	t.Parallel()

	cachePath := tempCachePath(t)

	// Pre-populate disk cache with a known snapshot, FetchedAt = now.
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	snap := Snapshot{
		FetchedAt: now,
		Providers: map[string]map[string]Pricing{
			"anthropic": {
				"claude-sonnet-4-5": {
					Provider:      "anthropic",
					Model:         "claude-sonnet-4-5",
					InputPerMTok:  3.0,
					OutputPerMTok: 15.0,
					UpdatedAt:     now,
				},
			},
		},
	}
	writeSnapshotFile(t, cachePath, snap)

	// HTTP server that FAILS the test if hit.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Errorf("unexpected HTTP call when disk cache is fresh")
	}))
	t.Cleanup(srv.Close)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  cachePath,
		Now:        func() time.Time { return now.Add(1 * time.Hour) },
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true from fresh disk cache")
	}
	if p.InputPerMTok != 3.0 {
		t.Errorf("pricing mismatch: %+v", p)
	}
}

func TestLookUp_StaleDiskCacheTriggersHTTP(t *testing.T) {
	t.Parallel()

	cachePath := tempCachePath(t)

	staleTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	snap := Snapshot{
		FetchedAt: staleTime,
		Providers: map[string]map[string]Pricing{
			"anthropic": {
				"claude-sonnet-4-5": {InputPerMTok: 999}, // bogus value to detect re-fetch
			},
		},
	}
	writeSnapshotFile(t, cachePath, snap)

	srv, hits := fixtureServer(t)
	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  cachePath,
		TTL:        1 * time.Hour,
		Now:        func() time.Time { return staleTime.Add(2 * time.Hour) },
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true after successful refetch")
	}
	if p.InputPerMTok != 3.0 {
		t.Errorf("expected refetched value 3.0, got %v (stale data leaked through?)", p.InputPerMTok)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected 1 HTTP hit on stale-cache refetch, got %d", got)
	}
}

func TestLookUp_FallbackWhenFetchFailsAndNoCache(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	t.Cleanup(srv.Close)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if fresh {
		t.Errorf("expected fresh=false on fallback")
	}
	// Fallback prices for sonnet are 3/15/0.3/3.75.
	if p.InputPerMTok != 3.0 || p.OutputPerMTok != 15.0 || p.CacheReadPerMTok != 0.3 || p.CacheWritePerMTok != 3.75 {
		t.Errorf("fallback pricing mismatch: %+v", p)
	}
}

func TestLookUp_NoFallbackNoCacheFetchFails_ReturnsErrModelNotPriced(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	t.Cleanup(srv.Close)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	_, _, err := c.LookUp(context.Background(), "totally-fake-provider", "totally-fake-model")
	if !errors.Is(err, ErrModelNotPriced) {
		t.Fatalf("expected ErrModelNotPriced, got %v", err)
	}
}

func TestLookUp_CorruptCacheFallsThroughToFetch(t *testing.T) {
	t.Parallel()

	cachePath := tempCachePath(t)
	if err := os.WriteFile(cachePath, []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt cache: %v", err)
	}
	srv, hits := fixtureServer(t)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  cachePath,
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true after recovery via fetch")
	}
	if p.InputPerMTok != 3.0 {
		t.Errorf("pricing mismatch: %+v", p)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected 1 HTTP hit, got %d", got)
	}
}

func TestLookUp_UnknownModelLivePresentInFallback(t *testing.T) {
	t.Parallel()

	srv, _ := fixtureServer(t)
	// fixtureServer's catalog has only "claude-sonnet-4-5" + "claude-zenith-9".
	// Ask for claude-opus-4-5 — not in live, but IS in fallback.
	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-opus-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if fresh {
		t.Errorf("expected fresh=false (fallback path)")
	}
	// Opus fallback: 15/75/1.5/18.75.
	if p.InputPerMTok != 15.0 || p.OutputPerMTok != 75.0 {
		t.Errorf("fallback opus pricing mismatch: %+v", p)
	}
}

func TestLookUp_ConcurrentSingleFlight(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64
	body := `{"anthropic":{"models":{"claude-sonnet-4-5":{"cost":{"input":3.0,"output":15.0}}}}}`

	// Add a small artificial delay so concurrent goroutines pile up on
	// the in-flight channel rather than each finishing before the next
	// arrives.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	const N = 8
	var wg sync.WaitGroup
	wg.Add(N)
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			_, _, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent LookUp error: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("expected 1 HTTP hit under single-flight, got %d", got)
	}
}

func TestRefresh_ForcesRefetchWithinTTL(t *testing.T) {
	t.Parallel()

	srv, hits := fixtureServer(t)
	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	if _, _, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5"); err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if got := hits.Load(); got != 1 {
		t.Errorf("after first LookUp: hits=%d, want 1", got)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if got := hits.Load(); got != 2 {
		t.Errorf("after Refresh: hits=%d, want 2", got)
	}
}

func TestLookUp_RelaxedDotDashMatch(t *testing.T) {
	t.Parallel()

	// models.dev uses "claude-sonnet-4-5"; hygge asks for "claude-sonnet-4-5".
	body := `{"anthropic":{"models":{"claude-sonnet-4-5":{"cost":{"input":3.0,"output":15.0}}}}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  tempCachePath(t),
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true on relaxed-match live hit")
	}
	if p.Model != "claude-sonnet-4-5" {
		t.Errorf("returned Pricing.Model = %q, want caller's spelling %q", p.Model, "claude-sonnet-4-5")
	}
	if p.InputPerMTok != 3.0 {
		t.Errorf("expected live data, got %+v", p)
	}
}

func TestLookUp_FetchFailsWithStaleCache_ReturnsStaleNotFresh(t *testing.T) {
	t.Parallel()

	cachePath := tempCachePath(t)
	staleTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	snap := Snapshot{
		FetchedAt: staleTime,
		Providers: map[string]map[string]Pricing{
			"anthropic": {
				"claude-sonnet-4-5": {InputPerMTok: 42, OutputPerMTok: 99},
			},
		},
	}
	writeSnapshotFile(t, cachePath, snap)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	t.Cleanup(srv.Close)

	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  cachePath,
		TTL:        1 * time.Hour,
		Now:        func() time.Time { return staleTime.Add(2 * time.Hour) },
	})

	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if fresh {
		t.Errorf("expected fresh=false when fetch fails and only stale cache exists")
	}
	if p.InputPerMTok != 42 {
		t.Errorf("expected stale value to be served, got %+v", p)
	}
}

func TestNewCatalog_DefaultsAreSane(t *testing.T) {
	t.Parallel()

	c := NewCatalog(CatalogOptions{})
	if c.baseURL != defaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, defaultBaseURL)
	}
	if c.ttl != defaultTTL {
		t.Errorf("ttl = %v, want %v", c.ttl, defaultTTL)
	}
	if c.now == nil {
		t.Errorf("now is nil")
	}
	if c.httpClient == nil {
		t.Errorf("httpClient is nil")
	}
	// resolveCachePath should yield a non-empty path on this test host.
	if got := c.resolveCachePath(); got == "" {
		t.Errorf("resolveCachePath returned empty string")
	}
}

func TestResolveCachePath_XDGOverride(t *testing.T) {
	t.Parallel()

	// Use an explicit CachePath override — most robust against env.
	override := filepath.Join(t.TempDir(), "cache.json")
	c := NewCatalog(CatalogOptions{CachePath: override})
	if got := c.resolveCachePath(); got != override {
		t.Errorf("resolveCachePath = %q, want %q", got, override)
	}
}

func TestWarnOnce_DoesNotPanicOnRepeats(t *testing.T) {
	t.Parallel()

	c := NewCatalog(CatalogOptions{CachePath: tempCachePath(t)})
	for i := 0; i < 5; i++ {
		c.warnOnce("provider-x", "model-y")
	}
	// If we got here, no panic.
}

func TestFallback_KnownAnthropicModels(t *testing.T) {
	t.Parallel()

	for _, m := range []string{"claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5"} {
		p, ok := lookupFallback("anthropic", m)
		if !ok {
			t.Errorf("fallback missing %q", m)
			continue
		}
		if p.InputPerMTok <= 0 || p.OutputPerMTok <= 0 {
			t.Errorf("fallback %q has non-positive pricing: %+v", m, p)
		}
		if p.Provider != "anthropic" || p.Model != m {
			t.Errorf("fallback %q wrong provider/model: %+v", m, p)
		}
	}

	if _, ok := lookupFallback("anthropic", "no-such-model"); ok {
		t.Errorf("fallback returned ok for nonexistent model")
	}
	if _, ok := lookupFallback("no-such-provider", "claude-sonnet-4-5"); ok {
		t.Errorf("fallback returned ok for nonexistent provider")
	}
}

// writeSnapshotFile is a test helper that writes a Snapshot to disk in the
// same format the catalog itself uses.
func writeSnapshotFile(t *testing.T, path string, snap Snapshot) {
	t.Helper()
	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// Compile-time assertion that ErrModelNotPriced formats sensibly.
var _ = fmt.Sprintf("%v", ErrModelNotPriced)
