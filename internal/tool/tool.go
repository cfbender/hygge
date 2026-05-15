// Package tool defines the tool execution framework for Hygge and its
// built-in tools.
//
// # IsError vs ToolError — the most important distinction
//
// A [Tool.Execute] call has two failure axes that must be kept separate:
//
//   - A [Result] with IsError: true is a NORMAL outcome the model is
//     expected to handle.  "File not found", "no matches", "command exited
//     non-zero", and even "user denied permission" are all IsError results.
//     They flow back to the model as ordinary tool output so the model can
//     adapt its plan.
//
//   - A returned error of type *ToolError is an INFRASTRUCTURE failure that
//     prevents the tool from producing a meaningful Result at all.  Bad JSON
//     in the arguments, an offline permission engine, or a recovered panic
//     all bubble up as ToolErrors and are surfaced to the user as a system
//     fault — not as a tool message the model should reason about.
//
// Tools never return both a Result and an error.  Either the Result is
// usable (IsError: true or false) and err is nil, or the Result is the
// zero value and err is a *ToolError.
//
// # Permission gating
//
// Every tool that touches the filesystem, runs a process, or hits the
// network MUST call [permission.Engine.Ask] before performing the side
// effect.  On [permission.ActionDeny] the tool returns an IsError Result
// (the user-deny case described above).  On engine/bus failure during the
// ask, the tool returns a *ToolError with Code = CodePermissionDenied.
//
// # JSON Schema is the source of truth
//
// [Tool.InputSchema] returns a JSON Schema object that is passed verbatim
// to the provider so the model knows how to construct arguments.  Tools
// decode the raw JSON into their own private args struct and validate the
// shape themselves; the schema is documentation for the model, not the
// runtime gate.
//
// # Streaming is opt-in
//
// Most tools return a single Result when they complete.  Tools that
// produce incremental output (currently just "bash") publish
// [bus.ToolCallProgress] events for each chunk and still return a complete
// Result at the end.  See the bash tool's documentation for details.
//
// # Parallel execution
//
// Tools that return true from [Tool.Parallelizable] may be invoked
// concurrently with other parallelizable tools within the same turn.
// Tools that return false are always executed serially after the
// parallel batch completes.
//
// The contract for Parallelizable: return true only when the tool's
// effects are commutative with any sibling parallelizable tool that could
// run in the same turn.  Read-only tools qualify; tools that mutate the
// filesystem, run shell commands, or hold shared mutable state must
// return false.
//
// Built-in mapping:
//
//   - read, grep, glob, skill, task → Parallelizable() == true
//   - bash, write, edit, todo       → Parallelizable() == false
//
// Plugin tools default to false; opt in via the Lua registration table:
//
//	hygge.register_tool { ..., parallelizable = true, ... }
package tool

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/permission"
)

// Tool is the interface every tool implements.
//
// Implementations must be safe for concurrent Execute calls from many
// goroutines.  The framework does not serialise tool calls.
type Tool interface {
	// Name is the stable identifier the model uses to invoke this tool.
	// Names must be unique within a registry and match the regular
	// expression [a-z][a-z0-9_]*.
	Name() string

	// Description is the human-language summary surfaced to the model in
	// the provider's tool-list payload.  Keep it terse: one or two
	// sentences explaining what the tool does and when to use it.
	Description() string

	// InputSchema returns a JSON Schema object describing the tool's
	// arguments.  The schema is shipped verbatim to the provider.
	// Implementations should return a fresh map per call so callers can
	// mutate it without affecting other tools.
	InputSchema() map[string]any

	// Execute runs the tool with the supplied raw JSON arguments and
	// returns either a [Result] (with err == nil) or a *ToolError (with
	// Result as the zero value).  See the package doc for the
	// IsError-vs-ToolError distinction.
	Execute(ctx context.Context, args json.RawMessage, ec ExecContext) (Result, error)

	// Parallelizable reports whether this tool is safe to invoke
	// concurrently with other parallelizable tools in the same turn.
	//
	// Return true only when the tool's effects are commutative with any
	// sibling parallelizable call in the same turn.  Read-only tools
	// (read, grep, glob, skill, task) return true.  Tools that mutate
	// the filesystem or run shell commands (bash, write, edit) return
	// false.
	//
	// The agent loop runs all parallelizable calls in a single concurrent
	// batch, then runs the sequential calls serially after the batch
	// completes.  Bus events from siblings within the parallel batch
	// arrive in undefined order; subscribers must not rely on
	// intra-batch ordering.
	//
	// Plugin tools default to false; they opt in via the registration
	// struct or the Lua `parallelizable = true` key.
	Parallelizable() bool
}

// ExecContext is the per-call runtime context handed to a tool.
//
// The struct is intentionally narrow: tools should reach for nothing else
// in their environment.  Add fields here when (and only when) a new tool
// genuinely needs them.
type ExecContext struct {
	// SessionID is the session that issued the tool call.  Used to scope
	// the read-tracker (anti-clobber) and to tag bus events.
	SessionID string

	// Pwd is the session's working directory; always an absolute path.
	// Tools resolve relative arguments against this.
	Pwd string

	// Bus is the in-process event bus.  Tools publish progress events
	// here; the agent loop is responsible for forwarding them onward.
	Bus *bus.Bus

	// Permission is the permission engine.  Tools call its Ask method
	// before any side effect.
	Permission *permission.Engine

	// ToolUseID is the provider-assigned identifier for this tool call.
	// Forwarded to bus events so subscribers can correlate progress with
	// the originating call.
	ToolUseID string

	// MessageID is the conversation message the tool call belongs to.
	// Forwarded to bus events.
	MessageID string

	// ModelName is the upstream model name the parent agent is using
	// for the current turn.  Tools that delegate to a fresh agent
	// (currently just `task`) read this so the sub-agent inherits
	// the parent's model.  Other tools may ignore it.
	ModelName string

	// Now is an injectable time source.  Defaults to time.Now via
	// ensureNow when zero.
	Now func() time.Time
}

// nowFn returns the configured clock, falling back to time.Now.
func (ec ExecContext) nowFn() func() time.Time {
	if ec.Now != nil {
		return ec.Now
	}
	return time.Now
}

// Result is the successful (or logical-error) outcome of a tool call.
type Result struct {
	// Content is the text representation of the result that flows back to
	// the model.  For binary or structured payloads, render a compact
	// human-readable summary here and attach the raw form to Metadata.
	Content string

	// IsError is true when the tool surfaced a logical error the model
	// must handle (file not found, command failed, permission denied,
	// ...).  See package doc for the IsError-vs-ToolError distinction.
	IsError bool

	// Metadata is structured information about the call: bytes written,
	// lines returned, exit codes, durations.  The agent loop persists
	// this alongside the tool message for audit and replay.
	Metadata map[string]any
}
