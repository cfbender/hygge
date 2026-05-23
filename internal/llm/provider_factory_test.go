package llm_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/llm"
	"github.com/cfbender/hygge/internal/provider"
)

func TestResolveProviderModel_OpenAI_NoNetwork(t *testing.T) {
	r, err := llm.ResolveProviderModel(t.Context(), "openai", "gpt-4o-mini", map[string]any{
		"api_key":  "sk-test",
		"base_url": "http://127.0.0.1:1/v1",
	}, nil)
	if err != nil {
		t.Fatalf("ResolveProviderModel: %v", err)
	}
	if r.Provider == nil {
		t.Fatal("provider is nil")
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "openai" {
		t.Fatalf("model provider = %q, want openai", got)
	}
	if got := r.Model.Model(); got != "gpt-4o-mini" {
		t.Fatalf("model id = %q, want gpt-4o-mini", got)
	}
}

func TestResolveProviderModel_CompatProvider_WithBaseURL(t *testing.T) {
	r, err := llm.ResolveProviderModel(t.Context(), "local", "my-model", map[string]any{
		"api_key":  "sk-test",
		"base_url": "http://127.0.0.1:11434/v1",
	}, nil)
	if err != nil {
		t.Fatalf("ResolveProviderModel: %v", err)
	}
	if got := r.Model.Provider(); got != "local" {
		t.Fatalf("model provider = %q, want local", got)
	}
}

func TestResolveProviderModel_CoreProviders_NoNetwork(t *testing.T) {
	tests := []struct {
		name         string
		providerID   string
		modelID      string
		opts         map[string]any
		wantProvider string
	}{
		{
			name:       "anthropic",
			providerID: "anthropic",
			modelID:    "claude-sonnet-4-5",
			opts: map[string]any{
				"api_key":  "sk-ant-test",
				"base_url": "http://127.0.0.1:1",
			},
		},
		{
			name:       "openrouter",
			providerID: "openrouter",
			modelID:    "anthropic/claude-sonnet-4.5",
			opts: map[string]any{
				"api_key":      "sk-or-test",
				"http_referer": "",
				"x_title":      "",
			},
		},
		{
			name:       "gemini",
			providerID: "gemini",
			modelID:    "gemini-2.5-pro",
			opts: map[string]any{
				"api_key":  "sk-google-test",
				"base_url": "http://127.0.0.1:1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := llm.ResolveProviderModel(t.Context(), tt.providerID, tt.modelID, tt.opts, nil)
			if err != nil {
				t.Fatalf("ResolveProviderModel: %v", err)
			}
			if r.Provider == nil {
				t.Fatal("provider is nil")
			}
			if r.Model == nil {
				t.Fatal("language model is nil")
			}
			wantProvider := tt.wantProvider
			if wantProvider == "" {
				wantProvider = tt.providerID
			}
			if got := r.Model.Provider(); got != wantProvider {
				t.Fatalf("model provider = %q, want %q", got, wantProvider)
			}
			if got := r.Model.Model(); got != tt.modelID {
				t.Fatalf("model id = %q, want %q", got, tt.modelID)
			}
		})
	}
}

func TestResolveProviderModel_UsesProviderEnvFallback(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	r, err := llm.ResolveProviderModel(t.Context(), "openai", "gpt-4o-mini", map[string]any{
		"base_url": "http://127.0.0.1:1/v1",
	}, nil)
	if err != nil {
		t.Fatalf("ResolveProviderModel: %v", err)
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
}

func TestResolveProviderModel_GeminiUsesGoogleAPIKeyEnvFallback(t *testing.T) {
	t.Setenv("GOOGLE_API_KEY", "sk-google-env")
	r, err := llm.ResolveProviderModel(t.Context(), "gemini", "gemini-2.5-pro", map[string]any{
		"base_url": "http://127.0.0.1:1",
	}, nil)
	if err != nil {
		t.Fatalf("ResolveProviderModel: %v", err)
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
}

func TestResolveProviderModel_RequiresCompatBaseURL(t *testing.T) {
	_, err := llm.ResolveProviderModel(t.Context(), "local", "my-model", map[string]any{
		"api_key": "sk-test",
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveProviderModel_RejectsMissingAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := llm.ResolveProviderModel(t.Context(), "openai", "gpt-4o-mini", nil, nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Fatalf("error = %v, want provider.ErrAuth", err)
	}
}

// ---------------------------------------------------------------------------
// opencode-go regression tests (success criteria 7)
// ---------------------------------------------------------------------------

// catalogFetcher is a test-only catalog.Fetcher that returns a pre-built snapshot.
type catalogFetcher struct {
	snap *catalog.Snapshot
}

func (f *catalogFetcher) Fetch(_ context.Context) (*catalog.Snapshot, error) {
	return f.snap, nil
}

// opencodeGoCatalog builds a catalog pre-loaded with opencode-go provider
// metadata so tests do not require a network connection.
func opencodeGoCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	snap := &catalog.Snapshot{
		FetchedAt: time.Now(),
		Providers: map[string]map[string]catalog.Entry{
			"opencode-go": {
				"minimax-m2.7": {
					Provider: "opencode-go",
					ID:       "minimax-m2.7",
					Name:     "MiniMax M2.7",
				},
			},
		},
		ProvidersMeta: map[string]catalog.ProviderMeta{
			"opencode-go": {
				Type:        "openai-compat",
				APIEndpoint: "https://opencode.ai/zen/go/v1",
				APIKeyRef:   "$OPENCODE_API_KEY",
			},
		},
	}
	off := new(bool)
	cat, err := catalog.Load(catalog.LoadOptions{
		StateDir:          t.TempDir(),
		Source:            &catalogFetcher{snap: snap},
		BackgroundRefresh: off,
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	if _, err := cat.Refresh(context.Background()); err != nil {
		t.Fatalf("catalog.Refresh: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	return cat
}

// TestResolveProviderModel_OpencodeGo_WithAPIKey is the regression test for
// the bug: switching to opencode-go failed with
// "unsupported provider: base_url required for compat provider".
//
// It verifies that when a catalog with opencode-go provider metadata is
// supplied (type=openai-compat, api_endpoint set), the resolver constructs
// a non-nil model using the compat provider — without any user-supplied base_url.
func TestResolveProviderModel_OpencodeGo_WithAPIKey(t *testing.T) {
	cat := opencodeGoCatalog(t)
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7", map[string]any{
		"api_key": "sk-opencode-test",
	}, cat)
	if err != nil {
		t.Fatalf("ResolveProviderModel: %v", err)
	}
	if r.Provider == nil {
		t.Fatal("provider is nil")
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "opencode-go" {
		t.Fatalf("model.Provider() = %q, want %q", got, "opencode-go")
	}
}

// TestResolveProviderModel_OpencodeGo_EnvAPIKey confirms that the resolver
// falls back to the catalog APIKeyRef ($OPENCODE_API_KEY) when no api_key
// option is given but the environment variable is set.
func TestResolveProviderModel_OpencodeGo_EnvAPIKey(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-env-opencode-test")
	cat := opencodeGoCatalog(t)
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7", nil, cat)
	if err != nil {
		t.Fatalf("ResolveProviderModel: %v", err)
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "opencode-go" {
		t.Fatalf("model.Provider() = %q, want %q", got, "opencode-go")
	}
}

// TestResolveProviderModel_OpencodeGo_ExplicitBaseURLOverrides confirms that
// an explicit base_url in opts takes precedence over the catalog endpoint.
func TestResolveProviderModel_OpencodeGo_ExplicitBaseURLOverrides(t *testing.T) {
	cat := opencodeGoCatalog(t)
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7", map[string]any{
		"api_key":  "sk-opencode-test",
		"base_url": "http://127.0.0.1:11434/v1",
	}, cat)
	if err != nil {
		t.Fatalf("ResolveProviderModel with explicit base_url: %v", err)
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
}

// ---------------------------------------------------------------------------
// Regression tests: nil catalog and stale catalog without ProvidersMeta.
//
// These cover the original failure mode: real builds can have a nil catalog
// (unit tests, bootstrap edge cases) or a disk snapshot that predates the
// ProvidersMeta field.  In both cases the embedded Catwalk data must be
// consulted as a fallback.
// ---------------------------------------------------------------------------

// TestResolveProviderModel_OpencodeGo_NilCatalog_WithAPIKey reproduces the
// exact error "llm: unsupported provider "opencode-go" (base_url required for
// compat provider)" that occurs when cat == nil.
func TestResolveProviderModel_OpencodeGo_NilCatalog_WithAPIKey(t *testing.T) {
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7", map[string]any{
		"api_key": "sk-opencode-test",
	}, nil /* nil catalog — must fall back to embedded */)
	if err != nil {
		t.Fatalf("ResolveProviderModel with nil catalog: %v", err)
	}
	if r.Provider == nil {
		t.Fatal("provider is nil")
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "opencode-go" {
		t.Fatalf("model.Provider() = %q, want %q", got, "opencode-go")
	}
}

// TestResolveProviderModel_OpencodeGo_NilCatalog_EnvAPIKey reproduces the
// failure when cat == nil and the api_key comes from $OPENCODE_API_KEY.
func TestResolveProviderModel_OpencodeGo_NilCatalog_EnvAPIKey(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-env-opencode-test")
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7",
		nil /* no opts */, nil /* nil catalog */)
	if err != nil {
		t.Fatalf("ResolveProviderModel with nil catalog + env key: %v", err)
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "opencode-go" {
		t.Fatalf("model.Provider() = %q, want %q", got, "opencode-go")
	}
}

// staleCatalogWithoutMeta builds a catalog whose in-memory snapshot has a
// Providers map for opencode-go but no ProvidersMeta — simulating an old disk
// snapshot written before provider-metadata support was added.
func staleCatalogWithoutMeta(t *testing.T) *catalog.Catalog {
	t.Helper()
	snap := &catalog.Snapshot{
		FetchedAt: time.Now(),
		Providers: map[string]map[string]catalog.Entry{
			"opencode-go": {
				"minimax-m2.7": {
					Provider: "opencode-go",
					ID:       "minimax-m2.7",
					Name:     "MiniMax M2.7",
				},
			},
		},
		// ProvidersMeta intentionally absent — old snapshot.
	}
	off := new(bool)
	cat, err := catalog.Load(catalog.LoadOptions{
		StateDir:          t.TempDir(),
		Source:            &catalogFetcher{snap: snap},
		BackgroundRefresh: off,
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	if _, err := cat.Refresh(context.Background()); err != nil {
		t.Fatalf("catalog.Refresh: %v", err)
	}
	t.Cleanup(func() { _ = cat.Close() })
	return cat
}

// TestResolveProviderModel_OpencodeGo_StaleSnapshotNoMeta exercises the case
// where a Catalog instance exists but its snapshot has no ProvidersMeta.
// The embedded fallback must provide the api_endpoint.
func TestResolveProviderModel_OpencodeGo_StaleSnapshotNoMeta(t *testing.T) {
	cat := staleCatalogWithoutMeta(t)
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7", map[string]any{
		"api_key": "sk-opencode-test",
	}, cat)
	if err != nil {
		t.Fatalf("ResolveProviderModel with stale snapshot: %v", err)
	}
	if r.Provider == nil {
		t.Fatal("provider is nil")
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "opencode-go" {
		t.Fatalf("model.Provider() = %q, want %q", got, "opencode-go")
	}
}

// TestResolveProviderModel_OpencodeGo_StaleSnapshot_EnvAPIKey exercises the
// combined stale-snapshot + env-based API key path.
func TestResolveProviderModel_OpencodeGo_StaleSnapshot_EnvAPIKey(t *testing.T) {
	t.Setenv("OPENCODE_API_KEY", "sk-env-opencode-test")
	cat := staleCatalogWithoutMeta(t)
	r, err := llm.ResolveProviderModel(t.Context(), "opencode-go", "minimax-m2.7",
		nil /* no opts */, cat)
	if err != nil {
		t.Fatalf("ResolveProviderModel with stale snapshot + env key: %v", err)
	}
	if r.Model == nil {
		t.Fatal("language model is nil")
	}
	if got := r.Model.Provider(); got != "opencode-go" {
		t.Fatalf("model.Provider() = %q, want %q", got, "opencode-go")
	}
}
