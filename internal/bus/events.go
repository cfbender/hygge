package bus

import "time"

// SessionStart fires once when a session is created or resumed.
type SessionStart struct {
	// SessionID is the unique identifier for the session.
	SessionID string
	// Resumed is true when this is a resumed session rather than a new one.
	Resumed bool
	// At is the wall-clock time the session was created or resumed.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// SessionEnd fires when a session is closed (clean exit or fork point).
type SessionEnd struct {
	// SessionID is the unique identifier for the session that ended.
	SessionID string
	// At is the wall-clock time the session ended.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// MessageAppended fires after a message is persisted to a session.
type MessageAppended struct {
	// SessionID is the session the message was appended to.
	SessionID string
	// MessageID is the unique identifier for the appended message.
	MessageID string
	// Role is the participant role for the message: "user", "assistant", "tool", or "system".
	Role string
	// At is the wall-clock time the message was persisted.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// ToolCallRequested fires when the agent asks for a tool execution.
type ToolCallRequested struct {
	// SessionID is the session in which the tool was requested.
	SessionID string
	// MessageID is the message that contains the tool-call request.
	MessageID string
	// ToolName is the name of the tool being invoked.
	ToolName string
	// Args is the raw JSON arguments; consumers decode as needed.
	Args []byte
	// At is the wall-clock time the tool call was requested.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// ToolCallProgress fires for each chunk of streaming output produced by a
// tool that supports incremental progress (e.g. bash stdout/stderr lines).
// Subscribers (e.g. the UI) render these in real time so the user does not
// have to wait for a long-running tool to finish before seeing output.
type ToolCallProgress struct {
	// SessionID is the session in which the tool is running.
	SessionID string
	// MessageID is the message that contains the tool-call request.
	MessageID string
	// ToolUseID is the provider-assigned identifier for this tool call.
	// Lets a subscriber correlate multiple progress events to one call.
	ToolUseID string
	// ToolName is the name of the tool producing output.
	ToolName string
	// Stream is "stdout" or "stderr"; tools that produce other channels
	// should pick a label and document it.
	Stream string
	// Line is a single line of output, without a trailing newline.
	Line string
	// At is the wall-clock time the line was emitted.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// ToolCallCompleted fires after a tool finishes (success or error).
type ToolCallCompleted struct {
	// SessionID is the session in which the tool ran.
	SessionID string
	// MessageID is the message associated with the tool call.
	MessageID string
	// ToolName is the name of the tool that ran.
	ToolName string
	// Result is the raw JSON result; nil on error.
	Result []byte
	// Err is the error message; empty on success.
	Err string
	// DurationMs is the elapsed time in milliseconds.
	DurationMs int64
	// At is the wall-clock time the tool call completed.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// PermissionAsked fires when a permission decision is requested.
// Subscribers (e.g. UI, notifications) react to this.
type PermissionAsked struct {
	// RequestID is the unique identifier for this permission request.
	RequestID string
	// SessionID is the session that triggered the permission check.
	SessionID string
	// Category is the permission category: "file.read", "file.write", "shell", or "network".
	Category string
	// Target is the path or command being acted on.
	Target string
	// ToolName is which tool is asking for permission.
	ToolName string
	// At is the wall-clock time the permission was requested.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// PermissionReplied fires when a permission decision is recorded.
// The permission package owns publishing this after Ask() resolves.
type PermissionReplied struct {
	// RequestID is the unique identifier for the permission request this reply resolves.
	RequestID string
	// Decision is the outcome: "allow" or "deny".
	Decision string
	// Scope is how long the decision applies: "once", "session", or "always".
	Scope string
	// At is the wall-clock time the decision was recorded.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// CostUpdated fires when the running cost or token total for a session changes.
type CostUpdated struct {
	// SessionID is the session whose cost was updated.
	SessionID string
	// InputTokens is the cumulative count of input tokens for the session.
	InputTokens int64
	// OutputTokens is the cumulative count of output tokens for the session.
	OutputTokens int64
	// CacheReadTokens is the cumulative count of cache-read tokens for the session.
	CacheReadTokens int64
	// CacheWriteTokens is the cumulative count of cache-write tokens for the session.
	CacheWriteTokens int64
	// DollarsTotal is the cumulative cost in USD for the session.
	DollarsTotal float64
	// At is the wall-clock time of the cost update.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// ContextUsageUpdated fires after each provider response with the current window usage.
type ContextUsageUpdated struct {
	// SessionID is the session whose context window was measured.
	SessionID string
	// UsedTokens is the number of tokens currently occupying the context window.
	UsedTokens int64
	// MaxTokens is the total capacity of the context window.
	MaxTokens int64
	// PctUsed is the fraction of the context window used, in the range [0.0, 1.0].
	PctUsed float64
	// At is the wall-clock time of the measurement.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}
