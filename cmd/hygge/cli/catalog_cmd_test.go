package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// catalogFixtureBody is a small canned models.dev catalog used by the
// catalog-command tests.
const catalogFixtureBody = `{
  "anthropic": {
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
      }
    }
  },
  "openai": {
    "models": {
      "o3-mini": {
        "id": "o3-mini",
        "reasoning": true,
        "tool_call": true,
        "limit": {"context": 200000, "output": 100000},
        "cost": {"input": 1.1, "output": 4.4}
      }
    }
  }
}`

// catalogFixtureServer returns an httptest server that serves
// catalogFixtureBody.  The returned server is closed in t.Cleanup.
func catalogFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(catalogFixtureBody))
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
	for _, want := range []string{"claude-sonnet-4-5", "200K", "CONTEXT", "CAPABILITIES"} {
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
	// The embedded snapshot always carries claude-sonnet-4-5, so even
	// without a refresh this lookup hits.
	root.SetArgs([]string{"catalog", "show", "anthropic/claude-sonnet-4-5"})
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
	for _, line := range strings.Split(got, "\n") {
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
