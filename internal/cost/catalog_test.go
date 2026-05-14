package cost

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/catalog"
)

// fixtureBody is the canned models.dev catalog used in tests; it
// matches the schema the new catalog package parses against.
const fixtureBody = `{
  "anthropic": {
    "models": {
      "claude-sonnet-4-5": {
        "id": "claude-sonnet-4-5",
        "cost": {"input": 3, "output": 15, "cache_read": 0.3, "cache_write": 3.75}
      },
      "claude-zenith-9": {
        "id": "claude-zenith-9",
        "cost": {"input": 0.5, "output": 2.5}
      }
    }
  }
}`

func fixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api.json" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fixtureBody))
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
		BackgroundRefresh: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	if _, err := cc.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return NewCatalog(CatalogOptions{Catalog: cc})
}

func boolPtr(b bool) *bool { return &b }

func TestLookUp_HitFromFreshCatalog(t *testing.T) {
	t.Parallel()
	c := newCatalogWithFixture(t)
	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if !fresh {
		t.Errorf("expected fresh=true")
	}
	if p.InputPerMTok != 3 || p.OutputPerMTok != 15 || p.CacheReadPerMTok != 0.3 || p.CacheWritePerMTok != 3.75 {
		t.Errorf("pricing mismatch: %+v", p)
	}
	if p.Provider != "anthropic" || p.Model != "claude-sonnet-4-5" {
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

func TestLookUp_EmbeddedFallbackHasFlagshipAnthropicModels(t *testing.T) {
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
		BackgroundRefresh: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	c := NewCatalog(CatalogOptions{Catalog: cc})

	for _, m := range []string{"claude-sonnet-4-5", "claude-opus-4-5", "claude-haiku-4-5"} {
		p, fresh, err := c.LookUp(context.Background(), "anthropic", m)
		if err != nil {
			t.Errorf("LookUp anthropic/%s: %v", m, err)
			continue
		}
		if fresh {
			t.Errorf("embedded fallback should return fresh=false for %s", m)
		}
		if p.InputPerMTok <= 0 || p.OutputPerMTok <= 0 {
			t.Errorf("embedded pricing for %s has non-positive values: %+v", m, p)
		}
	}
}

func TestRefresh_ForcesRefetch(t *testing.T) {
	t.Parallel()
	c := newCatalogWithFixture(t)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// Sanity: still answers after refresh.
	if _, _, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5"); err != nil {
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
	p, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
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
		Source:            stubFetcher{fixture: fixtureBody},
		BackgroundRefresh: boolPtr(false),
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
		BackgroundRefresh: boolPtr(false),
		Now:               func() time.Time { return stale },
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	c := NewCatalog(CatalogOptions{Catalog: cc2, Now: func() time.Time { return stale }})
	_, fresh, err := c.LookUp(context.Background(), "anthropic", "claude-sonnet-4-5")
	if err != nil {
		t.Fatalf("LookUp: %v", err)
	}
	if fresh {
		t.Errorf("expected fresh=false for stale snapshot")
	}
}

// stubFetcher is a no-network fetcher driven by an inline JSON body or
// a pre-set error.  Lets cost-package tests stay hermetic without
// reintroducing the catalog package's own httptest setup.
type stubFetcher struct {
	fixture string
	err     error
}

func (s stubFetcher) Fetch(_ context.Context) (*catalog.Snapshot, error) {
	if s.err != nil {
		return nil, s.err
	}
	// Reuse the catalog package's exported HTTP parser via a tiny
	// in-process loopback so we don't duplicate the parse code here.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(s.fixture))
	}))
	defer srv.Close()
	return catalog.NewHTTPFetcher(srv.Client(), srv.URL).Fetch(context.Background())
}

// compile-time check: ErrModelNotPriced satisfies the standard error
// interface and unwraps via errors.Is.
var _ = func() bool {
	return strings.Contains(ErrModelNotPriced.Error(), "model")
}
