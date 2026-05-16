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

// SessionTitleUpdated fires when a session's display title (slug) changes.
// Subscribers (e.g. the UI sidebar) react to update their cached title without
// re-reading the session row from the store.
type SessionTitleUpdated struct {
	// SessionID is the session whose title changed.
	SessionID string
	// Title is the new display title (also the session's Slug column).
	Title string
	// Source identifies which path produced the title:
	//   - "preview"   — initial copy of the user's first message, before
	//                   the title model has run.
	//   - "generated" — produced by the small title model (RefreshSessionTitle).
	//   - "tool"      — produced by the rename_session tool.
	Source string
	// At is the wall-clock time the title was changed.
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
	// ToolUseID is the provider-assigned identifier for this tool call.
	// Matches the ToolUseID on the corresponding ToolCallProgress and
	// ToolCallCompleted events so subscribers can correlate all events
	// for a single tool invocation without relying on ordering.
	ToolUseID string
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
	// ToolUseID is the provider-assigned identifier for this tool call.
	// Matches the ToolUseID on the corresponding ToolCallRequested and
	// ToolCallProgress events so subscribers can correlate all events
	// for a single tool invocation without relying on ordering.
	ToolUseID string
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

// QuestionOption is one selectable answer for a QuestionAsked event.
type QuestionOption struct {
	ID    string
	Label string
}

// QuestionAsked fires when the model asks the user to choose between bounded
// options. Subscribers render an interactive prompt and publish QuestionAnswered.
type QuestionAsked struct {
	RequestID string
	SessionID string
	ToolName  string
	Question  string
	Options   []QuestionOption
	At        time.Time
}

// QuestionAnswered resolves a QuestionAsked prompt.
type QuestionAnswered struct {
	RequestID string
	AnswerID  string
	Answer    string
	Canceled  bool
	At        time.Time
}

// MCPStatusUpdated fires when async MCP startup changes one configured server's
// status. The UI uses this to let MCP sidebar rows move from starting to ready
// or failed without delaying the first frame.
type MCPStatusUpdated struct {
	Name      string
	Transport string
	Enabled   bool
	Ready     bool
	Error     string
	ToolCount int
	Source    string
	At        time.Time
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
	// ReasoningTokens is the cumulative count of reasoning tokens
	// reported by reasoning-class models.  OpenAI reports these
	// alongside completion_tokens (they are a SUBSET, not in addition)
	// — the field is exposed separately so callers can show the
	// breakdown in the footer without changing how OutputTokens
	// itself is interpreted.
	ReasoningTokens int64
	// DollarsTotal is the cumulative cost in USD for the session.
	DollarsTotal float64
	// At is the wall-clock time of the cost update.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// AssistantTextDelta fires for each streamed chunk of assistant text content.
// Subscribers (e.g. the UI) render these in real time so the user does not
// have to wait for the full assistant response before seeing output.
type AssistantTextDelta struct {
	// SessionID is the session the delta belongs to.
	SessionID string
	// Text is the new chunk of text (not the cumulative buffer).
	Text string
	// At is the wall-clock time the delta was emitted.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// AssistantThinkingDelta fires for each streamed chunk of assistant
// reasoning content (Anthropic "thinking" blocks).  Treated separately
// from AssistantTextDelta so the UI can render it differently.
type AssistantThinkingDelta struct {
	// SessionID is the session the delta belongs to.
	SessionID string
	// Text is the new chunk of reasoning text.
	Text string
	// At is the wall-clock time the delta was emitted.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// IterationLimitReached fires when the agent loop hits its configured
// maximum iteration count and aborts.  The agent persists an assistant
// message describing the abort and returns ErrIterationLimit to the caller.
type IterationLimitReached struct {
	// SessionID is the session whose loop hit the limit.
	SessionID string
	// Limit is the configured maximum number of iterations.
	Limit int
	// At is the wall-clock time the limit was hit.
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
	// ReasoningTokens is the per-turn count of reasoning tokens
	// reported by reasoning-class models (see [CostUpdated]).  Zero
	// for non-reasoning models.  Surfaced alongside the window stats
	// so the TUI footer can show the per-turn breakdown without a
	// second event type.
	ReasoningTokens int64
	// At is the wall-clock time of the measurement.
	// Populated by the caller; the bus does not set this field.
	At time.Time
}

// SubagentStarted fires when the `task` tool dispatches a sub-agent.
// Subscribers (Stage C TUI, audit log) use it to track nested
// transcripts as they begin.
type SubagentStarted struct {
	// SubSessionID is the id of the freshly created sub-session.
	SubSessionID string
	// ParentSessionID is the dispatching (primary) session.
	ParentSessionID string
	// ParentMessageID is the parent's `task` tool_use_id -- the
	// stable provider-assigned identifier of the tool call that
	// spawned this sub-agent.  Stage C uses it to anchor the
	// rendered nested transcript under the right tool message.
	// May be empty if the dispatcher did not supply one.
	ParentMessageID string
	// Type is the sub-agent type name (e.g. "general").
	Type string
	// Description is the short human-language mission label.
	Description string
	// Model is the resolved provider+model identifier the sub-agent
	// is running under, in `<provider>/<model-id>` form (e.g.
	// `anthropic/claude-haiku-4-5`).  The slash-separated form is
	// chosen over the bare model id so the TUI's nested-block
	// header makes the provider explicit when a per-type override
	// pins something other than the parent's provider.  May be
	// empty if the runner could not assemble the pair.
	Model string
	// At is the wall-clock time the sub-agent was dispatched.
	At time.Time
}

// CompactionRequested fires when compaction has been requested but not yet
// started.  The source field identifies who raised it:
//   - "user"      — the user ran /compact (TUI should open the confirmation modal).
//   - "threshold" — context usage crossed the configured threshold (TUI should
//     show the suggestion banner; user must still confirm before anything runs).
//
// The TUI filters on this event type to decide which affordance to show; it
// does NOT auto-start compaction on "threshold".
type CompactionRequested struct {
	// SessionID is the session for which compaction is being considered.
	SessionID string
	// Source is "user" when the user explicitly requested compaction, or
	// "threshold" when the advisory suggestion fires from the usage monitor.
	Source string
	// UsagePct is the current context-window usage as a percentage (0–100).
	UsagePct float64
	// At is the wall-clock time the event was created.
	At time.Time
}

// CompactionStarted fires when Agent.Compact begins generating the summary.
// The TUI shows a "compacting…" notice from this point until Completed or
// Failed arrives.
type CompactionStarted struct {
	// SessionID is the session being compacted.
	SessionID string
	// MessagesToCompact is the number of messages since the latest marker.
	MessagesToCompact int
	// InputTokensBefore is the cumulative session input-token count before
	// compaction.  The TUI may display this in the confirmation modal.
	InputTokensBefore int64
	// At is the wall-clock time compaction started.
	At time.Time
}

// CompactionCompleted fires when a compaction marker has been persisted.
// The TUI uses this event to clear the "compacting…" notice and show a
// 5-second toast summarising the result.
type CompactionCompleted struct {
	// SessionID is the session that was compacted.
	SessionID string
	// MarkerID is the ULID of the newly created compaction marker.
	MarkerID string
	// SummaryTokens is the number of input tokens used to generate the summary.
	SummaryTokens int64
	// InputTokensAfter is the estimated remaining context usage after the
	// marker is folded into the system prompt on the next turn.
	InputTokensAfter int64
	// DurationMs is the wall-clock elapsed time in milliseconds.
	DurationMs int64
	// At is the wall-clock time compaction completed.
	At time.Time
}

// CompactionFailed fires when Agent.Compact returns a hard error (anything
// other than [agent.ErrNothingToCompact], which is a clean no-op).  The TUI
// shows an error toast and clears the "compacting…" notice.
type CompactionFailed struct {
	// SessionID is the session for which compaction failed.
	SessionID string
	// Reason is the human-readable error message.
	Reason string
	// At is the wall-clock time the failure was detected.
	At time.Time
}

// QueueChanged fires when a session's prompt queue size changes —
// on enqueue, dequeue, or clear.  Consumers (e.g. the UI) use this
// to update placeholder text or queue-depth indicators.
type QueueChanged struct {
	// SessionID is the session whose queue changed.
	SessionID string
	// Count is the new queue depth (0 = queue is now empty).
	Count int
	// Prompts contains the first PartText of each queued send (len == Count).
	// Truncated for display; may be empty strings when a send has no text part.
	Prompts []string
	// At is the wall-clock time the event was created.
	At time.Time
}

// TodoChanged fires when a session's lightweight agent todo list changes.
// Consumers use the summary counts for compact live status without needing
// the full item list.
type TodoChanged struct {
	SessionID  string
	Total      int
	Incomplete int
	InProgress int
	Completed  int
	Cancelled  int
	At         time.Time
}

// TurnStarted fires at the moment the agent begins executing a turn —
// after the user message is persisted and hooks have run, just before the
// provider stream loop starts.  Paired with TurnCompleted.  The UI uses
// this to flip its busy state without relying on the goroutine that called
// Agent.Send (whose context may already be cancelled by the time the queued
// send fires).
type TurnStarted struct {
	// SessionID is the session whose turn is starting.
	SessionID string
	// At is the wall-clock time the turn began.
	At time.Time
}

// TurnCompleted fires after the agent loop finishes a full assistant turn
// successfully (no error, no iteration-limit hit).  The UI and notification
// layer use this to optionally alert the user that the assistant is ready.
type TurnCompleted struct {
	// SessionID is the session whose turn completed.
	SessionID string
	// At is the wall-clock time the turn finished.
	At time.Time
}

// SubagentCompleted fires when a sub-agent finishes -- success,
// iteration-limit abort, or hard failure.  Pairs 1:1 with
// [SubagentStarted].
type SubagentCompleted struct {
	// SubSessionID is the id of the sub-session.
	SubSessionID string
	// ParentSessionID is the dispatching (primary) session.
	ParentSessionID string
	// Type is the sub-agent type name.
	Type string
	// Description is the short human-language mission label.
	Description string
	// DurationMs is the wall-clock elapsed time, in milliseconds.
	DurationMs int64
	// CostUSD is the sub-session's accumulated cost in USD.  Stage A
	// does NOT roll this into the parent's totals; it is surfaced
	// here so observers can show a per-sub-agent breakdown.
	CostUSD float64
	// HitIterLimit is true when the sub-agent loop terminated
	// because it hit the configured iteration cap.
	HitIterLimit bool
	// At is the wall-clock time the sub-agent finished.
	At time.Time
}
