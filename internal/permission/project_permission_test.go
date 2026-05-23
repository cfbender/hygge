package permission

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/state"
)

// --- Allow-always with ProjectDir writes to project .hygge/permissions.json --

func TestAlwaysAllow_WithProjectDir_WritesToProjectFile(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)

	homeDir := t.TempDir()
	projectDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}

	e, err := New(EngineOptions{
		Bus:        b,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
		Clock:      func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})
	defer stop()

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileWrite,
		Target:   "/repo/src/main.go",
		Pwd:      "/repo",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow || d.Scope != ScopeAlways {
		t.Errorf("decision: got %+v, want allow/always", d)
	}

	// Rule must appear in the project-scoped permissions.json.
	projectRules, err := state.LoadProjectAllowRules(projectDir)
	if err != nil {
		t.Fatalf("LoadProjectAllowRules: %v", err)
	}
	if len(projectRules) != 1 {
		t.Fatalf("project rules: got %d, want 1", len(projectRules))
	}
	r := projectRules[0]
	if r.Category != "file.write" || r.Pattern != "/repo/src/**" {
		t.Errorf("project rule: got %+v, want file.write @ /repo/src/**", r)
	}
	if r.CreatedAt == 0 {
		t.Error("project rule CreatedAt should be populated")
	}

	// The user-global state.json must NOT have been touched.
	globalStatePath, _ := state.Path(stateOpts)
	if _, err := os.Stat(globalStatePath); !os.IsNotExist(err) {
		t.Errorf("global state.json was written; expected not to exist; err = %v", err)
	}
}

// --- Allow-always without ProjectDir falls back to global state --------------

func TestAlwaysAllow_NoProjectDir_WritesToGlobalState(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)

	homeDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}

	e, err := New(EngineOptions{
		Bus:    b,
		Config: defaultCfg(),
		State:  stateOpts,
		// ProjectDir intentionally omitted.
		Clock: func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})
	defer stop()

	_, err = e.Ask(context.Background(), Request{
		Category: CategoryFileWrite,
		Target:   "/repo/src/main.go",
		Pwd:      "/repo",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}

	s, err := state.Load(stateOpts)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if len(s.AllowedRules) != 1 {
		t.Fatalf("global state AllowedRules: got %d, want 1", len(s.AllowedRules))
	}
}

// --- Project rules honored on next engine construction ----------------------

func TestAlwaysAllow_ProjectRuleHonoredOnRestart(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}

	// First engine: answer "allow always" to seed the project rule.
	b1 := bus.New()
	t.Cleanup(b1.Close)

	e1, err := New(EngineOptions{
		Bus:        b1,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
		Clock:      func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New (first engine): %v", err)
	}

	var firstEngineAsks atomic.Int32
	stop1 := fakeResponder(t, b1, func(_ bus.PermissionAsked) bus.PermissionReplied {
		firstEngineAsks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})
	_, err = e1.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "go test ./...",
	})
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}
	stop1()
	e1.Close()

	if got := firstEngineAsks.Load(); got != 1 {
		t.Fatalf("expected 1 ask on first engine, got %d", got)
	}

	// Second engine: same projectDir — should NOT ask the user again.
	b2 := bus.New()
	t.Cleanup(b2.Close)

	e2, err := New(EngineOptions{
		Bus:        b2,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
		Clock:      func() time.Time { return time.Unix(1700000001, 0) },
	})
	if err != nil {
		t.Fatalf("New (second engine): %v", err)
	}
	t.Cleanup(e2.Close)

	askedSub := bus.Subscribe[bus.PermissionAsked](b2, bus.SubscribeOptions{BufferSize: 4})
	defer askedSub.Unsubscribe()

	d, err := e2.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "go test ./...",
	})
	if err != nil {
		t.Fatalf("second engine Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("second engine decision: got %v, want allow", d.Action)
	}
	if !strings.Contains(d.Reason, "project-state") {
		t.Errorf("Reason: got %q, want mention of project-state", d.Reason)
	}

	select {
	case asked := <-askedSub.C():
		t.Errorf("PermissionAsked published on second engine (rule should have been honoured): %+v", asked)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- buildRules propagates corrupt project permissions error ----------------

func TestBuildRules_CorruptProjectPermissionsErrors(t *testing.T) {
	b := bus.New()
	defer b.Close()

	projectDir := t.TempDir()
	permPath := filepath.Join(projectDir, ".hygge", "permissions.json")
	if err := os.MkdirAll(filepath.Dir(permPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(permPath, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := New(EngineOptions{
		Bus:        b,
		Config:     defaultCfg(),
		State:      state.LoadOptions{HomeDir: t.TempDir()},
		ProjectDir: projectDir,
	})
	if err == nil {
		t.Fatal("expected error from corrupt project permissions, got nil")
	}
	if !strings.Contains(err.Error(), "project allow rules") {
		t.Errorf("error = %v, want mention of project allow rules", err)
	}
}

// --- Allow-always with ProjectDir does NOT prompt the second time ------------

func TestAlwaysAllow_WithProjectDir_NoBusPromptOnRepeat(t *testing.T) {
	b := bus.New()
	t.Cleanup(b.Close)

	homeDir := t.TempDir()
	projectDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}

	e, err := New(EngineOptions{
		Bus:        b,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
		Clock:      func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})
	defer stop()

	req := Request{
		Category: CategoryFileWrite,
		Target:   "/repo/src/main.go",
		Pwd:      "/repo",
	}

	// First call: prompts the user.
	if _, err := e.Ask(context.Background(), req); err != nil {
		t.Fatalf("first Ask: %v", err)
	}

	// Second call: session cache should serve it.
	sibling := req
	sibling.Target = "/repo/src/other.go"
	d, err := e.Ask(context.Background(), sibling)
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("second Ask action: got %v, want allow", d.Action)
	}
	if got := asks.Load(); got != 1 {
		t.Errorf("bus asks: got %d, want 1 (session cache should serve sibling)", got)
	}
}
