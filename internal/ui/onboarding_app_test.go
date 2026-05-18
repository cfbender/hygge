package ui

import (
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
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
