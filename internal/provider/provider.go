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
	// ModelName is the upstream model identifier (e.g. "claude-sonnet-4-5").
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

	// Reasoning configures the model's reasoning / extended-thinking
	// behaviour.  Adapters that don't support reasoning ignore this
	// field.  See [Reasoning] for the translation rules.
	Reasoning Reasoning
}

// Reasoning describes how much reasoning budget to give the model.
// The zero value means "off": no reasoning_effort is sent for OpenAI,
// no thinking block is sent for Anthropic.
//
// Effort is the user-facing knob.  Adapters that support discrete
// effort levels (OpenAI-family reasoning models: o1, o3, o4-*) map
// "low" / "medium" / "high" directly onto the reasoning_effort field.
// Adapters that take an explicit token budget (Anthropic extended
// thinking) map effort to BudgetTokens via [Reasoning.AnthropicBudget]
// unless BudgetTokens is non-zero, which overrides the mapping.
//
// Note on OpenAI: when an Effort other than off is requested but the
// model is NOT a reasoning-class model, the adapter silently drops
// the request — non-reasoning models reject reasoning_effort.  When
// the model IS reasoning-class but Effort is off, the adapter omits
// reasoning_effort and lets the server pick its default.
type Reasoning struct {
	// Effort is the discrete user-facing knob.  Empty string and "off"
	// both mean "no reasoning".  Other recognised values are "low",
	// "medium", and "high".
	Effort string

	// BudgetTokens is an explicit Anthropic-style token budget.  When
	// non-zero it overrides the Effort -> budget mapping.  Ignored by
	// OpenAI-family adapters; only the Effort field affects their
	// reasoning_effort wire field.
	BudgetTokens int
}

// IsOn reports whether reasoning should be requested at all.
// Returns true for Effort in {"low", "medium", "high"} or any non-zero
// BudgetTokens.  "" and "off" return false.
func (r Reasoning) IsOn() bool {
	if r.BudgetTokens > 0 {
		return true
	}
	switch r.Effort {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

// AnthropicBudget returns the token budget for Anthropic's thinking
// block.  Returns 0 when reasoning is off.  When BudgetTokens is set,
// returns it verbatim.  Otherwise maps low/medium/high to
// 2048/8192/16384 respectively.  These figures are the v0.1 mapping
// chosen to (a) sit comfortably under the model's max_tokens ceiling
// on a default 4096 cap once we raise it, and (b) give "high" enough
// room for non-trivial reasoning on hard tasks.
func (r Reasoning) AnthropicBudget() int {
	if !r.IsOn() {
		return 0
	}
	if r.BudgetTokens > 0 {
		return r.BudgetTokens
	}
	switch r.Effort {
	case "low":
		return 2048
	case "medium":
		return 8192
	case "high":
		return 16384
	default:
		return 0
	}
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

	// ReasoningTokens is the number of internal reasoning tokens the
	// model burned on this turn.  Populated when the provider exposes
	// the figure (OpenAI o-series via
	// completion_tokens_details.reasoning_tokens; Anthropic does not
	// report it separately — those tokens are folded into OutputTokens
	// for billing).  Zero when the model is not a reasoning model or
	// the provider does not surface the count.
	ReasoningTokens int64
}
