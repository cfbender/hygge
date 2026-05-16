package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// streamableDefaultConnectTimeout is the fallback when
// StreamableOptions.ConnectTimeout is zero.
const streamableDefaultConnectTimeout = 10 * time.Second

// streamableDeleteTimeout caps the HTTP DELETE sent on Close so a
// slow server cannot block the caller indefinitely.
const streamableDeleteTimeout = 2 * time.Second

// streamableTransport speaks MCP's Streamable HTTP transport
// (2025-03-26 spec): a single endpoint URL serves both client POSTs
// (requests) and optional GETs (server notifications).  The server
// responds to POSTs with either an immediate application/json body or
// a text/event-stream carrying multiple JSON-RPC messages.
//
// The transport tracks the server-assigned Mcp-Session-Id and includes
// it on every subsequent request.  Session termination uses HTTP
// DELETE.
//
// Concurrent Send calls are serialised by the Client's internal write
// mutex; streamableTransport itself need not be safe for concurrent
// Send.
type streamableTransport struct {
	opts   StreamableOptions
	client *http.Client

	// mu guards sessionID and notifStarted.
	mu           sync.Mutex
	sessionID    string
	notifStarted bool

	closed atomic.Bool

	// inbound carries messages received via the optional GET stream
	// and any SSE-or-JSON responses from POSTs.
	inbound chan []byte

	// inboundClosed guards against double-close of the inbound channel.
	inboundClosed atomic.Bool

	// notifCancel, when non-nil, is the cancel func for the
	// server-notification GET goroutine.
	notifCancel context.CancelFunc

	// lastEventID is the most recent SSE id seen on the notification
	// stream, for resumption on reconnect.
	lastEventID atomic.Value // stores string
}

// StreamableOptions configures NewStreamable.
type StreamableOptions struct {
	// ServerURL is the single MCP endpoint URL that serves both POST
	// requests and optional GET notifications.
	ServerURL string

	// Headers are added to every HTTP request (POST, GET, DELETE).
	// Use this for Authorization bearer tokens.
	Headers map[string]string

	// HTTPClient overrides the default http.Client.  When nil a client
	// with no timeout is used (the GET stream is long-lived).
	HTTPClient *http.Client

	// ServerName is a short identifier used in log messages.  Falls
	// back to the host portion of ServerURL when empty.
	ServerName string

	// OpenNotificationsStream controls whether the transport opens the
	// optional long-lived GET stream for server-initiated notifications
	// after the first successful POST.  Default true.
	//
	// Set to false for servers that do not support GET (they will
	// return 405 otherwise).
	OpenNotificationsStream bool

	// ConnectTimeout caps the initial POST handshake.  Default 10s.
	ConnectTimeout time.Duration

	// ReconnectInitialBackoff is the starting delay between GET stream
	// reconnect attempts.  Default 500ms; doubles on each retry.
	ReconnectInitialBackoff time.Duration

	// ReconnectMaxBackoff caps the per-attempt wait.  Default 30s.
	ReconnectMaxBackoff time.Duration

	// MaxReconnects caps total reconnect attempts before giving up.
	// 0 uses the built-in default of 5.
	MaxReconnects int
}

// NewStreamable constructs a Streamable HTTP Transport.  The connection
// is not established until the first Send call.
func NewStreamable(opts StreamableOptions) Transport {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			// No global timeout — the GET stream is indefinitely
			// long-lived.
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = streamableDefaultConnectTimeout
	}
	if opts.ReconnectInitialBackoff <= 0 {
		opts.ReconnectInitialBackoff = sseDefaultInitialBackoff
	}
	if opts.ReconnectMaxBackoff <= 0 {
		opts.ReconnectMaxBackoff = sseDefaultMaxBackoff
	}
	if opts.MaxReconnects == 0 {
		opts.MaxReconnects = sseDefaultMaxReconnects
	}
	t := &streamableTransport{
		opts:    opts,
		client:  httpClient,
		inbound: make(chan []byte, 64),
	}
	t.lastEventID.Store("")
	return t
}

// ServerLabel returns a short diagnostics-friendly label.
func (t *streamableTransport) ServerLabel() string {
	name := t.opts.ServerName
	if name != "" {
		return "http:" + name
	}
	if t.opts.ServerURL != "" {
		if u, err := url.Parse(t.opts.ServerURL); err == nil {
			return "http:" + u.Host
		}
	}
	return "http:(unset)"
}

// Start validates the configured URL.  The actual server connection is
// deferred to the first Send call (the spec requires the session-id to
// be established by the response to the first POST, not by a
// pre-flight).
func (t *streamableTransport) Start(_ context.Context) error {
	if t.opts.ServerURL == "" {
		return fmt.Errorf("mcp: streamable: url is required")
	}
	if _, err := url.Parse(t.opts.ServerURL); err != nil {
		return fmt.Errorf("mcp: streamable: invalid url %q: %w", t.opts.ServerURL, err)
	}
	return nil
}

// Send POSTs msg to ServerURL.  On the first call, any Mcp-Session-Id
// returned by the server is captured.  The response body is processed
// according to its Content-Type:
//   - application/json — delivered as a single inbound message.
//   - text/event-stream — drained as SSE; each message event is
//     delivered individually.
//   - 202 Accepted — no body; response will arrive on the GET stream.
//
// After the first successful POST, if OpenNotificationsStream is true
// and the GET stream has not yet been started, it is started.
func (t *streamableTransport) Send(ctx context.Context, msg []byte) error {
	if t.closed.Load() {
		return ErrClosed
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.opts.ServerURL, bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("mcp: streamable: build POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range t.opts.Headers {
		req.Header.Set(k, v)
	}
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: streamable: POST %s: %w", t.opts.ServerURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Capture / update session id.
	if newSID := resp.Header.Get("Mcp-Session-Id"); newSID != "" {
		t.mu.Lock()
		if t.sessionID == "" {
			t.sessionID = newSID
		} else if t.sessionID != newSID {
			slog.Warn("mcp: streamable: server rotated session id; adopting new id",
				"server", t.ServerLabel(),
				"old", t.sessionID, "new", newSID)
			t.sessionID = newSID
		}
		t.mu.Unlock()
	}

	switch resp.StatusCode {
	case http.StatusAccepted:
		// 202: server queued the request; response arrives on GET stream.
		t.maybeStartNotifGet()
		return nil
	case http.StatusOK:
		// fall through to body handling
	default:
		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return fmt.Errorf("mcp: streamable: POST %s: status %d: %s",
				t.opts.ServerURL, resp.StatusCode, strings.TrimSpace(string(body)))
		}
		// 2xx other than 200/202 — treat as 200.
	}

	// Decide how to handle the body.
	ct := resp.Header.Get("Content-Type")
	mediaType, _, parseErr := mime.ParseMediaType(ct)
	if parseErr != nil {
		// Unrecognised or absent Content-Type.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.maybeStartNotifGet()
		return fmt.Errorf("mcp: streamable: unrecognised Content-Type %q: %s",
			ct, strings.TrimSpace(string(body)))
	}

	switch mediaType {
	case "application/json":
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("mcp: streamable: read JSON response: %w", err)
		}
		data := strings.TrimSpace(string(body))
		if data != "" {
			select {
			case t.inbound <- []byte(data):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

	case "text/event-stream":
		if err := t.drainPostSSE(ctx, resp.Body); err != nil {
			return err
		}

	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.maybeStartNotifGet()
		return fmt.Errorf("mcp: streamable: unexpected Content-Type %q in POST response: %s",
			ct, strings.TrimSpace(string(body)))
	}

	t.maybeStartNotifGet()
	return nil
}

// drainPostSSE reads SSE events from the POST response body and
// delivers message payloads to inbound.  Returns nil on clean EOF.
func (t *streamableTransport) drainPostSSE(ctx context.Context, body io.Reader) error {
	scanner := newSSEScanner(body)
	for {
		ev, err := scanner.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("mcp: streamable: read SSE response: %w", err)
		}
		name := ev.name
		if name == "" {
			name = "message"
		}
		if name == "done" || ev.data == "[DONE]" {
			return nil
		}
		if name != "message" {
			continue
		}
		data := strings.TrimSpace(ev.data)
		if data == "" {
			continue
		}
		select {
		case t.inbound <- []byte(data):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// maybeStartNotifGet starts the optional GET notifications goroutine
// after the first successful POST, at most once.
func (t *streamableTransport) maybeStartNotifGet() {
	if !t.opts.OpenNotificationsStream {
		return
	}
	t.mu.Lock()
	if t.notifStarted || t.closed.Load() {
		t.mu.Unlock()
		return
	}
	t.notifStarted = true
	notifCtx, cancel := context.WithCancel(context.Background())
	t.notifCancel = cancel
	t.mu.Unlock()

	go t.runNotifGet(notifCtx, 0)
}

// runNotifGet opens a long-lived GET to ServerURL and drains its SSE
// stream, routing message events to inbound.  On disconnect it retries
// with exponential backoff up to MaxReconnects.  reconnects is the
// count of prior reconnect attempts in this session.
func (t *streamableTransport) runNotifGet(ctx context.Context, reconnects int) {
	req, err := t.buildGETRequest(ctx)
	if err != nil {
		slog.Warn("mcp: streamable: build GET request failed", "server", t.ServerLabel(), "err", err)
		t.retryNotifGet(ctx, reconnects)
		return
	}

	resp, err := t.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		slog.Warn("mcp: streamable: GET failed", "server", t.ServerLabel(), "err", err)
		t.retryNotifGet(ctx, reconnects)
		return
	}
	// Some servers may not support GET — log and silently disable.
	if resp.StatusCode == http.StatusMethodNotAllowed {
		_ = resp.Body.Close()
		slog.Warn("mcp: streamable: server returned 405 on GET notifications stream; disabling",
			"server", t.ServerLabel())
		return
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		slog.Warn("mcp: streamable: GET bad status",
			"server", t.ServerLabel(), "status", resp.StatusCode)
		t.retryNotifGet(ctx, reconnects)
		return
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		_ = resp.Body.Close()
		slog.Warn("mcp: streamable: GET unexpected Content-Type",
			"server", t.ServerLabel(), "content_type", ct)
		return
	}

	scanner := newSSEScanner(resp.Body)
	for {
		if ctx.Err() != nil {
			_ = resp.Body.Close()
			return
		}
		ev, err := scanner.Next()
		if err != nil {
			break
		}
		if ev.id != "" {
			t.lastEventID.Store(ev.id)
		}
		name := ev.name
		if name == "" {
			name = "message"
		}
		if name != "message" {
			continue
		}
		data := strings.TrimSpace(ev.data)
		if data == "" {
			continue
		}
		if t.closed.Load() {
			_ = resp.Body.Close()
			return
		}
		select {
		case t.inbound <- []byte(data):
		case <-ctx.Done():
			_ = resp.Body.Close()
			return
		}
	}
	_ = resp.Body.Close()

	// Stream ended — retry unless closed or ctx done.
	if t.closed.Load() || ctx.Err() != nil {
		return
	}
	t.retryNotifGet(ctx, reconnects)
}

// retryNotifGet applies exponential backoff and relaunches runNotifGet.
func (t *streamableTransport) retryNotifGet(ctx context.Context, reconnects int) {
	if t.closed.Load() || ctx.Err() != nil {
		return
	}
	maxRetries := t.opts.MaxReconnects
	if reconnects >= maxRetries {
		slog.Warn("mcp: streamable: GET notifications stream dropped; max reconnects reached; giving up",
			"server", t.ServerLabel(), "reconnects", reconnects)
		return
	}

	backoff := t.opts.ReconnectInitialBackoff
	for range reconnects {
		backoff *= 2
		if backoff > t.opts.ReconnectMaxBackoff {
			backoff = t.opts.ReconnectMaxBackoff
			break
		}
	}

	slog.Warn("mcp: streamable: GET notifications stream dropped; reconnecting",
		"server", t.ServerLabel(), "attempt", reconnects+1, "backoff", backoff)

	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}

	go t.runNotifGet(ctx, reconnects+1)
}

// buildGETRequest constructs the long-lived GET request for server
// notifications.
func (t *streamableTransport) buildGETRequest(ctx context.Context) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.opts.ServerURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	for k, v := range t.opts.Headers {
		req.Header.Set(k, v)
	}
	t.mu.Lock()
	sid := t.sessionID
	t.mu.Unlock()
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	if lastID, _ := t.lastEventID.Load().(string); lastID != "" {
		req.Header.Set("Last-Event-Id", lastID)
	}
	return req, nil
}

// Recv blocks until the next message arrives from the server.  Returns
// io.EOF when the transport has been closed cleanly.
func (t *streamableTransport) Recv(_ context.Context) ([]byte, error) {
	msg, ok := <-t.inbound
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

// Close terminates the transport:
//  1. Sets the closed flag.
//  2. Cancels the notifications GET goroutine.
//  3. Sends an HTTP DELETE to the server with the session id (best-effort;
//     capped at 2 s).
//  4. Closes the inbound channel.
//
// Idempotent.
func (t *streamableTransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}

	t.mu.Lock()
	cancel := t.notifCancel
	sid := t.sessionID
	t.mu.Unlock()

	if cancel != nil {
		cancel()
	}

	// Best-effort DELETE.
	if sid != "" {
		go func() {
			deleteCtx, deleteCancel := context.WithTimeout(context.Background(), streamableDeleteTimeout)
			defer deleteCancel()
			req, err := http.NewRequestWithContext(deleteCtx, http.MethodDelete, t.opts.ServerURL, nil)
			if err != nil {
				slog.Warn("mcp: streamable: build DELETE request failed",
					"server", t.ServerLabel(), "err", err)
				return
			}
			for k, v := range t.opts.Headers {
				req.Header.Set(k, v)
			}
			req.Header.Set("Mcp-Session-Id", sid)
			resp, err := t.client.Do(req)
			if err != nil {
				slog.Warn("mcp: streamable: DELETE failed",
					"server", t.ServerLabel(), "err", err)
				return
			}
			_ = resp.Body.Close()
			if resp.StatusCode >= 400 {
				slog.Warn("mcp: streamable: DELETE bad status",
					"server", t.ServerLabel(), "status", resp.StatusCode)
			}
		}()
	}

	// Allow the drain goroutine a brief moment to notice the closed
	// flag and exit before we close the channel.
	go func() {
		time.Sleep(10 * time.Millisecond)
		t.closeInbound()
	}()
	return nil
}

// closeInbound closes the inbound channel at most once.
func (t *streamableTransport) closeInbound() {
	if t.inboundClosed.Swap(true) {
		return
	}
	close(t.inbound)
}
