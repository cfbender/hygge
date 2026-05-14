// Package provider defines the abstraction every LLM provider implements and
// the streaming event protocol the agent loop consumes.
//
// # Streaming first
//
// Every provider call streams.  There is no separate "complete" entry point;
// a synchronous caller drains the stream and assembles the final message.
// Stream returns a receive-only channel that the implementation closes exactly
// once when the stream terminates.  The final event before close is either
// {Type: EventDone} or {Type: EventError, ...}.
//
// # Registry
//
// Each concrete provider lives in its own subpackage and self-registers in
// init() via Register.  The agent layer looks up providers by name through
// Get and instantiates them with their per-model options map.
//
// # Boundaries
//
// This package depends only on internal/session for the canonical Message and
// Part types.  It must not import internal/store, internal/agent, or
// internal/cost.  Cost calculation is the responsibility of internal/cost,
// which consumes the Usage struct emitted on EventUsage.
package provider

import (
	"context"

	"github.com/cfbender/hygge/internal/session"
)

// Provider is the abstraction every model provider implements.
type Provider interface {
	// Name returns the canonical provider name ("anthropic", "openai", ...).
	Name() string

	// Stream issues a completion request and returns a channel of streaming
	// events.  The channel is closed exactly once when the stream terminates
	// (success or error).  The final event before close is either
	// {Type: EventDone} or {Type: EventError, ...}.
	//
	// If the initial request cannot be issued (auth failure, malformed
	// request, transport error before any byte arrives), Stream returns a
	// non-nil error and a nil channel.  Errors that occur after the response
	// has begun streaming are delivered as EventError on the channel.
	Stream(ctx context.Context, req Request) (<-chan Event, error)

	// CountTokens returns an approximate input token count for the given
	// request.  Providers that expose a count endpoint use it; others may
	// return a tiktoken-like estimate.
	CountTokens(ctx context.Context, req Request) (int64, error)

	// ListModels returns the models this provider supports.  Implementations
	// may return a static slice when the upstream service does not expose a
	// list endpoint.
	ListModels(ctx context.Context) ([]Model, error)
}

// Request is the provider-agnostic completion request.  The same struct flows
// through every Provider; adapters translate it to their wire format.
type Request struct {
	// ModelName is the upstream model identifier (e.g. "claude-sonnet-4.5").
	ModelName string

	// Messages is the ordered conversation history.  Compaction summaries,
	// if any, are already folded in as system messages by the caller; the
	// adapter does not consult the Store.
	Messages []session.Message

	// System is the system prompt.  Empty means "no system prompt".  When
	// non-empty, adapters route this to the provider's system-prompt slot
	// rather than synthesising a system message.
	System string

	// Tools is the set of callable functions the assistant may invoke.
	// Empty means tool use is disabled for this call.
	Tools []Tool

	// Temperature is the sampling temperature in [0, 1].  Zero means "use
	// provider default" — adapters do not send a temperature field when
	// this is zero.
	Temperature float64

	// MaxTokens is the maximum number of output tokens.  Zero means "use
	// provider default".
	MaxTokens int

	// Options is a pass-through map for provider-specific knobs that have no
	// home in the cross-provider Request shape.  Examples for Anthropic
	// include {"thinking": {"type": "enabled", "budget_tokens": 8000}} and
	// {"cache": false} to disable prompt caching.
	Options map[string]any
}

// Tool describes a callable function the assistant can invoke.
type Tool struct {
	// Name is the function name surfaced to the model.
	Name string

	// Description is human-language documentation shown to the model.
	Description string

	// InputSchema is a JSON Schema object describing the function's
	// arguments.  Adapters serialise it directly into the provider's tool
	// schema field.
	InputSchema map[string]any
}

// Model describes a model available from a provider.
type Model struct {
	// Name is the upstream model identifier.
	Name string

	// ContextWindow is the maximum combined input+output token count the
	// model accepts.
	ContextWindow int64

	// MaxOutput is the maximum number of output tokens the model will
	// emit in a single call.
	MaxOutput int64

	// SupportsTools indicates whether the model accepts tool-use blocks.
	SupportsTools bool

	// SupportsImages indicates whether the model accepts inline image
	// parts in user messages.
	SupportsImages bool
}

// Usage is the cumulative token accounting reported by the provider on the
// trailing usage event of a stream.  Cost calculation is performed elsewhere
// (internal/cost); this struct is intentionally cost-free.
type Usage struct {
	// InputTokens is the number of prompt tokens billed for this turn.
	InputTokens int64

	// OutputTokens is the number of completion tokens emitted by the model.
	OutputTokens int64

	// CacheReadTokens is the number of tokens served from the provider's
	// prompt cache (Anthropic prompt caching).
	CacheReadTokens int64

	// CacheWriteTokens is the number of tokens written into the provider's
	// prompt cache on this turn.
	CacheWriteTokens int64
}
