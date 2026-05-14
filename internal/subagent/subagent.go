// Package subagent implements the registry and runtime for sub-agents
// dispatched by the `task` tool.
//
// # What is a sub-agent?
//
// A sub-agent is a one-shot delegation: the orchestrator hands a focused
// mission to a fresh agent, the sub-agent runs to completion in isolation,
// and the parent receives a single final assistant message as the tool
// result.  Sub-agents are useful for missions that would otherwise pollute
// the main conversation context (long codebase searches, focused refactors,
// documentation lookups).
//
// # Layering
//
// internal/subagent sits at the same architectural altitude as
// internal/agent.  It depends on bus, session, store, provider, permission,
// tool, and cost.  cmd/hygge/cli wires it together; it must not import
// internal/ui.
//
// # Stage A scope
//
// This package implements:
//
//   - [Type] and [Registry]: the catalogue of available sub-agent types,
//     with a built-in "general" type plus user / project TOML overrides.
//   - [Runner]: the entry point that runs a single sub-agent invocation
//     synchronously and returns the result.
//
// # Stage B
//
// Per-type model overrides are honoured at runtime via the
// [ProviderResolver] passed into [RunnerOptions].  When a [Type.Model]
// is set the runner resolves the override -- typically to a different
// provider entirely -- and runs the sub-agent against the returned
// provider and bare model id.  Malformed overrides are stripped at
// load time so the runtime always sees either an empty Model
// (inherit parent's) or a well-formed "<provider>/<model-id>".
//
// Live nested sub-transcripts in the TUI remain out of scope.  The
// TUI subscribes by session id and sub-agents run in their own
// sub-sessions, so the parent's UI naturally filters out the
// sub-agent's bus traffic.  Stage C will render the nested
// transcript.
//
// # Recursion guard
//
// Sub-agents never see the `task` tool.  The [Runner] strips it from the
// tool registry handed to every sub-agent invocation, regardless of what
// the type's TOML config asks for.  This is enforced by tests.
//
// # Permission
//
// Sub-agents share the parent's [permission.Engine].  The `task` tool
// itself asks once under [permission.CategoryAgent] before launching the
// sub-agent.  Once approved, the sub-agent's individual tool invocations
// go through the engine normally, so the user still controls each
// side-effect.  This matches the "approve the agent to act on my behalf"
// model rather than "approve every tool the agent might run".
//
// # Auditability
//
// Sub-sessions are persisted with [session.KindSubagent] and a ParentID
// linking back to the dispatching session.  They are NOT deleted on
// failure: even an aborted sub-agent run leaves a session row and its
// accumulated messages behind so the user can inspect what happened.
package subagent

import (
	"time"

	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/provider"
)

// Type describes a registered sub-agent kind.  The Name is what the
// parent model references through the `task` tool's `subagent_type`
// argument; everything else governs how the sub-agent is configured
// when launched.
type Type struct {
	// Name is the stable identifier matched against the regular
	// expression [a-z][a-z0-9_]*.  Must be unique within a registry.
	Name string

	// Description is one or two sentences telling the parent model
	// what this type is for.  Used by the task tool to populate its
	// input-schema description so the model can pick the right type.
	Description string

	// SystemPrompt is the full system prompt for this type's
	// sub-agent.  It should make explicit that the agent is operating
	// in isolation and must return one final message.
	SystemPrompt string

	// Tools is the allowlist of tool names this type may invoke.  An
	// empty slice means "all default sub-agent tools" (which is every
	// built-in except `task`).  The runtime always strips `task` from
	// the resolved list regardless of what TOML asks for.
	Tools []string

	// Source identifies which discovery layer this type came from.
	// Surfaced by `hygge subagents list`.  Values: "builtin", "user",
	// "project".
	Source string

	// Model, if non-empty, overrides the parent's provider / model
	// for this type.  Shape: "<provider>/<model-id>",
	// e.g. "anthropic/claude-haiku-4-5".  Malformed values are
	// stripped at TOML load time so the runtime only ever sees an
	// empty Model (meaning "inherit parent's") or a well-formed
	// reference.  The Runner resolves the override via the
	// [ProviderResolver] supplied through [RunnerOptions].
	Model string
}

// Result is what [Runner.Run] returns to the caller (in practice, the
// `task` tool).  It bundles the sub-session id, the final assistant
// text, and the usage accounting so the parent can surface a summary.
type Result struct {
	// SessionID is the id of the sub-session that was created and
	// persisted for this run.  Always set, even on error, so the
	// caller can link to the audit trail.
	SessionID string

	// FinalText is the text of the sub-agent's final assistant
	// message.  Empty when the run produced no textual output.
	FinalText string

	// Usage is the cumulative token usage from the sub-session, as
	// observed by the embedded agent loop.
	Usage provider.Usage

	// Cost is the sub-session's dollar cost.  Stage A does NOT roll
	// this into the parent session's totals -- the value is returned
	// here and embedded in the task tool's metadata so callers can
	// surface it explicitly.
	Cost cost.Money

	// Duration is the wall-clock time the Run call took, end to end.
	Duration time.Duration

	// HitIterLimit is true when the sub-agent loop terminated because
	// it hit the configured iteration cap rather than producing a
	// final answer.
	HitIterLimit bool
}
