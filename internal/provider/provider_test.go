package provider

import (
	"context"
	"errors"
	"sort"
	"testing"
)

// stubProvider is a Provider used only for registry tests.
type stubProvider struct{ name string }

func (s *stubProvider) Name() string                                          { return s.name }
func (s *stubProvider) Stream(context.Context, Request) (<-chan Event, error) { return nil, nil }
func (s *stubProvider) CountTokens(context.Context, Request) (int64, error)   { return 0, nil }
func (s *stubProvider) ListModels(context.Context) ([]Model, error)           { return nil, nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	called := false
	Register("alpha", func(map[string]any) (Provider, error) {
		called = true
		return &stubProvider{name: "alpha"}, nil
	})

	f, err := Get("alpha")
	if err != nil {
		t.Fatalf("Get alpha: %v", err)
	}
	p, err := f(nil)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if !called {
		t.Fatal("expected factory to be invoked")
	}
	if p.Name() != "alpha" {
		t.Errorf("Name: got %q", p.Name())
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	_, err := Get("nope")
	if !errors.Is(err, ErrUnknownProvider) {
		t.Errorf("expected ErrUnknownProvider, got %v", err)
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	f := func(map[string]any) (Provider, error) { return &stubProvider{}, nil }
	Register("dup", f)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on duplicate Register")
		}
	}()
	Register("dup", f)
}

func TestRegistry_RegisterEmptyNamePanics(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on empty name")
		}
	}()
	Register("", func(map[string]any) (Provider, error) { return nil, nil })
}

func TestRegistry_RegisterNilFactoryPanics(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on nil factory")
		}
	}()
	Register("x", nil)
}

func TestRegistry_Names(t *testing.T) {
	t.Cleanup(resetForTest)
	resetForTest()

	Register("b", func(map[string]any) (Provider, error) { return &stubProvider{}, nil })
	Register("a", func(map[string]any) (Provider, error) { return &stubProvider{}, nil })

	names := Names()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("Names: got %v", names)
	}
}

func TestEventType_Strings(t *testing.T) {
	// Sanity: every event type has a non-empty string form.
	all := []EventType{
		EventMessageStart, EventTextDelta, EventThinkingDelta,
		EventToolUse, EventUsage, EventDone, EventError,
	}
	for _, e := range all {
		if string(e) == "" {
			t.Errorf("empty event type")
		}
	}
}

func TestErrors_AreDistinct(t *testing.T) {
	all := []error{ErrAuth, ErrAuthOpRefUnsupported, ErrInvalidRequest, ErrRateLimited, ErrTransient, ErrUnknownProvider}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("expected %v != %v", a, b)
			}
		}
	}
}

func TestReasoning_IsOn(t *testing.T) {
	cases := []struct {
		name string
		r    Reasoning
		want bool
	}{
		{"zero value", Reasoning{}, false},
		{"empty effort", Reasoning{Effort: ""}, false},
		{"off", Reasoning{Effort: "off"}, false},
		{"low", Reasoning{Effort: "low"}, true},
		{"medium", Reasoning{Effort: "medium"}, true},
		{"high", Reasoning{Effort: "high"}, true},
		{"explicit budget alone", Reasoning{BudgetTokens: 5000}, true},
		{"unknown effort string", Reasoning{Effort: "extreme"}, false},
		{"unknown effort but budget set", Reasoning{Effort: "extreme", BudgetTokens: 5000}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.IsOn(); got != c.want {
				t.Errorf("IsOn=%v, want %v (%+v)", got, c.want, c.r)
			}
		})
	}
}

func TestReasoning_AnthropicBudget(t *testing.T) {
	cases := []struct {
		name string
		r    Reasoning
		want int
	}{
		{"off returns 0", Reasoning{}, 0},
		{"off explicit returns 0", Reasoning{Effort: "off"}, 0},
		{"low maps to 2048", Reasoning{Effort: "low"}, 2048},
		{"medium maps to 8192", Reasoning{Effort: "medium"}, 8192},
		{"high maps to 16384", Reasoning{Effort: "high"}, 16384},
		{"explicit budget wins over effort", Reasoning{Effort: "low", BudgetTokens: 12345}, 12345},
		{"explicit budget alone", Reasoning{BudgetTokens: 7777}, 7777},
		{"unknown effort returns 0", Reasoning{Effort: "extreme"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.r.AnthropicBudget(); got != c.want {
				t.Errorf("AnthropicBudget=%d, want %d (%+v)", got, c.want, c.r)
			}
		})
	}
}
