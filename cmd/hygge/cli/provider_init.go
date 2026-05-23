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
package cli

import (
	"context"

	"github.com/cfbender/hygge/internal/provider"
)

func init() {
	// Anthropic stub: satisfies provider.Provider for credential resolution
	// and agent Name() calls.  Actual streaming is handled by Fantasy.
	provider.Register("anthropic", func(_ map[string]any) (provider.Provider, error) {
		return namedStub{name: "anthropic"}, nil
	})

	// OpenAI stub: same purpose as the Anthropic stub above.
	provider.Register("openai", func(_ map[string]any) (provider.Provider, error) {
		return namedStub{name: "openai"}, nil
	})
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
