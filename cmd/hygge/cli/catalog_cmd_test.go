package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/catwalk/pkg/catwalk"
)

// catalogFixtureProviders is a small canned catwalk provider slice used by
// the catalog-command tests.
var catalogFixtureProviders = []catwalk.Provider{
	{
		ID:   "anthropic",
		Name: "Anthropic",
		Type: catwalk.TypeAnthropic,
		Models: []catwalk.Model{
			{
				ID:                 "claude-sonnet-4-5-20250929",
				Name:               "Claude Sonnet 4.5",
				CostPer1MIn:        3,
				CostPer1MOut:       15,
				CostPer1MInCached:  0.3,
				CostPer1MOutCached: 3.75,
				ContextWindow:      200000,
				DefaultMaxTokens:   64000,
				CanReason:          true,
				SupportsImages:     true,
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

// catalogFixtureServer returns an httptest server that serves the fixture
// providers at /v2/providers.  The returned server is closed in t.Cleanup.
func catalogFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	data, err := json.Marshal(catalogFixtureProviders) //nolint:gosec // G117: test fixture; no real credentials
	if err != nil {
		t.Fatalf("marshal catalog fixture: %v", err)
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

// withCatalogFixture installs the test override CatalogBaseURL so the
// CLI bootstrap builds a catalog against the fixture server.
func withCatalogFixture(t *testing.T) {
	t.Helper()
	srv := catalogFixtureServer(t)
	// hermeticHome installs overrides; mutate the existing struct.
	cur := testOverrides
	if cur == nil {
		t.Fatalf("withCatalogFixture: hermeticHome must be called first")
	}
	cur.CatalogBaseURL = srv.URL
	SetTestOverrides(cur)
}

func TestCatalogList_NoArgs_PrintsProviderSummary(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	// The embedded snapshot always loads, so we should see both the
	// embedded providers and the canonical labels.
	for _, want := range []string{"anthropic", "openai", "PROVIDER", "MODELS"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCatalogList_PerProvider_PrintsTable(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "list", "anthropic"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"200K", "CONTEXT", "CAPABILITIES"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCatalogList_UnknownProvider_Errors(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "list", "this-provider-does-not-exist-xyz"})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected error for unknown provider")
	}
}

func TestCatalogShow_HitPrintsDetail(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	// The embedded catwalk snapshot always carries claude-sonnet-4-5-20250929.
	root.SetArgs([]string{"catalog", "show", "anthropic/claude-sonnet-4-5-20250929"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"provider:", "id:", "context_window:", "reasoning:", "cost (USD"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

func TestCatalogShow_O3MiniReasoningTrue(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "show", "openai/o3-mini"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "reasoning:") {
		t.Fatalf("missing reasoning row:\n%s", got)
	}
	// The line should read "reasoning:  true".
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "reasoning:") {
			if !strings.Contains(line, "true") {
				t.Errorf("o3-mini reasoning row not true: %q", line)
			}
		}
	}
}

func TestCatalogShow_BadRef_Errors(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "show", "missing-slash"})
	if err := root.Execute(); err == nil {
		t.Fatalf("expected error for malformed ref")
	}
}

func TestCatalogRefresh_SuccessPrintsSummary(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "refresh"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "refreshed:") {
		t.Errorf("missing refreshed: line:\n%s", got)
	}
	if !strings.Contains(got, "providers") {
		t.Errorf("missing providers count:\n%s", got)
	}
}

func TestCatalogRefresh_Quiet(t *testing.T) {
	hermeticHome(t)
	withCatalogFixture(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"catalog", "refresh", "--quiet"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := buf.String(); strings.TrimSpace(got) != "" {
		t.Errorf("expected no output with --quiet, got:\n%s", got)
	}
}

func TestSplitProviderModel(t *testing.T) {
	cases := []struct {
		in           string
		wantProvider string
		wantModel    string
		ok           bool
	}{
		{"anthropic/claude-sonnet-4-5", "anthropic", "claude-sonnet-4-5", true},
		{"openrouter/openai/o3-mini", "openrouter", "openai/o3-mini", true},
		{"no-slash", "", "", false},
		{"trailing/", "", "", false},
		{"/leading", "", "", false},
		{"", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			p, m, ok := splitProviderModel(c.in)
			if ok != c.ok || p != c.wantProvider || m != c.wantModel {
				t.Errorf("splitProviderModel(%q) = (%q, %q, %v), want (%q, %q, %v)",
					c.in, p, m, ok, c.wantProvider, c.wantModel, c.ok)
			}
		})
	}
}

func TestFormatContext(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "-"},
		{-1, "-"},
		{200_000, "200K"},
		{1_000_000, "1M"},
		{128_500, "128500"},
	}
	for _, c := range cases {
		if got := formatContext(c.n); got != c.want {
			t.Errorf("formatContext(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	if got := formatMoney(0); got != "-" {
		t.Errorf("formatMoney(0) = %q, want -", got)
	}
	if got := formatMoney(3); got != "$3" {
		t.Errorf("formatMoney(3) = %q, want $3", got)
	}
	if got := formatMoney(0.3); got != "$0.3" {
		t.Errorf("formatMoney(0.3) = %q, want $0.3", got)
	}
}
