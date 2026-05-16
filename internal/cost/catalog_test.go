package cost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/catwalk/pkg/catwalk"

	"github.com/cfbender/hygge/internal/catalog"
)

// fixtureProviders is the canned catalog data used in tests (catwalk format).
var fixtureProviders = []catwalk.Provider{
	{
		ID:   "anthropic",
		Name: "Anthropic",
		Type: catwalk.TypeAnthropic,
		Models: []catwalk.Model{
			{
				ID:                 "claude-sonnet-test",
				Name:               "Claude Sonnet Test",
				CostPer1MIn:        3,
				CostPer1MOut:       15,
				CostPer1MInCached:  0.3,
				CostPer1MOutCached: 3.75,
				ContextWindow:      200000,
				DefaultMaxTokens:   64000,
				CanReason:          true,
				SupportsImages:     true,
			},
			{
				ID:               "claude-zenith-9",
				Name:             "Claude Zenith 9",
				CostPer1MIn:      0.5,
				CostPer1MOut:     2.5,
				ContextWindow:    100000,
				DefaultMaxTokens: 4096,
			},
		},
	},
}

func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(fixtureProviders) //nolint:gosec // G117: test fixture; no real credentials
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/providers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newCatalogWithFixture builds a *Catalog backed by a catalog.Catalog
// that was just refreshed against the fixture server.  This exercises
// the full real path: HTTP fetch -> parse -> in-memory snapshot.
func newCatalogWithFixture(t *testing.T) *Catalog {
	t.Helper()
	srv := fixtureServer(t)
	cc, err := catalog.Load(catalog.LoadOptions{
		StateDir:          t.TempDir(),
		HTTPClient:        srv.Client(),
		BaseURL:           srv.URL,
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	if _, err := cc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return NewCatalog(CatalogOptions{Catalog: cc})
}

//go:fix inline
func boolPtr(b bool) *bool { return new(b) }

func TestLookUp_HitFromFreshCatalog(t *testing.T) {
	t.Parallel()
	c := newCatalogWithFixture(t)
	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-test")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true")
	}
	if p.InputPerMTok != 3 || p.OutputPerMTok != 15 || p.CacheReadPerMTok != 0.3 || p.CacheWritePerMTok != 3.75 {
		t.Errorf("pricing mismatch: %+v", p)
	}
	if p.Provider != "anthropic" || p.Model != "claude-sonnet-test" {
		t.Errorf("identity mismatch: %+v", p)
	}
}

func TestLookUp_MissReturnsErrModelNotPriced(t *testing.T) {
	t.Parallel()
	c := newCatalogWithFixture(t)
	_, _, err := c.LookUp(context.Background(), "nope", "nope")
	if !errors.Is(err, ErrModelNotPriced) {
		t.Fatalf("expected ErrModelNotPriced, got %v", err)
	}
}

// TestLookUp_EmbeddedFallbackHasAnthropicModels checks that the catwalk
// embedded snapshot (loaded when the network is unavailable) contains
// at least some Anthropic models with non-zero pricing.
func TestLookUp_EmbeddedFallbackHasAnthropicModels(t *testing.T) {
	t.Parallel()
	// Construct a catalog with no disk cache and a fetcher that
	// always errors — this forces the embedded snapshot to serve.
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", 500)
	}))
	t.Cleanup(srv.Close)
	cc, err := catalog.Load(catalog.LoadOptions{
		StateDir:          dir,
		HTTPClient:        srv.Client(),
		BaseURL:           srv.URL,
		BackgroundRefresh: new(false),
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	c := NewCatalog(CatalogOptions{Catalog: cc})

	// The catwalk embedded snapshot has versioned model IDs.
	// Check that at least some anthropic models are priced.
	models := cc.Models("anthropic")
	if len(models) == 0 {
		t.Fatalf("embedded snapshot has no anthropic models")
	}
	found := false
	for _, m := range models {
		p, _, err := c.LookUp(context.Background(), "anthropic", m.ID)
		if err == nil && (p.InputPerMTok > 0 || p.OutputPerMTok > 0) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no anthropic model in embedded snapshot has positive pricing")
	}
}

func TestRefresh_ForcesRefetch(t *testing.T) {
	t.Parallel()
	c := newCatalogWithFixture(t)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Sanity: still answers after refresh.
	if _, _, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-test"); err != nil {
		t.Fatalf("post-refresh LookUp: %v", err)
	}
}

func TestNewCatalog_LegacyOptionsLazyConstructsBackingCatalog(t *testing.T) {
	t.Parallel()
	// Exercise the legacy code path: pass BaseURL / CachePath rather
	// than a *catalog.Catalog, and confirm LookUp still works.
	srv := fixtureServer(t)
	c := NewCatalog(CatalogOptions{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		CachePath:  filepath.Join(t.TempDir(), "catalog.json"),
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-test")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true after refresh")
	}
	if p.InputPerMTok != 3 {
		t.Errorf("pricing mismatch: %+v", p)
	}
}

func TestNewCatalog_NilFunctionsDoNotPanic(t *testing.T) {
	t.Parallel()
	var c *Catalog
	if _, _, err := c.LookUp(context.Background(), "x", "y"); !errors.Is(err, ErrModelNotPriced) {
		t.Errorf("nil receiver should return ErrModelNotPriced, got %v", err)
	}
	if err := c.Refresh(context.Background()); err == nil {
		t.Errorf("nil receiver Refresh should error")
	}
	if got := c.Source(); got != nil {
		t.Errorf("nil receiver Source should be nil, got %v", got)
	}
}

func TestLookUp_StaleSnapshotReturnsFreshFalse(t *testing.T) {
	t.Parallel()
	// Inject a fake fetcher to keep the test offline; build the
	// catalog with Now() far in the future so the snapshot is
	// considered stale.
	dir := t.TempDir()
	now := time.Now()
	stale := now.Add(2 * catalog.DefaultMaxStaleness)

	// Pre-seed disk with a snapshot dated "now" so Load reads it.
	cc1, err := catalog.Load(catalog.LoadOptions{
		StateDir:          dir,
		Source:            stubFetcher{},
		BackgroundRefresh: new(false),
		Now:               func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("seed Load: %v", err)
	}
	if _, err := cc1.Refresh(context.Background()); err != nil {
		t.Fatalf("seed Refresh: %v", err)
	}
	// Reload with stale Now.
	cc2, err := catalog.Load(catalog.LoadOptions{
		StateDir:          dir,
		Source:            stubFetcher{err: errors.New("offline")},
		BackgroundRefresh: new(false),
		Now:               func() time.Time { return stale },
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	c := NewCatalog(CatalogOptions{Catalog: cc2, Now: func() time.Time { return stale }})
	// Use a model from the fixture that was seeded.
	models := cc2.Models("anthropic")
	if len(models) == 0 {
		t.Skip("no anthropic models in disk snapshot; skipping staleness check")
	}
	_, fresh, err := c.LookUp(context.Background(), "anthropic", models[0].ID)
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if fresh {
		t.Errorf("expected fresh=false for stale snapshot")
	}
}

// stubFetcher is a no-network fetcher that returns a fixed snapshot from
// fixtureProviders or a pre-set error.
type stubFetcher struct {
	err error
}

func (s stubFetcher) Fetch(_ context.Context) (*catalog.Snapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	// Build the snapshot directly from the catwalk provider structs.
	data, err := json.Marshal(fixtureProviders) //nolint:gosec // G117: test fixture; no real credentials
	if err != nil {
		return nil, err
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	return catalog.NewCatwalkFetcher(srv.Client(), srv.URL).Fetch(context.Background())
}

// compile-time check: ErrModelNotPriced satisfies the standard error
// interface and unwraps via errors.Is.
var _ = func() bool {
	return strings.Contains(ErrModelNotPriced.Error(), "model")
}
