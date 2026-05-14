package anthropic

import "encoding/json"

// Wire-level JSON types for the Anthropic Messages API.  Kept private to the
// adapter package; the agent never sees these.

// requestBody is the JSON body POSTed to /v1/messages.
type requestBody struct {
	Model       string         `json:"model"`
	Messages    []wireMessage  `json:"messages"`
	System      []systemBlock  `json:"system,omitempty"`
	Tools       []wireTool     `json:"tools,omitempty"`
	MaxTokens   int            `json:"max_tokens"`
	Temperature *float64       `json:"temperature,omitempty"`
	Stream      bool           `json:"stream"`
	Thinking    map[string]any `json:"thinking,omitempty"`
}

// systemBlock is the structured system-prompt shape used by the Messages API
// when cache_control is desired.  We always send the array form so prompt
// caching can be applied to a static prefix.
type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type wireMessage struct {
	Role    string        `json:"role"`
	Content []contentItem `json:"content"`
}

// contentItem is the heterogeneous block inside a wireMessage.  Only fields
// relevant to Type are populated.
type contentItem struct {
	Type string `json:"type"`

	// text block
	Text string `json:"text,omitempty"`

	// tool_use block
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result block
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// thinking block
	Thinking string `json:"thinking,omitempty"`

	// image block
	Source *imageSource `json:"source,omitempty"`

	// optional cache_control marker.  Applied to the final text block on the
	// final user message to anchor prompt caching.
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type cacheControl struct {
	Type string `json:"type"` // always "ephemeral" in v0.1
}

type wireTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// SSE envelope types.

type sseMessageStart struct {
	Type    string     `json:"type"`
	Message sseMessage `json:"message"`
}

type sseMessage struct {
	ID         string   `json:"id"`
	Model      string   `json:"model"`
	Role       string   `json:"role"`
	StopReason string   `json:"stop_reason"`
	Usage      sseUsage `json:"usage"`
}

type sseContentBlockStart struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	ContentBlock sseContentBlock `json:"content_block"`
}

type sseContentBlock struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ID       string          `json:"id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Input    json.RawMessage `json:"input,omitempty"`
	Thinking string          `json:"thinking,omitempty"`
}

type sseContentBlockDelta struct {
	Type  string   `json:"type"`
	Index int      `json:"index"`
	Delta sseDelta `json:"delta"`
}

type sseDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
}

type sseContentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type sseMessageDelta struct {
	Type  string          `json:"type"`
	Delta sseMessageStop1 `json:"delta"`
	Usage sseUsage        `json:"usage"`
}

type sseMessageStop1 struct {
	StopReason   string `json:"stop_reason"`
	StopSequence string `json:"stop_sequence"`
}

type sseUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

type sseError struct {
	Type  string         `json:"type"`
	Error sseErrorDetail `json:"error"`
}

type sseErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// countTokensResponse is the body returned by /v1/messages/count_tokens.
type countTokensResponse struct {
	InputTokens int64 `json:"input_tokens"`
}

// apiErrorResponse is the body returned by /v1/messages on non-2xx HTTP
// statuses.  Mirrors the SSE error shape.
type apiErrorResponse struct {
	Type  string         `json:"type"`
	Error sseErrorDetail `json:"error"`
}
