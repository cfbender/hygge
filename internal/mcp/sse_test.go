package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- sseScanner unit tests ------------------------------------------------

func TestSSEScanner_BasicEvent(t *testing.T) {
	t.Parallel()
	input := "event: message\ndata: hello\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.name != "message" {
		t.Fatalf("name: got %q want %q", ev.name, "message")
	}
	if ev.data != "hello" {
		t.Fatalf("data: got %q want %q", ev.data, "hello")
	}

	// Next call should return EOF.
	_, err = s.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF after last event, got %v", err)
	}
}

func TestSSEScanner_EndpointEvent(t *testing.T) {
	t.Parallel()
	input := "event: endpoint\ndata: /messages\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.name != "endpoint" {
		t.Fatalf("name: got %q want %q", ev.name, "endpoint")
	}
	if ev.data != "/messages" {
		t.Fatalf("data: got %q want %q", ev.data, "/messages")
	}
}

func TestSSEScanner_MultiLineData(t *testing.T) {
	t.Parallel()
	input := "data: line1\ndata: line2\ndata: line3\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	want := "line1\nline2\nline3"
	if ev.data != want {
		t.Fatalf("data: got %q want %q", ev.data, want)
	}
}

func TestSSEScanner_Comments(t *testing.T) {
	t.Parallel()
	// Comments (lines starting with ':') are discarded.
	input := ": this is a comment\nevent: message\ndata: real\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.name != "message" || ev.data != "real" {
		t.Fatalf("unexpected event: %+v", ev)
	}
}

func TestSSEScanner_IDField(t *testing.T) {
	t.Parallel()
	input := "id: 42\ndata: payload\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.id != "42" {
		t.Fatalf("id: got %q want %q", ev.id, "42")
	}
	if ev.data != "payload" {
		t.Fatalf("data: got %q want %q", ev.data, "payload")
	}
}

func TestSSEScanner_NoEventName(t *testing.T) {
	t.Parallel()
	// No event: field means the name is empty (caller defaults to "message").
	input := "data: anonymous\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.name != "" {
		t.Fatalf("name: got %q want empty", ev.name)
	}
	if ev.data != "anonymous" {
		t.Fatalf("data: got %q want %q", ev.data, "anonymous")
	}
}

func TestSSEScanner_EmptyData(t *testing.T) {
	t.Parallel()
	// data: with no value — valid; data field is empty string.
	input := "event: ping\ndata:\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.name != "ping" {
		t.Fatalf("name: got %q", ev.name)
	}
	// data: (with no value) produces an empty string.
	if ev.data != "" {
		t.Fatalf("data: got %q want empty", ev.data)
	}
}

func TestSSEScanner_MultipleEvents(t *testing.T) {
	t.Parallel()
	input := "data: first\n\ndata: second\n\ndata: third\n\n"
	s := newSSEScanner(strings.NewReader(input))
	for _, want := range []string{"first", "second", "third"} {
		ev, err := s.Next()
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if ev.data != want {
			t.Fatalf("data: got %q want %q", ev.data, want)
		}
	}
	_, err := s.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestSSEScanner_CRLFLineEndings(t *testing.T) {
	t.Parallel()
	input := "event: endpoint\r\ndata: /rpc\r\n\r\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.name != "endpoint" {
		t.Fatalf("name: got %q", ev.name)
	}
	if ev.data != "/rpc" {
		t.Fatalf("data: got %q", ev.data)
	}
}

func TestSSEScanner_RetryField(t *testing.T) {
	t.Parallel()
	// retry: is accepted and ignored per the spec.
	input := "retry: 1000\ndata: ok\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.data != "ok" {
		t.Fatalf("data: got %q want ok", ev.data)
	}
}

func TestSSEScanner_UnknownField(t *testing.T) {
	t.Parallel()
	// Unknown fields are silently ignored.
	input := "x-custom: stuff\ndata: body\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.data != "body" {
		t.Fatalf("data: got %q", ev.data)
	}
}

func TestSSEScanner_EmptyStream(t *testing.T) {
	t.Parallel()
	s := newSSEScanner(strings.NewReader(""))
	_, err := s.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF for empty stream, got %v", err)
	}
}

func TestSSEScanner_OnlyComments(t *testing.T) {
	t.Parallel()
	input := ": heartbeat\n: another\n"
	s := newSSEScanner(strings.NewReader(input))
	_, err := s.Next()
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF for comment-only stream, got %v", err)
	}
}

func TestSSEScanner_SpaceStrippedFromDataValue(t *testing.T) {
	t.Parallel()
	// The spec says "If value begins with a U+0020 SPACE, remove it."
	input := "data: has leading space\n\n"
	s := newSSEScanner(strings.NewReader(input))
	ev, err := s.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if ev.data != "has leading space" {
		t.Fatalf("data: got %q", ev.data)
	}
}

func TestSSEScanner_DataSizeLimitExceeded(t *testing.T) {
	t.Parallel()
	// Build a data line just over the limit.
	const chunk = 1024 * 1024 // 1 MiB per line
	var sb strings.Builder
	for i := 0; i < 17; i++ { // 17 MiB total > 16 MiB limit
		sb.WriteString("data: ")
		sb.WriteString(strings.Repeat("x", chunk))
		sb.WriteByte('\n')
	}
	sb.WriteString("\n")
	s := newSSEScanner(strings.NewReader(sb.String()))
	_, err := s.Next()
	if err == nil {
		t.Fatal("expected error for oversized event data")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- transport tests -------------------------------------------------------

func TestSSETransport_HappyPath(t *testing.T) {
	t.Parallel()
	// notifCh carries events to push on the GET stream after the initial endpoint.
	notifCh := make(chan string, 4)

	mux := http.NewServeMux()
	var srvURL string
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			return
		}
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
		f.Flush()
		// Drain additional events until client disconnects.
		for {
			select {
			case ev := <-notifCh:
				_, _ = fmt.Fprint(w, ev)
				f.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", string(body))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL + "/sse",
		ConnectTimeout: 5 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	msg := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(got) != string(msg) {
		t.Fatalf("Recv: got %q want %q", got, msg)
	}
}

func TestSSETransport_StartTwice(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", "http://localhost:0")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL,
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	if err := tr.Start(ctx); err == nil {
		t.Fatal("expected error on second Start")
	}
}

func TestSSETransport_HandshakeFailBadFirstEvent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Send a message event instead of endpoint.
		_, _ = fmt.Fprint(w, "event: message\ndata: {\"hello\":true}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer srv.Close()

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL,
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := tr.Start(ctx)
	if err == nil {
		_ = tr.Close()
		t.Fatal("expected error when first event is not endpoint")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestSSETransport_HandshakeFailNonSSEResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"error":"not an SSE endpoint"}`)
	}))
	defer srv.Close()

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL,
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	err := tr.Start(ctx)
	if err == nil {
		_ = tr.Close()
		t.Fatal("expected error for non-SSE Content-Type")
	}
}

func TestSSETransport_Notification(t *testing.T) {
	// Server sends an unsolicited notification on the GET stream.
	t.Parallel()
	notifCh := make(chan string, 4)

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			return
		}
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
		f.Flush()
		for {
			select {
			case ev := <-notifCh:
				_, _ = fmt.Fprint(w, ev)
				f.Flush()
			case <-r.Context().Done():
				return
			}
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL,
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	// Send a notification on the GET stream.
	notif := `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`
	notifCh <- fmt.Sprintf("data: %s\n\n", notif)

	msg, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !strings.Contains(string(msg), "tools/list_changed") {
		t.Fatalf("unexpected notification: %q", msg)
	}
}

func TestSSETransport_AuthorizationHeader(t *testing.T) {
	t.Parallel()
	var gotAuthGET, gotAuthPOST atomic.Value

	var endpointPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		gotAuthGET.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", endpointPath)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hold open.
		<-r.Context().Done()
	})
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		gotAuthPOST.Store(r.Header.Get("Authorization"))
		_, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "event: message\ndata: {}\n\n")
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	endpointPath = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL + "/sse",
		Headers:        map[string]string{"Authorization": "Bearer test-token"},
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	if err := tr.Send(ctx, []byte(`{}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Drain the message.
	_, _ = tr.Recv(ctx)

	if v, _ := gotAuthGET.Load().(string); v != "Bearer test-token" {
		t.Fatalf("GET Authorization: got %q want %q", v, "Bearer test-token")
	}
	if v, _ := gotAuthPOST.Load().(string); v != "Bearer test-token" {
		t.Fatalf("POST Authorization: got %q want %q", v, "Bearer test-token")
	}
}

func TestSSETransport_SendPostCorrelation(t *testing.T) {
	// Send two requests; verify responses are routed correctly via inbound.
	t.Parallel()
	type rpcMsg struct {
		ID     int    `json:"id"`
		Method string `json:"method"`
	}

	var mu sync.Mutex
	seen := make(map[int]bool)

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", "")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	})
	var srvURL string
	mux.HandleFunc("/rpc", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()
		var m rpcMsg
		if err := json.Unmarshal(body, &m); err == nil {
			mu.Lock()
			seen[m.ID] = true
			mu.Unlock()
		}
		// Echo the request back as a response.
		resp := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, m.ID)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:      srvURL + "/sse",
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	// Fix the endpoint after Start — the handler embeds "" for URL, so
	// inject the correct path directly.
	tr.(*sseTransport).endpoint = srvURL + "/rpc"
	tr.(*sseTransport).started.Store(true)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for i := 1; i <= 3; i++ {
		msg, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": i, "method": "ping"})
		if err := tr.Send(ctx, msg); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		resp, err := tr.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv %d: %v", i, err)
		}
		if !strings.Contains(string(resp), fmt.Sprintf(`"id":%d`, i)) {
			t.Fatalf("Recv %d: got %q", i, resp)
		}
	}

	_ = tr.Close()
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i <= 3; i++ {
		if !seen[i] {
			t.Fatalf("POST for id %d not seen", i)
		}
	}
}

func TestSSETransport_Reconnect(t *testing.T) {
	// Simulate a dropped GET stream and verify the transport reconnects.
	t.Parallel()

	var connectCount atomic.Int32
	// drop signals the first handler to return (simulating stream close).
	drop := make(chan struct{})
	// connected signals that the second connection was received.
	connected := make(chan struct{})

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		idx := int(connectCount.Add(1)) - 1
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			return
		}
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
		f.Flush()
		if idx == 0 {
			// First connection: wait for drop signal.
			select {
			case <-drop:
			case <-r.Context().Done():
			}
		} else {
			// Second (reconnect) connection: signal and keep alive.
			select {
			case connected <- struct{}{}:
			default:
			}
			<-r.Context().Done()
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:               srv.URL,
		ConnectTimeout:          3 * time.Second,
		MaxReconnects:           3,
		ReconnectInitialBackoff: 20 * time.Millisecond,
		ReconnectMaxBackoff:     50 * time.Millisecond,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	// Signal the first GET stream to close.
	close(drop)

	// Wait for the reconnect to succeed.
	select {
	case <-connected:
		// Reconnect happened.
	case <-time.After(2 * time.Second):
		t.Fatalf("transport did not reconnect within 2s; connect count: %d", connectCount.Load())
	}
	if n := connectCount.Load(); n < 2 {
		t.Fatalf("expected at least 2 GET connections (initial + reconnect), got %d", n)
	}
}

func TestSSETransport_CloseIdempotent(t *testing.T) {
	t.Parallel()
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL,
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = tr.Close()
	_ = tr.Close() // Second Close must not panic.
}

func TestSSETransport_CloseBeforeStart(t *testing.T) {
	t.Parallel()
	tr := NewSSE(SSEOptions{
		ServerURL:     "http://localhost:1",
		MaxReconnects: 1,
	})
	// Close before Start must be a no-op.
	if err := tr.Close(); err != nil {
		t.Fatalf("Close before Start: %v", err)
	}
}

func TestSSETransport_NoReconnectAfterClose(t *testing.T) {
	t.Parallel()
	var connectCount atomic.Int32
	// dropCh is closed to cause the GET stream to end, triggering reconnect logic.
	dropCh := make(chan struct{})

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connectCount.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Wait until the test signals drop or the client disconnects.
		select {
		case <-dropCh:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:               srv.URL,
		ConnectTimeout:          3 * time.Second,
		MaxReconnects:           5,
		ReconnectInitialBackoff: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Close the transport.
	_ = tr.Close()

	// Signal the stream to drop — transport must NOT reconnect.
	close(dropCh)

	time.Sleep(150 * time.Millisecond)
	if n := connectCount.Load(); n > 1 {
		t.Fatalf("transport reconnected %d times after Close; expected 0 reconnects", n-1)
	}
}

func TestSSETransport_RecvEOFAfterClose(t *testing.T) {
	t.Parallel()
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv.Close()
	srvURL = srv.URL

	tr := NewSSE(SSEOptions{
		ServerURL:      srv.URL,
		ConnectTimeout: 3 * time.Second,
		MaxReconnects:  1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	_ = tr.Close()

	// After Close, Recv must return io.EOF promptly.
	done := make(chan error, 1)
	go func() {
		_, err := tr.Recv(ctx)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("Recv after Close: got %v want io.EOF", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Recv after Close did not return promptly")
	}
}

// TestSSETransport_GoroutineLeak verifies that opening and closing the
// transport 100 times does not leak goroutines.  Run with -race.
func TestSSETransport_GoroutineLeak(t *testing.T) {
	t.Parallel()

	for i := 0; i < 100; i++ {
		var srvURL string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprintf(w, "event: endpoint\ndata: %s/rpc\n\n", srvURL)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-r.Context().Done()
		}))
		srvURL = srv.URL

		tr := NewSSE(SSEOptions{
			ServerURL:      srv.URL,
			ConnectTimeout: 2 * time.Second,
			MaxReconnects:  1,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := tr.Start(ctx); err != nil {
			cancel()
			srv.Close()
			t.Fatalf("iteration %d Start: %v", i, err)
		}
		_ = tr.Close()
		cancel()
		srv.Close()
	}
	// Give goroutines a moment to fully exit.
	time.Sleep(50 * time.Millisecond)
}

// --- resolveEndpoint unit tests -------------------------------------------

func TestResolveEndpoint_Absolute(t *testing.T) {
	t.Parallel()
	got, err := resolveEndpoint("https://server.example.com/sse", "https://other.example.com/rpc")
	if err != nil {
		t.Fatalf("resolveEndpoint: %v", err)
	}
	if got != "https://other.example.com/rpc" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveEndpoint_Relative(t *testing.T) {
	t.Parallel()
	got, err := resolveEndpoint("https://server.example.com/sse", "/rpc")
	if err != nil {
		t.Fatalf("resolveEndpoint: %v", err)
	}
	if got != "https://server.example.com/rpc" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveEndpoint_RelativeSamePath(t *testing.T) {
	t.Parallel()
	got, err := resolveEndpoint("http://localhost:8080/sse", "rpc")
	if err != nil {
		t.Fatalf("resolveEndpoint: %v", err)
	}
	// "rpc" is relative to the directory of /sse, so /rpc.
	if !strings.HasSuffix(got, "/rpc") {
		t.Fatalf("got %q", got)
	}
}

// --- ServerLabel tests ----------------------------------------------------

func TestSSETransport_ServerLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		opts    SSEOptions
		wantPfx string
	}{
		{"with name", SSEOptions{ServerName: "linear"}, "sse:linear"},
		{"from url", SSEOptions{ServerURL: "https://mcp.linear.app/sse"}, "sse:mcp.linear.app"},
		{"empty", SSEOptions{}, "sse:(unset)"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := NewSSE(tc.opts)
			got := tr.ServerLabel()
			if !strings.HasPrefix(got, tc.wantPfx) {
				t.Fatalf("got %q want prefix %q", got, tc.wantPfx)
			}
		})
	}
}

// --- expandHeaderEnv tests ------------------------------------------------

func TestExpandHeaderEnv(t *testing.T) {
	t.Parallel()
	lookup := func(key string) (string, bool) {
		m := map[string]string{"TOKEN": "abc", "HOST": "example.com"}
		v, ok := m[key]
		return v, ok
	}
	headers := map[string]string{
		"Authorization": "Bearer ${TOKEN}",
		"X-Host":        "$HOST",
		"Static":        "no-expand",
	}
	got := expandHeaderEnv(headers, lookup)
	if got["Authorization"] != "Bearer abc" {
		t.Fatalf("Authorization: got %q", got["Authorization"])
	}
	if got["X-Host"] != "example.com" {
		t.Fatalf("X-Host: got %q", got["X-Host"])
	}
	if got["Static"] != "no-expand" {
		t.Fatalf("Static: got %q", got["Static"])
	}
}

func TestExpandHeaderEnv_UnsetVar(t *testing.T) {
	t.Parallel()
	lookup := func(_ string) (string, bool) { return "", false }
	headers := map[string]string{"Authorization": "Bearer ${MISSING_TOKEN}"}
	got := expandHeaderEnv(headers, lookup)
	// Unset vars become empty string.
	if got["Authorization"] != "Bearer " {
		t.Fatalf("Authorization: got %q", got["Authorization"])
	}
}

func TestExpandHeaderEnv_NilMap(t *testing.T) {
	t.Parallel()
	got := expandHeaderEnv(nil, nil)
	if got != nil {
		t.Fatalf("expected nil for nil input, got %v", got)
	}
}

// --- bufio.Writer-flushing test for inline SSE pipe -----------------------

// TestSSEScanner_PipeReader verifies that the scanner works against a
// bufio.Reader wrapping a pipe, which is how the transport uses it.
func TestSSEScanner_PipeReader(t *testing.T) {
	t.Parallel()
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()

	go func() {
		wr := bufio.NewWriter(w)
		_, _ = fmt.Fprint(wr, "event: endpoint\ndata: /rpc\n\n")
		_ = wr.Flush()
		_, _ = fmt.Fprint(wr, "data: {\"hello\":1}\n\n")
		_ = wr.Flush()
		_ = w.Close()
	}()

	s := newSSEScanner(r)
	ev1, err := s.Next()
	if err != nil {
		t.Fatalf("first Next: %v", err)
	}
	if ev1.name != "endpoint" || ev1.data != "/rpc" {
		t.Fatalf("first event: %+v", ev1)
	}
	ev2, err := s.Next()
	if err != nil {
		t.Fatalf("second Next: %v", err)
	}
	if ev2.data != `{"hello":1}` {
		t.Fatalf("second event data: %q", ev2.data)
	}
}
