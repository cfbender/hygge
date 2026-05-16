package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// --- helpers ----------------------------------------------------------

// sseEvent formats a single SSE event for injection into a test server.
func fmtSSEEvent(eventName, data, id string) string {
	var sb strings.Builder
	if id != "" {
		sb.WriteString("id: ")
		sb.WriteString(id)
		sb.WriteByte('\n')
	}
	if eventName != "" {
		sb.WriteString("event: ")
		sb.WriteString(eventName)
		sb.WriteByte('\n')
	}
	sb.WriteString("data: ")
	sb.WriteString(data)
	sb.WriteString("\n\n")
	return sb.String()
}

// newStreamableTransport is a test helper that builds a transport with
// safe defaults and OpenNotificationsStream disabled unless opts
// explicitly sets it.
func newStreamableTransport(opts StreamableOptions) Transport {
	if opts.ConnectTimeout == 0 {
		opts.ConnectTimeout = 3 * time.Second
	}
	if opts.MaxReconnects == 0 {
		opts.MaxReconnects = 1
	}
	return NewStreamable(opts)
}

// --- Immediate JSON response ------------------------------------------

func TestStreamable_ImmediateJSON(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !strings.Contains(string(got), `"id":1`) {
		t.Fatalf("unexpected response: %q", got)
	}
}

// --- SSE response stream from POST ------------------------------------

func TestStreamable_SSEResponse(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter does not implement Flusher")
			return
		}
		// Send two message events.
		_, _ = fmt.Fprint(w, fmtSSEEvent("message", `{"jsonrpc":"2.0","id":1,"result":{"progress":50}}`, ""))
		f.Flush()
		_, _ = fmt.Fprint(w, fmtSSEEvent("message", `{"jsonrpc":"2.0","id":1,"result":{"progress":100}}`, ""))
		f.Flush()
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"call"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Should receive both messages.
	for i, wantFrag := range []string{`"progress":50`, `"progress":100`} {
		got, err := tr.Recv(ctx)
		if err != nil {
			t.Fatalf("Recv[%d]: %v", i, err)
		}
		if !strings.Contains(string(got), wantFrag) {
			t.Fatalf("Recv[%d]: got %q want fragment %q", i, got, wantFrag)
		}
	}
}

// --- 202 Accepted (response arrives on GET) --------------------------

func TestStreamable_202Accepted(t *testing.T) {
	t.Parallel()

	// notifCh feeds events into the GET stream.
	notifCh := make(chan string, 4)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			// Server accepts immediately; response will arrive on GET.
			w.Header().Set("Mcp-Session-Id", "sess-1")
			w.WriteHeader(http.StatusAccepted)
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			f, ok := w.(http.Flusher)
			if !ok {
				return
			}
			for {
				select {
				case ev := <-notifCh:
					_, _ = fmt.Fprint(w, ev)
					f.Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: true,
		MaxReconnects:           1,
		ReconnectInitialBackoff: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	// POST returns 202; GET stream is opened automatically.
	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Give the GET stream time to open.
	time.Sleep(50 * time.Millisecond)

	// Now deliver the response on the GET stream.
	notifCh <- fmtSSEEvent("message", `{"jsonrpc":"2.0","id":2,"result":{"tools":[]}}`, "")

	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !strings.Contains(string(got), `"tools"`) {
		t.Fatalf("unexpected response: %q", got)
	}
}

// --- Session-id capture and propagation ------------------------------

func TestStreamable_SessionIDCapture(t *testing.T) {
	t.Parallel()
	var receivedSIDs []string
	var mu atomic.Value // stores []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// 405 so notifications stream is disabled cleanly.
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Capture the sid the client sent.
		sid := r.Header.Get("Mcp-Session-Id")
		if prev, _ := mu.Load().([]string); prev == nil {
			mu.Store([]string{sid})
		} else {
			mu.Store(append(prev, sid))
		}
		// Return a session id on every response.
		w.Header().Set("Mcp-Session-Id", "session-abc")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
	}))
	defer srv.Close()
	_ = receivedSIDs

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	// First request — session id not yet known.
	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)); err != nil {
		t.Fatalf("Send 1: %v", err)
	}
	_, _ = tr.Recv(ctx)

	// Second request — must carry session id.
	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)); err != nil {
		t.Fatalf("Send 2: %v", err)
	}
	_, _ = tr.Recv(ctx)

	sids, _ := mu.Load().([]string)
	if len(sids) < 2 {
		t.Fatalf("expected at least 2 POST calls, got %d", len(sids))
	}
	// First POST should have no session id.
	if sids[0] != "" {
		t.Fatalf("first POST should send no session id, got %q", sids[0])
	}
	// Second POST must carry the captured session id.
	if sids[1] != "session-abc" {
		t.Fatalf("second POST session id: got %q want %q", sids[1], "session-abc")
	}
}

// --- Session-id rotation ---------------------------------------------

func TestStreamable_SessionIDRotation(t *testing.T) {
	t.Parallel()
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		n := callCount.Add(1)
		// Return a different session id on the second POST.
		newSID := "session-v1"
		if n >= 2 {
			newSID = "session-v2"
		}
		w.Header().Set("Mcp-Session-Id", newSID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{}}`, n)
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	for i := 1; i <= 2; i++ {
		if err := tr.Send(ctx, fmt.Appendf(nil, `{"jsonrpc":"2.0","id":%d,"method":"ping"}`, i)); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
		_, _ = tr.Recv(ctx)
	}

	// After 2 calls the transport should have adopted "session-v2".
	sst := tr.(*streamableTransport)
	sst.mu.Lock()
	gotSID := sst.sessionID
	sst.mu.Unlock()
	if gotSID != "session-v2" {
		t.Fatalf("sessionID after rotation: got %q want %q", gotSID, "session-v2")
	}
}

// --- Notifications GET stream ----------------------------------------

func TestStreamable_NotificationsGET(t *testing.T) {
	t.Parallel()

	notifCh := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Mcp-Session-Id", "sess-notif")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			f, ok := w.(http.Flusher)
			if !ok {
				return
			}
			for {
				select {
				case ev := <-notifCh:
					_, _ = fmt.Fprint(w, ev)
					f.Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: true,
		MaxReconnects:           1,
		ReconnectInitialBackoff: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	// Trigger POST so GET stream opens.
	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = tr.Recv(ctx) // drain the POST response

	// Give the GET stream a moment to open.
	time.Sleep(50 * time.Millisecond)

	// Push a server-initiated notification.
	notifCh <- fmtSSEEvent("message", `{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`, "")

	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv notification: %v", err)
	}
	if !strings.Contains(string(got), "tools/list_changed") {
		t.Fatalf("unexpected notification body: %q", got)
	}
}

// --- Last-Event-Id resumption ----------------------------------------

func TestStreamable_Resumption(t *testing.T) {
	t.Parallel()

	var getCount atomic.Int32
	var lastEventIDReceived atomic.Value // string

	notifCh := make(chan string, 4)
	dropCh := make(chan struct{})
	resumedCh := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Mcp-Session-Id", "sess-resume")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case http.MethodGet:
			idx := int(getCount.Add(1)) - 1
			// Record Last-Event-Id on reconnects.
			if leid := r.Header.Get("Last-Event-Id"); leid != "" {
				lastEventIDReceived.Store(leid)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			f, ok := w.(http.Flusher)
			if !ok {
				return
			}
			if idx == 0 {
				// First GET: send one event with an id, then drop.
				_, _ = fmt.Fprint(w, fmtSSEEvent("message", `{"jsonrpc":"2.0","method":"ping"}`, "evt-42"))
				f.Flush()
				select {
				case <-dropCh:
				case <-r.Context().Done():
				}
				return
			}
			// Second GET (reconnect): signal and stay open.
			select {
			case resumedCh <- struct{}{}:
			default:
			}
			for {
				select {
				case ev := <-notifCh:
					_, _ = fmt.Fprint(w, ev)
					f.Flush()
				case <-r.Context().Done():
					return
				}
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: true,
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

	// Trigger first POST so GET stream opens.
	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = tr.Recv(ctx) // drain POST response

	// Receive the notification from the first GET that carries an id.
	_, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv first notification: %v", err)
	}

	// Drop the first GET.
	close(dropCh)

	// Wait for the reconnect.
	select {
	case <-resumedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("transport did not reconnect within 2s")
	}

	// Verify the reconnect carried Last-Event-Id.
	gotLEID, _ := lastEventIDReceived.Load().(string)
	if gotLEID != "evt-42" {
		t.Fatalf("Last-Event-Id on reconnect: got %q want %q", gotLEID, "evt-42")
	}
}

// --- DELETE on Close -------------------------------------------------

func TestStreamable_DeleteOnClose(t *testing.T) {
	t.Parallel()

	var deletedSID atomic.Value // string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Mcp-Session-Id", "sess-del")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case http.MethodGet:
			// Let the GET stream hang until context is cancelled.
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		case http.MethodDelete:
			deletedSID.Store(r.Header.Get("Mcp-Session-Id"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: true,
		MaxReconnects:           1,
		ReconnectInitialBackoff: 10 * time.Millisecond,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = tr.Recv(ctx) // drain

	// Close should trigger the DELETE.
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Give the goroutine time to complete the DELETE.
	deadline := time.Now().Add(streamableDeleteTimeout + 200*time.Millisecond)
	for time.Now().Before(deadline) {
		if sid, _ := deletedSID.Load().(string); sid != "" {
			if sid != "sess-del" {
				t.Fatalf("DELETE session id: got %q want %q", sid, "sess-del")
			}
			return // success
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("DELETE was not received within deadline")
}

// --- Auth headers applied to POST, GET, DELETE -----------------------

func TestStreamable_AuthHeaders(t *testing.T) {
	t.Parallel()

	var postAuth, getAuth, deleteAuth atomic.Value // string each

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			postAuth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Mcp-Session-Id", "auth-sess")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case http.MethodGet:
			getAuth.Store(r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		case http.MethodDelete:
			deleteAuth.Store(r.Header.Get("Authorization"))
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		Headers:                 map[string]string{"Authorization": "Bearer my-token"},
		OpenNotificationsStream: true,
		MaxReconnects:           1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = tr.Recv(ctx)

	// Let GET stream open.
	time.Sleep(50 * time.Millisecond)

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Allow DELETE goroutine to complete.
	time.Sleep(streamableDeleteTimeout + 100*time.Millisecond)

	const want = "Bearer my-token"
	if v, _ := postAuth.Load().(string); v != want {
		t.Fatalf("POST Authorization: got %q want %q", v, want)
	}
	if v, _ := getAuth.Load().(string); v != want {
		t.Fatalf("GET Authorization: got %q want %q", v, want)
	}
	if v, _ := deleteAuth.Load().(string); v != want {
		t.Fatalf("DELETE Authorization: got %q want %q", v, want)
	}
}

// --- Malformed Content-Type ------------------------------------------

func TestStreamable_MalformedContentType(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		// Intentionally malformed Content-Type.
		w.Header().Set("Content-Type", "bogus/!!!")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "not json")
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err == nil {
		t.Fatal("expected error for malformed Content-Type, got nil")
	}
}

// --- Unexpected Content-Type -----------------------------------------

func TestStreamable_UnexpectedContentType(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "unexpected plain text response")
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	if err == nil {
		t.Fatal("expected error for unexpected Content-Type, got nil")
	}
}

// --- 4xx / 5xx error status ------------------------------------------

func TestStreamable_ErrorStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []int{400, 401, 403, 404, 500} {
		t.Run(fmt.Sprintf("HTTP%d", status), func(t *testing.T) {
			t.Parallel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost {
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				w.WriteHeader(status)
			}))
			defer srv.Close()

			tr := newStreamableTransport(StreamableOptions{
				ServerURL:               srv.URL,
				OpenNotificationsStream: false,
			})
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if err := tr.Start(ctx); err != nil {
				t.Fatalf("Start: %v", err)
			}
			defer func() { _ = tr.Close() }()

			err := tr.Send(ctx, []byte(`{}`))
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", status)
			}
		})
	}
}

// --- ServerLabel -----------------------------------------------------

func TestStreamable_ServerLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		opts    StreamableOptions
		wantPfx string
	}{
		{"with name", StreamableOptions{ServerName: "github"}, "http:github"},
		{"from url", StreamableOptions{ServerURL: "https://api.github.com/mcp/"}, "http:api.github.com"},
		{"empty", StreamableOptions{}, "http:(unset)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := NewStreamable(tc.opts)
			got := tr.ServerLabel()
			if !strings.HasPrefix(got, tc.wantPfx) {
				t.Fatalf("got %q want prefix %q", got, tc.wantPfx)
			}
		})
	}
}

// --- Start validates URL ---------------------------------------------

func TestStreamable_StartNoURL(t *testing.T) {
	t.Parallel()
	tr := newStreamableTransport(StreamableOptions{})
	err := tr.Start(context.Background())
	if err == nil {
		t.Fatal("expected error when url is missing")
	}
	if !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- Close idempotent ------------------------------------------------

func TestStreamable_CloseIdempotent(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = tr.Recv(ctx)

	if err := tr.Close(); err != nil {
		t.Fatalf("First Close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("Second Close (should be no-op): %v", err)
	}
}

// --- RecvEOFAfterClose -----------------------------------------------

func TestStreamable_RecvEOFAfterClose(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, _ = tr.Recv(ctx) // drain

	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

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

// --- SendAfterClose --------------------------------------------------

func TestStreamable_SendAfterClose(t *testing.T) {
	t.Parallel()
	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               "http://localhost:1",
		OpenNotificationsStream: false,
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_ = tr.Close()

	err := tr.Send(context.Background(), []byte(`{}`))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed after Close, got %v", err)
	}
}

// --- Goroutine leak (open/close 100 transports) ----------------------

func TestStreamable_GoroutineLeak(t *testing.T) {
	t.Parallel()

	for i := range 100 {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost:
				w.Header().Set("Mcp-Session-Id", "leak-test")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
			case http.MethodGet:
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				<-r.Context().Done()
			case http.MethodDelete:
				w.WriteHeader(http.StatusNoContent)
			default:
				w.WriteHeader(http.StatusMethodNotAllowed)
			}
		}))

		tr := newStreamableTransport(StreamableOptions{
			ServerURL:               srv.URL,
			OpenNotificationsStream: true,
			MaxReconnects:           1,
			ReconnectInitialBackoff: 5 * time.Millisecond,
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		if err := tr.Start(ctx); err != nil {
			cancel()
			srv.Close()
			t.Fatalf("iteration %d Start: %v", i, err)
		}
		if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
			cancel()
			srv.Close()
			t.Fatalf("iteration %d Send: %v", i, err)
		}
		_, _ = tr.Recv(ctx)
		_ = tr.Close()
		cancel()
		srv.Close()
	}

	// Give goroutines a moment to fully exit.
	time.Sleep(50 * time.Millisecond)
}

// --- 405 on GET disables notifications stream cleanly ----------------

func TestStreamable_GET405DisablesNotifications(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			w.Header().Set("Mcp-Session-Id", "no-get-sess")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":{}}`)
		case http.MethodGet:
			// Server does not support notifications GET.
			w.WriteHeader(http.StatusMethodNotAllowed)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	tr := newStreamableTransport(StreamableOptions{
		ServerURL:               srv.URL,
		OpenNotificationsStream: true,
		MaxReconnects:           1,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := tr.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	if err := tr.Send(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := tr.Recv(ctx)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !strings.Contains(string(got), `"id":1`) {
		t.Fatalf("unexpected response: %q", got)
	}
	// Transport should still be open (GET 405 is logged and silently disabled).
	if tr.(*streamableTransport).closed.Load() {
		t.Fatal("transport should not be closed after 405 on GET notifications")
	}
}
