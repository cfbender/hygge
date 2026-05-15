package openaicompat

import (
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

	"charm.land/catwalk/pkg/catwalk"

	"github.com/cfbender/hygge/internal/catalog"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// newSSEServer returns an httptest.Server that streams the given SSE fixture
// from testdata.  It also captures the last request body and headers for
// assertion.
func newSSEServer(t *testing.T, sseFile string) (*httptest.Server, *capturedReq) {
	t.Helper()
	body, err := os.ReadFile("testdata/" + sseFile) //nolint:gosec // test fixture path
	if err != nil {
		t.Fatalf("read fixture %s: %v", sseFile, err)
	}
	capt := &capturedReq{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ := io.ReadAll(r.Body)
		capt.body = buf
		capt.headers = r.Header.Clone()
		capt.path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, capt
}

type capturedReq struct {
	body    []byte
	headers http.Header
	path    string
}

func newCompat(t *testing.T, cfg Config) provider.Provider {
	t.Helper()
	if cfg.Name == "" {
		cfg.Name = "test"
	}
	if cfg.APIKey == "" {
		cfg.APIKey = "sk-test"
	}
	if cfg.Models == nil {
		cfg.Models = []provider.Model{}
	}
	p, err := New(cfg)
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

func eventTypes(evs []provider.Event) []provider.EventType {
	out := make([]provider.EventType, 0, len(evs))
	for _, e := range evs {
		out = append(out, e.Type)
	}
	return out
}

func basicReq() provider.Request {
	return provider.Request{
		ModelName: "gpt-test",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
	}
}

// --- Stream tests ---

func TestStream_BasicText(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	gotTypes := eventTypes(events)
	want := []provider.EventType{
		provider.EventMessageStart,
		provider.EventTextDelta,
		provider.EventTextDelta,
		provider.EventUsage,
		provider.EventDone,
	}
	if !equalTypes(gotTypes, want) {
		t.Fatalf("event types:\n got %v\nwant %v", gotTypes, want)
	}

	var sb strings.Builder
	for _, e := range events {
		if e.Type == provider.EventTextDelta {
			sb.WriteString(e.Text)
		}
	}
	if sb.String() != "Hello world" {
		t.Errorf("text: %q", sb.String())
	}

	usage := events[len(events)-2]
	if usage.Usage.InputTokens != 12 || usage.Usage.OutputTokens != 42 {
		t.Errorf("usage: %+v", usage.Usage)
	}
}

func TestStream_WithToolUse(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_with_tool_use.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
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
		t.Fatalf("expected EventToolUse, got %v", eventTypes(events))
	}
	if tu.ToolID != "call_abc" || tu.ToolName != "read" {
		t.Errorf("tool id/name: id=%q name=%q", tu.ToolID, tu.ToolName)
	}

	var parsed map[string]string
	if err := json.Unmarshal(tu.ToolInput, &parsed); err != nil {
		t.Fatalf("tool input invalid: %v: %s", err, tu.ToolInput)
	}
	if parsed["path"] != "a.go" {
		t.Errorf("tool input: %v", parsed)
	}
}

func TestStream_MultiToolCalls(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_multi_tool_calls.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	var tools []provider.Event
	for _, e := range events {
		if e.Type == provider.EventToolUse {
			tools = append(tools, e)
		}
	}
	if len(tools) != 2 {
		t.Fatalf("want 2 EventToolUse, got %d: %v", len(tools), eventTypes(events))
	}

	// Order is by first-seen index: 0 then 1.
	if tools[0].ToolID != "call_one" || tools[0].ToolName != "read" {
		t.Errorf("tool[0]: id=%q name=%q", tools[0].ToolID, tools[0].ToolName)
	}
	if tools[1].ToolID != "call_two" || tools[1].ToolName != "write" {
		t.Errorf("tool[1]: id=%q name=%q", tools[1].ToolID, tools[1].ToolName)
	}

	var first map[string]any
	if err := json.Unmarshal(tools[0].ToolInput, &first); err != nil {
		t.Fatalf("tool[0] args invalid: %v: %s", err, tools[0].ToolInput)
	}
	if first["path"] != "a.go" {
		t.Errorf("tool[0] args: %v", first)
	}

	var second map[string]any
	if err := json.Unmarshal(tools[1].ToolInput, &second); err != nil {
		t.Fatalf("tool[1] args invalid: %v: %s", err, tools[1].ToolInput)
	}
	if second["path"] != "b.txt" || second["data"] != "x" {
		t.Errorf("tool[1] args: %v", second)
	}
}

func TestStream_DoneImmediately(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_done_immediately.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	// Must end with Done.  No Usage event because the fixture omits it.
	last := events[len(events)-1]
	if last.Type != provider.EventDone {
		t.Errorf("last: %v", last.Type)
	}
	for _, e := range events {
		if e.Type == provider.EventUsage {
			t.Errorf("did not expect usage; got %+v", e)
		}
	}
}

func TestStream_WithUsage(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_with_usage.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	var u *provider.Event
	for i := range events {
		if events[i].Type == provider.EventUsage {
			u = &events[i]
		}
	}
	if u == nil {
		t.Fatalf("missing EventUsage: %v", eventTypes(events))
	}
	if u.Usage.InputTokens != 3 || u.Usage.OutputTokens != 1 {
		t.Errorf("usage: %+v", u.Usage)
	}
}

func TestStream_HTTP401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid key","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()
	p := newCompat(t, Config{BaseURL: srv.URL})

	_, err := p.Stream(t.Context(), basicReq())
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("want ErrAuth, got %v", err)
	}
}

func TestStream_HTTP429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer srv.Close()
	p := newCompat(t, Config{BaseURL: srv.URL})

	_, err := p.Stream(t.Context(), basicReq())
	if !errors.Is(err, provider.ErrRateLimited) {
		t.Errorf("want ErrRateLimited, got %v", err)
	}
}

func TestStream_HTTP500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()
	p := newCompat(t, Config{BaseURL: srv.URL})

	_, err := p.Stream(t.Context(), basicReq())
	if !errors.Is(err, provider.ErrTransient) {
		t.Errorf("want ErrTransient, got %v", err)
	}
}

func TestStream_HTTP400(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"malformed"}}`))
	}))
	defer srv.Close()
	p := newCompat(t, Config{BaseURL: srv.URL})

	_, err := p.Stream(t.Context(), basicReq())
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("want ErrInvalidRequest, got %v", err)
	}
}

func TestStream_HTTP418(t *testing.T) {
	// Non-classified 4xx should NOT match any sentinel but still carry the
	// status code in the message.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":{"message":"teapot"}}`))
	}))
	defer srv.Close()
	p := newCompat(t, Config{BaseURL: srv.URL})

	_, err := p.Stream(t.Context(), basicReq())
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if errors.Is(err, provider.ErrAuth) || errors.Is(err, provider.ErrRateLimited) || errors.Is(err, provider.ErrTransient) || errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("4xx other should not match sentinels: %v", err)
	}
	if !strings.Contains(err.Error(), "418") {
		t.Errorf("error should mention status; got: %v", err)
	}
}

func TestStream_InvalidToolCallJSON(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_error.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	last := events[len(events)-1]
	if last.Type != provider.EventError {
		t.Fatalf("expected EventError, got %v: %v", last.Type, eventTypes(events))
	}
	if last.Err == nil || !strings.Contains(last.Err.Error(), "invalid JSON") {
		t.Errorf("error should mention invalid JSON: %v", last.Err)
	}
}

func TestStream_MidStreamEOF(t *testing.T) {
	// Server hangs up mid-stream without sending [DONE].
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetLinger(0)
		}
		_ = conn.Close()
	}))
	defer srv.Close()

	p := newCompat(t, Config{BaseURL: srv.URL})
	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)
	if len(events) == 0 {
		t.Fatal("no events")
	}
	last := events[len(events)-1]
	if last.Type != provider.EventError {
		t.Fatalf("want EventError, got %v: %v", last.Type, eventTypes(events))
	}
	if !errors.Is(last.Err, io.ErrUnexpectedEOF) {
		t.Errorf("want wrapped io.ErrUnexpectedEOF, got %v", last.Err)
	}
}

func TestStream_ContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"x\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"m\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n"))
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
	p := newCompat(t, Config{BaseURL: srv.URL})
	ch, err := p.Stream(ctx, basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}

	first := <-ch
	if first.Type != provider.EventMessageStart {
		t.Errorf("first: %v", first.Type)
	}
	cancel()

	done := make(chan struct{})
	go func() {
		//nolint:revive // draining is the point
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("channel did not close after cancel")
	}
}

func TestStream_RequestShape(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	msgs := []session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "read a.go"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{
			{Kind: session.PartText, Text: "Sure."},
			{Kind: session.PartToolUse, ToolID: "call_xyz", ToolName: "read", ToolInput: json.RawMessage(`{"path":"a.go"}`)},
		}},
		{Role: session.RoleTool, Parts: []session.Part{
			{Kind: session.PartToolResult, ToolUseID: "call_xyz", Content: "package main"},
		}},
	}

	ch, err := p.Stream(t.Context(), provider.Request{
		ModelName: "gpt-test",
		System:    "Be concise.",
		Messages:  msgs,
		Tools: []provider.Tool{{
			Name:        "read",
			Description: "read a file",
			InputSchema: map[string]any{"type": "object"},
		}},
		MaxTokens:   1024,
		Temperature: 0.5,
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	if capt.path != streamPath {
		t.Errorf("path: %q", capt.path)
	}

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("decode captured: %v: %s", err, capt.body)
	}

	if got["model"] != "gpt-test" {
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

	so, ok := got["stream_options"].(map[string]any)
	if !ok {
		t.Fatalf("stream_options missing: %v", got)
	}
	if so["include_usage"] != true {
		t.Errorf("include_usage: %v", so)
	}

	msgsGot := got["messages"].([]any)
	// 1 system + user + assistant + tool = 4 messages.
	if len(msgsGot) != 4 {
		t.Fatalf("want 4 messages, got %d: %v", len(msgsGot), msgsGot)
	}

	first := msgsGot[0].(map[string]any)
	if first["role"] != "system" {
		t.Errorf("msg0 role: %v", first["role"])
	}
	if first["content"] != "Be concise." {
		t.Errorf("msg0 content: %v", first["content"])
	}

	second := msgsGot[1].(map[string]any)
	if second["role"] != "user" || second["content"] != "read a.go" {
		t.Errorf("msg1: %v", second)
	}

	third := msgsGot[2].(map[string]any)
	if third["role"] != "assistant" {
		t.Errorf("msg2 role: %v", third["role"])
	}
	if third["content"] != "Sure." {
		t.Errorf("msg2 content: %v", third["content"])
	}
	tcs := third["tool_calls"].([]any)
	if len(tcs) != 1 {
		t.Fatalf("msg2 tool_calls: %v", tcs)
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_xyz" || tc["type"] != "function" {
		t.Errorf("tool_call: %v", tc)
	}
	tcfn := tc["function"].(map[string]any)
	if tcfn["name"] != "read" || tcfn["arguments"] != `{"path":"a.go"}` {
		t.Errorf("tool_call function: %v", tcfn)
	}

	fourth := msgsGot[3].(map[string]any)
	if fourth["role"] != "tool" || fourth["tool_call_id"] != "call_xyz" {
		t.Errorf("msg3: %v", fourth)
	}
	if fourth["content"] != "package main" {
		t.Errorf("msg3 content: %v", fourth["content"])
	}

	tools := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools: %v", tools)
	}
	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type: %v", tool)
	}
	tfn := tool["function"].(map[string]any)
	if tfn["name"] != "read" {
		t.Errorf("tool name: %v", tfn)
	}

	if got["tool_choice"] != "auto" {
		t.Errorf("tool_choice: %v", got["tool_choice"])
	}
}

func TestStream_AuthAndExtraHeaders(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := newCompat(t, Config{
		BaseURL: srv.URL,
		APIKey:  "sk-test",
		ExtraHeaders: map[string]string{
			"HTTP-Referer": "https://example.com",
			"X-Title":      "hygge",
		},
	})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	if got := gotHeaders.Get("Authorization"); got != "Bearer sk-test" {
		t.Errorf("Authorization: %q", got)
	}
	if got := gotHeaders.Get("HTTP-Referer"); got != "https://example.com" {
		t.Errorf("HTTP-Referer: %q", got)
	}
	if got := gotHeaders.Get("X-Title"); got != "hygge" {
		t.Errorf("X-Title: %q", got)
	}
}

func TestStream_OmitStreamOptionsWhenIncludeUsageFalse(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	off := false
	p := newCompat(t, Config{BaseURL: srv.URL, IncludeUsage: &off})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, present := got["stream_options"]; present {
		t.Errorf("stream_options should be omitted: %v", got)
	}
}

func TestStream_OmitTemperatureWhenZero(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := got["temperature"]; present {
		t.Errorf("temperature should be omitted when zero: %v", got)
	}
}

func TestStream_OmitMaxTokensWhenBothZero(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, present := got["max_tokens"]; present {
		t.Errorf("max_tokens should be omitted when both zero: %v", got)
	}
}

func TestStream_DefaultMaxTokens(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL, DefaultMaxTokens: 2048})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got["max_tokens"].(float64) != 2048 {
		t.Errorf("max_tokens: %v", got["max_tokens"])
	}
}

func TestStream_RejectsEmptyModelName(t *testing.T) {
	p := newCompat(t, Config{BaseURL: "http://unused"})
	_, err := p.Stream(t.Context(), provider.Request{})
	if !errors.Is(err, provider.ErrInvalidRequest) {
		t.Errorf("want ErrInvalidRequest, got %v", err)
	}
}

// --- New / Config validation tests ---

func TestNew_RejectsMissingName(t *testing.T) {
	_, err := New(Config{BaseURL: "x", APIKey: "x", Models: []provider.Model{}})
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestNew_RejectsMissingBaseURL(t *testing.T) {
	_, err := New(Config{Name: "x", APIKey: "x", Models: []provider.Model{}})
	if err == nil {
		t.Fatal("want error, got nil")
	}
}

func TestNew_RejectsMissingAPIKey(t *testing.T) {
	_, err := New(Config{Name: "x", BaseURL: "x", Models: []provider.Model{}})
	if !errors.Is(err, provider.ErrAuth) {
		t.Errorf("want ErrAuth, got %v", err)
	}
}

func TestNew_RejectsNilModels(t *testing.T) {
	_, err := New(Config{Name: "x", BaseURL: "x", APIKey: "x"})
	if err == nil {
		t.Fatal("want error for nil Models")
	}
}

// --- Provider methods ---

func TestProviderName(t *testing.T) {
	p := newCompat(t, Config{Name: "openrouter", BaseURL: "http://unused"})
	if p.Name() != "openrouter" {
		t.Errorf("Name: %q", p.Name())
	}
}

func TestCountTokens_ReturnsZero(t *testing.T) {
	p := newCompat(t, Config{BaseURL: "http://unused"})
	n, err := p.CountTokens(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("CountTokens: %v", err)
	}
	if n != 0 {
		t.Errorf("want 0, got %d", n)
	}
}

func TestListModels_ReturnsStaticSlice(t *testing.T) {
	models := []provider.Model{
		{Name: "m1", ContextWindow: 100, MaxOutput: 10, SupportsTools: true},
		{Name: "m2", ContextWindow: 200, MaxOutput: 20},
	}
	p := newCompat(t, Config{BaseURL: "http://unused", Models: models})
	got, err := p.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 2 || got[0].Name != "m1" || got[1].Name != "m2" {
		t.Errorf("got %v", got)
	}
}

func TestListModels_EmptySlice(t *testing.T) {
	p := newCompat(t, Config{BaseURL: "http://unused", Models: []provider.Model{}})
	got, err := p.ListModels(t.Context())
	if err != nil {
		t.Fatalf("ListModels: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("want empty, got %v", got)
	}
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

// --- Reasoning-model detection --------------------------------------------

func TestMatchesReasoningPrefix(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"o1", true},
		{"o1-mini", true},
		{"o1-preview", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"o4-MINI", true}, // case-insensitive
		{"O1", true},
		{"reasoning-test", true},
		{"openai/o3-mini", true},
		{"openrouter/openai/o3", true},
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"claude-sonnet-4-5", false},
		{"claude-foo", false},
		{"", false},
		{"o2-not-a-real-model", false}, // unmatched prefix
		{"oxford", false},
		{"reasoning", false}, // bare word, no dash
	}
	for _, c := range cases {
		t.Run(c.id, func(t *testing.T) {
			if got := matchesReasoningPrefix(c.id); got != c.want {
				t.Errorf("matchesReasoningPrefix(%q)=%v, want %v", c.id, got, c.want)
			}
		})
	}
}

// --- Reasoning request-body construction ----------------------------------

func TestBuildRequestBody_ReasoningModel_DropsTemperatureAndUsesMaxCompletionTokens(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	req := provider.Request{
		ModelName:   "o3-mini",
		Messages:    []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
		Temperature: 0.7,
		MaxTokens:   2048,
		Reasoning:   provider.Reasoning{Effort: "medium"},
	}
	ch, err := p.Stream(t.Context(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("unmarshal: %v: %s", err, capt.body)
	}
	if _, present := got["temperature"]; present {
		t.Errorf("temperature should be omitted entirely for reasoning models: %v", got)
	}
	if _, present := got["max_tokens"]; present {
		t.Errorf("max_tokens should be omitted for reasoning models: %v", got)
	}
	if mct, ok := got["max_completion_tokens"].(float64); !ok || int(mct) != 2048 {
		t.Errorf("max_completion_tokens=%v, want 2048", got["max_completion_tokens"])
	}
	if got["reasoning_effort"] != "medium" {
		t.Errorf("reasoning_effort=%v, want medium", got["reasoning_effort"])
	}
}

func TestBuildRequestBody_ReasoningOff_NoReasoningEffort(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	req := provider.Request{
		ModelName: "o3-mini",
		Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
		// Reasoning zero value: still routed through reasoning path
		// because the model is reasoning-class, but no
		// reasoning_effort field is sent.
	}
	ch, err := p.Stream(t.Context(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := got["reasoning_effort"]; present {
		t.Errorf("reasoning_effort should be omitted when Reasoning is off: %v", got)
	}
	if _, present := got["temperature"]; present {
		t.Errorf("temperature should be omitted: %v", got)
	}
}

func TestBuildRequestBody_NonReasoningModel_IgnoresReasoning(t *testing.T) {
	srv, capt := newSSEServer(t, "stream_basic_text.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	req := provider.Request{
		ModelName:   "gpt-4o",
		Messages:    []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
		Temperature: 0.5,
		MaxTokens:   1024,
		Reasoning:   provider.Reasoning{Effort: "high"},
	}
	ch, err := p.Stream(t.Context(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	_ = collect(ch)

	var got map[string]any
	if err := json.Unmarshal(capt.body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := got["reasoning_effort"]; present {
		t.Errorf("reasoning_effort should be absent on non-reasoning models: %v", got)
	}
	if _, present := got["max_completion_tokens"]; present {
		t.Errorf("max_completion_tokens should be absent on non-reasoning models: %v", got)
	}
	if got["max_tokens"].(float64) != 1024 {
		t.Errorf("max_tokens=%v, want 1024", got["max_tokens"])
	}
	if got["temperature"].(float64) != 0.5 {
		t.Errorf("temperature=%v, want 0.5", got["temperature"])
	}
}

func TestBuildRequestBody_ReasoningEffortValues(t *testing.T) {
	for _, eff := range []string{"low", "medium", "high"} {
		t.Run(eff, func(t *testing.T) {
			srv, capt := newSSEServer(t, "stream_basic_text.sse")
			p := newCompat(t, Config{BaseURL: srv.URL})
			ch, err := p.Stream(t.Context(), provider.Request{
				ModelName: "o4-mini",
				Messages:  []session.Message{{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}}},
				Reasoning: provider.Reasoning{Effort: eff},
			})
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
			_ = collect(ch)
			var got map[string]any
			if err := json.Unmarshal(capt.body, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got["reasoning_effort"] != eff {
				t.Errorf("reasoning_effort=%v, want %q", got["reasoning_effort"], eff)
			}
		})
	}
}

// --- Reasoning usage / streaming-summary ----------------------------------

func TestStream_ReasoningUsage(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_reasoning_usage.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	var u *provider.Event
	for i := range events {
		if events[i].Type == provider.EventUsage {
			u = &events[i]
		}
	}
	if u == nil {
		t.Fatalf("expected EventUsage: %v", eventTypes(events))
	}
	if u.Usage.InputTokens != 5 || u.Usage.OutputTokens != 11 {
		t.Errorf("usage tokens: %+v", u.Usage)
	}
	if u.Usage.ReasoningTokens != 7 {
		t.Errorf("reasoning tokens: got %d want 7", u.Usage.ReasoningTokens)
	}
}

func TestStream_ReasoningSummaryDeltas(t *testing.T) {
	srv, _ := newSSEServer(t, "stream_reasoning_summary.sse")
	p := newCompat(t, Config{BaseURL: srv.URL})

	ch, err := p.Stream(t.Context(), basicReq())
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	events := collect(ch)

	var thinking, text strings.Builder
	for _, e := range events {
		switch e.Type {
		case provider.EventThinkingDelta:
			thinking.WriteString(e.Text)
		case provider.EventTextDelta:
			text.WriteString(e.Text)
		}
	}
	if got := thinking.String(); got != "Let me think... carefully." {
		t.Errorf("thinking: %q", got)
	}
	if got := text.String(); got != "The answer is 42." {
		t.Errorf("text: %q", got)
	}
	last := events[len(events)-1]
	if last.Type != provider.EventDone {
		t.Errorf("last event: %v", last.Type)
	}
}

func TestReasoningDelta_HelperHandlesBothSpellings(t *testing.T) {
	cases := []struct {
		name string
		d    chatDelta
		want string
	}{
		{"empty", chatDelta{}, ""},
		{"summary only", chatDelta{ReasoningSummary: "hi"}, "hi"},
		{"reasoning only", chatDelta{Reasoning: "hi"}, "hi"},
		{"both", chatDelta{ReasoningSummary: "a", Reasoning: "b"}, "ab"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reasoningDelta(c.d); got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// --- Catalog-driven reasoning detection -----------------------------------

func TestAdapter_IsReasoningModel_CatalogHit(t *testing.T) {
	t.Parallel()
	// Build a catalog that says model "future-reason-1" is a
	// reasoning model and "future-plain-1" is not.  Confirm both
	// answers come from the catalog rather than the prefix matcher.
	cat := buildFixtureCatalog(t, []catwalk.Provider{
		{
			ID:   "openai",
			Name: "OpenAI",
			Type: catwalk.TypeOpenAI,
			Models: []catwalk.Model{
				{ID: "future-reason-1", Name: "Future Reason 1", CanReason: true},
				{ID: "future-plain-1", Name: "Future Plain 1", CanReason: false},
			},
		},
	})

	p := newCompat(t, Config{
		Name:    "openai",
		BaseURL: "http://unused",
		APIKey:  "x",
		Models:  []provider.Model{},
		Catalog: cat,
	})
	a := p.(*adapter)
	if !a.isReasoningModel("future-reason-1") {
		t.Errorf("catalog-driven detection missed reasoning model")
	}
	if a.isReasoningModel("future-plain-1") {
		t.Errorf("catalog-driven detection wrongly flagged plain model")
	}
}

func TestAdapter_IsReasoningModel_CatalogMissFallsBackToPrefix(t *testing.T) {
	t.Parallel()
	// Empty openai provider: no model entries match.  The legacy prefix matcher
	// must still answer correctly for o-series ids.
	cat := buildFixtureCatalog(t, []catwalk.Provider{
		{
			ID:     "openai",
			Name:   "OpenAI",
			Type:   catwalk.TypeOpenAI,
			Models: []catwalk.Model{},
		},
	})
	p := newCompat(t, Config{
		Name:    "openai",
		BaseURL: "http://unused",
		APIKey:  "x",
		Models:  []provider.Model{},
		Catalog: cat,
	})
	a := p.(*adapter)
	if !a.isReasoningModel("o3-mini") {
		t.Errorf("prefix fallback missed o3-mini")
	}
	if a.isReasoningModel("gpt-4o") {
		t.Errorf("prefix fallback wrongly flagged gpt-4o")
	}
}

func TestAdapter_IsReasoningModel_NoCatalog_PrefixMatcher(t *testing.T) {
	t.Parallel()
	// No catalog wired at all (the historical default).  Prefix
	// matcher is the only source.
	p := newCompat(t, Config{
		Name:    "openai",
		BaseURL: "http://unused",
		APIKey:  "x",
		Models:  []provider.Model{},
	})
	a := p.(*adapter)
	if !a.isReasoningModel("o4-mini") {
		t.Errorf("expected o4-mini to be reasoning")
	}
	if a.isReasoningModel("future-unknown-model") {
		t.Errorf("unknown model should not be reasoning")
	}
}

// buildFixtureCatalog spins up an httptest server that serves the given
// catwalk.Provider slice as JSON at /v2/providers, then constructs a
// *catalog.Catalog refreshed against it.  Returned catalog has
// BackgroundRefresh disabled and is fully populated by the time this
// helper returns.
func buildFixtureCatalog(t *testing.T, providers []catwalk.Provider) *catalog.Catalog {
	t.Helper()
	body, err := json.Marshal(providers) //nolint:gosec // G117: test fixture; no real credentials
	if err != nil {
		t.Fatalf("buildFixtureCatalog: marshal: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/providers" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	bg := false
	cat, err := catalog.Load(catalog.LoadOptions{
		StateDir:          t.TempDir(),
		HTTPClient:        srv.Client(),
		BaseURL:           srv.URL,
		BackgroundRefresh: &bg,
	})
	if err != nil {
		t.Fatalf("catalog.Load: %v", err)
	}
	if _, err := cat.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	return cat
}
