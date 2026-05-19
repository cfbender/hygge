package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

func TestInitOpensOnboardingWhenNeeded(t *testing.T) {
	b := bus.New()
	app, err := New(AppOptions{
		Bus:             b,
		Theme:           theme.ShellTheme(),
		ProjectDir:      t.TempDir(),
		NeedsOnboarding: true,
		KnownProviders:  []string{"openai"},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(); b.Close() })

	_ = app.Init()
	if top, ok := app.overlays.Top(); !ok || top != overlayOnboarding {
		t.Fatalf("top overlay = %q, %v; want onboarding", top, ok)
	}
	if app.onboardingWizard.Providers[0] != "openai" {
		t.Fatalf("providers = %#v", app.onboardingWizard.Providers)
	}
}

func TestOnboardingPasteAPIKey(t *testing.T) {
	b := bus.New()
	app, err := New(AppOptions{
		Bus:             b,
		Theme:           theme.ShellTheme(),
		ProjectDir:      t.TempDir(),
		NeedsOnboarding: true,
		KnownProviders:  []string{"openai"},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(); b.Close() })

	_ = app.Init()
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.onboardingWizard.Step != components.OnboardStepAPIKey {
		t.Fatalf("step = %v, want api key", app.onboardingWizard.Step)
	}

	app.Update(tea.PasteMsg{Content: " sk-pasted-key\r\n"})
	app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if got := app.onboardingWizard.ProviderKeys["openai"]; got != "sk-pasted-key" {
		t.Fatalf("provider key = %q, want pasted key", got)
	}
	if app.onboardingWizard.Step != components.OnboardStepProviderMore {
		t.Fatalf("step = %v, want provider more", app.onboardingWizard.Step)
	}
}
