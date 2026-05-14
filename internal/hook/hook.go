// Package hook implements a programmatic event-gate framework for the agent
// loop.  Hooks react to four agent events — pre_tool, post_tool,
// pre_message, post_message — and can allow, deny, or modify the event
// payload.
//
// # Hook sources
//
// Hooks are configured via hooks.toml discovered at the standard
// four-layer paths:
//
//  1. ~/.agents/hooks.toml             — vendor-neutral, per-user
//  2. ~/.config/hygge/hooks.toml       — hygge-native, per-user
//  3. <project-root>/.agents/hooks.toml — vendor-neutral, per-project
//  4. <project-root>/.hygge/hooks.toml  — hygge-native, per-project
//
// # Shell hook protocol
//
// Each hook invokes an external command.  The agent serialises the
// [Input] struct as JSON and pipes it to the command's stdin.  The
// command writes an [Action] JSON object to stdout (or nothing, which is
// treated as Allow).  A non-zero exit code is treated as Deny; the deny
// reason is read from stderr (truncated to 1 KiB).
//
// # Sync vs async
//
// Sync hooks (the default) block the agent until Run returns or the
// timeout fires.  Async hooks are dispatched in a goroutine and are
// only valid for post_* events; declaring async on a pre_* event is
// rejected at load time and the hook is skipped with a warning.
//
// Post-message hooks are always treated as async by the registry: if a
// hook with events=["post_message"] is declared sync, the registry
// coerces it and logs a slog.Warn.
//
// # Lifecycle
//
// Call [New] to build an empty registry, [Registry.Register] to add
// hooks, or [Load] for the full TOML-discovery path.  After use call
// [Registry.Close] to wait up to 2 s for in-flight async goroutines.
package hook

import (
	"context"
	"encoding/json"
	"time"
)

// Event identifies which agent event a hook is reacting to.
type Event string

const (
	// EventPreTool fires before a tool is executed, after the permission
	// gate has passed.  Sync-only.  Can deny or modify tool input.
	EventPreTool Event = "pre_tool"

	// EventPostTool fires after a tool returns.  Sync hooks run first
	// (modify accumulates); async hooks are dispatched afterwards.
	EventPostTool Event = "post_tool"

	// EventPreMessage fires before the user message is persisted, right
	// at the start of Send.  Sync-only.  Can deny or modify the text.
	EventPreMessage Event = "pre_message"

	// EventPostMessage fires after the assistant message is committed.
	// Always treated as async regardless of the hook's declared mode.
	EventPostMessage Event = "post_message"
)

// Mode controls whether a hook runs synchronously (blocking) or
// asynchronously (fire-and-forget).
type Mode string

const (
	// ModeSync is the default.  The agent waits for Run to return (or
	// timeout to fire) before continuing.
	ModeSync Mode = "sync"

	// ModeAsync runs the hook in a goroutine.  Only valid for post_*
	// events; if declared on a pre_* event the hook is skipped with a
	// warning.
	ModeAsync Mode = "async"
)

// Decision is the hook's verdict on the event.
type Decision string

const (
	// DecisionAllow lets the event proceed unchanged.  The zero value of
	// Decision is treated as Allow.
	DecisionAllow Decision = "allow"

	// DecisionDeny blocks the event.  The agent surfaces Reason to the
	// model as an error result.
	DecisionDeny Decision = "deny"

	// DecisionModify allows the event but replaces parts of the payload
	// before continuing.  Only ModifiedToolInput (pre_tool),
	// ModifiedMessage (pre_message / post_message), and
	// ModifiedToolResult (post_tool) are acted on.
	DecisionModify Decision = "modify"
)

// Input is the JSON payload written to the hook's stdin.
type Input struct {
	// Event is the lifecycle event that triggered this hook.
	Event Event `json:"event"`

	// SessionID identifies the active session.
	SessionID string `json:"session_id"`

	// HookName is the name of this specific hook.
	HookName string `json:"hook_name"`

	// Pwd is the working directory of the agent process.
	Pwd string `json:"pwd"`

	// ToolName is set for pre_tool / post_tool.
	ToolName string `json:"tool_name,omitempty"`

	// ToolInput is the raw JSON arguments.  Set for pre_tool / post_tool.
	ToolInput json.RawMessage `json:"tool_input,omitempty"`

	// ToolResult is the result returned by the tool.  Set for post_tool
	// only.
	ToolResult *ToolResult `json:"tool_result,omitempty"`

	// Message is the text payload.  Set for pre_message (user input) and
	// post_message (assistant text).
	Message string `json:"message,omitempty"`
}

// ToolResult carries a tool's outcome for post_tool hooks.
type ToolResult struct {
	IsError bool   `json:"is_error"`
	Content string `json:"content"`
}

// Action is the hook's response, parsed from its stdout.  The zero
// value is "allow".
type Action struct {
	// Decision is "allow", "deny", or "modify".  Empty defaults to
	// "allow".
	Decision Decision `json:"decision,omitempty"`

	// Reason is surfaced to the agent on deny.
	Reason string `json:"reason,omitempty"`

	// ModifiedToolInput, when Decision="modify" on a pre_tool event,
	// replaces the tool's input args before execution.
	ModifiedToolInput json.RawMessage `json:"modified_tool_input,omitempty"`

	// ModifiedMessage, when Decision="modify" on a pre_message event,
	// replaces the user's message text.  On post_message it replaces
	// the assistant text (use with care — this is redaction territory).
	ModifiedMessage string `json:"modified_message,omitempty"`

	// ModifiedToolResult, when Decision="modify" on a post_tool event,
	// replaces the tool's result before returning to the model.
	ModifiedToolResult *ToolResult `json:"modified_tool_result,omitempty"`
}

// Warning captures a non-fatal hook execution error (e.g. malformed
// stdout) that fell-open.
type Warning struct {
	HookName string
	Err      string
}

// Hook is a single hook instance.  Implementations run synchronously
// from the agent's perspective (the agent waits for Run to return, up
// to the hook's Timeout).  Async dispatch is handled by the Registry.
//
// Implementations must be safe for concurrent Run calls from multiple
// sessions.
type Hook interface {
	// Name returns the unique identifier used in TOML and log output.
	Name() string

	// Description is the one-line human summary.
	Description() string

	// Source is "user" or "project".
	Source() string

	// Events returns the set of events this hook is registered for.
	Events() []Event

	// Mode returns the hook's declared mode.
	Mode() Mode

	// Timeout is the per-invocation timeout.  Zero means no timeout.
	Timeout() time.Duration

	// Run executes the hook.  in carries the event payload; the
	// returned Action describes the hook's decision.  A non-nil error
	// means the hook itself failed (not a deny decision).
	Run(ctx context.Context, in Input) (Action, error)
}
