package llm_test

import (
	"errors"
	"testing"

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
		name       string
		providerID string
		modelID    string
		opts       map[string]any
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
			if got := r.Model.Provider(); got != tt.providerID {
				t.Fatalf("model provider = %q, want %q", got, tt.providerID)
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
