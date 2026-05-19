package components

import "testing"

func TestOnboardingConfiguredProviderSkipsAPIKey(t *testing.T) {
	w := OnboardingWizard{
		Providers:           []string{"anthropic", "openai"},
		ConfiguredProviders: map[string]bool{"openai": true},
		ProviderCursor:      1,
	}

	got, msg := w.HandleKey(OnboardingKey{Name: "enter"})
	if msg != nil {
		t.Fatalf("msg = %#v, want nil", msg)
	}
	if got.Step != OnboardStepPickModel {
		t.Fatalf("step = %v, want pick model", got.Step)
	}
	if got.ProviderName != "openai" {
		t.Fatalf("provider = %q, want openai", got.ProviderName)
	}
	if got.APIKey != "" || got.inputBuf != "" {
		t.Fatalf("api/input should stay empty, got api=%q input=%q", got.APIKey, got.inputBuf)
	}
}

func TestOnboardingUnconfiguredProviderStillRequiresAPIKey(t *testing.T) {
	w := OnboardingWizard{
		Providers:           []string{"anthropic", "openai"},
		ConfiguredProviders: map[string]bool{"openai": true},
	}

	got, msg := w.HandleKey(OnboardingKey{Name: "enter"})
	if msg != nil {
		t.Fatalf("msg = %#v, want nil", msg)
	}
	if got.Step != OnboardStepAPIKey {
		t.Fatalf("step = %v, want api key", got.Step)
	}
	if got.ProviderName != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", got.ProviderName)
	}
}

func TestOnboardingConfiguredProviderBackReturnsToWelcome(t *testing.T) {
	w := OnboardingWizard{
		Step:                OnboardStepPickModel,
		ProviderName:        "openai",
		ConfiguredProviders: map[string]bool{"openai": true},
	}

	got, _ := w.HandleKey(OnboardingKey{Name: "esc"})
	if got.Step != OnboardStepWelcome {
		t.Fatalf("step = %v, want welcome", got.Step)
	}
}
