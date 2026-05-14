package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/tool"
)

// ---------- shared scripted fake provider -----------------------------------
//
// Mirrors the fakeProvider in internal/agent's tests but lives here so
// the subagent package can be tested in isolation.  Each call to
// Stream pops one script from the front of the queue.

type fakeProvider struct {
	mu      sync.Mutex
	scripts []fakeScript
	calls   atomic.Int32
}

type fakeScript struct {
	events []provider.Event
}

func newFakeProvider(scripts ...fakeScript) *fakeProvider {
	return &fakeProvider{scripts: scripts}
}

func (f *fakeProvider) Name() string { return "fake" }

func (f *fakeProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	f.calls.Add(1)
	f.mu.Lock()
	if len(f.scripts) == 0 {
		f.mu.Unlock()
		return nil, fmt.Errorf("fakeProvider: out of scripts")
	}
	s := f.scripts[0]
	f.scripts = f.scripts[1:]
	f.mu.Unlock()

	ch := make(chan provider.Event, len(s.events)+1)
	go func() {
		defer close(ch)
		for _, ev := range s.events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func (f *fakeProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}

func (f *fakeProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}

func scriptText(text string) fakeScript {
	return fakeScript{events: []provider.Event{
		{Type: provider.EventTextDelta, Text: text},
		{Type: provider.EventDone},
	}}
}

func scriptToolUse(t *testing.T, id, name string, input map[string]any) fakeScript {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal tool input: %v", err)
	}
	return fakeScript{events: []provider.Event{
		{
			Type:      provider.EventToolUse,
			ToolID:    id,
			ToolName:  name,
			ToolInput: raw,
		},
		{Type: provider.EventDone},
	}}
}

// pricelessCatalog mirrors agent's helper: an httptest server that
// returns 500 so every catalog lookup misses, ensuring tests never
// hit the real network.
func pricelessCatalog(t *testing.T) *cost.Catalog {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return cost.NewCatalog(cost.CatalogOptions{
		BaseURL:   srv.URL,
		CachePath: filepath.Join(t.TempDir(), "catalog.json"),
	})
}

// autoAllow replies to every PermissionAsked event with allow.  Used
// so the sub-agent's tool calls (and the task tool's own ask) flow
// through without blocking.
func autoAllow(t *testing.T, b *bus.Bus) {
	t.Helper()
	sub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 64})
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				bus.Publish(b, bus.PermissionReplied{
					RequestID: ev.RequestID,
					Decision:  string(permission.ActionAllow),
					Scope:     string(permission.ScopeOnce),
					At:        time.Now(),
				})
			}
		}
	}()
	t.Cleanup(func() {
		close(done)
		sub.Unsubscribe()
	})
}

// runnerEnv wires every dependency the Runner needs.
type runnerEnv struct {
	t            *testing.T
	bus          *bus.Bus
	store        *store.Store
	perm         *permission.Engine
	catalog      *cost.Catalog
	parentTools  *tool.Registry
	parentSessID string
	pwd          string
}

func newRunnerEnv(t *testing.T) *runnerEnv {
	t.Helper()
	ctx := context.Background()

	b := bus.New()
	t.Cleanup(b.Close)

	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tmpState := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpState)
	t.Setenv("HOME", tmpState)

	pe, err := permission.New(permission.EngineOptions{
		Bus:   b,
		State: state.LoadOptions{},
	})
	if err != nil {
		t.Fatalf("permission.New: %v", err)
	}
	t.Cleanup(pe.Close)

	autoAllow(t, b)

	pwd := t.TempDir()
	parent, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: pwd,
		Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	return &runnerEnv{
		t:            t,
		bus:          b,
		store:        st,
		perm:         pe,
		catalog:      pricelessCatalog(t),
		parentTools:  tool.Default(),
		parentSessID: parent.ID,
		pwd:          pwd,
	}
}

func (e *runnerEnv) newRunner(reg *Registry, prov provider.Provider) *Runner {
	e.t.Helper()
	if reg == nil {
		var err error
		reg, err = Load(LoadOptions{
			HomeDir:      e.t.TempDir(),
			DefaultTools: []string{"read", "grep", "glob", "bash"},
		})
		if err != nil {
			e.t.Fatalf("Load: %v", err)
		}
	}
	r, err := NewRunner(RunnerOptions{
		Bus:           e.bus,
		Store:         e.store,
		Provider:      prov,
		Permission:    e.perm,
		Catalog:       e.catalog,
		Registry:      reg,
		ParentTools:   e.parentTools,
		Pwd:           e.pwd,
		ContextWindow: 0,
		Now:           time.Now,
	})
	if err != nil {
		e.t.Fatalf("NewRunner: %v", err)
	}
	return r
}

// ---------- happy path tests -----------------------------------------------

func TestRun_FinalText(t *testing.T) {
	env := newRunnerEnv(t)
	prov := newFakeProvider(scriptText("done: hello"))
	r := env.newRunner(nil, prov)

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general",
		Description:     "say hello",
		Prompt:          "Please greet me.",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "done: hello" {
		t.Fatalf("FinalText: got %q want %q", res.FinalText, "done: hello")
	}
	if res.SessionID == "" {
		t.Fatal("SessionID should be set")
	}
	if res.HitIterLimit {
		t.Fatal("should not have hit iteration limit")
	}
}

func TestRun_PersistsSubSessionWithParentLinkage(t *testing.T) {
	env := newRunnerEnv(t)
	prov := newFakeProvider(scriptText("ok"))
	r := env.newRunner(nil, prov)

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general",
		Description:     "smoke",
		Prompt:          "smoke",
		ModelName:       "fake-model",
		ParentToolUseID: "tool_use_abc",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	sub, err := env.store.GetSession(context.Background(), res.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sub.ParentID != env.parentSessID {
		t.Fatalf("ParentID: got %q want %q", sub.ParentID, env.parentSessID)
	}
	if sub.Kind != session.KindSubagent {
		t.Fatalf("Kind: got %q want %q", sub.Kind, session.KindSubagent)
	}
	// Subagent sessions don't require fork_message_id (the audit
	// linkage is via parent_id + kind).
	if sub.ForkMessageID != "" {
		t.Fatalf("ForkMessageID should be empty for subagent, got %q", sub.ForkMessageID)
	}
}

// ---------- tool-call path -------------------------------------------------

func TestRun_ToolCallLoop(t *testing.T) {
	env := newRunnerEnv(t)
	// Turn 1: ask for the "read" tool.  Turn 2: final text.  The
	// read tool will fail (no such file) and surface IsError back
	// to the model -- but the loop continues to turn 2.
	prov := newFakeProvider(
		scriptToolUse(t, "tu1", "read", map[string]any{
			"file_path": filepath.Join(env.pwd, "nope.txt"),
		}),
		scriptText("read failed, here is my summary"),
	)
	r := env.newRunner(nil, prov)

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general",
		Description:     "tool test",
		Prompt:          "go",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "read failed, here is my summary" {
		t.Fatalf("FinalText: %q", res.FinalText)
	}
}

// ---------- recursion guard -------------------------------------------------

func TestRun_TaskToolNeverInSubagentRegistry(t *testing.T) {
	env := newRunnerEnv(t)

	// Pre-register a stub task tool in the parent's registry so we
	// can prove the runtime strips it.  Use a dummy that records if
	// it's ever called -- it MUST NOT BE.
	called := atomic.Bool{}
	stub := &recordingTool{name: "task", called: &called}
	if err := env.parentTools.Register(stub); err != nil {
		t.Fatalf("register stub task: %v", err)
	}

	// TOML asks for `task` explicitly -- the runtime should strip it.
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.evil]
description = "wants task"
prompt = "go"
tools = ["task", "read", "grep"]
`)
	reg, err := Load(LoadOptions{
		HomeDir:      home,
		DefaultTools: []string{"read", "grep", "glob", "bash", "task"},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Also use the "general" type (which inherits defaults including
	// "task" we just added) -- the runtime must still strip it.

	subReg := r2SubTools(env, reg)
	_ = subReg

	// Build the per-call tool registry for both types and confirm
	// neither contains `task`.
	prov := newFakeProvider(scriptText("ok"))
	run := env.newRunner(reg, prov)

	for _, typeName := range []string{"general", "evil"} {
		ty, ok := reg.Get(typeName)
		if !ok {
			t.Fatalf("missing type %q", typeName)
		}
		built := run.buildToolRegistry(ty)
		if _, ok := built.Get("task"); ok {
			t.Fatalf("type %q: task tool leaked into sub-agent registry", typeName)
		}
	}
	if called.Load() {
		t.Fatal("recording tool was invoked, but it should never have been")
	}
}

// r2SubTools is a tiny accessor so we exercise Registry.DefaultTools
// through the same path the runtime uses.
func r2SubTools(_ *runnerEnv, reg *Registry) []string {
	return reg.DefaultTools()
}

// recordingTool is a minimal tool.Tool implementation used to assert
// that the registry never invokes `task` inside a sub-agent run.
type recordingTool struct {
	name   string
	called *atomic.Bool
}

func (r *recordingTool) Name() string        { return r.name }
func (r *recordingTool) Description() string { return "stub" }
func (r *recordingTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (r *recordingTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	r.called.Store(true)
	return tool.Result{Content: "should not have been called"}, nil
}

// ---------- iteration-limit path -------------------------------------------

func TestRun_HitIterationLimit(t *testing.T) {
	env := newRunnerEnv(t)
	// Always emit a tool-use so the loop never terminates.  Use the
	// read tool against a non-existent file; the IsError result
	// is fed back to the model and we loop again.
	scripts := make([]fakeScript, 6)
	for i := range scripts {
		scripts[i] = scriptToolUse(t, fmt.Sprintf("tu%d", i), "read", map[string]any{
			"file_path": filepath.Join(env.pwd, "nope.txt"),
		})
	}
	prov := newFakeProvider(scripts...)

	reg, err := Load(LoadOptions{
		HomeDir:      t.TempDir(),
		DefaultTools: []string{"read"},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	r, err := NewRunner(RunnerOptions{
		Bus:           env.bus,
		Store:         env.store,
		Provider:      prov,
		Permission:    env.perm,
		Catalog:       env.catalog,
		Registry:      reg,
		ParentTools:   env.parentTools,
		Pwd:           env.pwd,
		MaxIterations: 3, // tight cap so we don't burn forever
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general",
		Description:     "loop",
		Prompt:          "loop forever",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v (expected nil even on iteration limit)", err)
	}
	if !res.HitIterLimit {
		t.Fatal("expected HitIterLimit=true")
	}
	if res.FinalText == "" {
		t.Fatal("FinalText should contain the abort note")
	}
}

// ---------- argument / nil-input validation -------------------------------

func TestNewRunner_RequiresAllOptions(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*RunnerOptions)
		want string
	}{
		{"no bus", func(o *RunnerOptions) { o.Bus = nil }, "Bus"},
		{"no store", func(o *RunnerOptions) { o.Store = nil }, "Store"},
		{"no provider", func(o *RunnerOptions) { o.Provider = nil }, "Provider"},
		{"no permission", func(o *RunnerOptions) { o.Permission = nil }, "Permission"},
		{"no catalog", func(o *RunnerOptions) { o.Catalog = nil }, "Catalog"},
		{"no registry", func(o *RunnerOptions) { o.Registry = nil }, "Registry"},
		{"no parent tools", func(o *RunnerOptions) { o.ParentTools = nil }, "ParentTools"},
	}
	env := newRunnerEnv(t)
	prov := newFakeProvider()
	reg, _ := Load(LoadOptions{HomeDir: t.TempDir()})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := RunnerOptions{
				Bus: env.bus, Store: env.store, Provider: prov,
				Permission: env.perm, Catalog: env.catalog,
				Registry: reg, ParentTools: env.parentTools,
			}
			tt.mut(&opts)
			_, err := NewRunner(opts)
			if err == nil {
				t.Fatalf("expected error mentioning %s", tt.want)
			}
		})
	}
}

func TestRun_RejectsEmptyInputs(t *testing.T) {
	env := newRunnerEnv(t)
	r := env.newRunner(nil, newFakeProvider())

	tests := []struct {
		name string
		in   RunInput
	}{
		{"no parent", RunInput{Type: "general", Prompt: "x", ModelName: "fake-model"}},
		{"no type", RunInput{ParentSessionID: env.parentSessID, Prompt: "x", ModelName: "fake-model"}},
		{"no prompt", RunInput{ParentSessionID: env.parentSessID, Type: "general", ModelName: "fake-model"}},
		{"no model", RunInput{ParentSessionID: env.parentSessID, Type: "general", Prompt: "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.Run(context.Background(), tt.in); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestRun_UnknownType(t *testing.T) {
	env := newRunnerEnv(t)
	r := env.newRunner(nil, newFakeProvider())
	_, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "does_not_exist",
		Prompt:          "x",
		ModelName:       "fake-model",
	})
	if err == nil {
		t.Fatal("expected error for unknown type")
	}
}

// ---------- bus events ------------------------------------------------------

func TestRun_PublishesSubagentStartedAndCompleted(t *testing.T) {
	env := newRunnerEnv(t)

	startedSub := bus.Subscribe[bus.SubagentStarted](env.bus, bus.SubscribeOptions{BufferSize: 4})
	defer startedSub.Unsubscribe()
	completedSub := bus.Subscribe[bus.SubagentCompleted](env.bus, bus.SubscribeOptions{BufferSize: 4})
	defer completedSub.Unsubscribe()

	prov := newFakeProvider(scriptText("done"))
	r := env.newRunner(nil, prov)

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general",
		Description:     "test",
		Prompt:          "hi",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	select {
	case ev := <-startedSub.C():
		if ev.SubSessionID != res.SessionID {
			t.Fatalf("Started.SubSessionID: got %q want %q", ev.SubSessionID, res.SessionID)
		}
		if ev.ParentSessionID != env.parentSessID {
			t.Fatalf("Started.ParentSessionID: got %q", ev.ParentSessionID)
		}
		if ev.Type != "general" {
			t.Fatalf("Started.Type: got %q", ev.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SubagentStarted")
	}

	select {
	case ev := <-completedSub.C():
		if ev.SubSessionID != res.SessionID {
			t.Fatalf("Completed.SubSessionID mismatch")
		}
		if ev.HitIterLimit {
			t.Fatal("Completed.HitIterLimit should be false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SubagentCompleted")
	}
}

// ---------- provider initial failure surfaces error ------------------------

func TestRun_ProviderErrorSurfaces(t *testing.T) {
	env := newRunnerEnv(t)
	prov := &erroringProvider{err: errors.New("boom")}
	r := env.newRunner(nil, prov)
	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general",
		Description:     "x",
		Prompt:          "x",
		ModelName:       "fake-model",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	// Sub-session should still exist for audit (we created it before
	// starting the loop).
	if res.SessionID == "" {
		t.Fatal("SessionID should be set even on error")
	}
	if _, err := env.store.GetSession(context.Background(), res.SessionID); err != nil {
		t.Fatalf("sub-session not persisted after error: %v", err)
	}
}

// erroringProvider always fails Stream so we can test the error path.
type erroringProvider struct {
	err error
}

func (e *erroringProvider) Name() string { return "erroring" }
func (e *erroringProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	return nil, e.err
}
func (e *erroringProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (e *erroringProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}
