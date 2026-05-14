// Package anthropic implements the provider.Provider interface against
// Anthropic's Messages API.
//
// # Wire protocol
//
// The adapter POSTs to /v1/messages with stream=true and consumes an
// SSE response.  Tool-use blocks are part of the same stream: their JSON
// arguments arrive as input_json_delta fragments and the adapter accumulates
// them into a single complete JSON object before emitting provider.EventToolUse.
//
// # Prompt caching
//
// The adapter unconditionally requests anthropic-beta: prompt-caching, and
// (unless disabled via opts["cache"] = false) attaches cache_control: ephemeral
// to the system prompt and to the trailing content block of the final user
// message.  This anchors prompt caching at the longest stable suffix of the
// request without requiring callers to think about it.
//
// # Auth
//
// The API key is resolved in this order: opts["api_key"] (literal,
// $ENVVAR reference, or op:// reference), then ANTHROPIC_API_KEY, then a
// typed ErrAuth.  op:// references return ErrAuthOpRefUnsupported in v0.1;
// the 1Password CLI shell-out is a Task 10 item.
//
// # Boundaries
//
// This package depends only on internal/provider and internal/session.  It
// must not import internal/store, internal/agent, or internal/cost.
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cfbender/hygge/internal/provider"
)

const (
	defaultBaseURL    = "https://api.anthropic.com"
	apiVersion        = "2023-06-01"
	promptCachingBeta = "prompt-caching-2024-07-31"
	streamPath        = "/v1/messages"
	countTokensPath   = "/v1/messages/count_tokens" //nolint:gosec // not credentials
)

func init() {
	provider.Register("anthropic", New)
}

// adapter is the concrete provider.Provider implementation.
type adapter struct {
	apiKey  string
	baseURL string
	client  *http.Client
	cache   bool // attach cache_control markers when true
}

// New constructs an Anthropic adapter from the given options map.  Supported
// keys:
//
//   - "api_key" (string): explicit key, or "$ENV" / "op://..." reference.
//   - "base_url" (string): override the API base URL (testing).
//   - "cache" (bool): attach prompt-caching markers.  Defaults to true.
//   - "timeout_seconds" (int): HTTP client timeout.  Defaults to 600s.
//
// Returns provider.ErrAuth (wrapped) when no key can be resolved.
func New(opts map[string]any) (provider.Provider, error) {
	key, err := resolveAPIKey(opts)
	if err != nil {
		return nil, err
	}
	a := &adapter{
		apiKey:  key,
		baseURL: defaultBaseURL,
		cache:   true,
		client:  &http.Client{Timeout: 600 * time.Second},
	}
	if v, ok := opts["base_url"].(string); ok && v != "" {
		a.baseURL = v
	}
	if v, ok := opts["cache"].(bool); ok {
		a.cache = v
	}
	if v, ok := opts["timeout_seconds"].(int); ok && v > 0 {
		a.client = &http.Client{Timeout: time.Duration(v) * time.Second}
	}
	return a, nil
}

// Name implements provider.Provider.
func (a *adapter) Name() string { return "anthropic" }

// Stream implements provider.Provider.
func (a *adapter) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	body, err := a.buildRequestBody(req, true)
	if err != nil {
		return nil, err
	}
	httpReq, err := a.newRequest(ctx, http.MethodPost, streamPath, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Accept", "text/event-stream")

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic: stream request: %w", classifyTransportError(err))
	}
	if resp.StatusCode/100 != 2 {
		return nil, a.readHTTPError(resp)
	}

	out := make(chan provider.Event, streamEventBufSize)
	go parseStream(ctx, resp.Body, out)
	return out, nil
}

// CountTokens implements provider.Provider.
func (a *adapter) CountTokens(ctx context.Context, req provider.Request) (int64, error) {
	body, err := a.buildRequestBody(req, false)
	if err != nil {
		return 0, err
	}
	httpReq, err := a.newRequest(ctx, http.MethodPost, countTokensPath, body)
	if err != nil {
		return 0, err
	}

	resp, err := a.client.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("anthropic: count_tokens: %w", classifyTransportError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		return 0, a.readHTTPError(resp)
	}
	var ct countTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&ct); err != nil {
		return 0, fmt.Errorf("anthropic: decode count_tokens: %w", err)
	}
	return ct.InputTokens, nil
}

// ListModels implements provider.Provider.
//
// Anthropic does not expose a public models list endpoint.  The static slice
// returned here is replaced by the models.dev catalog integration in Task 10.
func (a *adapter) ListModels(_ context.Context) ([]provider.Model, error) {
	return []provider.Model{
		{
			Name:           "claude-sonnet-4-5",
			ContextWindow:  200_000,
			MaxOutput:      8192,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "claude-opus-4-5",
			ContextWindow:  200_000,
			MaxOutput:      8192,
			SupportsTools:  true,
			SupportsImages: true,
		},
		{
			Name:           "claude-haiku-4-5",
			ContextWindow:  200_000,
			MaxOutput:      8192,
			SupportsTools:  true,
			SupportsImages: true,
		},
	}, nil
}

// buildRequestBody serialises a provider.Request into the Messages API JSON
// body.  When stream is true, the "stream" field is set and max_tokens
// defaults to 4096 (Anthropic requires the field on stream calls).
func (a *adapter) buildRequestBody(req provider.Request, stream bool) ([]byte, error) {
	if req.ModelName == "" {
		return nil, fmt.Errorf("%w: model_name is required", provider.ErrInvalidRequest)
	}
	wireMsgs, sysBlocks, err := toWireMessages(req.Messages, a.cache)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", provider.ErrInvalidRequest, err)
	}
	if req.System != "" {
		sb := systemBlock{Type: "text", Text: req.System}
		if a.cache {
			sb.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		sysBlocks = append([]systemBlock{sb}, sysBlocks...)
	}

	body := requestBody{
		Model:     req.ModelName,
		Messages:  wireMsgs,
		System:    sysBlocks,
		Tools:     toWireTools(req.Tools),
		MaxTokens: req.MaxTokens,
		Stream:    stream,
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = 4096
	}
	if req.Temperature > 0 {
		t := req.Temperature
		body.Temperature = &t
	}

	// Reasoning translation: the typed Request.Reasoning field wins
	// over the legacy Options["thinking"] passthrough so callers can
	// migrate without touching their adapter glue.  When the typed
	// field is off and a legacy thinking option is present, the
	// legacy shape is forwarded verbatim for back-compat.
	switch {
	case req.Reasoning.IsOn():
		budget := req.Reasoning.AnthropicBudget()
		body.Thinking = map[string]any{
			"type":          "enabled",
			"budget_tokens": budget,
		}
		// Anthropic requires max_tokens >= budget_tokens + headroom.
		// Only raise the DEFAULT — callers who pinned an explicit
		// MaxTokens keep their value (we trust they know what they
		// want, even if the server may reject it).
		if req.MaxTokens == 0 && body.MaxTokens < budget+1024 {
			body.MaxTokens = budget + 1024
		}
	case req.Options != nil:
		if t, ok := req.Options["thinking"].(map[string]any); ok {
			body.Thinking = t
		}
	}
	return json.Marshal(body)
}

// newRequest constructs an authenticated HTTP request with the standard
// Anthropic headers attached.
func (a *adapter) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	url := a.baseURL + path
	r, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: build request: %w", err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-api-key", a.apiKey)
	r.Header.Set("anthropic-version", apiVersion)
	r.Header.Set("anthropic-beta", promptCachingBeta)
	return r, nil
}

// readHTTPError consumes an error response body and returns a typed,
// classified error.  The response body is read and closed regardless.
func (a *adapter) readHTTPError(resp *http.Response) error {
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var detail apiErrorResponse
	_ = json.Unmarshal(raw, &detail)

	msg := detail.Error.Message
	if msg == "" {
		msg = string(raw)
	}
	if msg == "" {
		msg = resp.Status
	}

	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: %s", provider.ErrAuth, msg)
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return fmt.Errorf("%w: %s", provider.ErrInvalidRequest, msg)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", provider.ErrRateLimited, msg)
	default:
		if resp.StatusCode >= 500 {
			return fmt.Errorf("%w: %d %s", provider.ErrTransient, resp.StatusCode, msg)
		}
		return fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, msg)
	}
}

// classifyTransportError wraps low-level HTTP transport errors as ErrTransient,
// except for context errors which are returned verbatim so callers can branch
// on ctx.Err().
func classifyTransportError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %w", provider.ErrTransient, err)
}
