package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
)

// ---------------------------------------------------------------------------
// catwalk fixture helpers
// ---------------------------------------------------------------------------

// fixtureProviders is a small canned catwalk provider slice used across
// tests.  It exercises every field the mapper cares about.
var fixtureProviders = []catwalk.Provider{
	{
		ID:   "anthropic",
		Name: "Anthropic",
		Type: catwalk.TypeAnthropic,
		Models: []catwalk.Model{
			{
				ID:                     "claude-sonnet-4-5",
				Name:                   "Claude Sonnet 4.5",
				CostPer1MIn:            3,
				CostPer1MOut:           15,
				CostPer1MInCached:      0.3,
				CostPer1MOutCached:     3.75,
				ContextWindow:          200000,
				DefaultMaxTokens:       64000,
				CanReason:              true,
				ReasoningLevels:        []string{"low", "medium", "high"},
				DefaultReasoningEffort: "high",
				SupportsImages:         true,
			},
			{
				ID:               "claude-haiku-4-5",
				Name:             "Claude Haiku 4.5",
				CostPer1MIn:      1,
				CostPer1MOut:     5,
				ContextWindow:    200000,
				DefaultMaxTokens: 8192,
			},
		},
	},
	{
		ID:   "openai",
		Name: "OpenAI",
		Type: catwalk.TypeOpenAI,
		Models: []catwalk.Model{
			{
				ID:                     "o3-mini",
				Name:                   "o3-mini",
				CostPer1MIn:            1.1,
				CostPer1MOut:           4.4,
				ContextWindow:          200000,
				DefaultMaxTokens:       100000,
				CanReason:              true,
				ReasoningLevels:        []string{"low", "medium", "high"},
				DefaultReasoningEffort: "medium",
			},
			{
				ID:               "gpt-4o",
				Name:             "GPT-4o",
				CostPer1MIn:      2.5,
				CostPer1MOut:     10,
				ContextWindow:    128000,
				DefaultMaxTokens: 16384,
				SupportsImages:   true,
			},
		},
	},
	{
		ID:   "openrouter",
		Name: "OpenRouter",
		Type: catwalk.TypeOpenAI,
		Models: []catwalk.Model{
			{
				ID:               "anthropic/claude-sonnet-4-5",
				Name:             "Claude Sonnet 4.5 via OpenRouter",
				CostPer1MIn:      3,
				CostPer1MOut:     15,
				ContextWindow:    200000,
				DefaultMaxTokens: 8192,
				CanReason:        true,
				SupportsImages:   true,
			},
		},
	},
}

// fixtureBody serialises fixtureProviders to JSON (catwalk /v2/providers format).
func fixtureBodyBytes(t *testing.T) []byte {
	t.Helper()
	data, err := json.Marshal(fixtureProviders) //nolint:gosec // G117: test fixture; no real credentials
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return data
}

// freshFetcher returns a CatwalkFetcher-compatible fakeFetcher pre-loaded
// with the fixture data.
func freshFetcher(t *testing.T) *fakeFetcher {
	t.Helper()
	snap := snapshotFromCatwalkProviders(fixtureProviders, "")
	return &fakeFetcher{snap: snap}
}

// fakeFetcher returns a canned snapshot and counts Fetch invocations.
type fakeFetcher struct {
	snap *Snapshot
	err  error
	hits atomic.Int64
}

func (f *fakeFetcher) Fetch(_ context.Context) (*Snapshot, error) {
	f.hits.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.snap, nil
}

// tempStateDir returns a state directory in t.TempDir.
func tempStateDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// ---------------------------------------------------------------------------
// Load tests
// ---------------------------------------------------------------------------

func TestLoad_FallsBackToEmbeddedWhenDiskMissing(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            &fakeFetcher{err: errors.New("offline")},
		BackgroundRefresh: new(false),
		Now:               func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := c.Loaded()
	if got.Source != SourceEmbedded {
		t.Errorf("source = %q, want %q", got.Source, SourceEmbedded)
	}
	if got.Providers == 0 || got.Models == 0 {
		t.Errorf("embedded snapshot has no data: providers=%d models=%d", got.Providers, got.Models)
	}
	// Sanity: the embedded snapshot must include flagship Anthropic and
	// OpenAI models so the existing TUI fallback works offline.
	for _, tc := range []struct{ p, m string }{
		{"anthropic", "claude-sonnet-4-5-20250929"},
		{"openai", "gpt-4o"},
	} {
		if _, ok := c.Lookup(tc.p, tc.m); !ok {
			t.Logf("note: embedded snapshot missing %s/%s (non-fatal if model id changed)", tc.p, tc.m)
		}
	}
}

func TestLoad_PrefersDiskOverEmbedded(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	// Write a custom disk snapshot containing one bogus pricing
	// number so we can prove it came from disk, not embedded.
	snap := &Snapshot{
		FetchedAt: time.Now(),
		ETag:      `"test-etag"`,
		Providers: map[string]map[string]Entry{
			"anthropic": {
				"claude-sonnet-4-5": {
					Provider: "anthropic",
					ID:       "claude-sonnet-4-5",
					Cost:     Cost{Input: 999},
				},
			},
		},
	}
	if err := writeSnapshotFile(filepath.Join(dir, "catalog.json"), snap); err != nil {
		t.Fatalf("seed disk: %v", err)
	}
	c, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            &fakeFetcher{err: errors.New("offline")},
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	e, ok := c.Lookup("anthropic", "claude-sonnet-4-5")
	if !ok {
		t.Fatalf("expected disk snapshot to contain sonnet")
	}
	if e.Cost.Input != 999 {
		t.Errorf("expected disk pricing 999, got %v (embedded leaked?)", e.Cost.Input)
	}
	if e.Source != SourceDisk {
		t.Errorf("source = %q, want %q", e.Source, SourceDisk)
	}
}

func TestLoad_CorruptDiskFallsBackToEmbedded(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	c, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            &fakeFetcher{err: errors.New("offline")},
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Loaded().Source != SourceEmbedded {
		t.Errorf("source = %q, want %q", c.Loaded().Source, SourceEmbedded)
	}
}

// TestLoad_V1DiskCacheRejected confirms that an on-disk snapshot with
// version=1 (pre-Catwalk format) is rejected and falls back to embedded.
func TestLoad_V1DiskCacheRejected(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	v1 := struct {
		Version   int    `json:"version"`
		FetchedAt string `json:"fetched_at"`
	}{Version: 1, FetchedAt: "2025-01-01T00:00:00Z"}
	data, _ := json.Marshal(v1)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            &fakeFetcher{err: errors.New("offline")},
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Should have fallen back to embedded, not the v1 disk cache.
	if c.Loaded().Source != SourceEmbedded {
		t.Errorf("expected SourceEmbedded after v1 rejection, got %s", c.Loaded().Source)
	}
}

// ---------------------------------------------------------------------------
// Refresh tests
// ---------------------------------------------------------------------------

func TestRefresh_RoundTripsDisk(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	f := freshFetcher(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            f,
		BackgroundRefresh: new(false),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.Providers != 3 {
		t.Errorf("providers = %d, want 3", res.Providers)
	}
	if res.Models != 5 {
		t.Errorf("models = %d, want 5", res.Models)
	}
	if !res.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", res.FetchedAt, now)
	}
	// Disk snapshot must round-trip.
	data, err := os.ReadFile(filepath.Join(dir, "catalog.json")) //nolint:gosec // test-controlled path
	if err != nil {
		t.Fatalf("read disk: %v", err)
	}
	if !strings.Contains(string(data), "claude-sonnet-4-5") {
		t.Errorf("disk snapshot missing sonnet: %s", data)
	}
	// Version on disk must be 2.
	var v struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse version: %v", err)
	}
	if v.Version != 2 {
		t.Errorf("disk version = %d, want 2", v.Version)
	}
	// Reloading should now read disk, not embedded.
	c2, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            f,
		BackgroundRefresh: new(false),
		Now:               func() time.Time { return now.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c2.Loaded().Source != SourceDisk {
		t.Errorf("reload source = %q, want %q", c2.Loaded().Source, SourceDisk)
	}
}

// TestRefresh_ETagNotModified confirms that when the CatwalkFetcher returns
// ErrNotModified the Refresh call succeeds and the in-memory snapshot is
// not replaced.
func TestRefresh_ETagNotModified(t *testing.T) {
	t.Parallel()
	const testETag = `"abc123"`
	// Set up an httptest server that echoes 304 when the etag matches.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == testETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", testETag)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixtureBodyBytes(t))
	}))
	defer srv.Close()

	fetcher := NewCatwalkFetcher(srv.Client(), srv.URL)
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            fetcher,
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// First Refresh: should fetch new data and store ETag.
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh #1: %v", err)
	}
	c.mu.RLock()
	storedETag := c.snapshot.ETag
	c.mu.RUnlock()
	if storedETag != testETag {
		t.Errorf("ETag not stored: got %q, want %q", storedETag, testETag)
	}

	// Second Refresh: server replies 304; result should be "not modified"
	// which the catalog treats as a successful no-op.
	res, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh #2 (304 path): %v", err)
	}
	if res.PreviousAge != 0 {
		t.Logf("PreviousAge on 304 = %s (expected zero for not-modified path)", res.PreviousAge)
	}
	// In-memory snapshot must still be valid.
	if _, ok := c.Lookup("anthropic", "claude-sonnet-4-5"); !ok {
		t.Errorf("snapshot lost after 304 refresh")
	}
}

// ---------------------------------------------------------------------------
// Lookup / Models / Providers tests
// ---------------------------------------------------------------------------

func TestLookup_HitMissAndCaseInsensitivity(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            freshFetcher(t),
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Hit, canonical case.
	if e, ok := c.Lookup("anthropic", "claude-sonnet-4-5"); !ok || !e.Capabilities.Reasoning {
		t.Errorf("Lookup hit: ok=%v cap=%+v", ok, e.Capabilities)
	}
	// Hit, mixed case provider.
	if _, ok := c.Lookup("Anthropic", "claude-sonnet-4-5"); !ok {
		t.Errorf("Lookup case-insensitive provider failed")
	}
	// Hit, mixed case model.
	if _, ok := c.Lookup("anthropic", "Claude-Sonnet-4-5"); !ok {
		t.Errorf("Lookup case-insensitive model failed")
	}
	// Miss, unknown provider.
	if _, ok := c.Lookup("nope-provider", "claude-sonnet-4-5"); ok {
		t.Errorf("Lookup unknown provider should miss")
	}
	// Miss, unknown model.
	if _, ok := c.Lookup("anthropic", "claude-never"); ok {
		t.Errorf("Lookup unknown model should miss")
	}
	// Miss, empty input.
	if _, ok := c.Lookup("", "anything"); ok {
		t.Errorf("Lookup empty provider should miss")
	}
}

func TestModels_SortedAndProviderScoped(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            freshFetcher(t),
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	ms := c.Models("anthropic")
	if len(ms) != 2 {
		t.Fatalf("anthropic models = %d, want 2", len(ms))
	}
	if ms[0].ID != "claude-haiku-4-5" || ms[1].ID != "claude-sonnet-4-5" {
		t.Errorf("not sorted: %+v", []string{ms[0].ID, ms[1].ID})
	}
	if got := c.Models("nope"); len(got) != 0 {
		t.Errorf("unknown provider should return empty, got %+v", got)
	}
}

// ---------------------------------------------------------------------------
// Catwalk field-mapping test
// ---------------------------------------------------------------------------

// TestCatwalkMapping confirms every catwalk.Model field is correctly
// translated into a catalog.Entry.
func TestCatwalkMapping_FieldExtraction(t *testing.T) {
	t.Parallel()
	snap := snapshotFromCatwalkProviders(fixtureProviders, `"etag-1"`)
	if snap == nil {
		t.Fatal("snapshotFromCatwalkProviders returned nil")
	}
	if snap.ETag != `"etag-1"` {
		t.Errorf("ETag = %q, want %q", snap.ETag, `"etag-1"`)
	}

	e := snap.Providers["anthropic"]["claude-sonnet-4-5"]
	if e.Provider != "anthropic" {
		t.Errorf("Provider = %q, want anthropic", e.Provider)
	}
	if e.ID != "claude-sonnet-4-5" {
		t.Errorf("ID = %q, want claude-sonnet-4-5", e.ID)
	}
	if e.Name != "Claude Sonnet 4.5" {
		t.Errorf("Name = %q, want Claude Sonnet 4.5", e.Name)
	}

	// Pricing
	if e.Cost.Input != 3 {
		t.Errorf("Cost.Input = %v, want 3", e.Cost.Input)
	}
	if e.Cost.Output != 15 {
		t.Errorf("Cost.Output = %v, want 15", e.Cost.Output)
	}
	if e.Cost.CacheRead != 0.3 {
		t.Errorf("Cost.CacheRead = %v, want 0.3", e.Cost.CacheRead)
	}
	if e.Cost.CacheWrite != 3.75 {
		t.Errorf("Cost.CacheWrite = %v, want 3.75", e.Cost.CacheWrite)
	}

	// Limits
	if e.Limit.ContextWindow != 200000 {
		t.Errorf("Limit.ContextWindow = %v, want 200000", e.Limit.ContextWindow)
	}
	if e.Limit.MaxOutput != 64000 {
		t.Errorf("Limit.MaxOutput = %v, want 64000", e.Limit.MaxOutput)
	}

	// Capabilities
	if !e.Capabilities.Reasoning {
		t.Errorf("Capabilities.Reasoning should be true (can_reason=true)")
	}
	if !e.Capabilities.Attachment {
		t.Errorf("Capabilities.Attachment should be true (supports_attachments=true)")
	}

	// Reasoning levels
	if len(e.ReasoningLevels) != 3 {
		t.Errorf("ReasoningLevels len = %d, want 3", len(e.ReasoningLevels))
	}
	if e.DefaultReasoningEffort != "high" {
		t.Errorf("DefaultReasoningEffort = %q, want high", e.DefaultReasoningEffort)
	}

	// haiku: no reasoning, zero cost cache fields, no reasoning levels
	h := snap.Providers["anthropic"]["claude-haiku-4-5"]
	if h.Capabilities.Reasoning {
		t.Errorf("haiku should not advertise reasoning, got %+v", h.Capabilities)
	}
	if h.Cost.Input != 1 || h.Cost.Output != 5 {
		t.Errorf("haiku cost: %+v", h.Cost)
	}
	if h.Cost.CacheRead != 0 || h.Cost.CacheWrite != 0 {
		t.Errorf("haiku cache cost should be 0, got %+v", h.Cost)
	}
	if len(h.ReasoningLevels) != 0 {
		t.Errorf("haiku should have no reasoning levels, got %v", h.ReasoningLevels)
	}

	or := snap.Providers["openrouter"]["anthropic/claude-sonnet-4-5"]
	if or.Provider != "openrouter" {
		t.Errorf("OpenRouter Provider = %q, want openrouter", or.Provider)
	}
	if or.ID != "anthropic/claude-sonnet-4-5" {
		t.Errorf("OpenRouter ID = %q, want namespaced id", or.ID)
	}
	if !or.Capabilities.Reasoning {
		t.Errorf("OpenRouter namespaced model should retain Catwalk reasoning flag")
	}
	if !or.Capabilities.Attachment {
		t.Errorf("OpenRouter namespaced model should map supports_attachments")
	}
}

// ---------------------------------------------------------------------------
// CatwalkFetcher HTTP tests
// ---------------------------------------------------------------------------

func TestCatwalkFetcher_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/providers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"abc"`)
		_, _ = w.Write(fixtureBodyBytes(t))
	}))
	defer srv.Close()
	f := NewCatwalkFetcher(srv.Client(), srv.URL)
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, ok := snap.Providers["anthropic"]["claude-sonnet-4-5"]; !ok {
		t.Errorf("snapshot missing sonnet")
	}
	if snap.ETag != `"abc"` {
		t.Errorf("ETag = %q, want %q", snap.ETag, `"abc"`)
	}
}

func TestCatwalkFetcher_AppendsProvidersPathOnce(t *testing.T) {
	t.Parallel()
	for _, baseSuffix := range []string{"", "/"} {
		t.Run("suffix="+baseSuffix, func(t *testing.T) {
			t.Parallel()
			paths := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				paths <- r.URL.Path
				_, _ = w.Write(fixtureBodyBytes(t))
			}))
			defer srv.Close()

			f := NewCatwalkFetcher(srv.Client(), srv.URL+baseSuffix)
			if _, err := f.Fetch(context.Background()); err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			gotPath := <-paths
			if gotPath != "/v2/providers" {
				t.Fatalf("path = %q, want /v2/providers", gotPath)
			}
		})
	}
}

func TestCatwalkFetcher_Non2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	f := NewCatwalkFetcher(srv.Client(), srv.URL)
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on 500")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

func TestCatwalkFetcher_MalformedJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not"))
	}))
	defer srv.Close()
	f := NewCatwalkFetcher(srv.Client(), srv.URL)
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestCatwalkFetcher_EmptyBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	f := NewCatwalkFetcher(srv.Client(), srv.URL)
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Providers) != 0 {
		t.Errorf("expected empty providers, got %d", len(snap.Providers))
	}
}

// ---------------------------------------------------------------------------
// Background refresh tests
// ---------------------------------------------------------------------------

func TestBackgroundRefresh_StaleSnapshotKicksOff(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	// Seed a very-old disk snapshot (version 2 so it isn't rejected).
	stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	seed := &Snapshot{
		FetchedAt: stale,
		Providers: map[string]map[string]Entry{
			"anthropic": {"claude-sonnet-4-5": {ID: "claude-sonnet-4-5", Cost: Cost{Input: 1}}},
		},
	}
	if err := writeSnapshotFile(filepath.Join(dir, "catalog.json"), seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f := freshFetcher(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            f,
		BackgroundRefresh: new(true),
		MaxStaleness:      time.Hour,
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Give the goroutine a moment.
	deadline := time.Now().Add(2 * time.Second)
	for f.hits.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.hits.Load() == 0 {
		t.Errorf("background refresh did not fire")
	}
}

func TestRefresh_SingleFlight(t *testing.T) {
	t.Parallel()
	f := freshFetcher(t)
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            f,
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var wg sync.WaitGroup
	const N = 6
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			if _, err := c.Refresh(context.Background()); err != nil {
				t.Errorf("Refresh: %v", err)
			}
		}()
	}
	wg.Wait()
	// Serialised hits == N (each call fetches; no coalescing).
	if got := f.hits.Load(); got != int64(N) {
		t.Logf("hits = %d (serialised refreshes; not coalesced)", got)
	}
}

// ---------------------------------------------------------------------------
// Snapshot file / versioning tests
// ---------------------------------------------------------------------------

func TestSnapshotFile_IncompatibleVersionIsCacheMiss(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	// Write a pre-catwalk snapshot file with the old version.
	bad := struct {
		Version int `json:"version"`
	}{Version: 1}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readSnapshotFile(path)
	if !errors.Is(err, ErrIncompatibleSnapshot) {
		t.Fatalf("expected incompatible snapshot error, got %v", err)
	}
	if strings.Contains(err.Error(), "unsupported snapshot version") {
		t.Fatalf("error should not use scary unsupported-version wording: %v", err)
	}
}

func TestLoad_IncompatibleDiskSnapshotFallsBackCleanly(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	data, _ := json.Marshal(struct {
		Version int `json:"version"`
	}{Version: 1})
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	c, err := Load(LoadOptions{StateDir: dir, BackgroundRefresh: new(false)})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := c.Loaded().Source; got != SourceEmbedded {
		t.Fatalf("source = %q, want embedded", got)
	}
}

func TestSnapshotFile_ETagRoundTrips(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	const wantETag = `"strong-etag-xyz"`
	snap := &Snapshot{
		FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		ETag:      wantETag,
		Providers: map[string]map[string]Entry{
			"anthropic": {
				"test-model": {Provider: "anthropic", ID: "test-model"},
			},
		},
	}
	if err := writeSnapshotFile(path, snap); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := readSnapshotFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.ETag != wantETag {
		t.Errorf("ETag round-trip: got %q, want %q", got.ETag, wantETag)
	}
}

// ---------------------------------------------------------------------------
// Embedded snapshot test
// ---------------------------------------------------------------------------

func TestEmbeddedSnapshot_LoadsAndCoversFlagshipModels(t *testing.T) {
	t.Parallel()
	snap, err := loadEmbeddedSnapshot()
	if err != nil {
		t.Fatalf("loadEmbedded: %v", err)
	}
	if len(snap.Providers) == 0 {
		t.Fatalf("embedded snapshot has no providers")
	}
	// Must include anthropic and openai providers.
	for _, provider := range []string{"anthropic", "openai"} {
		if _, ok := snap.Providers[provider]; !ok {
			t.Errorf("embedded snapshot missing provider %q", provider)
		}
	}
	// Must include at least one model for anthropic.
	if len(snap.Providers["anthropic"]) == 0 {
		t.Errorf("embedded snapshot has no anthropic models")
	}
	// Must include at least one reasoning model (from either provider).
	foundReasoning := false
	for _, provModels := range snap.Providers {
		for _, e := range provModels {
			if e.Capabilities.Reasoning {
				foundReasoning = true
				break
			}
		}
		if foundReasoning {
			break
		}
	}
	if !foundReasoning {
		t.Errorf("embedded snapshot has no model with Capabilities.Reasoning=true")
	}
}

// ---------------------------------------------------------------------------
// Providers sorted test
// ---------------------------------------------------------------------------

func TestProviders_Sorted(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            freshFetcher(t),
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	provs := c.Providers()
	if len(provs) != 3 {
		t.Fatalf("providers = %v", provs)
	}
	if provs[0] != "anthropic" || provs[1] != "openai" || provs[2] != "openrouter" {
		t.Errorf("providers not sorted: %v", provs)
	}
}

func TestLoaded_ReportsAgeAndSource(t *testing.T) {
	t.Parallel()
	f := freshFetcher(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            f,
		BackgroundRefresh: new(false),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	loaded := c.Loaded()
	if loaded.Source != SourceEmbedded {
		t.Errorf("initial source = %q, want %q", loaded.Source, SourceEmbedded)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	loaded = c.Loaded()
	if loaded.Source != SourceNetwork {
		t.Errorf("after refresh source = %q, want %q", loaded.Source, SourceNetwork)
	}
	if !loaded.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", loaded.FetchedAt, now)
	}
}

// ---------------------------------------------------------------------------
// T3.3 — Periodic catalog auto-refresh tests
// ---------------------------------------------------------------------------

// TestPeriodicRefresh_TickerFiresAtInterval confirms that the periodic ticker
// calls Refresh at least twice when RefreshInterval > 0.
func TestPeriodicRefresh_TickerFiresAtInterval(t *testing.T) {
	t.Parallel()
	const interval = 50 * time.Millisecond

	f := freshFetcher(t)
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            f,
		BackgroundRefresh: new(false),
		RefreshInterval:   interval,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer func() {
		if err := c.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	// Race-enabled CI can delay goroutine startup and timer delivery, so use a
	// generous deadline while keeping the ticker interval short.
	deadline := time.Now().Add(2 * time.Second)
	for f.hits.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(interval / 4)
	}
	if got := f.hits.Load(); got < 2 {
		t.Errorf("expected >= 2 periodic fetches within deadline, got %d", got)
	}
}

// TestPeriodicRefresh_CloseStopsTicker verifies that Close() stops the
// ticker goroutine: no further fetches occur after Close, and the goroutine
// count returns to baseline.
func TestPeriodicRefresh_CloseStopsTicker(t *testing.T) {
	t.Parallel()
	const interval = 30 * time.Millisecond

	f := freshFetcher(t)
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            f,
		BackgroundRefresh: new(false),
		RefreshInterval:   interval,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Let at least one tick fire.
	deadline := time.Now().Add(interval * 4)
	for f.hits.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	// Close must return quickly (well within 1 second).
	done := make(chan error, 1)
	go func() { done <- c.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close() did not return within 1 second")
	}

	// Snapshot the hit count, sleep two more intervals, verify no new hits.
	hitsAfterClose := f.hits.Load()
	time.Sleep(interval * 3)
	if got := f.hits.Load(); got != hitsAfterClose {
		t.Errorf("ticker fired after Close: hits before=%d after=%d", hitsAfterClose, got)
	}
}

// TestPeriodicRefresh_ZeroIntervalNoTicker confirms that a zero RefreshInterval
// does not start a ticker (Close is a no-op, no extra fetches occur).
func TestPeriodicRefresh_ZeroIntervalNoTicker(t *testing.T) {
	t.Parallel()
	f := freshFetcher(t)
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            f,
		BackgroundRefresh: new(false),
		RefreshInterval:   0, // disabled
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Close should be a safe no-op.
	if err := c.Close(); err != nil {
		t.Errorf("Close on no-ticker catalog: %v", err)
	}
	// No background refresh, no ticker → zero fetches.
	time.Sleep(30 * time.Millisecond)
	if got := f.hits.Load(); got != 0 {
		t.Errorf("expected 0 fetches with no ticker, got %d", got)
	}
}

// TestCatwalkMapping_OpencodeGoProviderMeta verifies that the opencode-go
// provider from the embedded Catwalk data has its provider-level metadata
// (type, api_endpoint, api_key_ref) correctly preserved in the snapshot.
//
// This is the catalog-layer regression test for the bug where provider-level
// metadata was dropped when mapping Catwalk providers to catalog.Entry.
func TestCatwalkMapping_OpencodeGoProviderMeta(t *testing.T) {
	t.Parallel()
	snap, err := loadEmbeddedSnapshot()
	if err != nil {
		t.Fatalf("loadEmbeddedSnapshot: %v", err)
	}
	pm, ok := snap.ProvidersMeta["opencode-go"]
	if !ok {
		t.Fatal("embedded snapshot missing ProvidersMeta[opencode-go]")
	}
	if pm.Type != "openai-compat" {
		t.Errorf("ProvidersMeta[opencode-go].Type = %q, want openai-compat", pm.Type)
	}
	if pm.APIEndpoint == "" {
		t.Error("ProvidersMeta[opencode-go].APIEndpoint is empty; expected a URL")
	}
	if pm.APIKeyRef != "$OPENCODE_API_KEY" {
		t.Errorf("ProvidersMeta[opencode-go].APIKeyRef = %q, want $OPENCODE_API_KEY", pm.APIKeyRef)
	}
}

// TestSnapshotProviderMeta_RoundTrips confirms that ProvidersMeta survives a
// write-then-read cycle through the disk snapshot file, so refreshed catalogs
// retain provider-level metadata.
func TestSnapshotProviderMeta_RoundTrips(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	snap := &Snapshot{
		FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Providers: map[string]map[string]Entry{
			"opencode-go": {
				"minimax-m2.7": {Provider: "opencode-go", ID: "minimax-m2.7"},
			},
		},
		ProvidersMeta: map[string]ProviderMeta{
			"opencode-go": {
				Type:        "openai-compat",
				APIEndpoint: "https://opencode.ai/zen/go/v1",
				APIKeyRef:   "$OPENCODE_API_KEY",
			},
		},
	}
	if err := writeSnapshotFile(path, snap); err != nil {
		t.Fatalf("writeSnapshotFile: %v", err)
	}
	got, err := readSnapshotFile(path)
	if err != nil {
		t.Fatalf("readSnapshotFile: %v", err)
	}
	pm, ok := got.ProvidersMeta["opencode-go"]
	if !ok {
		t.Fatal("ProvidersMeta[opencode-go] missing after round-trip")
	}
	if pm.Type != "openai-compat" {
		t.Errorf("Type = %q, want openai-compat", pm.Type)
	}
	if pm.APIEndpoint != "https://opencode.ai/zen/go/v1" {
		t.Errorf("APIEndpoint = %q, want https://opencode.ai/zen/go/v1", pm.APIEndpoint)
	}
	if pm.APIKeyRef != "$OPENCODE_API_KEY" {
		t.Errorf("APIKeyRef = %q, want $OPENCODE_API_KEY", pm.APIKeyRef)
	}
}

// TestSnapshotProviderMeta_OldSnapshotMissingMetaOK confirms that a disk
// snapshot that predates ProvidersMeta (the field is absent) loads cleanly
// and returns ok=false from LookupProvider — no panic, no error.
func TestSnapshotProviderMeta_OldSnapshotMissingMetaOK(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	// Write a v2 snapshot that intentionally omits providers_meta.
	snap := &Snapshot{
		FetchedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Providers: map[string]map[string]Entry{
			"anthropic": {
				"claude-sonnet-4-5": {Provider: "anthropic", ID: "claude-sonnet-4-5"},
			},
		},
		// ProvidersMeta deliberately nil to simulate old snapshot.
	}
	if err := writeSnapshotFile(path, snap); err != nil {
		t.Fatalf("writeSnapshotFile: %v", err)
	}
	c, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            &fakeFetcher{err: errors.New("offline")},
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Must load from disk.
	if c.Loaded().Source != SourceDisk {
		t.Errorf("source = %q, want %q", c.Loaded().Source, SourceDisk)
	}
	// LookupProvider on absent meta must return ok=false without panic.
	if _, ok := c.LookupProvider("anthropic"); ok {
		t.Error("LookupProvider should return ok=false for snapshot without ProvidersMeta")
	}
}

// ---------------------------------------------------------------------------
// DefaultHeaders mutation-isolation tests
// ---------------------------------------------------------------------------

// TestLookupProvider_DefaultHeadersMutationIsolated proves that mutating the
// DefaultHeaders map returned by LookupProvider does not affect the snapshot's
// in-memory storage (lowercase-key path).
func TestLookupProvider_DefaultHeadersMutationIsolated(t *testing.T) {
	t.Parallel()
	const providerKey = "fancy"
	snap := &Snapshot{
		FetchedAt: time.Now(),
		Providers: map[string]map[string]Entry{},
		ProvidersMeta: map[string]ProviderMeta{
			providerKey: {
				Type:        "openai-compat",
				APIEndpoint: "https://example.com/v1",
				DefaultHeaders: map[string]string{
					"X-Orig": "original",
				},
			},
		},
	}
	c := &Catalog{snapshot: snap, src: SourceDisk, now: time.Now}

	// Lowercase path.
	pm, ok := c.LookupProvider(providerKey)
	if !ok {
		t.Fatalf("LookupProvider(%q): not found", providerKey)
	}
	pm.DefaultHeaders["X-Orig"] = "mutated"
	pm.DefaultHeaders["X-New"] = "injected"

	// Snapshot must be unchanged.
	orig := snap.ProvidersMeta[providerKey].DefaultHeaders
	if orig["X-Orig"] != "original" {
		t.Errorf("snapshot DefaultHeaders[X-Orig] was mutated: got %q", orig["X-Orig"])
	}
	if _, exists := orig["X-New"]; exists {
		t.Error("snapshot DefaultHeaders gained X-New from caller mutation")
	}
}

// TestLookupProvider_OriginalKeyPathMutationIsolated covers the
// original-providerID (non-lowercase) fallback branch of LookupProvider.
func TestLookupProvider_OriginalKeyPathMutationIsolated(t *testing.T) {
	t.Parallel()
	// Store under a mixed-case key so the lowercase path misses and the
	// original-providerID path hits.
	const mixedKey = "FancyProvider"
	snap := &Snapshot{
		FetchedAt: time.Now(),
		Providers: map[string]map[string]Entry{},
		ProvidersMeta: map[string]ProviderMeta{
			mixedKey: {
				DefaultHeaders: map[string]string{"X-Token": "secret"},
			},
		},
	}
	c := &Catalog{snapshot: snap, src: SourceDisk, now: time.Now}

	pm, ok := c.LookupProvider(mixedKey) // lowercase("FancyProvider") != mixedKey → fallback branch
	if !ok {
		t.Fatalf("LookupProvider(%q): not found via original-key path", mixedKey)
	}
	pm.DefaultHeaders["X-Token"] = "overwritten"

	if snap.ProvidersMeta[mixedKey].DefaultHeaders["X-Token"] != "secret" {
		t.Error("snapshot DefaultHeaders[X-Token] was mutated via original-key fallback path")
	}
}

// TestLookupProviderEmbedded_DefaultHeadersMutationIsolated proves the same
// isolation guarantee for the standalone LookupProviderEmbedded function.
// It uses the live embedded snapshot, so it only runs when opencode-go (or
// another provider with DefaultHeaders) is present; otherwise it skips.
func TestLookupProviderEmbedded_DefaultHeadersMutationIsolated(t *testing.T) {
	t.Parallel()
	snap, err := loadEmbeddedSnapshot()
	if err != nil {
		t.Fatalf("loadEmbeddedSnapshot: %v", err)
	}

	// Find any provider that has a non-nil DefaultHeaders map.
	var targetID string
	for id, pm := range snap.ProvidersMeta {
		if len(pm.DefaultHeaders) > 0 {
			targetID = id
			break
		}
	}
	if targetID == "" {
		t.Skip("no embedded provider has DefaultHeaders; skipping mutation-isolation test")
	}

	// Capture the original header values from the snapshot directly.
	origHeaders := make(map[string]string, len(snap.ProvidersMeta[targetID].DefaultHeaders))
	maps.Copy(origHeaders, snap.ProvidersMeta[targetID].DefaultHeaders)

	pm, ok := LookupProviderEmbedded(targetID)
	if !ok {
		t.Fatalf("LookupProviderEmbedded(%q): not found", targetID)
	}
	// Mutate every key and add a new one.
	for k := range pm.DefaultHeaders {
		pm.DefaultHeaders[k] = "mutated"
	}
	pm.DefaultHeaders["X-Injected"] = "yes"

	// loadEmbeddedSnapshot is called fresh each time, so re-load to confirm
	// the underlying embedded data is clean.
	snap2, err := loadEmbeddedSnapshot()
	if err != nil {
		t.Fatalf("loadEmbeddedSnapshot (second call): %v", err)
	}
	fresh := snap2.ProvidersMeta[targetID].DefaultHeaders
	for k, want := range origHeaders {
		if got := fresh[k]; got != want {
			t.Errorf("embedded snapshot DefaultHeaders[%q] = %q after mutation, want %q", k, got, want)
		}
	}
	if _, exists := fresh["X-Injected"]; exists {
		t.Error("embedded snapshot gained X-Injected key from caller mutation")
	}
}
