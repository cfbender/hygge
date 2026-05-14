package mcp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// maxSSEEventSize caps a single SSE event's accumulated data at 16 MiB.
// A rogue or malfunctioning server cannot trigger unbounded allocation.
const maxSSEEventSize = 16 * 1024 * 1024

// sseDefaultConnectTimeout is used when SSEOptions.ConnectTimeout is zero.
const sseDefaultConnectTimeout = 10 * time.Second

// sseDefaultInitialBackoff is the starting reconnect delay.
const sseDefaultInitialBackoff = 500 * time.Millisecond

// sseDefaultMaxBackoff caps the exponential backoff ceiling.
const sseDefaultMaxBackoff = 30 * time.Second

// sseDefaultMaxReconnects is the retry cap applied when SSEOptions.MaxReconnects is zero.
const sseDefaultMaxReconnects = 5

// SSEOptions configures NewSSE.
type SSEOptions struct {
	// ServerURL is the SSE endpoint URL the client GETs.
	ServerURL string

	// Headers are added to every HTTP request (GET stream and POST
	// request bodies).  Set Authorization here for bearer tokens.
	Headers map[string]string

	// HTTPClient overrides the default http.Client.  When nil a client
	// with no timeout is used — the GET stream is long-lived so a
	// global timeout would mis-fire.
	HTTPClient *http.Client

	// ServerName is a short identifier used in logs.  Falls back to
	// the host of ServerURL when empty.
	ServerName string

	// ConnectTimeout caps the initial handshake (GET + first endpoint
	// event).  Default 10 s.
	ConnectTimeout time.Duration

	// ReconnectInitialBackoff is the starting backoff duration after a
	// dropped GET stream.  Default 500 ms.  Doubles on each retry.
	ReconnectInitialBackoff time.Duration

	// ReconnectMaxBackoff caps the per-attempt wait.  Default 30 s.
	ReconnectMaxBackoff time.Duration

	// MaxReconnects caps total reconnect attempts before giving up.
	// 0 uses the built-in default of 5.
	MaxReconnects int
}

// sseTransport speaks MCP's SSE wire format.  It opens a long-lived GET
// to the server's SSE endpoint, parses out the per-request `endpoint`
// event, and POSTs each outgoing JSON-RPC request to that endpoint URL
// while listening for the matching response on the same POST's response
// body (which is also SSE-shaped).
//
// Concurrent Send calls are serialised by the Client's internal write
// mutex; sseTransport itself need not be safe for concurrent Send.
type sseTransport struct {
	opts   SSEOptions
	client *http.Client

	// mu guards endpoint, lastEventID, and cancel.
	mu          sync.Mutex
	endpoint    string             // POST URL once handshake completes
	lastEventID string             // Last-Event-ID for reconnection
	cancel      context.CancelFunc // cancels the GET stream goroutine

	// inbound receives raw JSON payloads from both the GET stream
	// (server-initiated notifications) and POST response streams.
	inbound chan []byte

	closed  atomic.Bool
	started atomic.Bool

	// inboundClosed guards against double-close of the inbound channel.
	inboundClosed atomic.Bool
}

// NewSSE constructs an SSE Transport.  The connection is not established
// until Start is called.
func NewSSE(opts SSEOptions) Transport {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			// No timeout: the GET stream is indefinitely long-lived.
			Transport: &http.Transport{
				MaxIdleConnsPerHost: 4,
				IdleConnTimeout:     90 * time.Second,
			},
		}
	}
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = sseDefaultConnectTimeout
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
	return &sseTransport{
		opts:    opts,
		client:  httpClient,
		inbound: make(chan []byte, 64),
	}
}

// ServerLabel returns a short diagnostics label.
func (t *sseTransport) ServerLabel() string {
	name := t.opts.ServerName
	if name != "" {
		return "sse:" + name
	}
	if t.opts.ServerURL != "" {
		if u, err := url.Parse(t.opts.ServerURL); err == nil {
			return "sse:" + u.Host
		}
	}
	return "sse:(unset)"
}

// Start opens the SSE GET stream and waits for the first `endpoint`
// event.  Returns an error when the handshake does not complete within
// ConnectTimeout or when the server sends an unexpected first event.
func (t *sseTransport) Start(ctx context.Context) error {
	if t.started.Swap(true) {
		return fmt.Errorf("mcp: sse: already started")
	}
	if t.closed.Load() {
		return ErrClosed
	}
	if t.opts.ServerURL == "" {
		return fmt.Errorf("mcp: sse: url is required")
	}

	// Bound the handshake with a connect-timeout context.
	handshakeCtx, handshakeCancel := context.WithTimeout(ctx, t.opts.ConnectTimeout)
	defer handshakeCancel()

	endpoint, lastID, cancel, err := t.openStream(handshakeCtx)
	if err != nil {
		return fmt.Errorf("mcp: sse: handshake: %w", err)
	}

	t.mu.Lock()
	t.endpoint = endpoint
	t.lastEventID = lastID
	t.cancel = cancel
	t.mu.Unlock()

	return nil
}

// openStream performs the SSE GET handshake: opens the connection,
// reads the first `endpoint` event, then starts a background goroutine
// that continues draining the stream.
//
// Returns: (endpointURL, lastEventID, cancelFunc, error).
// On success the caller must eventually call cancel to stop the goroutine.
func (t *sseTransport) openStream(ctx context.Context) (string, string, context.CancelFunc, error) {
	// The stream context lives independently of the handshake timeout.
	// We create it here so the long-lived GET response body is not
	// cancelled when the caller's handshake-timeout context expires.
	streamCtx, cancel := context.WithCancel(context.Background())

	req, err := t.buildGETRequest(streamCtx)
	if err != nil {
		cancel()
		return "", "", nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		cancel()
		return "", "", nil, fmt.Errorf("GET %s: %w", t.opts.ServerURL, err)
	}
	if resp.StatusCode != http.StatusOK {
		cancel()
		_ = resp.Body.Close()
		return "", "", nil, fmt.Errorf("GET %s: unexpected status %d", t.opts.ServerURL, resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		cancel()
		_ = resp.Body.Close()
		return "", "", nil, fmt.Errorf("GET %s: unexpected Content-Type %q (want text/event-stream)", t.opts.ServerURL, ct)
	}

	// Use a context with the handshake timeout only for reading the first
	// event.  We do not pass it to the scanner; after the first event is
	// read the stream runs under streamCtx which has no deadline.
	scanner := newSSEScanner(resp.Body)

	// First event MUST be `endpoint`.  Apply handshake deadline via a
	// goroutine that cancels the stream context if the first event
	// doesn't arrive in time.  After the first event arrives we let
	// streamCtx run without a deadline.
	firstEventCh := make(chan struct {
		ev  sseEvent
		err error
	}, 1)
	go func() {
		ev, err := scanner.Next()
		firstEventCh <- struct {
			ev  sseEvent
			err error
		}{ev, err}
	}()

	var firstEv sseEvent
	select {
	case result := <-firstEventCh:
		if result.err != nil {
			cancel()
			_ = resp.Body.Close()
			if errors.Is(result.err, io.EOF) {
				return "", "", nil, fmt.Errorf("GET stream closed before endpoint event")
			}
			return "", "", nil, fmt.Errorf("reading first event: %w", result.err)
		}
		firstEv = result.ev
	case <-ctx.Done():
		cancel()
		_ = resp.Body.Close()
		return "", "", nil, fmt.Errorf("reading first event: %w", ctx.Err())
	}

	if firstEv.name != "endpoint" {
		cancel()
		_ = resp.Body.Close()
		return "", "", nil, fmt.Errorf("expected first event to be \"endpoint\", got %q", firstEv.name)
	}
	endpointURL, err := resolveEndpoint(t.opts.ServerURL, strings.TrimSpace(firstEv.data))
	if err != nil {
		cancel()
		_ = resp.Body.Close()
		return "", "", nil, fmt.Errorf("resolve endpoint %q: %w", firstEv.data, err)
	}

	lastEventID := firstEv.id

	// Spawn the drain goroutine under streamCtx (no deadline).
	go t.drainStream(streamCtx, resp.Body, scanner, 0)

	return endpointURL, lastEventID, cancel, nil
}

// drainStream reads events from an open SSE stream and forwards message
// payloads to t.inbound.  When the stream ends (EOF, error, ctx cancel),
// it attempts reconnection if the transport has not been closed and
// reconnect attempts remain.  reconnects is the count of prior reconnect
// attempts in this session.
func (t *sseTransport) drainStream(ctx context.Context, body io.Closer, scanner *sseScanner, reconnects int) {
	defer func() { _ = body.Close() }()

	for {
		if ctx.Err() != nil {
			return
		}
		ev, err := scanner.Next()
		if err != nil {
			break
		}
		// Track the last event id for reconnection.
		if ev.id != "" {
			t.mu.Lock()
			t.lastEventID = ev.id
			t.mu.Unlock()
		}
		// Default event name is "message".
		name := ev.name
		if name == "" {
			name = "message"
		}
		if name != "message" {
			// Heartbeats and other event names are silently ignored.
			continue
		}
		data := strings.TrimSpace(ev.data)
		if data == "" {
			continue
		}
		if t.closed.Load() {
			return
		}
		select {
		case t.inbound <- []byte(data):
		case <-ctx.Done():
			return
		}
	}

	// Stream ended; attempt reconnection if not closed.
	if t.closed.Load() || ctx.Err() != nil {
		return
	}
	maxRetries := t.opts.MaxReconnects
	if reconnects >= maxRetries {
		slog.Warn("mcp: sse: GET stream dropped; max reconnects reached; giving up",
			"server", t.ServerLabel(), "reconnects", reconnects)
		t.closed.Store(true)
		t.closeInbound()
		return
	}

	backoff := t.opts.ReconnectInitialBackoff
	for i := 0; i < reconnects; i++ {
		backoff *= 2
		if backoff > t.opts.ReconnectMaxBackoff {
			backoff = t.opts.ReconnectMaxBackoff
			break
		}
	}

	slog.Warn("mcp: sse: GET stream dropped; reconnecting",
		"server", t.ServerLabel(), "attempt", reconnects+1, "backoff", backoff)

	timer := time.NewTimer(backoff)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		return
	}

	// Re-open the stream under the same long-lived context.  The HTTP
	// Transport's DialTimeout handles connection latency; no additional
	// per-reconnect context deadline is applied so the response body
	// stays live until ctx is cancelled.
	req, err := t.buildGETRequest(ctx)
	if err != nil {
		slog.Warn("mcp: sse: reconnect request build failed", "err", err)
		t.scheduleRetry(ctx, reconnects+1)
		return
	}

	resp, err := t.client.Do(req)
	if err != nil {
		slog.Warn("mcp: sse: reconnect GET failed", "err", err, "server", t.ServerLabel())
		t.scheduleRetry(ctx, reconnects+1)
		return
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		slog.Warn("mcp: sse: reconnect GET bad status", "status", resp.StatusCode)
		t.scheduleRetry(ctx, reconnects+1)
		return
	}

	newScanner := newSSEScanner(resp.Body)
	// First event on reconnect must also be `endpoint`; update the stored URL.
	ev, err := newScanner.Next()
	if err != nil || ev.name != "endpoint" {
		_ = resp.Body.Close()
		slog.Warn("mcp: sse: reconnect: expected endpoint event",
			"got_event", ev.name, "err", err)
		t.scheduleRetry(ctx, reconnects+1)
		return
	}
	endpointURL, err := resolveEndpoint(t.opts.ServerURL, strings.TrimSpace(ev.data))
	if err != nil {
		_ = resp.Body.Close()
		slog.Warn("mcp: sse: reconnect: resolve endpoint failed", "err", err)
		t.scheduleRetry(ctx, reconnects+1)
		return
	}
	t.mu.Lock()
	t.endpoint = endpointURL
	if ev.id != "" {
		t.lastEventID = ev.id
	}
	t.mu.Unlock()

	go t.drainStream(ctx, resp.Body, newScanner, reconnects+1)
}

// scheduleRetry spawns a new drain loop against an empty stream so the
// reconnect counter increments without a body.  Used when a reconnect
// HTTP request itself fails so we still respect MaxReconnects.
func (t *sseTransport) scheduleRetry(ctx context.Context, reconnects int) {
	go t.drainStream(ctx, io.NopCloser(bytes.NewReader(nil)), newSSEScanner(bytes.NewReader(nil)), reconnects)
}

// closeInbound closes the inbound channel at most once.
func (t *sseTransport) closeInbound() {
	if t.inboundClosed.Swap(true) {
		return
	}
	close(t.inbound)
}

// buildGETRequest constructs the long-lived GET request for the SSE endpoint.
func (t *sseTransport) buildGETRequest(ctx context.Context) (*http.Request, error) {
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
	lastID := t.lastEventID
	t.mu.Unlock()
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	return req, nil
}

// Send POSTs msg to the captured endpoint URL and drains the response
// body as SSE, routing any message events into the inbound channel.
func (t *sseTransport) Send(ctx context.Context, msg []byte) error {
	if t.closed.Load() {
		return ErrClosed
	}

	t.mu.Lock()
	endpoint := t.endpoint
	t.mu.Unlock()

	if endpoint == "" {
		return fmt.Errorf("mcp: sse: transport not ready (Start not called or handshake failed)")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(msg))
	if err != nil {
		return fmt.Errorf("mcp: sse: build POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range t.opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("mcp: sse: POST %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusAccepted {
		// 202 Accepted: the server may respond asynchronously on the
		// GET stream.  Nothing to drain here.
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("mcp: sse: POST %s: status %d", endpoint, resp.StatusCode)
	}

	// Drain the POST response body as SSE.  Some servers send a single
	// response message; others stream progress notifications followed
	// by the response.  All are forwarded to inbound.
	scanner := newSSEScanner(resp.Body)
	for {
		ev, err := scanner.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("mcp: sse: read POST response: %w", err)
		}
		name := ev.name
		if name == "" {
			name = "message"
		}
		if name == "done" || ev.data == "[DONE]" {
			break
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
	return nil
}

// Recv blocks until the next message arrives from the server (either
// via the GET stream or a POST response).  Returns io.EOF when the
// transport has been closed cleanly.
func (t *sseTransport) Recv(_ context.Context) ([]byte, error) {
	msg, ok := <-t.inbound
	if !ok {
		return nil, io.EOF
	}
	return msg, nil
}

// Close shuts the transport down by cancelling the GET stream goroutine
// and marking the transport closed.  Idempotent.
func (t *sseTransport) Close() error {
	if t.closed.Swap(true) {
		return nil
	}
	t.mu.Lock()
	cancel := t.cancel
	t.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// Close inbound after a brief pause to let the drain goroutine
	// observe the closed flag and exit naturally first.
	go func() {
		time.Sleep(10 * time.Millisecond)
		t.closeInbound()
	}()
	return nil
}

// resolveEndpoint resolves endpointRef against baseURL.  If
// endpointRef is already an absolute URL it is returned as-is;
// otherwise it is resolved relative to baseURL.
func resolveEndpoint(baseURL, endpointRef string) (string, error) {
	u, err := url.Parse(endpointRef)
	if err != nil {
		return "", err
	}
	if u.IsAbs() {
		return endpointRef, nil
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(u).String(), nil
}

// sseEvent is one parsed SSE event.
type sseEvent struct {
	name string
	data string // accumulated data lines joined with \n
	id   string
}

// sseScanner parses a stream of SSE events from r.
type sseScanner struct {
	br  *bufio.Reader
	err error // sticky error; once set, Next always returns this error
}

func newSSEScanner(r io.Reader) *sseScanner {
	return &sseScanner{br: bufio.NewReader(r)}
}

// Next reads and returns the next SSE event from the stream.  Returns
// (zero, io.EOF) when the stream ends cleanly.  Comment lines (starting
// with ':') are silently discarded.  Multi-line data values are joined
// with '\n'.  An event whose accumulated data exceeds maxSSEEventSize
// returns an error.
func (s *sseScanner) Next() (sseEvent, error) {
	if s.err != nil {
		return sseEvent{}, s.err
	}
	var ev sseEvent
	var dataLines []string
	var totalDataBytes int

	for {
		line, readErr := s.br.ReadString('\n')
		// Trim the line endings; SSE uses LF or CRLF.
		trimmed := strings.TrimRight(line, "\r\n")

		if trimmed == "" {
			// Empty line = event boundary.
			if len(dataLines) > 0 || ev.name != "" || ev.id != "" {
				ev.data = strings.Join(dataLines, "\n")
				return ev, nil
			}
			// Leading or consecutive blank lines: keep scanning.
			if readErr != nil {
				if errors.Is(readErr, io.EOF) {
					s.err = io.EOF
					return sseEvent{}, io.EOF
				}
				s.err = readErr
				return sseEvent{}, readErr
			}
			continue
		}

		// Handle read errors after we've processed the (possibly partial) line.
		var isEOF bool
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				isEOF = true
				// Partial unterminated line at EOF — treat as complete.
			} else {
				s.err = readErr
				return sseEvent{}, readErr
			}
		}

		// Comment lines start with ':'.
		if strings.HasPrefix(trimmed, ":") {
			if isEOF {
				s.err = io.EOF
			}
			continue
		}

		field, value, hasColon := strings.Cut(trimmed, ":")
		if !hasColon {
			field = trimmed
			value = ""
		} else {
			// The spec: "If value begins with a U+0020 SPACE, remove it."
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
		}

		switch field {
		case "event":
			ev.name = value
		case "data":
			totalDataBytes += len(value)
			if totalDataBytes > maxSSEEventSize {
				s.err = fmt.Errorf("mcp: sse: event data exceeds %d bytes", maxSSEEventSize)
				return sseEvent{}, s.err
			}
			dataLines = append(dataLines, value)
		case "id":
			ev.id = value
		case "retry":
			// Retry hints are accepted but not acted on.
		default:
			// Unknown field names are ignored per the SSE spec.
		}

		if isEOF {
			// Partial line was the last data in the stream; emit the event
			// if we have one.
			if len(dataLines) > 0 || ev.name != "" || ev.id != "" {
				ev.data = strings.Join(dataLines, "\n")
				s.err = io.EOF
				return ev, nil
			}
			s.err = io.EOF
			return sseEvent{}, io.EOF
		}
	}
}

// expandHeaderEnv expands ${VAR} and $VAR references in header values
// using os.LookupEnv.  Unset variables are replaced with the empty
// string and logged at Warn level so misconfigured tokens are visible
// without failing the load.
func expandHeaderEnv(headers map[string]string, lookup func(string) (string, bool)) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	if lookup == nil {
		lookup = os.LookupEnv
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		expanded := os.Expand(v, func(key string) string {
			val, ok := lookup(key)
			if !ok {
				slog.Warn("mcp: sse: header references unset env var",
					"header", k, "var", key)
				return ""
			}
			return val
		})
		out[k] = expanded
	}
	return out
}
