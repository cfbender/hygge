package tool

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/state"
)

// stubTool is a minimal Tool used to exercise the registry and the
// ExecContext plumbing without depending on any built-in tool.  Tests
// configure the Execute closure to assert what the framework hands the
// tool.
type stubTool struct {
	name    string
	desc    string
	schema  map[string]any
	execute func(ctx context.Context, args json.RawMessage, ec ExecContext) (Result, error)
}

func (s *stubTool) Name() string                { return s.name }
func (s *stubTool) Description() string         { return s.desc }
func (s *stubTool) InputSchema() map[string]any { return s.schema }
func (s *stubTool) Parallelizable() bool        { return false }
func (s *stubTool) Execute(ctx context.Context, args json.RawMessage, ec ExecContext) (Result, error) {
	if s.execute == nil {
		return Result{}, nil
	}
	return s.execute(ctx, args, ec)
}

func newStub(name string) *stubTool {
	return &stubTool{
		name:   name,
		desc:   "stub tool " + name,
		schema: map[string]any{"type": "object", "additionalProperties": false},
	}
}

// --- ToolError / IsError contract -------------------------------------------

func TestToolError_ErrorMessages(t *testing.T) {
	plain := newInvalidArgs("missing path", nil)
	if plain.Error() == "" {
		t.Error("empty error message")
	}
	wrapped := newExecutionFailed("open", errors.New("boom"))
	if !errors.Is(wrapped, wrapped.Wrapped) {
		t.Error("Unwrap broken")
	}
	if (*ToolError)(nil).Error() != "<nil>" {
		t.Error("nil receiver should produce <nil>")
	}
}

func TestToolError_CodeConstants(t *testing.T) {
	// Sanity check that the documented codes are distinct strings.
	codes := []string{CodeInvalidArgs, CodePermissionDenied, CodeExecutionFailed}
	seen := map[string]bool{}
	for _, c := range codes {
		if c == "" {
			t.Error("empty code constant")
		}
		if seen[c] {
			t.Errorf("duplicate code %q", c)
		}
		seen[c] = true
	}
}

// --- Registry tests ---------------------------------------------------------

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	st := newStub("alpha")
	if err := r.Register(st); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("alpha")
	if !ok || got.Name() != "alpha" {
		t.Errorf("Get(alpha): got %v ok=%v", got, ok)
	}
	if _, ok := r.Get("missing"); ok {
		t.Errorf("Get(missing): expected ok=false")
	}
}

func TestRegistry_DuplicateRegisterFails(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(newStub("dup")); err != nil {
		t.Fatalf("first Register: %v", err)
	}
	if err := r.Register(newStub("dup")); err == nil {
		t.Fatal("second Register: expected duplicate error")
	}
}

func TestRegistry_NilToolFails(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(nil); err == nil {
		t.Fatal("Register(nil): expected error")
	}
}

func TestRegistry_EmptyNameFails(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(newStub("")); err == nil {
		t.Fatal("Register(empty): expected error")
	}
}

func TestRegistry_AllSorted(t *testing.T) {
	r := NewRegistry()
	for _, n := range []string{"charlie", "alpha", "bravo"} {
		if err := r.Register(newStub(n)); err != nil {
			t.Fatalf("Register %s: %v", n, err)
		}
	}
	got := r.All()
	if len(got) != 3 {
		t.Fatalf("All: got %d, want 3", len(got))
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, tt := range got {
		if tt.Name() != want[i] {
			t.Errorf("All[%d]: got %q, want %q", i, tt.Name(), want[i])
		}
	}
}

func TestRegistry_AsProviderTools(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(newStub("bravo"))
	_ = r.Register(newStub("alpha"))

	pts := r.AsProviderTools()
	if len(pts) != 2 {
		t.Fatalf("AsProviderTools: got %d, want 2", len(pts))
	}
	if pts[0].Name != "alpha" || pts[1].Name != "bravo" {
		t.Errorf("names not sorted: %v %v", pts[0].Name, pts[1].Name)
	}
	for _, p := range pts {
		if p.Description == "" {
			t.Errorf("%s: empty description", p.Name)
		}
		if p.InputSchema == nil {
			t.Errorf("%s: nil InputSchema", p.Name)
		}
	}
}

func TestRegistry_ConcurrentReads(t *testing.T) {
	t.Helper()
	r := NewRegistry()
	for i := 0; i < 4; i++ {
		_ = r.Register(newStub("t" + string(rune('a'+i))))
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.All()
			_, _ = r.Get("ta")
			_ = r.AsProviderTools()
		}()
	}
	wg.Wait()
}

func TestRegistry_ReadTracker(t *testing.T) {
	r := NewRegistry()
	rt := r.ReadTracker()
	if rt == nil {
		t.Fatal("ReadTracker returned nil")
	}
	rt.markRead("s1", "/tmp/foo")
	if !rt.hasRead("s1", "/tmp/foo") {
		t.Errorf("markRead/hasRead: round-trip failed")
	}
	if rt.hasRead("s2", "/tmp/foo") {
		t.Errorf("hasRead: different session should not see mark")
	}
	rt.forget("s1")
	if rt.hasRead("s1", "/tmp/foo") {
		t.Errorf("forget: state not cleared")
	}
}

// --- ExecContext / helpers tests --------------------------------------------

func TestExecContext_NowDefaultsToTimeNow(t *testing.T) {
	ec := ExecContext{}
	now := ec.nowFn()()
	if time.Since(now) > time.Second {
		t.Errorf("default Now too far from time.Now: %v", now)
	}
	fixed := time.Unix(42, 0)
	ec.Now = func() time.Time { return fixed }
	if got := ec.nowFn()(); !got.Equal(fixed) {
		t.Errorf("custom Now not honoured: got %v", got)
	}
}

func TestDecodeArgs_Strict(t *testing.T) {
	type sample struct {
		Foo string `json:"foo"`
	}
	var s sample
	if err := decodeArgs([]byte(`{"foo":"x"}`), &s); err != nil {
		t.Fatalf("valid args: %v", err)
	}
	if s.Foo != "x" {
		t.Errorf("Foo: %q", s.Foo)
	}
	// Unknown fields must error.
	err := decodeArgs([]byte(`{"foo":"x","bar":1}`), &sample{})
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeInvalidArgs {
		t.Errorf("unknown field: got %v", err)
	}
	// Empty input → "{}" — no error.
	if err := decodeArgs(nil, &sample{}); err != nil {
		t.Errorf("empty input: %v", err)
	}
}

func TestRawHasKey(t *testing.T) {
	cases := []struct {
		raw  string
		key  string
		want bool
	}{
		{`{"a":1}`, "a", true},
		{`{"a":1}`, "b", false},
		{`{}`, "a", false},
		{``, "a", false},
		{`not json`, "a", false},
	}
	for _, c := range cases {
		if got := rawHasKey([]byte(c.raw), c.key); got != c.want {
			t.Errorf("rawHasKey(%q, %q) = %v, want %v", c.raw, c.key, got, c.want)
		}
	}
}

func TestResolvePath(t *testing.T) {
	ec := ExecContext{Pwd: "/repo"}
	abs, err := resolvePath(ec, "foo/bar")
	if err != nil {
		t.Fatalf("relative: %v", err)
	}
	if abs != "/repo/foo/bar" {
		t.Errorf("relative: got %q", abs)
	}
	abs, err = resolvePath(ec, "/tmp/x")
	if err != nil {
		t.Fatalf("absolute: %v", err)
	}
	if abs != "/tmp/x" {
		t.Errorf("absolute: got %q", abs)
	}
	if _, err := resolvePath(ec, ""); err == nil {
		t.Error("empty path should error")
	}
}

// --- askPermission contract -------------------------------------------------

// testEngine boots a real permission engine wired to a fresh bus.  decide is
// called for every PermissionAsked event; tests use it to allow, deny, or
// inspect the request.
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
	cfg.Permission.Subagent = config.PermAsk

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

func TestAskPermission_Allow(t *testing.T) {
	e, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	ec := ExecContext{SessionID: "s1", Pwd: "/repo", Bus: b, Permission: e}
	_, deny, perr := askPermission(context.Background(), ec, permission.Request{
		Category: permission.CategoryShell,
		Target:   "ls",
	})
	if perr != nil {
		t.Fatalf("perr: %v", perr)
	}
	if deny != nil {
		t.Errorf("expected nil deny result on allow, got %+v", deny)
	}
}

func TestAskPermission_Deny(t *testing.T) {
	e, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "deny", Scope: "once"}
	})
	ec := ExecContext{SessionID: "s1", Pwd: "/repo", Bus: b, Permission: e}
	_, deny, perr := askPermission(context.Background(), ec, permission.Request{
		Category: permission.CategoryShell,
		Target:   "rm -rf /",
	})
	if perr != nil {
		t.Fatalf("perr: %v", perr)
	}
	if deny == nil {
		t.Fatal("expected deny Result")
	}
	if !deny.IsError {
		t.Error("deny Result should have IsError=true")
	}
}

func TestAskPermission_NilEngineIsToolError(t *testing.T) {
	ec := ExecContext{SessionID: "s1"}
	_, _, perr := askPermission(context.Background(), ec, permission.Request{
		Category: permission.CategoryShell,
		Target:   "ls",
	})
	if perr == nil {
		t.Fatal("expected ToolError for nil engine")
	}
	var te *ToolError
	if !errors.As(perr, &te) || te.Code != CodeExecutionFailed {
		t.Errorf("got %v, want execution_failed", perr)
	}
}
