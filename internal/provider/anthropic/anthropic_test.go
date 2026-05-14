package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// newTestServer returns an httptest.Server that serves the given SSE file
// from testdata.  It also captures the last request body for assertion.
func newTestServer(t *testing.T, sseFile string) (*httptest.Server, *[]byte) {
	t.Helper()
	body, err := os.ReadFile("testdata/" + sseFile) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read fixture %s: %v", sseFile, err)
	}
	var captured []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		captured = buf
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &captured
}

func newAdapter(t *testing.T, baseURL string, extra map[string]any) provider.Provider {
	t.Helper()
	opts := map[string]any{
		"api_key":  "test-key",
		"base_url": baseURL,
		"cache":    false,
	}
	for k, v := range extra {
		opts[k] = v
	}
	p, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func collect(ch <-chan provider.Event) []provider.Event {
	var out []provider.Event
	for ev := range ch {
		out = append(out, ev)
	}
	return out
}

func TestStream_BasicText(t *testing.T) {
	srv, _ := newTestServer(t, "stream_basic_text.sse")
	p := newAdapter(t, srv.URL, nil)

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	types := eventTypes(events)
	want := []provider.EventType{
		provider.EventMessageStart,
		provider.EventTextDelta,
		provider.EventTextDelta,
		provider.EventUsage,
		provider.EventDone,
	}
	if !equalTypes(types, want) {
		t.Fatalf("event types:\n got %v\nwant %v", types, want)
	}

	var sb strings.Builder
	for _, e := range events {
		if e.Type == provider.EventTextDelta {
			sb.WriteString(e.Text)
		}
	}
	if got := sb.String(); got != "Hello world" {
		t.Errorf("text: got %q", got)
	}

	// Verify final usage merges input from message_start and output from message_delta.
	last := events[len(events)-2]
	if last.Type != provider.EventUsage {
		t.Fatalf("expected usage as second-to-last; got %v", last.Type)
	}
	if last.Usage.InputTokens != 12 || last.Usage.OutputTokens != 42 {
		t.Errorf("usage: got %+v", last.Usage)
	}
}

func TestStream_WithToolUse(t *testing.T) {
	srv, _ := newTestServer(t, "stream_with_tool_use.sse")
	p := newAdapter(t, srv.URL, nil)

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "read a.go"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	var tu *provider.Event
	for i := range events {
		if events[i].Type == provider.EventToolUse {
			tu = &events[i]
		}
	}
	if tu == nil {
		t.Fatalf("expected an EventToolUse; got %v", eventTypes(events))
	}
	if tu.ToolID != "toolu_01" || tu.ToolName != "read" {
		t.Errorf("tool id/name: %+v", tu)
	}
	var parsed map[string]string
	if err := json.Unmarshal(tu.ToolInput, &parsed); err != nil {
		t.Fatalf("tool input not valid JSON: %v: %s", err, tu.ToolInput)
	}
	if parsed["path"] != "a.go" {
		t.Errorf("tool input: %v", parsed)
	}
}

func TestStream_WithThinking(t *testing.T) {
	srv, _ := newTestServer(t, "stream_with_thinking.sse")
	p := newAdapter(t, srv.URL, nil)

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	var thinking, text strings.Builder
	for _, e := range events {
		if e.Type == provider.EventThinkingDelta {
			thinking.WriteString(e.Text)
		}
		if e.Type == provider.EventTextDelta {
			text.WriteString(e.Text)
		}
	}
	if got := thinking.String(); got != "Let me think... about this." {
		t.Errorf("thinking: %q", got)
	}
	if got := text.String(); got != "The answer is 42." {
		t.Errorf("text: %q", got)
	}

	// Last event before close must be Done.
	last := events[len(events)-1]
	if last.Type != provider.EventDone {
		t.Errorf("last event: %v", last.Type)
	}
}

func TestStream_MidStreamError(t *testing.T) {
	srv, _ := newTestServer(t, "stream_error.sse")
	p := newAdapter(t, srv.URL, nil)

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	last := events[len(events)-1]
	if last.Type != provider.EventError {
		t.Fatalf("expected EventError last; got %v: %+v", last.Type, eventTypes(events))
	}
	if !errors.Is(last.Err, provider.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", last.Err)
	}
}

func TestStream_PartialThenDone(t *testing.T) {
	srv, _ := newTestServer(t, "stream_partial_then_done.sse")
	p := newAdapter(t, srv.URL, nil)

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)
	if events[len(events)-1].Type != provider.EventDone {
		t.Errorf("expected done; got %v", events[len(events)-1].Type)
	}
}

func TestStream_NetworkFailureMidStream(t *testing.T) {
	// Hijack the connection mid-stream and close it abruptly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"x\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("response writer does not support hijack")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// Close hard so the client sees a connection reset / EOF.
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	p := newAdapter(t, srv.URL, nil)
	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)
	// The parser may see the message_start, then either an EventError
	// (read error) or an EventDone (graceful EOF) depending on how the
	// runtime classifies the close.  We accept either as long as the
	// channel terminates.
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
	last := events[len(events)-1]
	if last.Type != provider.EventError && last.Type != provider.EventDone {
		t.Errorf("expected error/done terminal; got %v", last.Type)
	}
}

func TestStream_HTTP401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`))
	}))
	defer srv.Close()
	p := newAdapter(t, srv.URL, nil)
	_, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestStream_HTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"slow down"}}`))
	}))
	defer srv.Close()
	p := newAdapter(t, srv.URL, nil)
	_, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
}

func TestStream_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"api_error","message":"boom"}}`))
	}))
	defer srv.Close()
	p := newAdapter(t, srv.URL, nil)
	_, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if !errors.Is(err, provider.ErrTransient) {
		t.Errorf("expected ErrTransient, got %v", err)
	}
}

func TestStream_HTTP400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer srv.Close()
	p := newAdapter(t, srv.URL, nil)
	_, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestStream_RequestShape(t *testing.T) {
	srv, captured := newTestServer(t, "stream_basic_text.sse")
	p := newAdapter(t, srv.URL, nil)

	// A 3-turn conversation: user, assistant (which called a tool), tool result.
	msgs := []session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "read a.go"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{
			{Kind: session.PartText, Text: "Sure."},
			{Kind: session.PartToolUse, ToolID: "toolu_xyz", ToolName: "read", ToolInput: json.RawMessage(`{"path":"a.go"}`)},
		}},
		{Role: session.RoleTool, Parts: []session.Part{
			{Kind: session.PartToolResult, ToolUseID: "toolu_xyz", Content: "package main\n"},
		}},
	}

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		System:    "Be concise.",
		Messages:  msgs,
		Tools: []provider.Tool{{
			Name:        "read",
			Description: "read a file",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}},
		}},
		MaxTokens:   1024,
		Temperature: 0.5,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(*captured, &got); err != nil {
		t.Fatalf("unmarshal captured: %v: %s", err, string(*captured))
	}

	if got["model"] != "claude-sonnet-4.5" {
		t.Errorf("model: %v", got["model"])
	}
	if got["stream"] != true {
		t.Errorf("stream: %v", got["stream"])
	}
	if got["max_tokens"].(float64) != 1024 {
		t.Errorf("max_tokens: %v", got["max_tokens"])
	}
	if got["temperature"].(float64) != 0.5 {
		t.Errorf("temperature: %v", got["temperature"])
	}

	sys := got["system"].([]any)
	if len(sys) != 1 || sys[0].(map[string]any)["text"] != "Be concise." {
		t.Errorf("system: %v", sys)
	}

	msgsGot := got["messages"].([]any)
	if len(msgsGot) != 3 {
		t.Fatalf("messages: want 3, got %d", len(msgsGot))
	}

	// First: user text.
	first := msgsGot[0].(map[string]any)
	if first["role"] != "user" {
		t.Errorf("msg0 role: %v", first["role"])
	}
	c0 := first["content"].([]any)
	if c0[0].(map[string]any)["type"] != "text" {
		t.Errorf("msg0 content type: %v", c0[0])
	}

	// Second: assistant with text + tool_use.
	second := msgsGot[1].(map[string]any)
	if second["role"] != "assistant" {
		t.Errorf("msg1 role: %v", second["role"])
	}
	c1 := second["content"].([]any)
	if len(c1) != 2 {
		t.Fatalf("msg1 content: want 2, got %d", len(c1))
	}
	if c1[1].(map[string]any)["type"] != "tool_use" {
		t.Errorf("msg1 c1 type: %v", c1[1])
	}

	// Third: tool result mapped to user role.
	third := msgsGot[2].(map[string]any)
	if third["role"] != "user" {
		t.Errorf("msg2 role: want user, got %v", third["role"])
	}
	c2 := third["content"].([]any)
	if c2[0].(map[string]any)["type"] != "tool_result" {
		t.Errorf("msg2 c0 type: %v", c2[0])
	}

	tools := got["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "read" {
		t.Errorf("tools: %v", tools)
	}
}

func TestStream_HeadersPresent(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	p := newAdapter(t, srv.URL, nil)
	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	if gotHeaders.Get("x-api-key") != "test-key" {
		t.Errorf("x-api-key: %q", gotHeaders.Get("x-api-key"))
	}
	if gotHeaders.Get("anthropic-version") != apiVersion {
		t.Errorf("anthropic-version: %q", gotHeaders.Get("anthropic-version"))
	}
	if gotHeaders.Get("anthropic-beta") != promptCachingBeta {
		t.Errorf("anthropic-beta: %q", gotHeaders.Get("anthropic-beta"))
	}
}

func TestCountTokens(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != countTokensPath {
			t.Errorf("expected %s, got %s", countTokensPath, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens": 42}`))
	}))
	defer srv.Close()

	p := newAdapter(t, srv.URL, nil)
	n, err := p.CountTokens(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if n != 42 {
		t.Errorf("got %d, want 42", n)
	}
}

func TestCountTokens_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	p := newAdapter(t, srv.URL, nil)
	_, err := p.CountTokens(t.Context(), provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestListModels(t *testing.T) {
	p := newAdapter(t, "http://unused", nil)
	models, err := p.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(models) < 3 {
		t.Fatalf("want >= 3 models, got %d", len(models))
	}
	names := map[string]bool{}
	for _, m := range models {
		names[m.Name] = true
		if m.ContextWindow != 200_000 {
			t.Errorf("%s context: %d", m.Name, m.ContextWindow)
		}
		if !m.SupportsTools || !m.SupportsImages {
			t.Errorf("%s capability flags: %+v", m.Name, m)
		}
	}
	for _, want := range []string{"claude-sonnet-4.5", "claude-opus-4.7", "claude-haiku-3.5"} {
		if !names[want] {
			t.Errorf("missing model %s", want)
		}
	}
}

func TestAuthResolution_OptsApiKeyWins(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	key, err := resolveAPIKey(map[string]any{"api_key": "literal-key"})
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "literal-key" {
		t.Errorf("got %q", key)
	}
}

func TestAuthResolution_EnvFallback(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	key, err := resolveAPIKey(nil)
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "env-key" {
		t.Errorf("got %q", key)
	}
}

func TestAuthResolution_DollarEnvReference(t *testing.T) {
	t.Setenv("MY_CUSTOM_KEY", "dollar-key")
	key, err := resolveAPIKey(map[string]any{"api_key": "$MY_CUSTOM_KEY"})
	if err != nil {
		t.Fatalf("resolveAPIKey: %v", err)
	}
	if key != "dollar-key" {
		t.Errorf("got %q", key)
	}
}

func TestAuthResolution_DollarEnvMissing(t *testing.T) {
	t.Setenv("HYGGE_TEST_MISSING_KEY", "")
	_, err := resolveAPIKey(map[string]any{"api_key": "$HYGGE_TEST_MISSING_KEY"})
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestAuthResolution_OpRefUnsupported(t *testing.T) {
	_, err := resolveAPIKey(map[string]any{"api_key": "op://Personal/anthropic/key"})
	if !errors.Is(err, provider.ErrAuthOpRefUnsupported) {
		t.Errorf("expected ErrAuthOpRefUnsupported, got %v", err)
	}
}

func TestAuthResolution_Missing(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := resolveAPIKey(nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestNew_MissingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	_, err := New(nil)
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("expected ErrAuth, got %v", err)
	}
}

func TestNew_RegistersUnderName(t *testing.T) {
	// Verify that init() registered the factory.  We don't unregister
	// (no API for that), so just confirm Get succeeds.
	if _, err := provider.Get("anthropic"); err != nil {
		t.Errorf("Get(anthropic): %v", err)
	}
}

func TestStream_ContextCancellation(t *testing.T) {
	// Slow server that streams a chunk then sleeps far longer than ctx.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"x\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"m\",\"usage\":{\"input_tokens\":1}}}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(t.Context())
	p := newAdapter(t, srv.URL, nil)
	ch, err := p.Stream(ctx, provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	// Read the first event then cancel.
	first := <-ch
	if first.Type != provider.EventMessageStart {
		t.Errorf("first: %v", first.Type)
	}
	cancel()

	// Drain — channel must close in bounded time.
	done := make(chan struct{})
	go func() {
		//nolint:revive // draining channel to completion is the entire point
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("channel did not close after context cancel")
	}
}

func TestStream_RejectsEmptyModelName(t *testing.T) {
	p := newAdapter(t, "http://unused", nil)
	_, err := p.Stream(t.Context(), provider.Request{})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("expected ErrInvalidRequest, got %v", err)
	}
}

func TestProvider_Name(t *testing.T) {
	p := newAdapter(t, "http://unused", nil)
	if p.Name() != "anthropic" {
		t.Errorf("Name: %q", p.Name())
	}
}

func TestBuildRequestBody_CacheControlMarkers(t *testing.T) {
	// With cache=true, the system block and the final user text block should
	// receive cache_control markers.
	p, err := New(map[string]any{"api_key": "k", "base_url": "http://unused", "cache": true})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := p.(*adapter)
	body, err := a.buildRequestBody(provider.Request{
		ModelName: "claude-sonnet-4.5",
		System:    "sys",
		Messages: []session.Message{
			{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}},
		},
	}, true)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if !bytes.Contains(body, []byte(`"cache_control":{"type":"ephemeral"}`)) {
		t.Errorf("expected cache_control marker; body=%s", body)
	}
}

func TestBuildRequestBody_NoCacheWhenOff(t *testing.T) {
	p, err := New(map[string]any{"api_key": "k", "base_url": "http://unused", "cache": false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := p.(*adapter)
	body, err := a.buildRequestBody(provider.Request{
		ModelName: "claude-sonnet-4.5",
		System:    "sys",
		Messages: []session.Message{
			{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}},
		},
	}, true)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if bytes.Contains(body, []byte("cache_control")) {
		t.Errorf("expected no cache_control marker; body=%s", body)
	}
}

func TestBuildRequestBody_ThinkingOption(t *testing.T) {
	p, err := New(map[string]any{"api_key": "k", "base_url": "http://unused", "cache": false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := p.(*adapter)
	body, err := a.buildRequestBody(provider.Request{
		ModelName: "claude-sonnet-4.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
		Options:   map[string]any{"thinking": map[string]any{"type": "enabled", "budget_tokens": 8000}},
	}, true)
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if !bytes.Contains(body, []byte(`"thinking":{`)) {
		t.Errorf("expected thinking field; body=%s", body)
	}
}

// Live smoke test — gated behind HYGGE_LIVE=1 so CI never runs it.
func TestLive_AnthropicSmoke(t *testing.T) {
	if os.Getenv("HYGGE_LIVE") != "1" {
		t.Skip("HYGGE_LIVE not set; skipping live smoke test")
	}
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	p, err := New(map[string]any{"cache": false})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	ch, err := p.Stream(ctx, provider.Request{
		ModelName: "claude-haiku-3.5",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "Reply with the single word: hi"}}}},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var sawDone bool
	for ev := range ch {
		if ev.Type == provider.EventDone {
			sawDone = true
		}
		if ev.Type == provider.EventError {
			t.Fatalf("stream error: %v", ev.Err)
		}
	}
	if !sawDone {
		t.Errorf("never saw EventDone")
	}
}

func eventTypes(evs []provider.Event) []provider.EventType {
	out := make([]provider.EventType, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func equalTypes(a, b []provider.EventType) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
