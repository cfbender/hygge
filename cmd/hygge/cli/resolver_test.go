package cli

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/subagent"
)

// countingProvider is a minimal provider.Provider used to verify
// buildProviderResolver's caching: the test asserts the factory is
// invoked exactly once per provider name across multiple resolver
// calls.
type countingProvider struct{ name string }

func (c countingProvider) Name() string { return c.name }
func (c countingProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	close(ch)
	return ch, nil
}
func (c countingProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (c countingProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}

func TestBuildProviderResolver_ParentShortCircuits(t *testing.T) {
	// When the modelRef's provider name matches the parent's
	// configured provider, the resolver returns the parent without
	// any construction.  This is critical: the parent is always
	// already authenticated, so reusing it avoids redundant auth-
	// store reads (and tests that don't register any factory).
	cfg := &config.Config{}
	cfg.Model.Provider = "parentprov"
	cfg.Model.Name = "parent-model"
	parent := countingProvider{name: "parentprov"}

	r := buildProviderResolver(cfg, state.LoadOptions{}, parent)
	prv, modelID, err := r(context.Background(), "parentprov/some-model")
	if err != nil {
		t.Fatalf("resolver err: %v", err)
	}
	if prv != parent {
		t.Errorf("expected parent provider; got %#v", prv)
	}
	if modelID != "some-model" {
		t.Errorf("modelID: got %q want some-model", modelID)
	}
}

func TestBuildProviderResolver_InvalidRef(t *testing.T) {
	cfg := &config.Config{}
	cfg.Model.Provider = "parentprov"
	parent := countingProvider{name: "parentprov"}

	r := buildProviderResolver(cfg, state.LoadOptions{}, parent)
	_, _, err := r(context.Background(), "not-a-ref")
	if err == nil {
		t.Fatal("expected error for invalid ref")
	}
	if !errors.Is(err, subagent.ErrInvalidModelRef) {
		t.Errorf("error not wrapping ErrInvalidModelRef: %v", err)
	}
}

func TestBuildProviderResolver_UnknownProviderReturnsStub(t *testing.T) {
	// Unknown providers now resolve to a namedStub; ErrUnknownProvider
	// is no longer returned at build time — Fantasy/Catwalk handles
	// capability errors at runtime.
	cfg := &config.Config{}
	cfg.Model.Provider = "parentprov"
	parent := countingProvider{name: "parentprov"}

	r := buildProviderResolver(cfg, state.LoadOptions{HomeDir: t.TempDir()}, parent)
	prv, modelID, err := r(context.Background(), "no_such_provider/model")
	if err != nil {
		t.Fatalf("expected nil error for unknown provider; got %v", err)
	}
	if prv == nil {
		t.Fatal("expected non-nil stub provider for unknown name")
	}
	if prv.Name() != "no_such_provider" {
		t.Errorf("Name() = %q, want no_such_provider", prv.Name())
	}
	if modelID != "model" {
		t.Errorf("modelID = %q, want model", modelID)
	}
}

// testFactoryProvider registers itself under a unique name so we can
// exercise buildProviderResolver's cache path without colliding with
// other registered providers.
var testFactoryCalls atomic.Int32

const testFactoryName = "hyggetest_resolver_provider"

func init() {
	provider.Register(testFactoryName, func(_ map[string]any) (provider.Provider, error) {
		testFactoryCalls.Add(1)
		return countingProvider{name: testFactoryName}, nil
	})
}

func TestBuildProviderResolver_CachesByName(t *testing.T) {
	// Resolve the same modelRef twice; the factory must be invoked
	// only once.  Then resolve a different model id (same provider);
	// still only one factory call total.
	t.Setenv("HYGGETEST_RESOLVER_PROVIDER_API_KEY", "") // belt-and-braces

	startCalls := testFactoryCalls.Load()

	cfg := &config.Config{}
	cfg.Model.Provider = "parentprov"
	cfg.Model.Name = "p-model"
	parent := countingProvider{name: "parentprov"}

	r := buildProviderResolver(cfg, state.LoadOptions{HomeDir: t.TempDir()}, parent)

	p1, m1, err := r(context.Background(), testFactoryName+"/model-a")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if m1 != "model-a" {
		t.Errorf("m1: got %q", m1)
	}

	p2, m2, err := r(context.Background(), testFactoryName+"/model-b")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if m2 != "model-b" {
		t.Errorf("m2: got %q", m2)
	}
	if p1 != p2 {
		t.Error("expected cached provider to be reused across resolver calls")
	}
	if got := testFactoryCalls.Load() - startCalls; got != 1 {
		t.Errorf("factory call count: got %d want 1 (cached after first call)", got)
	}
}

func TestBuildProviderFor_UnknownProvider(t *testing.T) {
	cfg := &config.Config{}
	cfg.Model.Provider = "parentprov"
	// Unknown providers now return a namedStub so Fantasy/Catwalk can
	// resolve them at runtime rather than failing at build time.
	prv, err := buildProviderFor("no_such_provider_xyz", cfg, state.LoadOptions{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("expected nil error for unknown provider; got %v", err)
	}
	if prv == nil {
		t.Fatal("expected non-nil stub provider for unknown name")
	}
	if prv.Name() != "no_such_provider_xyz" {
		t.Errorf("Name() = %q, want %q", prv.Name(), "no_such_provider_xyz")
	}
}

func TestBuildProviderFor_EmptyName(t *testing.T) {
	cfg := &config.Config{}
	_, err := buildProviderFor("", cfg, state.LoadOptions{})
	if err == nil {
		t.Fatal("expected error for empty provider name")
	}
	if !strings.Contains(err.Error(), "empty provider name") {
		t.Errorf("error message: %v", err)
	}
}
