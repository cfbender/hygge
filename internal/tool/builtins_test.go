package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/state"
)

// --- shared test helpers for the built-in tools -----------------------------
//
// Each built-in's test file uses these helpers; they live here in
// builtins_test.go so the framework-only tool_test.go does not have to
// import the full builtin test surface.

// builtinTestEngine boots a real permission engine wired to a fresh bus.
// decide is called for every PermissionAsked event; tests use it to
// allow, deny, or inspect the request.  Returns the engine and bus; the
// responder goroutine is unsubscribed at test cleanup.
func builtinTestEngine(t *testing.T, decide func(asked bus.PermissionAsked) bus.PermissionReplied) (*permission.Engine, *bus.Bus) {
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

// allowAll is the decision function for tests that should always succeed.
func allowAll(_ bus.PermissionAsked) bus.PermissionReplied {
	return bus.PermissionReplied{Decision: "allow", Scope: "once"}
}

// denyAll is the decision function for tests that exercise the deny path.
func denyAll(_ bus.PermissionAsked) bus.PermissionReplied {
	return bus.PermissionReplied{Decision: "deny", Scope: "once"}
}

// recordingResponder captures every PermissionAsked event it sees and
// always responds with the supplied decision.  Tests use it to assert the
// Category/Target/ToolName the tool sent.
type recordingResponder struct {
	mu       sync.Mutex
	requests []bus.PermissionAsked
	decision bus.PermissionReplied
}

func newRecordingResponder(decision bus.PermissionReplied) *recordingResponder {
	return &recordingResponder{decision: decision}
}

func (r *recordingResponder) decide(asked bus.PermissionAsked) bus.PermissionReplied {
	r.mu.Lock()
	r.requests = append(r.requests, asked)
	r.mu.Unlock()
	return r.decision
}

func (r *recordingResponder) snapshot() []bus.PermissionAsked {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bus.PermissionAsked, len(r.requests))
	copy(out, r.requests)
	return out
}

// newExecContext is a convenience for builtin tests.
func newExecContext(b *bus.Bus, e *permission.Engine, pwd string) ExecContext {
	return ExecContext{
		SessionID:  "sess-test",
		Pwd:        pwd,
		Bus:        b,
		Permission: e,
		ToolUseID:  "tu-1",
		MessageID:  "msg-1",
		Now:        func() time.Time { return time.Unix(1700000010, 0) },
	}
}

// --- Default() registry tests -----------------------------------------------

func TestDefault_RegistersBuiltInTools(t *testing.T) {
	r := Default()
	want := []string{"bash", "edit", "glob", "grep", "read", "todo", "write"}
	got := make([]string, 0, len(want))
	for _, t := range r.All() {
		got = append(got, t.Name())
	}
	if !equalSorted(got, want) {
		t.Errorf("registered tools: got %v, want %v", got, want)
	}
}

func TestDefault_AsProviderToolsSorted(t *testing.T) {
	r := Default()
	pts := r.AsProviderTools()
	if len(pts) != 7 {
		t.Fatalf("AsProviderTools: got %d entries, want 7", len(pts))
	}
	names := make([]string, len(pts))
	for i, p := range pts {
		names[i] = p.Name
		if p.Description == "" {
			t.Errorf("%s: empty description", p.Name)
		}
		if p.InputSchema == nil {
			t.Errorf("%s: nil InputSchema", p.Name)
		} else if p.InputSchema["type"] != "object" {
			t.Errorf("%s: schema type is not object", p.Name)
		}
	}
	if !sort.StringsAreSorted(names) {
		t.Errorf("AsProviderTools: names not sorted: %v", names)
	}
}

func TestDefault_DuplicateRegisterFails(t *testing.T) {
	r := Default()
	if err := r.Register(newBashTool()); err == nil {
		t.Fatal("expected duplicate-name error when re-registering bash")
	}
}

// TestExecute_PermissionDenyIsResultNotError pins the most important contract
// in this package: a denied permission decision is a Result with IsError=true,
// NOT a returned *ToolError.  The read tool is the simplest carrier of that
// contract; if this test ever flips the IsError-vs-ToolError invariant has
// regressed and the provider layer will misclassify denies as crashes.
func TestExecute_PermissionDenyIsResultNotError(t *testing.T) {
	e, b := builtinTestEngine(t, denyAll)
	dir := t.TempDir()
	outside := t.TempDir() // distinct from pwd so file_read_outside_pwd applies
	ec := newExecContext(b, e, dir)

	tt := newReadTool(newReadTracker())
	args := json.RawMessage(`{"path":"` + outside + `/missing.txt"}`)
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError result for deny, got %+v", res)
	}
	if got, want := res.Metadata["permission"], "denied"; got != want {
		t.Errorf("metadata.permission: got %v want %v", got, want)
	}
}

// Ensure errors.As still threads through ToolError so callers can check codes.
func TestBuiltins_InvalidArgsToolErrorPath(t *testing.T) {
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, t.TempDir())
	tt := newReadTool(newReadTracker())
	_, err := tt.Execute(context.Background(), json.RawMessage(`{`), ec)
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %T", err)
	}
	if te.Code != CodeInvalidArgs {
		t.Errorf("Code: got %q", te.Code)
	}
}

func equalSorted(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string{}, a...)
	bb := append([]string{}, b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for i := range aa {
		if aa[i] != bb[i] {
			return false
		}
	}
	return true
}
