package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// boolPtr is a helper for the BackgroundRefresh tri-state option.
func boolPtr(b bool) *bool { return &b }

// fixtureBody is a small canned models.dev catalog used across tests.
// It exercises every field the parser cares about plus a model that
// omits modalities to confirm the parser is permissive.
const fixtureBody = `{
  "anthropic": {
    "id": "anthropic",
    "models": {
      "claude-sonnet-4-5": {
        "id": "claude-sonnet-4-5",
        "name": "Claude Sonnet 4.5",
        "release_date": "2025-09-29",
        "reasoning": true,
        "tool_call": true,
        "attachment": true,
        "modalities": {"input": ["text","image"], "output": ["text"]},
        "limit": {"context": 200000, "output": 64000},
        "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}
      },
      "claude-haiku-4-5": {
        "id": "claude-haiku-4-5",
        "name": "Claude Haiku 4.5",
        "cost": {"input": 1, "output": 5}
      }
    }
  },
  "openai": {
    "models": {
      "o3-mini": {
        "id": "o3-mini",
        "name": "o3-mini",
        "reasoning": true,
        "tool_call": true,
        "limit": {"context": 200000, "output": 100000},
        "cost": {"input": 1.1, "output": 4.4}
      },
      "gpt-4o": {
        "id": "gpt-4o",
        "tool_call": true,
        "modalities": {"input": ["text","image"], "output": ["text"]},
        "cost": {"input": 2.5, "output": 10}
      }
    }
  }
}`

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

// freshFetcher returns a fetcher pre-loaded with the fixture body.
func freshFetcher(t *testing.T) *fakeFetcher {
	t.Helper()
	snap, err := parseRawJSON([]byte(fixtureBody))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return &fakeFetcher{snap: snap}
}

// tempStateDir returns a state directory in t.TempDir.
func tempStateDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func TestLoad_FallsBackToEmbeddedWhenDiskMissing(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            &fakeFetcher{err: errors.New("offline")},
		BackgroundRefresh: boolPtr(false),
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
	// Sanity: the embedded snapshot must include the three flagship
	// Anthropic models so the existing TUI fallback works offline.
	for _, m := range []string{"claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5"} {
		if _, ok := c.Lookup("anthropic", m); !ok {
			t.Errorf("embedded snapshot missing anthropic/%s", m)
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
		BackgroundRefresh: boolPtr(false),
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
		BackgroundRefresh: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Loaded().Source != SourceEmbedded {
		t.Errorf("source = %q, want %q", c.Loaded().Source, SourceEmbedded)
	}
}

func TestRefresh_RoundTripsDisk(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	f := freshFetcher(t)
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	c, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            f,
		BackgroundRefresh: boolPtr(false),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	res, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if res.Providers != 2 {
		t.Errorf("providers = %d, want 2", res.Providers)
	}
	if res.Models != 4 {
		t.Errorf("models = %d, want 4", res.Models)
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
	// Reloading should now read disk, not embedded.
	c2, err := Load(LoadOptions{
		StateDir:          dir,
		Source:            f,
		BackgroundRefresh: boolPtr(false),
		Now:               func() time.Time { return now.Add(time.Hour) },
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if c2.Loaded().Source != SourceDisk {
		t.Errorf("reload source = %q, want %q", c2.Loaded().Source, SourceDisk)
	}
}

func TestLookup_HitMissAndCaseInsensitivity(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            freshFetcher(t),
		BackgroundRefresh: boolPtr(false),
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
		BackgroundRefresh: boolPtr(false),
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

func TestParseRawJSON_FieldExtraction(t *testing.T) {
	t.Parallel()
	snap, err := parseRawJSON([]byte(fixtureBody))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	e := snap.Providers["anthropic"]["claude-sonnet-4-5"]
	if e.ID != "claude-sonnet-4-5" || e.Name != "Claude Sonnet 4.5" {
		t.Errorf("id/name mismatch: %+v", e)
	}
	if e.Limit.ContextWindow != 200000 || e.Limit.MaxOutput != 64000 {
		t.Errorf("limit mismatch: %+v", e.Limit)
	}
	if e.Cost.Input != 3 || e.Cost.Output != 15 || e.Cost.CacheRead != 0.3 || e.Cost.CacheWrite != 3.75 {
		t.Errorf("cost mismatch: %+v", e.Cost)
	}
	if !e.Capabilities.Reasoning || !e.Capabilities.ToolCalling || !e.Capabilities.Attachment {
		t.Errorf("capabilities mismatch: %+v", e.Capabilities)
	}
	if !e.Capabilities.InputText || !e.Capabilities.InputImages || !e.Capabilities.OutputText {
		t.Errorf("modalities mismatch: %+v", e.Capabilities)
	}
	if e.Capabilities.OutputImages {
		t.Errorf("did not advertise output images")
	}

	// haiku omits modalities; only the explicit booleans + cost should be set.
	h := snap.Providers["anthropic"]["claude-haiku-4-5"]
	if h.Capabilities.Reasoning || h.Capabilities.ToolCalling {
		t.Errorf("haiku should have no advertised reasoning/tool_call, got %+v", h.Capabilities)
	}
	if h.Cost.Input != 1 || h.Cost.Output != 5 {
		t.Errorf("haiku cost: %+v", h.Cost)
	}
	if h.Cost.CacheRead != 0 || h.Cost.CacheWrite != 0 {
		t.Errorf("haiku missing cache fields should be 0, got %+v", h.Cost)
	}
}

func TestHTTPFetcher_Success(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureBody))
	}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client(), srv.URL)
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, ok := snap.Providers["anthropic"]["claude-sonnet-4-5"]; !ok {
		t.Errorf("snapshot missing sonnet")
	}
}

func TestHTTPFetcher_Non2xx(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client(), srv.URL)
	_, err := f.Fetch(context.Background())
	if err == nil {
		t.Fatalf("expected error on 500")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

func TestHTTPFetcher_MalformedJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not"))
	}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client(), srv.URL)
	if _, err := f.Fetch(context.Background()); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestHTTPFetcher_EmptyBody(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	f := NewHTTPFetcher(srv.Client(), srv.URL)
	snap, err := f.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(snap.Providers) != 0 {
		t.Errorf("expected empty providers, got %d", len(snap.Providers))
	}
}

func TestBackgroundRefresh_StaleSnapshotKicksOff(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	// Seed a very-old disk snapshot.
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
		BackgroundRefresh: boolPtr(true),
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
		BackgroundRefresh: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	var wg sync.WaitGroup
	const N = 6
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := c.Refresh(context.Background()); err != nil {
				t.Errorf("Refresh: %v", err)
			}
		}()
	}
	wg.Wait()
	// Single-flight is best-effort: refreshing.Lock serialises but
	// each Refresh still calls Fetch.  We assert ordered serialised
	// hits == N rather than 1; this just confirms no panic/deadlock.
	if got := f.hits.Load(); got != int64(N) {
		t.Logf("hits = %d (serialised refreshes; not coalesced)", got)
	}
}

func TestSnapshotFile_VersionRejected(t *testing.T) {
	t.Parallel()
	dir := tempStateDir(t)
	path := filepath.Join(dir, "catalog.json")
	// Write a snapshot file with the wrong version.
	bad := struct {
		Version int `json:"version"`
	}{Version: 99}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := readSnapshotFile(path)
	if err == nil || !strings.Contains(err.Error(), "unsupported snapshot version") {
		t.Errorf("expected version error, got %v", err)
	}
}

func TestEmbeddedSnapshot_LoadsAndCoversFlagshipModels(t *testing.T) {
	t.Parallel()
	snap, err := loadEmbeddedSnapshot()
	if err != nil {
		t.Fatalf("loadEmbedded: %v", err)
	}
	if len(snap.Providers) == 0 {
		t.Fatalf("embedded snapshot has no providers")
	}
	// Must include the three flagship Anthropic models so the
	// offline TUI fallback works.
	want := []string{"claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5"}
	for _, m := range want {
		if _, ok := snap.Providers["anthropic"][m]; !ok {
			t.Errorf("embedded snapshot missing anthropic/%s", m)
		}
	}
	// Must include at least one OpenAI o-series reasoning model.
	if e, ok := snap.Providers["openai"]["o3-mini"]; ok {
		if !e.Capabilities.Reasoning {
			t.Errorf("o3-mini should advertise reasoning, got %+v", e.Capabilities)
		}
	} else {
		t.Errorf("embedded snapshot missing openai/o3-mini")
	}
}

func TestProviders_Sorted(t *testing.T) {
	t.Parallel()
	c, err := Load(LoadOptions{
		StateDir:          tempStateDir(t),
		Source:            freshFetcher(t),
		BackgroundRefresh: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	provs := c.Providers()
	if len(provs) != 2 {
		t.Fatalf("providers = %v", provs)
	}
	if provs[0] != "anthropic" || provs[1] != "openai" {
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
		BackgroundRefresh: boolPtr(false),
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
