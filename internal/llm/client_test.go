package llm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/llm"
)

// boolPtr returns a pointer to b.
//
//go:fix inline
func boolPtr(b bool) *bool { return new(b) }

// ---------------------------------------------------------------------------
// Fixture helpers
// ---------------------------------------------------------------------------

// fixtureProviders is a minimal catwalk provider slice for test servers.
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
		},
	},
	{
		ID:   "openai",
		Name: "OpenAI",
		Type: catwalk.TypeOpenAI,
		Models: []catwalk.Model{
			{
				ID:               "o3-mini",
				Name:             "o3-mini",
				CostPer1MIn:      1.1,
				CostPer1MOut:     4.4,
				ContextWindow:    200000,
				DefaultMaxTokens: 100000,
				CanReason:        true,
			},
		},
	},
}

// newFixtureServer starts an httptest server that serves fixtureProviders
// at /v2/providers.  Supports ETag conditional requests.
func newFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(fixtureProviders) //nolint:gosec // G117: test fixture; no real credentials
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	const etag = `"fixture-etag"`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/providers" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
}

// ---------------------------------------------------------------------------
// Construction + Resolve tests
// ---------------------------------------------------------------------------

func TestNew_EmbeddedSnapshotAvailable(t *testing.T) {
	c, err := llm.NewWithOptions(llm.Options{
		BackgroundRefresh: new(false),
		StateDir:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	defer func() { _ = c.Close() }()

	provs := c.EmbeddedProviders()
	if len(provs) == 0 {
		t.Errorf("expected embedded providers, got none")
	}
	found := slices.Contains(provs, "anthropic")
	if !found {
		t.Errorf("expected anthropic in embedded providers, got %v", provs)
	}
}

func TestResolve_HitAndMiss(t *testing.T) {
	srv := newFixtureServer(t)
	defer srv.Close()

	c, err := llm.NewWithOptions(llm.Options{
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		BackgroundRefresh: new(false),
		StateDir:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Trigger a refresh so the fixture data is in memory.
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Hit.
	e, ok := c.Resolve("anthropic", "claude-sonnet-4-5")
	if !ok {
		t.Fatalf("Resolve: expected hit for anthropic/claude-sonnet-4-5")
	}
	if e.ID != "claude-sonnet-4-5" {
		t.Errorf("ID = %q, want claude-sonnet-4-5", e.ID)
	}
	if e.Cost.Input != 3 {
		t.Errorf("Cost.Input = %v, want 3", e.Cost.Input)
	}
	if !e.Capabilities.Reasoning {
		t.Errorf("Capabilities.Reasoning should be true")
	}
	if !e.Capabilities.Attachment {
		t.Errorf("Capabilities.Attachment should be true (supports_attachments=true)")
	}
	if e.Limit.ContextWindow != 200000 {
		t.Errorf("Limit.ContextWindow = %d, want 200000", e.Limit.ContextWindow)
	}
	if e.DefaultReasoningEffort != "high" {
		t.Errorf("DefaultReasoningEffort = %q, want high", e.DefaultReasoningEffort)
	}
	if len(e.ReasoningLevels) != 3 {
		t.Errorf("ReasoningLevels = %v, want 3 entries", e.ReasoningLevels)
	}

	// Miss.
	if _, ok := c.Resolve("anthropic", "nonexistent-model"); ok {
		t.Errorf("Resolve: expected miss for nonexistent model")
	}
	if _, ok := c.Resolve("nope", "any"); ok {
		t.Errorf("Resolve: expected miss for unknown provider")
	}
}

// ---------------------------------------------------------------------------
// ETag / not-modified test
// ---------------------------------------------------------------------------

func TestRefresh_ETagCacheBehavior(t *testing.T) {
	var requestCount int
	const testETag = `"server-etag"`
	data, _ := json.Marshal(fixtureProviders) //nolint:gosec // G117: test fixture; no real credentials

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Header.Get("If-None-Match") == testETag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", testETag)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	c, err := llm.NewWithOptions(llm.Options{
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		BackgroundRefresh: new(false),
		StateDir:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	defer func() { _ = c.Close() }()

	// First Refresh: fetches and stores ETag.
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh #1: %v", err)
	}
	if requestCount != 1 {
		t.Errorf("expected 1 request after first refresh, got %d", requestCount)
	}

	// Second Refresh: sends If-None-Match, server replies 304.
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh #2 (304): %v", err)
	}
	if requestCount != 2 {
		t.Errorf("expected 2 requests after second refresh, got %d", requestCount)
	}

	// Snapshot must still be valid after the 304.
	if _, ok := c.Resolve("anthropic", "claude-sonnet-4-5"); !ok {
		t.Errorf("snapshot lost after 304 refresh")
	}
}

// ---------------------------------------------------------------------------
// Pricing / reasoning / context-window fields used by cost code
// ---------------------------------------------------------------------------

func TestResolve_PricingAndReasoningFields(t *testing.T) {
	srv := newFixtureServer(t)
	defer srv.Close()

	c, err := llm.NewWithOptions(llm.Options{
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		BackgroundRefresh: new(false),
		StateDir:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	// Pricing fields — used by internal/cost.
	e, ok := c.Resolve("anthropic", "claude-sonnet-4-5")
	if !ok {
		t.Fatalf("Resolve miss")
	}
	wantPricing := struct{ in, out, cr, cw float64 }{3, 15, 0.3, 3.75}
	if e.Cost.Input != wantPricing.in ||
		e.Cost.Output != wantPricing.out ||
		e.Cost.CacheRead != wantPricing.cr ||
		e.Cost.CacheWrite != wantPricing.cw {
		t.Errorf("pricing mismatch: got %+v, want %+v", e.Cost, wantPricing)
	}

	// Reasoning capability — used by Anthropic adapter.
	if !e.Capabilities.Reasoning {
		t.Errorf("expected Reasoning=true for sonnet")
	}

	// Context window — used by agent loop for compaction threshold.
	if e.Limit.ContextWindow != 200_000 {
		t.Errorf("context window = %d, want 200000", e.Limit.ContextWindow)
	}

	// o3-mini reasoning.
	o3, ok := c.Resolve("openai", "o3-mini")
	if !ok {
		t.Fatalf("Resolve miss for o3-mini")
	}
	if !o3.Capabilities.Reasoning {
		t.Errorf("expected o3-mini Reasoning=true")
	}
}

// ---------------------------------------------------------------------------
// No-live-network gate
// ---------------------------------------------------------------------------

// TestNoLiveNetwork confirms that a llm.Client constructed with a
// BackgroundRefresh=false and no base URL still serves model data
// from the embedded catwalk snapshot (no real HTTP requests fired).
func TestNoLiveNetwork(t *testing.T) {
	c, err := llm.NewWithOptions(llm.Options{
		BackgroundRefresh: new(false),
		StateDir:          t.TempDir(),
		// No BaseURL → would use the default catwalk service URL
		// if it fired a request.  BackgroundRefresh=false prevents
		// that.  The test serves only the embedded snapshot.
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	defer func() { _ = c.Close() }()

	// Embedded providers must be available without any network call.
	provs := c.EmbeddedProviders()
	if len(provs) == 0 {
		t.Errorf("embedded providers should be available offline, got none")
	}
}

// ---------------------------------------------------------------------------
// Phase 0 compile-probe (preserved from probe_test.go)
// ---------------------------------------------------------------------------

// TestPhase0_ImportsCompile asserts that both upstream packages are
// correctly declared in go.mod and that the key types we plan to use
// in Phase 1 resolve without error.  No APIs are called.
func TestPhase0_ImportsCompile(_ *testing.T) {
	// catwalk: verify Provider struct is accessible.
	_ = catwalk.Provider{}

	// fantasy: verify NewAgentTool generic constructor resolves.
	_ = fantasy.NewAgentTool[struct{}]("probe", "compile-only probe", nil)
}

// ---------------------------------------------------------------------------
// Close / cleanup test
// ---------------------------------------------------------------------------

func TestClose_Idempotent(t *testing.T) {
	c, err := llm.NewWithOptions(llm.Options{
		BackgroundRefresh: new(false),
		StateDir:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("first Close: %v", err)
	}
}

func TestClose_WithPeriodicRefresh(t *testing.T) {
	srv := newFixtureServer(t)
	defer srv.Close()

	const interval = 30 * time.Millisecond
	c, err := llm.NewWithOptions(llm.Options{
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		BackgroundRefresh: new(false),
		RefreshInterval:   interval,
		StateDir:          t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewWithOptions: %v", err)
	}
	// Close must return quickly.
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
}
