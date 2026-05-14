package openaicompat

import "encoding/json"

// Wire-level JSON types for the OpenAI Chat Completions API.  Kept private
// to the adapter package; the agent never sees these.

// chatRequest is the JSON body POSTed to /chat/completions.  Pointer/omitempty
// fields are used aggressively because OpenAI-compatible providers vary in
// what they tolerate — sending a zero-valued field where the spec says
// "optional" is the safest path.
type chatRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Tools         []chatTool     `json:"tools,omitempty"`
	ToolChoice    string         `json:"tool_choice,omitempty"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
}

// streamOptions enables the trailing usage chunk on the SSE stream.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// chatMessage is one entry of the OpenAI messages array.  Content is
// polymorphic: a plain string for simple turns, an array of content blocks
// when images or other typed parts are mixed in, or null on assistant
// messages that contain only tool_calls.  We model it as json.RawMessage so
// we can emit exactly the shape required for each case without contorting
// the type system.
type chatMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content,omitempty"`
	ToolCalls  []chatToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Name       string          `json:"name,omitempty"`
}

// chatToolCall is the OpenAI tool_call shape (function-call only — the API
// supports nothing else at v0.2).
type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function"
	Function chatToolFunction `json:"function"`
}

type chatToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // serialised JSON, NOT an object
}

// chatTool is one entry of the tools array.
type chatTool struct {
	Type     string                 `json:"type"` // always "function"
	Function chatToolFunctionSchema `json:"function"`
}

type chatToolFunctionSchema struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// chatContentBlock is an element of a polymorphic content array.  Used for
// user messages that mix text and images.
type chatContentBlock struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

type chatImageURL struct {
	URL string `json:"url"`
}

// SSE streamed chunk envelope.

type chatResponseChunk struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   *chatUsage   `json:"usage,omitempty"`
}

type chatChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason string    `json:"finish_reason,omitempty"`
}

type chatDelta struct {
	Role      string              `json:"role,omitempty"`
	Content   string              `json:"content,omitempty"`
	ToolCalls []chatToolCallDelta `json:"tool_calls,omitempty"`
}

// chatToolCallDelta is the streamed-form of a tool call.  Every field is
// optional; deltas across the same Index accumulate into a final tool call.
type chatToolCallDelta struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id,omitempty"`
	Type     string                 `json:"type,omitempty"`
	Function *chatToolCallDeltaFunc `json:"function,omitempty"`
}

type chatToolCallDeltaFunc struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type chatUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

// apiErrorResponse is the body returned by /chat/completions on non-2xx
// HTTP statuses for OpenAI-compatible providers.
type apiErrorResponse struct {
	Error apiErrorDetail `json:"error"`
}

type apiErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}
