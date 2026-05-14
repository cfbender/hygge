package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeTransport pairs an io.Pipe for client→server and another for
// server→client.  Tests script the server side by reading frames off
// the client-bound writer and pushing canned responses back.
type fakeTransport struct {
	// clientToServer carries frames written by the Client (the test
	// reads them off serverIn).
	serverInR *io.PipeReader
	clientW   *io.PipeWriter

	// serverToClient carries frames the test writes (the Client reads
	// them off clientRead).
	clientR *io.PipeReader
	serverW *io.PipeWriter

	clientRead *bufio.Reader

	started atomic.Bool
	closed  atomic.Bool

	label string
}

func newFakeTransport() *fakeTransport {
	cr, sw := io.Pipe()
	sr, cw := io.Pipe()
	return &fakeTransport{
		serverInR: sr,
		clientW:   cw,
		clientR:   cr,
		serverW:   sw,
		label:     "fake",
	}
}

func (f *fakeTransport) Start(_ context.Context) error {
	if !f.started.CompareAndSwap(false, true) {
		return errors.New("fake: already started")
	}
	f.clientRead = bufio.NewReader(f.clientR)
	return nil
}

func (f *fakeTransport) Send(_ context.Context, body []byte) error {
	if !f.started.Load() {
		return errors.New("fake: not started")
	}
	if f.closed.Load() {
		return ErrClosed
	}
	_, err := WriteFrame(f.clientW, body)
	return err
}

func (f *fakeTransport) Recv(_ context.Context) ([]byte, error) {
	if !f.started.Load() {
		return nil, errors.New("fake: not started")
	}
	body, err := ReadFrame(f.clientRead)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (f *fakeTransport) Close() error {
	if !f.closed.CompareAndSwap(false, true) {
		return nil
	}
	// Closing both ends fails any pending Reads/Writes with
	// io.ErrClosedPipe -> ReadFrame surfaces ErrMalformedFrame for an
	// in-progress header, or io.EOF at a frame boundary.
	_ = f.clientW.Close()
	_ = f.serverW.Close()
	_ = f.clientR.Close()
	_ = f.serverInR.Close()
	return nil
}

func (f *fakeTransport) ServerLabel() string { return f.label }

// writeServerFrame sends one frame from the server side to the Client.
func (f *fakeTransport) writeServerFrame(t *testing.T, body []byte) {
	t.Helper()
	if _, err := WriteFrame(f.serverW, body); err != nil {
		t.Fatalf("writeServerFrame: %v", err)
	}
}

// scriptedServer drives a fakeTransport on a goroutine.  It exposes a
// channel of inbound RPCRequests so the test can assert and respond.
type scriptedServer struct {
	t        *testing.T
	tr       *fakeTransport
	inbox    chan RPCRequest
	wg       sync.WaitGroup
	br       *bufio.Reader
	stop     chan struct{}
	stopOnce sync.Once
}

func newScriptedServer(t *testing.T, tr *fakeTransport) *scriptedServer {
	t.Helper()
	s := &scriptedServer{
		t:     t,
		tr:    tr,
		inbox: make(chan RPCRequest, 16),
		stop:  make(chan struct{}),
		br:    bufio.NewReader(tr.serverInR),
	}
	s.wg.Add(1)
	go s.loop()
	return s
}

func (s *scriptedServer) loop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			return
		default:
		}
		body, err := ReadFrame(s.br)
		if err != nil {
			return
		}
		var req RPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			s.t.Errorf("server: decode frame: %v (%s)", err, body)
			return
		}
		s.inbox <- req
	}
}

func (s *scriptedServer) Stop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		_ = s.tr.Close()
		s.wg.Wait()
	})
}

func (s *scriptedServer) RespondOK(id json.RawMessage, result any) {
	s.t.Helper()
	resultJSON, err := json.Marshal(result)
	if err != nil {
		s.t.Fatalf("marshal result: %v", err)
	}
	body, err := json.Marshal(RPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  resultJSON,
	})
	if err != nil {
		s.t.Fatalf("marshal response: %v", err)
	}
	s.tr.writeServerFrame(s.t, body)
}

func (s *scriptedServer) RespondErr(id json.RawMessage, code int, msg string) {
	s.t.Helper()
	body, err := json.Marshal(RPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &RPCError{Code: code, Message: msg},
	})
	if err != nil {
		s.t.Fatalf("marshal error response: %v", err)
	}
	s.tr.writeServerFrame(s.t, body)
}

// --- Tests ------------------------------------------------------------

func TestClient_InitializeAndInitialized(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()

	c := New(ClientOptions{
		Transport:      tr,
		Name:           "test",
		ClientVersion:  "0.0.0-test",
		RequestTimeout: 2 * time.Second,
	})
	defer func() { _ = c.Close() }()

	resultCh := make(chan *InitializeResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := c.Initialize(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- r
	}()

	// Expect initialize.
	req := <-srv.inbox
	if req.Method != MethodInitialize {
		t.Fatalf("expected %s, got %s", MethodInitialize, req.Method)
	}
	srv.RespondOK(req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
		ServerInfo:      ServerInfo{Name: "fake-server", Version: "1.0"},
	})

	// Expect initialized notification.
	note := <-srv.inbox
	if note.Method != MethodInitialized {
		t.Fatalf("expected %s, got %s", MethodInitialized, note.Method)
	}
	if len(note.ID) != 0 {
		t.Fatalf("notification should have no id; got %s", note.ID)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Initialize failed: %v", err)
	case got := <-resultCh:
		if got.ServerInfo.Name != "fake-server" {
			t.Fatalf("ServerInfo.Name: got %q want %q", got.ServerInfo.Name, "fake-server")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Initialize never returned")
	}
}

func TestClient_InitializeTimeout(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()

	c := New(ClientOptions{
		Transport:      tr,
		RequestTimeout: 75 * time.Millisecond,
	})
	defer func() { _ = c.Close() }()

	err := make(chan error, 1)
	go func() { _, e := c.Initialize(context.Background()); err <- e }()

	// Drain the request without responding.
	<-srv.inbox

	select {
	case e := <-err:
		if e == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if !strings.Contains(e.Error(), "context deadline exceeded") {
			t.Fatalf("unexpected error: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Initialize did not time out")
	}
}

func TestClient_ListTools(t *testing.T) {
	t.Parallel()
	c, srv, tr := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()
	_ = tr

	errCh := make(chan error, 1)
	toolsCh := make(chan []MCPToolDef, 1)
	go func() {
		tools, err := c.ListTools(context.Background())
		if err != nil {
			errCh <- err
			return
		}
		toolsCh <- tools
	}()

	req := <-srv.inbox
	if req.Method != MethodToolsList {
		t.Fatalf("expected %s got %s", MethodToolsList, req.Method)
	}
	srv.RespondOK(req.ID, ListToolsResult{
		Tools: []MCPToolDef{
			{Name: "create_issue", Description: "Create a GitHub issue", InputSchema: map[string]any{"type": "object"}},
			{Name: "list_repos", InputSchema: map[string]any{"type": "object"}},
		},
	})

	select {
	case e := <-errCh:
		t.Fatalf("ListTools: %v", e)
	case tools := <-toolsCh:
		if len(tools) != 2 || tools[0].Name != "create_issue" {
			t.Fatalf("unexpected tools: %+v", tools)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ListTools never returned")
	}
}

func TestClient_CallTool_TextResult(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()

	resCh := make(chan *CallToolResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := c.CallTool(context.Background(), "create_issue", json.RawMessage(`{"title":"hi"}`))
		if err != nil {
			errCh <- err
			return
		}
		resCh <- r
	}()

	req := <-srv.inbox
	if req.Method != MethodToolsCall {
		t.Fatalf("expected %s got %s", MethodToolsCall, req.Method)
	}
	srv.RespondOK(req.ID, CallToolResult{
		Content: []ContentBlock{{Type: "text", Text: "Created issue #42"}},
	})

	select {
	case e := <-errCh:
		t.Fatalf("CallTool: %v", e)
	case r := <-resCh:
		if r.IsError || len(r.Content) != 1 || r.Content[0].Text != "Created issue #42" {
			t.Fatalf("unexpected result: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool never returned")
	}
}

func TestClient_CallTool_IsErrorTrueIsNotGoError(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()

	resCh := make(chan *CallToolResult, 1)
	go func() {
		r, err := c.CallTool(context.Background(), "x", nil)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}
		resCh <- r
	}()

	req := <-srv.inbox
	srv.RespondOK(req.ID, CallToolResult{
		IsError: true,
		Content: []ContentBlock{{Type: "text", Text: "missing required arg"}},
	})

	select {
	case r := <-resCh:
		if !r.IsError {
			t.Fatalf("expected IsError=true, got %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool never returned")
	}
}

func TestClient_CallTool_RPCErrorIsGoError(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()

	errCh := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "x", nil)
		errCh <- err
	}()

	req := <-srv.inbox
	srv.RespondErr(req.ID, -32602, "Invalid params")

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		var rpcErr *RPCError
		if !errors.As(err, &rpcErr) {
			t.Fatalf("expected *RPCError, got %T: %v", err, err)
		}
		if rpcErr.Code != -32602 {
			t.Fatalf("RPCError.Code = %d, want -32602", rpcErr.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("CallTool never returned")
	}
}

func TestClient_ConcurrentCalls(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()

	const n = 4
	results := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			r, err := c.CallTool(context.Background(), "echo",
				json.RawMessage(`{"i":`+itoa(i)+`}`))
			if err != nil {
				errs <- err
				return
			}
			if len(r.Content) > 0 {
				results <- r.Content[0].Text
			} else {
				results <- ""
			}
		}(i)
	}

	// Server responds to each inbound request with the same i it
	// received (round-trip identity).
	for i := 0; i < n; i++ {
		req := <-srv.inbox
		// Echo the arguments back in the result so we can verify.
		var p CallToolParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			t.Fatalf("decode params: %v", err)
		}
		srv.RespondOK(req.ID, CallToolResult{
			Content: []ContentBlock{{Type: "text", Text: string(p.Arguments)}},
		})
	}

	got := make(map[string]bool)
	for i := 0; i < n; i++ {
		select {
		case r := <-results:
			got[r] = true
		case e := <-errs:
			t.Fatalf("CallTool: %v", e)
		case <-time.After(2 * time.Second):
			t.Fatal("concurrent CallTool stalled")
		}
	}
	if len(got) != n {
		t.Fatalf("expected %d distinct results; got %v", n, got)
	}
}

func TestClient_TransportEOFMidCall(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()

	errCh := make(chan error, 1)
	go func() {
		_, err := c.CallTool(context.Background(), "slow", nil)
		errCh <- err
	}()

	// Drain the request, then yank the transport.
	<-srv.inbox
	srv.Stop()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error after EOF; got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call never woke after EOF")
	}
}

func TestClient_Ping(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()

	errCh := make(chan error, 1)
	go func() { errCh <- c.Ping(context.Background()) }()
	req := <-srv.inbox
	if req.Method != MethodPing {
		t.Fatalf("expected %s got %s", MethodPing, req.Method)
	}
	srv.RespondOK(req.ID, struct{}{})
	select {
	case e := <-errCh:
		if e != nil {
			t.Fatalf("Ping: %v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ping never returned")
	}
}

func TestClient_CloseIsIdempotent(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer srv.Stop()
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// Subsequent operations report ErrClosed.
	if _, err := c.ListTools(context.Background()); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed, got %v", err)
	}
}

func TestClient_CallBeforeInitialize(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	defer func() { _ = tr.Close() }()
	c := New(ClientOptions{Transport: tr})
	if _, err := c.ListTools(context.Background()); !errors.Is(err, ErrNotInitialized) {
		t.Fatalf("expected ErrNotInitialized, got %v", err)
	}
}

func TestClient_CtxCancellationDrainsLateResponse(t *testing.T) {
	t.Parallel()
	c, srv, _ := initOK(t)
	defer func() { _ = c.Close() }()
	defer srv.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := c.CallTool(ctx, "slow", nil)
		errCh <- err
	}()

	// Drain the inbound request.
	req := <-srv.inbox
	// Cancel before responding.
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled call never returned")
	}

	// Now respond late: this should be silently discarded.
	srv.RespondOK(req.ID, CallToolResult{Content: []ContentBlock{{Type: "text", Text: "late"}}})

	// Issue another call and make sure dispatcher still works.
	resCh := make(chan *CallToolResult, 1)
	go func() {
		r, err := c.CallTool(context.Background(), "ok", nil)
		if err != nil {
			t.Errorf("follow-up call: %v", err)
			return
		}
		resCh <- r
	}()
	req2 := <-srv.inbox
	srv.RespondOK(req2.ID, CallToolResult{Content: []ContentBlock{{Type: "text", Text: "ok"}}})
	select {
	case r := <-resCh:
		if len(r.Content) == 0 || r.Content[0].Text != "ok" {
			t.Fatalf("unexpected result: %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up call stalled")
	}
}

// initOK runs Initialize on a fresh fake-transport-backed Client and
// returns the wired-up Client + server + transport.  Convenience for
// tests that need a ready-to-call Client.
func initOK(t *testing.T) (*Client, *scriptedServer, *fakeTransport) {
	t.Helper()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	c := New(ClientOptions{
		Transport:      tr,
		Name:           "test",
		RequestTimeout: 2 * time.Second,
	})

	done := make(chan error, 1)
	go func() { _, err := c.Initialize(context.Background()); done <- err }()
	// initialize
	req := <-srv.inbox
	srv.RespondOK(req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "fake", Version: "1.0"},
		Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
	})
	// initialized notification
	<-srv.inbox
	if err := <-done; err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	return c, srv, tr
}
