package provider

import "encoding/json"

// EventType identifies the kind of payload carried by an Event.
type EventType string

// Stream event kinds.
const (
	// EventMessageStart is the optional first event in a stream.  Adapters
	// may emit it to deliver initial usage information (e.g. input token
	// count) before the first content arrives.  Consumers that only care
	// about content may ignore this event.
	EventMessageStart EventType = "message_start"

	// EventTextDelta is a chunk of streamed assistant text.  Concatenating
	// the Text field across consecutive deltas yields the full text.
	EventTextDelta EventType = "text_delta"

	// EventThinkingDelta is a chunk of streamed assistant reasoning content
	// (Anthropic "thinking" blocks).  Treated separately from EventTextDelta
	// so the UI can render it differently.
	EventThinkingDelta EventType = "thinking_delta"

	// EventToolUse is emitted once per complete tool call, after the
	// provider has finished streaming its argument JSON.  ToolInput is
	// guaranteed to be valid JSON.
	EventToolUse EventType = "tool_use"

	// EventUsage carries the final token usage for the stream.  Typically
	// emitted just before EventDone.
	EventUsage EventType = "usage"

	// EventDone signals successful end of stream.  No further events are
	// emitted after EventDone; the channel is closed immediately after.
	EventDone EventType = "done"

	// EventError signals stream termination due to an error.  Err is
	// non-nil and classified via the typed errors in this package
	// (ErrAuth, ErrRateLimited, ErrTransient, ErrInvalidRequest) where the
	// provider gave us enough information to do so.
	EventError EventType = "error"
)

// Event is a single item in a streaming response.  Only the fields relevant
// to Type are populated; consumers should switch on Type and read only the
// associated fields.
type Event struct {
	// Type discriminates which other fields are populated.
	Type EventType

	// Text carries the delta text for EventTextDelta and EventThinkingDelta.
	Text string

	// ToolID, ToolName, ToolInput are populated for EventToolUse.  ToolInput
	// is the complete, validated JSON arguments object.
	ToolID    string
	ToolName  string
	ToolInput json.RawMessage

	// Usage is populated for EventMessageStart and EventUsage.
	Usage Usage

	// Err is populated for EventError.
	Err error
}
