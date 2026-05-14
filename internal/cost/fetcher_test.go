package cost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchAndParse_SampleFixture(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile("testdata/models_dev.sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			t.Errorf("unexpected path: %q", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %q", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snap, err := fetchAndParse(context.Background(), srv.Client(), srv.URL, now)
	if err != nil {
		t.Fatalf("fetchAndParse: %v", err)
	}
	if snap == nil {
		t.Fatalf("snapshot is nil")
	}
	if !snap.FetchedAt.Equal(now) {
		t.Errorf("FetchedAt = %v, want %v", snap.FetchedAt, now)
	}

	got, ok := snap.Providers["anthropic"]["claude-sonnet-4.5"]
	if !ok {
		t.Fatalf("anthropic/claude-sonnet-4.5 not in snapshot; providers=%v", keys(snap.Providers))
	}
	if got.InputPerMTok != 3 || got.OutputPerMTok != 15 || got.CacheReadPerMTok != 0.3 || got.CacheWritePerMTok != 3.75 {
		t.Errorf("sonnet pricing mismatch: %+v", got)
	}
	if got.Provider != "anthropic" || got.Model != "claude-sonnet-4.5" {
		t.Errorf("provider/model mismatch: %+v", got)
	}
}

func TestFetchAndParse_PermissiveMissingFields(t *testing.T) {
	t.Parallel()

	// haiku entry in the fixture omits cache_read/cache_write.
	data, err := os.ReadFile("testdata/models_dev.sample.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	snap, err := fetchAndParse(context.Background(), srv.Client(), srv.URL, time.Now())
	if err != nil {
		t.Fatalf("fetchAndParse: %v", err)
	}
	got, ok := snap.Providers["anthropic"]["claude-haiku-3-5"]
	if !ok {
		t.Fatalf("haiku not present")
	}
	if got.InputPerMTok != 1.0 || got.OutputPerMTok != 5.0 {
		t.Errorf("haiku input/output: %+v", got)
	}
	if got.CacheReadPerMTok != 0 || got.CacheWritePerMTok != 0 {
		t.Errorf("missing cache fields should be 0, got %+v", got)
	}
}

func TestFetchAndParse_Http500ReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	_, err := fetchAndParse(context.Background(), srv.Client(), srv.URL, time.Now())
	if err == nil {
		t.Fatalf("expected error on 500")
	}
	if !strings.Contains(err.Error(), "http 500") {
		t.Errorf("error should mention status code, got %v", err)
	}
}

func TestFetchAndParse_MalformedJsonReturnsError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{not json"))
	}))
	defer srv.Close()

	_, err := fetchAndParse(context.Background(), srv.Client(), srv.URL, time.Now())
	if err == nil {
		t.Fatalf("expected error on malformed JSON")
	}
}

func TestFetchAndParse_EmptyBodyReturnsEmptySnapshot(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// no body, no headers, just 200
	}))
	defer srv.Close()

	snap, err := fetchAndParse(context.Background(), srv.Client(), srv.URL, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(snap.Providers) != 0 {
		t.Errorf("expected empty providers, got %d", len(snap.Providers))
	}
}

func TestFetchAndParse_TransportError(t *testing.T) {
	t.Parallel()

	// Server that closes immediately to force a transport error.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	srv.Close()

	_, err := fetchAndParse(context.Background(), srv.Client(), srv.URL, time.Now())
	if err == nil {
		t.Fatalf("expected transport error after server close")
	}
}

// keys is a tiny helper for diagnostic output.
func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// helper: a CachePath inside t.TempDir() that exists.
func tempCachePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "models_catalog.json")
}
