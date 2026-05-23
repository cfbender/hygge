// Package cli — provider stub registrations.
//
// The legacy internal/provider/anthropic and internal/provider/openai
// packages have been removed.  Streaming for those providers is now
// handled entirely by Fantasy (via internal/llm.ResolveProviderModelWith).
//
// However, the provider registry is still consulted by bootstrap when
// resolving credentials and constructing the provider.Provider value
// the agent holds for metadata (Name()).  The stubs below satisfy those
// requirements without carrying any HTTP implementation.
//
// Auth-error semantics are preserved: each factory returns
// provider.ErrAuth when no credential can be found, matching the
// contract of the legacy adapters.  Bootstrap catches this error and
// falls back to a no-op stub so CLI inspection commands work without
// an API key.  callers that need a real streaming provider rely on
// Fantasy (buildFantasyModelResolver), which enforces its own key
// check independently.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/cfbender/hygge/internal/provider"
)

func init() {
	// Anthropic stub: satisfies provider.Provider for credential resolution
	// and agent Name() calls.  Actual streaming is handled by Fantasy.
	// Returns provider.ErrAuth when no credential is available so that
	// the bootstrap auth-fallback path works identically to the old adapter.
	provider.Register("anthropic", func(opts map[string]any) (provider.Provider, error) {
		if err := requireAnyKey(opts, "ANTHROPIC_API_KEY"); err != nil {
			return nil, err
		}
		return namedStub{name: "anthropic"}, nil
	})

	// OpenAI stub: same purpose and auth contract as the Anthropic stub above.
	provider.Register("openai", func(opts map[string]any) (provider.Provider, error) {
		if err := requireAnyKey(opts, "OPENAI_API_KEY"); err != nil {
			return nil, err
		}
		return namedStub{name: "openai"}, nil
	})
}

// requireAnyKey returns provider.ErrAuth (wrapped) when neither
// opts["api_key"] nor the canonical environment variable envVar contains
// a non-empty credential.  It is intentionally lenient: any non-empty
// value satisfies the check because the caller (Fantasy) validates the
// key's correctness when it actually makes the request.
func requireAnyKey(opts map[string]any, envVar string) error {
	if v, ok := opts["api_key"]; ok {
		if s, ok := v.(string); ok && s != "" {
			return nil
		}
	}
	if os.Getenv(envVar) != "" {
		return nil
	}
	return fmt.Errorf("%w: no credential found; set %s or run `hygge provider auth`", provider.ErrAuth, envVar)
}

// namedStub is a no-network provider.Provider that returns its name and
// satisfies the interface.  It is used when Fantasy handles the actual
// LLM calls and only the provider name is needed at runtime.
type namedStub struct{ name string }

func (n namedStub) Name() string { return n.name }
func (n namedStub) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	close(ch)
	return ch, nil
}
func (n namedStub) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (n namedStub) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}
