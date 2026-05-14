package openrouter

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

func TestNew_ExplicitAPIKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	p, err := New(map[string]any{"api_key": "sk-or-explicit"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Errorf("Name: %q", p.Name())
	}
}

func TestNew_FallsBackToEnvVar(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env")
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "openrouter" {
		t.Errorf("Name: %q", p.Name())
	}
}

func TestNew_OptsApiKeyWinsOverEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-env")
	_, err := New(map[string]any{"api_key": "sk-or-explicit"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNew_DollarEnvReference(t *testing.T) {
	t.Setenv("MY_CUSTOM_OPENROUTER", "sk-or-dollar")
	_, err := New(map[string]any{"api_key": "$MY_CUSTOM_OPENROUTER"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNew_DollarEnvMissing(t *testing.T) {
	t.Setenv("HYGGE_MISSING_OPENROUTER_KEY", "")
	_, err := New(map[string]any{"api_key": "$HYGGE_MISSING_OPENROUTER_KEY"})
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("want ErrAuth, got %v", err)
	}
}

func TestNew_OpRefUnsupported(t *testing.T) {
	_, err := New(map[string]any{"api_key": "op://Personal/openrouter/key"})
	if !errors.Is(err, provider.ErrAuthOpRefUnsupported) {
		t.Errorf("want ErrAuthOpRefUnsupported, got %v", err)
	}
}

func TestNew_MissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	_, err := New(nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("want ErrAuth, got %v", err)
	}
}

func TestNew_BaseURLOverride(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-x")
	p, err := New(map[string]any{"base_url": "https://custom.example.com/v1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

// TestNew_HeaderOverrides confirms the factory accepts custom
// http_referer / x_title without error.  Header transmission is verified
// by TestStream_AttributionHeaders below.
func TestNew_HeaderOverrides(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-x")
	p, err := New(map[string]any{
		"http_referer": "https://my.app.example.com",
		"x_title":      "my-fork",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("nil provider")
	}
}

func TestNew_BaseURLDefaultUsedWhenAbsent(t *testing.T) {
	if got := stringOpt(map[string]any{}, "base_url", defaultBaseURL); got != defaultBaseURL {
		t.Errorf("default base url: %q", got)
	}
}

func TestNew_BaseURLDefaultUsedWhenEmptyString(t *testing.T) {
	if got := stringOpt(map[string]any{"base_url": ""}, "base_url", defaultBaseURL); got != defaultBaseURL {
		t.Errorf("default base url: %q", got)
	}
}

func TestNew_BaseURLDefaultUsedWhenNonString(t *testing.T) {
	if got := stringOpt(map[string]any{"base_url": 42}, "base_url", defaultBaseURL); got != defaultBaseURL {
		t.Errorf("default base url: %q", got)
	}
}

// TestStringOptAllowEmpty: empty string is preserved as an explicit
// opt-out signal, but missing key / wrong type still fall back.
func TestStringOptAllowEmpty(t *testing.T) {
	cases := []struct {
		name string
		opts map[string]any
		want string
	}{
		{"absent uses default", map[string]any{}, "def"},
		{"explicit empty preserved", map[string]any{"k": ""}, ""},
		{"non-string falls back", map[string]any{"k": 42}, "def"},
		{"non-empty wins", map[string]any{"k": "value"}, "value"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := stringOptAllowEmpty(c.opts, "k", "def"); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestRegistered(t *testing.T) {
	f, err := provider.Get("openrouter")
	if err != nil {
		t.Fatalf("Get(openrouter): %v", err)
	}
	if f == nil {
		t.Fatal("factory is nil")
	}
}

func TestListModels(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-x")
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := p.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) < 6 {
		t.Errorf("want >= 6 models, got %d", len(models))
	}
	names := map[string]bool{}
	for _, m := range models {
		names[m.Name] = true
		if m.ContextWindow <= 0 {
			t.Errorf("%s: ContextWindow must be positive", m.Name)
		}
		// All curated entries are namespaced "<vendor>/<model>".
		if !containsSlash(m.Name) {
			t.Errorf("%s: expected namespaced <vendor>/<model> id", m.Name)
		}
	}
	for _, want := range []string{
		"anthropic/claude-sonnet-4-5",
		"anthropic/claude-opus-4-5",
		"openai/gpt-5",
		"openai/gpt-4o",
		"google/gemini-2.5-pro",
		"meta-llama/llama-3.3-70b-instruct",
		"mistralai/mistral-large-2411",
		"deepseek/deepseek-chat",
	} {
		if !names[want] {
			t.Errorf("missing model %s", want)
		}
	}
}

func TestModels_StableAcrossCalls(t *testing.T) {
	a := Models()
	b := Models()
	if len(a) != len(b) {
		t.Fatalf("len differs: %d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Name != b[i].Name {
			t.Errorf("Models()[%d] name: %q vs %q", i, a[i].Name, b[i].Name)
		}
	}
}

// TestStream_AttributionHeaders is the one openrouter-specific behaviour
// worth a real assertion: a Stream call carries the configured
// HTTP-Referer and X-Title headers.  The rest of the SSE plumbing is
// covered by openaicompat's own tests.
func TestStream_AttributionHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p, err := New(map[string]any{
		"api_key":  "sk-or-test",
		"base_url": srv.URL,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "anthropic/claude-sonnet-4-5",
		Messages: []session.Message{{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	//nolint:revive // intentional drain of the streaming channel
	for range ch {
	}

	if got := gotHeaders.Get("Authorization"); got != "Bearer sk-or-test" {
		t.Errorf("Authorization: %q", got)
	}
	if got := gotHeaders.Get("HTTP-Referer"); got != defaultHTTPReferer {
		t.Errorf("HTTP-Referer: got %q, want %q", got, defaultHTTPReferer)
	}
	if got := gotHeaders.Get("X-Title"); got != defaultXTitle {
		t.Errorf("X-Title: got %q, want %q", got, defaultXTitle)
	}
}

// TestStream_AttributionHeadersOverridden: user-supplied http_referer /
// x_title win over the defaults, and an explicit "" omits the header.
func TestStream_AttributionHeadersOverridden(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p, err := New(map[string]any{
		"api_key":      "sk-or-test",
		"base_url":     srv.URL,
		"http_referer": "https://my.fork.example/",
		"x_title":      "", // explicit opt-out
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "openai/gpt-5",
		Messages: []session.Message{{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "hi"}},
		}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	//nolint:revive // intentional drain of the streaming channel
	for range ch {
	}

	if got := gotHeaders.Get("HTTP-Referer"); got != "https://my.fork.example/" {
		t.Errorf("HTTP-Referer: got %q, want override", got)
	}
	if got := gotHeaders.Get("X-Title"); got != "" {
		t.Errorf("X-Title: got %q, want empty (opted out)", got)
	}
}

func containsSlash(s string) bool {
	for _, r := range s {
		if r == '/' {
			return true
		}
	}
	return false
}
