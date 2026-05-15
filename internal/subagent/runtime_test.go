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

func TestRun_SubagentToolNeverInSubagentRegistry(t *testing.T) {
	env := newRunnerEnv(t)

	// Pre-register a stub subagent tool in the parent's registry so we
	// can prove the runtime strips it.  Use a dummy that records if
	// it's ever called -- it MUST NOT BE.
	called := atomic.Bool{}
	stub := &recordingTool{name: "subagent", called: &called}
	if err := env.parentTools.Register(stub); err != nil {
		t.Fatalf("register stub subagent: %v", err)
	}

	// TOML asks for `subagent` explicitly -- the runtime should strip it.
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.evil]
description = "wants subagent"
prompt = "go"
tools = ["subagent", "read", "grep"]
`)
	reg, err := Load(LoadOptions{
		HomeDir:      home,
		DefaultTools: []string{"read", "grep", "glob", "bash", "subagent"},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	subReg := r2SubTools(env, reg)
	_ = subReg

	// Build the per-call tool registry and confirm it doesn't contain
	// `subagent` (the runtime's recursion guard must strip it).
	prov := newFakeProvider(scriptText("ok"))
	run := env.newRunner(reg, prov)

	ty, ok := reg.Get("evil")
	if !ok {
		t.Fatal("missing type \"evil\"")
	}
	built := run.buildToolRegistry(ty)
	if _, ok := built.Get("subagent"); ok {
		t.Fatal("type \"evil\": subagent tool leaked into sub-agent registry")
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
func (r *recordingTool) Parallelizable() bool { return false }
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
		ParentToolUseID: "tool_use_z",
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
		if ev.ParentMessageID != "tool_use_z" {
			t.Fatalf("Started.ParentMessageID: got %q want %q", ev.ParentMessageID, "tool_use_z")
		}
		if ev.Type != "general" {
			t.Fatalf("Started.Type: got %q", ev.Type)
		}
		// Stage C: Model is populated as `<provider>/<model-id>`.
		// The fake provider is named "fake" and ModelName is
		// "fake-model".
		if got, want := ev.Model, "fake/fake-model"; got != want {
			t.Fatalf("Started.Model: got %q want %q", got, want)
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

// ---------- Stage B: per-type model overrides ------------------------------

// namedFakeProvider lets a test build an alternate fakeProvider that
// reports a chosen Name(), so we can assert which provider the runner
// actually used.
func namedFakeProvider(name string, scripts ...fakeScript) *namedFake {
	return &namedFake{name: name, fake: newFakeProvider(scripts...)}
}

type namedFake struct {
	name string
	fake *fakeProvider
}

func (n *namedFake) Name() string { return n.name }
func (n *namedFake) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	return n.fake.Stream(ctx, req)
}
func (n *namedFake) CountTokens(ctx context.Context, req provider.Request) (int64, error) {
	return n.fake.CountTokens(ctx, req)
}
func (n *namedFake) ListModels(ctx context.Context) ([]provider.Model, error) {
	return n.fake.ListModels(ctx)
}

// loadRegistryWithModel writes a subagents.toml that pins a model
// override on a single named type and returns the resolved registry.
func loadRegistryWithModel(t *testing.T, typeName, modelRef string) *Registry {
	t.Helper()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), fmt.Sprintf(`
[subagents.%s]
description = "pinned"
prompt = "go"
model = %q
`, typeName, modelRef))
	reg, err := Load(LoadOptions{
		HomeDir:      home,
		DefaultTools: []string{"read", "grep", "glob", "bash"},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return reg
}

func TestRun_ModelOverride_RoutesToResolverProvider(t *testing.T) {
	env := newRunnerEnv(t)

	// The resolver returns a *different* provider than the runner's
	// parent provider.  The sub-session's persisted model id and
	// provider name must reflect the override.
	parentProv := newFakeProvider() // never used; resolver intercepts
	altProv := namedFakeProvider("alt", scriptText("override worked"))

	var resolverCalls int
	var gotRef string
	resolver := func(_ context.Context, ref string) (provider.Provider, string, error) {
		resolverCalls++
		gotRef = ref
		_, id, err := ParseModelRef(ref)
		if err != nil {
			return nil, "", err
		}
		return altProv, id, nil
	}

	reg := loadRegistryWithModel(t, "fancy", "alt/cool-model")

	r, err := NewRunner(RunnerOptions{
		Bus:              env.bus,
		Store:            env.store,
		Provider:         parentProv,
		Permission:       env.perm,
		Catalog:          env.catalog,
		Registry:         reg,
		ParentTools:      env.parentTools,
		Pwd:              env.pwd,
		ProviderResolver: resolver,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "fancy",
		Description:     "override",
		Prompt:          "go",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resolverCalls != 1 {
		t.Errorf("resolver calls: got %d want 1", resolverCalls)
	}
	if gotRef != "alt/cool-model" {
		t.Errorf("resolver got %q want %q", gotRef, "alt/cool-model")
	}
	if res.FinalText != "override worked" {
		t.Errorf("FinalText: %q (alt provider should have produced it)", res.FinalText)
	}
	if parentProv.calls.Load() != 0 {
		t.Errorf("parent provider should not have been called, got %d calls", parentProv.calls.Load())
	}

	// Persisted sub-session must use the resolved provider name and
	// the bare model id (no provider prefix).
	sub, err := env.store.GetSession(context.Background(), res.SessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sub.Model.Provider != "alt" {
		t.Errorf("sub-session Provider: got %q want alt", sub.Model.Provider)
	}
	if sub.Model.Name != "cool-model" {
		t.Errorf("sub-session Name: got %q want cool-model", sub.Model.Name)
	}
}

func TestRun_NoModelOverride_UsesParentProvider(t *testing.T) {
	env := newRunnerEnv(t)
	parentProv := newFakeProvider(scriptText("parent ran"))

	resolverCalls := 0
	resolver := func(_ context.Context, _ string) (provider.Provider, string, error) {
		resolverCalls++
		return nil, "", fmt.Errorf("should not be called")
	}

	r, err := NewRunner(RunnerOptions{
		Bus:              env.bus,
		Store:            env.store,
		Provider:         parentProv,
		Permission:       env.perm,
		Catalog:          env.catalog,
		Registry:         mustLoad(t),
		ParentTools:      env.parentTools,
		Pwd:              env.pwd,
		ProviderResolver: resolver,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "general", // no override
		Description:     "x",
		Prompt:          "go",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resolverCalls != 0 {
		t.Errorf("resolver should not be called when type has no override; got %d", resolverCalls)
	}
	if res.FinalText != "parent ran" {
		t.Errorf("FinalText: %q", res.FinalText)
	}
	sub, _ := env.store.GetSession(context.Background(), res.SessionID)
	if sub.Model.Provider != "fake" {
		t.Errorf("sub-session Provider: got %q want fake", sub.Model.Provider)
	}
	if sub.Model.Name != "fake-model" {
		t.Errorf("sub-session Name: got %q want fake-model", sub.Model.Name)
	}
}

func TestRun_ResolverError_Surfaces(t *testing.T) {
	env := newRunnerEnv(t)
	parentProv := newFakeProvider()
	boom := errors.New("no creds")
	resolver := func(_ context.Context, _ string) (provider.Provider, string, error) {
		return nil, "", boom
	}

	reg := loadRegistryWithModel(t, "fancy", "alt/cool-model")
	r, err := NewRunner(RunnerOptions{
		Bus:              env.bus,
		Store:            env.store,
		Provider:         parentProv,
		Permission:       env.perm,
		Catalog:          env.catalog,
		Registry:         reg,
		ParentTools:      env.parentTools,
		Pwd:              env.pwd,
		ProviderResolver: resolver,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	_, err = r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "fancy",
		Description:     "x",
		Prompt:          "go",
		ModelName:       "fake-model",
	})
	if err == nil {
		t.Fatal("expected error from resolver to bubble up")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error did not wrap resolver error: %v", err)
	}
	if parentProv.calls.Load() != 0 {
		t.Errorf("parent provider should not have streamed when resolver failed")
	}
}

func TestRun_MalformedStoredModel_FallsBackToParent(t *testing.T) {
	// The registry strips malformed model strings at load time, but
	// we exercise defence-in-depth: if some path ever produces a
	// Registry with a malformed Model still attached, Run treats it
	// as "use parent's" rather than aborting.
	env := newRunnerEnv(t)
	parentProv := newFakeProvider(scriptText("fell back to parent"))

	// Build a registry by hand with a malformed Model on a type.
	reg := &Registry{
		byName: map[string]Type{
			"general": builtinGeneral,
			"hand": {
				Name:         "hand",
				Description:  "hand-rolled",
				SystemPrompt: "go",
				Source:       "test",
				Model:        "this is not valid", // bypasses load-time validation
			},
		},
		defaultTools: []string{"read"},
	}
	reg.types = []Type{reg.byName["general"], reg.byName["hand"]}

	resolverCalls := 0
	resolver := func(_ context.Context, _ string) (provider.Provider, string, error) {
		resolverCalls++
		return nil, "", fmt.Errorf("should not be called for malformed override")
	}

	r, err := NewRunner(RunnerOptions{
		Bus:              env.bus,
		Store:            env.store,
		Provider:         parentProv,
		Permission:       env.perm,
		Catalog:          env.catalog,
		Registry:         reg,
		ParentTools:      env.parentTools,
		Pwd:              env.pwd,
		ProviderResolver: resolver,
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "hand",
		Description:     "x",
		Prompt:          "go",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run should fall back, not fail: %v", err)
	}
	if resolverCalls != 0 {
		t.Errorf("resolver should not be invoked for malformed stored override; got %d calls", resolverCalls)
	}
	if res.FinalText != "fell back to parent" {
		t.Errorf("FinalText: %q", res.FinalText)
	}
}

func TestRun_OverrideWithoutResolver_UsesParent(t *testing.T) {
	// If a type pins a model but the CLI bootstrap did not wire a
	// resolver, we degrade to the parent's provider rather than
	// crashing.  Mirrors the documented behaviour in Runner.Run.
	env := newRunnerEnv(t)
	parentProv := newFakeProvider(scriptText("no resolver, used parent"))

	reg := loadRegistryWithModel(t, "fancy", "alt/cool-model")
	r, err := NewRunner(RunnerOptions{
		Bus:         env.bus,
		Store:       env.store,
		Provider:    parentProv,
		Permission:  env.perm,
		Catalog:     env.catalog,
		Registry:    reg,
		ParentTools: env.parentTools,
		Pwd:         env.pwd,
		// ProviderResolver: nil (deliberate)
	})
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	res, err := r.Run(context.Background(), RunInput{
		ParentSessionID: env.parentSessID,
		Type:            "fancy",
		Description:     "x",
		Prompt:          "go",
		ModelName:       "fake-model",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FinalText != "no resolver, used parent" {
		t.Errorf("FinalText: %q", res.FinalText)
	}
}

func mustLoad(t *testing.T) *Registry {
	t.Helper()
	reg, err := Load(LoadOptions{
		HomeDir:      t.TempDir(),
		DefaultTools: []string{"read", "grep", "glob", "bash"},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return reg
}
