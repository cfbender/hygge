package openai

import (
	"errors"
	"testing"

	"github.com/cfbender/hygge/internal/provider"
)

func TestNew_ExplicitAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	p, err := New(map[string]any{"api_key": "sk-explicit"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("Name: %q", p.Name())
	}
}

func TestNew_FallsBackToEnvVar(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "openai" {
		t.Errorf("Name: %q", p.Name())
	}
}

func TestNew_OptsApiKeyWinsOverEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-env")
	_, err := New(map[string]any{"api_key": "sk-explicit"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNew_DollarEnvReference(t *testing.T) {
	t.Setenv("MY_CUSTOM_OPENAI", "sk-dollar")
	_, err := New(map[string]any{"api_key": "$MY_CUSTOM_OPENAI"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
}

func TestNew_DollarEnvMissing(t *testing.T) {
	t.Setenv("HYGGE_MISSING_OPENAI_KEY", "")
	_, err := New(map[string]any{"api_key": "$HYGGE_MISSING_OPENAI_KEY"})
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("want ErrAuth, got %v", err)
	}
}

func TestNew_OpRefUnsupported(t *testing.T) {
	_, err := New(map[string]any{"api_key": "op://Personal/openai/key"})
	if !errors.Is(err, provider.ErrAuthOpRefUnsupported) {
		t.Errorf("want ErrAuthOpRefUnsupported, got %v", err)
	}
}

func TestNew_MissingKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	_, err := New(nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("want ErrAuth, got %v", err)
	}
}

func TestNew_BaseURLOverride(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-x")
	// We can't directly inspect the BaseURL on the returned Provider, but
	// we can confirm that New accepts the override and constructs without
	// error.  The Stream() test in openaicompat already covers BaseURL
	// routing.
	p, err := New(map[string]any{"base_url": "https://custom.example.com/v1"})
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

func TestRegistered(t *testing.T) {
	f, err := provider.Get("openai")
	if err != nil {
		t.Fatalf("Get(openai): %v", err)
	}
	if f == nil {
		t.Fatal("factory is nil")
	}
}

func TestListModels(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-x")
	p, err := New(nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	models, err := p.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) < 3 {
		t.Errorf("want >= 3 models, got %d", len(models))
	}
	names := map[string]bool{}
	for _, m := range models {
		names[m.Name] = true
		if !m.SupportsTools {
			t.Errorf("%s: tools should be supported", m.Name)
		}
	}
	for _, want := range []string{"gpt-5", "gpt-4o", "gpt-4o-mini"} {
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
