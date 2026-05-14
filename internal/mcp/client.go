package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// defaultRequestTimeout bounds individual RPC calls when ClientOptions
// does not specify one.
const defaultRequestTimeout = 30 * time.Second

// ClientOptions configures Client.
type ClientOptions struct {
	// Transport is the wire-level connection.  Required.
	Transport Transport

	// Name uniquely identifies this client to hygge's config — it is
	// the value of `name = ...` in mcp.toml.  Used by NewMCPTool to
	// prefix tool names; not sent on the wire.
	Name string

	// ClientName is sent in InitializeParams.ClientInfo.Name.
	// Defaults to "hygge".
	ClientName string

	// ClientVersion is sent in InitializeParams.ClientInfo.Version.
	ClientVersion string

	// Now is an injectable clock; defaults to time.Now.
	Now func() time.Time

	// RequestTimeout bounds individual RPC calls.  Defaults to
	// defaultRequestTimeout.  A negative value disables the per-call
	// timeout (callers must supply their own ctx deadline).
	RequestTimeout time.Duration
}

// Client owns one MCP server connection and dispatches RPC traffic.
type Client struct {
	name           string
	transport      Transport
	clientName     string
	clientVersion  string
	now            func() time.Time
	requestTimeout time.Duration

	// idCounter is monotonically incremented for every outbound
	// request that expects a response.
	idCounter atomic.Int64

	// Pending response channels keyed by JSON-RPC id (encoded as a
	// JSON number string).  Mu guards pending and the closed/init
	// flags.
	mu          sync.Mutex
	pending     map[string]chan RPCResponse
	closed      bool
	initialized bool
	startErr    error

	// readDone is closed when the dispatch goroutine exits.
	readDone chan struct{}

	// caps is the server's advertised capability set, populated by
	// Initialize.  Read-only after Initialize returns successfully.
	caps ServerCapabilities

	// info mirrors caps for the ServerInfo struct.
	info ServerInfo
}

// New constructs a Client.  Does not start the transport.
func New(opts ClientOptions) *Client {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	clientName := opts.ClientName
	if clientName == "" {
		clientName = "hygge"
	}
	timeout := opts.RequestTimeout
	if timeout == 0 {
		timeout = defaultRequestTimeout
	}
	return &Client{
		name:           opts.Name,
		transport:      opts.Transport,
		clientName:     clientName,
		clientVersion:  opts.ClientVersion,
		now:            now,
		requestTimeout: timeout,
		pending:        make(map[string]chan RPCResponse),
		readDone:       make(chan struct{}),
	}
}

// Name returns the configured server name (the mcp.toml entry's name).
// Empty when the Client was constructed without one.
func (c *Client) Name() string { return c.name }

// ServerInfo returns the ServerInfo reported by the server.  Empty
// until Initialize succeeds.
func (c *Client) ServerInfo() ServerInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.info
}

// Capabilities returns the server's advertised capabilities.  Empty
// until Initialize succeeds.
func (c *Client) Capabilities() ServerCapabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Initialize starts the transport, sends `initialize`, waits for the
// response, then sends `notifications/initialized`.  Safe to call
// once; subsequent calls return an error.
func (c *Client) Initialize(ctx context.Context) (*InitializeResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrClosed
	}
	if c.initialized {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: client already initialized")
	}
	c.mu.Unlock()

	if c.transport == nil {
		return nil, fmt.Errorf("mcp: client: transport is nil")
	}
	if err := c.transport.Start(ctx); err != nil {
		c.mu.Lock()
		c.startErr = err
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp: initialize: start transport: %w", err)
	}

	// Spawn the dispatch goroutine BEFORE sending initialize so the
	// response can be routed back to us.
	go c.readLoop()

	params := InitializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ClientCapabilities{},
		ClientInfo: ClientInfo{
			Name:    c.clientName,
			Version: c.clientVersion,
		},
	}
	var result InitializeResult
	if err := c.call(ctx, MethodInitialize, params, &result); err != nil {
		return nil, fmt.Errorf("mcp: initialize: %w", err)
	}

	// Fire-and-forget the initialized notification.  No reply is
	// expected.
	if err := c.notify(ctx, MethodInitialized, struct{}{}); err != nil {
		return nil, fmt.Errorf("mcp: initialize: send initialized: %w", err)
	}

	c.mu.Lock()
	c.initialized = true
	c.caps = result.Capabilities
	c.info = result.ServerInfo
	c.mu.Unlock()

	return &result, nil
}

// ListTools returns the server's tool catalog.  Initialize must
// succeed first.
func (c *Client) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	if err := c.ensureInitialized(); err != nil {
		return nil, err
	}
	var result ListToolsResult
	if err := c.call(ctx, MethodToolsList, nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

// CallTool invokes a named tool with raw JSON args.  Returns the
// result on RPC success; IsError=true is a normal outcome the caller
// passes to the model.  An RPC-level error (transport or server
// error object) is returned as a Go error.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) (*CallToolResult, error) {
	if err := c.ensureInitialized(); err != nil {
		return nil, err
	}
	params := CallToolParams{Name: name, Arguments: args}
	var result CallToolResult
	if err := c.call(ctx, MethodToolsCall, params, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Ping issues a JSON-RPC ping and waits for the pong.  Used by
// `hygge mcp ping`.
func (c *Client) Ping(ctx context.Context) error {
	if err := c.ensureInitialized(); err != nil {
		return err
	}
	var ignored json.RawMessage
	return c.call(ctx, MethodPing, struct{}{}, &ignored)
}

// Close shuts down the transport and fails any in-flight calls with
// ErrClosed.  Idempotent.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	// Snapshot pending channels so we can fail them after releasing
	// the lock.
	pending := c.pending
	c.pending = make(map[string]chan RPCResponse)
	c.mu.Unlock()

	var err error
	if c.transport != nil {
		err = c.transport.Close()
	}
	for id, ch := range pending {
		// Closing the channel lets the waiter wake up with the
		// zero-value RPCResponse; the caller distinguishes via
		// ErrClosed.
		close(ch)
		_ = id
	}
	return err
}

// ensureInitialized returns an error when the client is not yet
// initialized (or already closed).
func (c *Client) ensureInitialized() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrClosed
	}
	if !c.initialized {
		return ErrNotInitialized
	}
	return nil
}

// notify sends a JSON-RPC notification (no ID, no expected response).
func (c *Client) notify(ctx context.Context, method string, params any) error {
	paramsJSON, err := encodeParams(params)
	if err != nil {
		return err
	}
	body, err := json.Marshal(RPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  paramsJSON,
	})
	if err != nil {
		return err
	}
	return c.transport.Send(ctx, body)
}

// call sends a JSON-RPC request, blocks for the matching response, and
// decodes Result into out (which must be a pointer).  If out is nil,
// the result is discarded.  Respects ctx cancellation AND the Client's
// request timeout.
func (c *Client) call(ctx context.Context, method string, params any, out any) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.mu.Unlock()

	paramsJSON, err := encodeParams(params)
	if err != nil {
		return err
	}

	idNum := c.idCounter.Add(1)
	idStr := strconv.FormatInt(idNum, 10)
	idRaw := json.RawMessage(idStr)

	ch := make(chan RPCResponse, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrClosed
	}
	c.pending[idStr] = ch
	c.mu.Unlock()

	// Compose call context honouring the Client's request timeout
	// while still being cancellable via ctx.
	callCtx := ctx
	var cancel context.CancelFunc
	if c.requestTimeout > 0 {
		callCtx, cancel = context.WithTimeout(ctx, c.requestTimeout)
		defer cancel()
	}

	body, err := json.Marshal(RPCRequest{
		JSONRPC: "2.0",
		ID:      idRaw,
		Method:  method,
		Params:  paramsJSON,
	})
	if err != nil {
		c.removePending(idStr)
		return err
	}
	if err := c.transport.Send(callCtx, body); err != nil {
		c.removePending(idStr)
		return fmt.Errorf("mcp: send %s: %w", method, err)
	}

	select {
	case resp, ok := <-ch:
		if !ok {
			return ErrClosed
		}
		if resp.Error != nil {
			return resp.Error
		}
		if out == nil {
			return nil
		}
		if len(resp.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(resp.Result, out); err != nil {
			return fmt.Errorf("mcp: decode %s result: %w", method, err)
		}
		return nil
	case <-callCtx.Done():
		// Remove the pending entry and spawn a one-shot drainer so
		// any late response from the server is consumed (and does
		// not leak a goroutine blocked on send).  The channel is
		// buffered (cap 1) so the dispatcher's send completes
		// without blocking even if no one is reading.
		c.removePending(idStr)
		return callCtx.Err()
	}
}

// removePending drops the pending entry for id, if present.
func (c *Client) removePending(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

// readLoop pumps incoming frames off the transport.  Responses with
// IDs are routed to the matching pending channel; server-initiated
// requests are logged and discarded (v0.2 does not act on them).  On
// transport error or EOF the loop fails every outstanding call with
// the underlying error.
func (c *Client) readLoop() {
	defer close(c.readDone)
	for {
		body, err := c.transport.Recv(context.Background())
		if err != nil {
			c.failAll(err)
			return
		}
		c.dispatch(body)
	}
}

// dispatch routes one inbound frame.
func (c *Client) dispatch(body []byte) {
	// Probe the message: a response has a non-empty id and either a
	// result or an error.  A notification has no id; a server-
	// initiated request has an id and a method.
	var probe struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		slog.Warn("mcp: malformed inbound frame", "err", err, "body", truncateForLog(body))
		return
	}

	if probe.Method != "" {
		// Server-initiated request or notification.  v0.2 doesn't
		// service these — log + discard.  notifications/cancelled
		// in particular is harmless here because we never use
		// server-side cancellation.
		slog.Debug("mcp: server-initiated message (ignored)",
			"method", probe.Method, "has_id", len(probe.ID) > 0)
		return
	}

	// Response — look up the pending channel.
	if len(probe.ID) == 0 || string(probe.ID) == "null" {
		slog.Warn("mcp: response with no id", "body", truncateForLog(body))
		return
	}
	idStr := idAsString(probe.ID)

	var resp RPCResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		slog.Warn("mcp: decode response", "err", err, "id", idStr)
		return
	}

	c.mu.Lock()
	ch, ok := c.pending[idStr]
	if ok {
		delete(c.pending, idStr)
	}
	c.mu.Unlock()
	if !ok {
		// Late response or cancelled call: silently discard.
		slog.Debug("mcp: unmatched response (likely cancelled)", "id", idStr)
		return
	}
	// Channel is buffered (cap 1); send completes immediately.
	ch <- resp
}

// failAll closes every pending channel after the read loop terminates,
// so blocked callers see ErrClosed (or, if you cared about the
// distinction, the underlying transport error — we collapse to
// ErrClosed for caller simplicity).
func (c *Client) failAll(transportErr error) {
	c.mu.Lock()
	pending := c.pending
	c.pending = make(map[string]chan RPCResponse)
	c.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	if !errors.Is(transportErr, io.EOF) {
		slog.Debug("mcp: read loop ended with error", "err", transportErr)
	}
}

// idAsString returns a canonical string form of a JSON-RPC id.  Numbers
// have whitespace trimmed; strings are returned as their JSON-encoded
// form (quotes included) so the namespace cannot collide with numbers.
func idAsString(raw json.RawMessage) string {
	s := string(raw)
	// Trim surrounding whitespace; encoders should not emit any but
	// be tolerant.
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// encodeParams marshals params to JSON.  A nil params is encoded as
// the zero RawMessage (omitted on the wire via omitempty).
func encodeParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("mcp: encode params: %w", err)
	}
	return b, nil
}

// truncateForLog keeps log lines short.
func truncateForLog(b []byte) string {
	const limit = 200
	if len(b) <= limit {
		return string(b)
	}
	return string(b[:limit]) + "…"
}
