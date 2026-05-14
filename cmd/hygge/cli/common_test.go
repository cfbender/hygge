package cli

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// fakeProvider is a no-network provider stand-in used by every test
// that bootstraps the CLI runtime.  It returns ListModels with a
// realistic context window so the agent's pct-used math has a
// denominator to work with, and Stream returns a closed channel
// immediately (the tests under cmd/hygge/cli never drive Send through
// to a real model — Task 11's agent tests already cover that path).
type fakeProvider struct{}

func (fakeProvider) Name() string { return "anthropic" }
func (fakeProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	close(ch)
	return ch, nil
}
func (fakeProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (fakeProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return []provider.Model{{
		Name:           "claude-sonnet-4-5",
		ContextWindow:  200_000,
		MaxOutput:      8192,
		SupportsTools:  true,
		SupportsImages: true,
	}}, nil
}

// fakeProviderFactory is the bootstrap-injectable factory variant.
func fakeProviderFactory(_ map[string]any) (provider.Provider, error) {
	return fakeProvider{}, nil
}

// hermeticHome returns a fresh tempdir with the standard .config and
// .local/state subdirectories, plus a SetTestOverrides hook installed
// so every CLI command sees the same hermetic paths.  The hook is
// cleared in t.Cleanup.
func hermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	xdgConfig := filepath.Join(home, ".config")
	xdgState := filepath.Join(home, ".local", "state")

	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   xdgConfig,
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: fakeProviderFactory,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})
	t.Cleanup(func() { SetTestOverrides(nil) })
	return home
}

// seedSession creates a session row in the store via a one-shot
// bootstrap — useful for tests that need the store to be non-empty.
// Returns the session id.
func seedSession(t *testing.T, projectDir string) string {
	t.Helper()
	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	sess, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: projectDir,
		Model: session.ModelRef{
			Provider: "anthropic",
			Name:     "claude-sonnet-4-5",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess.ID
}

func TestBootstrapBuildsAllComponents(t *testing.T) {
	hermeticHome(t)

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.Config == nil {
		t.Error("Config nil")
	}
	if rt.Provenance == nil {
		t.Error("Provenance nil")
	}
	if rt.State == nil {
		t.Error("State nil")
	}
	if rt.Bus == nil {
		t.Error("Bus nil")
	}
	if rt.Store == nil {
		t.Error("Store nil")
	}
	if rt.Provider == nil {
		t.Error("Provider nil")
	}
	if rt.Permission == nil {
		t.Error("Permission nil")
	}
	if rt.Tools == nil {
		t.Error("Tools nil")
	}
	if rt.Catalog == nil {
		t.Error("Catalog nil")
	}
	if rt.Agent == nil {
		t.Error("Agent nil")
	}
	if rt.Theme == nil {
		t.Error("Theme nil")
	}
}
