package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/tool"
)

func TestMCPTool_NameAndDescriptionAndSchema(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, Name: "github", RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}

	def := MCPToolDef{
		Name:        "create-Issue",
		Description: "Create a GitHub issue in a repo.",
		InputSchema: map[string]any{
			"type":     "object",
			"required": []any{"title"},
		},
	}
	tl := NewMCPTool(c, def, permission.CategoryMCP)
	if got := tl.Name(); got != "github_create-issue" {
		t.Fatalf("Name: got %q, want %q", got, "github_create-issue")
	}
	if !strings.HasPrefix(tl.Description(), "[github]") {
		t.Fatalf("Description should be prefixed with server: %q", tl.Description())
	}
	schema := tl.InputSchema()
	if schema["type"] != "object" {
		t.Fatalf("InputSchema lost type: %+v", schema)
	}
	// Mutating the returned schema must not affect the underlying def.
	schema["mutated"] = true
	if _, ok := def.InputSchema["mutated"]; ok {
		t.Fatal("InputSchema returned the live map; should be a copy")
	}
}

func TestMCPTool_Name_FallsBackWhenServerNameEmpty(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}
	tl := NewMCPTool(c, MCPToolDef{Name: "do"}, permission.CategoryMCP)
	if tl.Name() != "mcp_do" {
		t.Fatalf("got %q, want %q", tl.Name(), "mcp_do")
	}
}

func TestMCPTool_Execute_PermissionDeniedNoCall(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, Name: "github", RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}

	pe, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "deny", Scope: "once"}
	})

	tl := NewMCPTool(c, MCPToolDef{Name: "create_issue"}, permission.CategoryMCP)
	res, err := tl.Execute(context.Background(), json.RawMessage(`{}`),
		tool.ExecContext{SessionID: "s1", Permission: pe, Bus: b})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError on denial")
	}
	// No inbound call should have been issued to the server.
	select {
	case got := <-srv.inbox:
		t.Fatalf("server should not have been called; got %+v", got)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestMCPTool_Execute_TextContentRendered(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, Name: "fs", RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}

	pe, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})

	tl := NewMCPTool(c, MCPToolDef{Name: "read_file"}, permission.CategoryMCP)
	resCh := make(chan tool.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		r, err := tl.Execute(context.Background(), json.RawMessage(`{"path":"/x"}`),
			tool.ExecContext{SessionID: "s1", Permission: pe, Bus: b})
		if err != nil {
			errCh <- err
			return
		}
		resCh <- r
	}()

	req := <-srv.inbox
	if req.Method != MethodToolsCall {
		t.Fatalf("expected tools/call, got %s", req.Method)
	}
	srv.RespondOK(req.ID, CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "line one"},
			{Type: "text", Text: "line two"},
		},
	})
	select {
	case e := <-errCh:
		t.Fatalf("Execute: %v", e)
	case r := <-resCh:
		if r.IsError {
			t.Fatalf("expected IsError=false, got %+v", r)
		}
		if r.Content != "line one\nline two" {
			t.Fatalf("Content: %q", r.Content)
		}
		if got := r.Metadata["mcp_server"]; got != "fs" {
			t.Fatalf("mcp_server metadata: %v", got)
		}
		if got := r.Metadata["content_blocks"]; got != 2 {
			t.Fatalf("content_blocks: %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute stalled")
	}
}

func TestMCPTool_Execute_ImageContentDroppedWithPlaceholder(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, Name: "img", RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}
	pe, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	tl := NewMCPTool(c, MCPToolDef{Name: "snapshot"}, permission.CategoryMCP)

	resCh := make(chan tool.Result, 1)
	go func() {
		r, _ := tl.Execute(context.Background(), nil,
			tool.ExecContext{SessionID: "s1", Permission: pe, Bus: b})
		resCh <- r
	}()
	req := <-srv.inbox
	srv.RespondOK(req.ID, CallToolResult{
		Content: []ContentBlock{
			{Type: "text", Text: "preface"},
			{Type: "image", MimeType: "image/png", Data: "xxx"},
		},
	})
	select {
	case r := <-resCh:
		if !strings.Contains(r.Content, "preface") {
			t.Fatalf("missing text: %q", r.Content)
		}
		if !strings.Contains(r.Content, "[image dropped") {
			t.Fatalf("missing placeholder: %q", r.Content)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute stalled")
	}
}

func TestMCPTool_Execute_RPCErrorIsToolError(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, Name: "x", RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}
	pe, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	tl := NewMCPTool(c, MCPToolDef{Name: "boom"}, permission.CategoryMCP)
	errCh := make(chan error, 1)
	go func() {
		_, err := tl.Execute(context.Background(), nil,
			tool.ExecContext{SessionID: "s1", Permission: pe, Bus: b})
		errCh <- err
	}()
	req := <-srv.inbox
	srv.RespondErr(req.ID, -32602, "bad args")

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected ToolError")
		}
		var te *tool.ToolError
		if !errors.As(err, &te) {
			t.Fatalf("expected *tool.ToolError, got %T: %v", err, err)
		}
		if te.Code != tool.CodeExecutionFailed {
			t.Fatalf("Code: got %q want execution_failed", te.Code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute stalled")
	}
}

func TestMCPTool_Execute_NilEngineIsToolError(t *testing.T) {
	t.Parallel()
	tr := newFakeTransport()
	srv := newScriptedServer(t, tr)
	defer srv.Stop()
	c := New(ClientOptions{Transport: tr, Name: "x", RequestTimeout: time.Second})
	defer func() { _ = c.Close() }()
	if err := primeInit(t, c, srv); err != nil {
		t.Fatalf("init: %v", err)
	}
	tl := NewMCPTool(c, MCPToolDef{Name: "boom"}, permission.CategoryMCP)
	_, err := tl.Execute(context.Background(), nil, tool.ExecContext{})
	if err == nil {
		t.Fatal("expected ToolError")
	}
}

// --- helpers ---------------------------------------------------------

func primeInit(t *testing.T, c *Client, srv *scriptedServer) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { _, err := c.Initialize(context.Background()); done <- err }()
	req := <-srv.inbox
	srv.RespondOK(req.ID, InitializeResult{
		ProtocolVersion: ProtocolVersion,
		ServerInfo:      ServerInfo{Name: "fake", Version: "0"},
		Capabilities:    ServerCapabilities{Tools: &ToolsCapability{}},
	})
	<-srv.inbox // initialized notification
	return <-done
}

// testEngine boots a real permission engine wired to a fresh bus.
func testEngine(t *testing.T, decide func(asked bus.PermissionAsked) bus.PermissionReplied) (*permission.Engine, *bus.Bus) {
	t.Helper()
	b := bus.New()
	t.Cleanup(b.Close)

	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}

	cfg := &config.Config{}
	cfg.Permission.FileReadOutsidePwd = config.PermAsk
	cfg.Permission.FileWrite = config.PermAsk
	cfg.Permission.Shell = config.PermAsk
	cfg.Permission.Network = config.PermDeny
	cfg.Permission.MCP = config.PermAsk

	e, err := permission.New(permission.EngineOptions{
		Bus:    b,
		Config: cfg,
		State:  stateOpts,
		Clock:  func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("permission.New: %v", err)
	}
	t.Cleanup(e.Close)

	sub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 256})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for asked := range sub.C() {
			reply := decide(asked)
			reply.RequestID = asked.RequestID
			if reply.At.IsZero() {
				reply.At = time.Unix(1700000001, 0)
			}
			bus.Publish(b, reply)
		}
	}()
	t.Cleanup(func() {
		sub.Unsubscribe()
		<-done
	})
	return e, b
}
