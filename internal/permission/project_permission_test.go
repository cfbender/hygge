package permission

import (
	"context"
	"errors"
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

	// Create a real source file so os.Stat in promoteTarget can confirm it is a
	// regular file and promote to the parent directory glob.
	srcDir := t.TempDir()
	mainFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(mainFile, []byte("package main"), 0o644); err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	wantPattern := filepath.Join(srcDir, "**")

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
		Target:   mainFile,
		Pwd:      srcDir,
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
	if r.Category != "file.write" || r.Pattern != wantPattern {
		t.Errorf("project rule: got %+v, want file.write @ %s", r, wantPattern)
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

	srcDir := t.TempDir()
	mainFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(mainFile, []byte("package main"), 0o644); err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	wantPattern := filepath.Join(srcDir, "**")

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
		Target:   mainFile,
		Pwd:      srcDir,
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
	if got := s.AllowedRules[0].Pattern; got != wantPattern {
		t.Fatalf("global state pattern = %q, want %q", got, wantPattern)
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

	// Create a real source file so os.Stat in promoteTarget can promote to the
	// parent directory glob and cover the sibling via the session cache.
	srcDir := t.TempDir()
	mainFile := filepath.Join(srcDir, "main.go")
	if err := os.WriteFile(mainFile, []byte("package main"), 0o644); err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	// siblingFile does not need to exist on disk; it just needs to be in the same dir.
	siblingFile := filepath.Join(srcDir, "other.go")

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

	// First call: prompts the user.
	if _, err := e.Ask(context.Background(), Request{
		Category: CategoryFileWrite,
		Target:   mainFile,
		Pwd:      srcDir,
	}); err != nil {
		t.Fatalf("first Ask: %v", err)
	}

	// Second call: session cache should serve it (sibling is covered by dir glob).
	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileWrite,
		Target:   siblingFile,
		Pwd:      srcDir,
	})
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

// --- Project session ignores global file.* allow rules ----------------------

// TestProjectSession_GlobalFileRuleIgnored verifies that when ProjectDir is
// set, a user-global file.read allow rule for a parent directory does NOT
// silently auto-allow reads of files outside the project's PWD.
//
// Success criterion 1 from the spec.
func TestProjectSession_GlobalFileRuleIgnored(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}
	base := t.TempDir()
	githubDir := filepath.Join(base, "github")
	outsideRepoFile := filepath.Join(githubDir, "repo-a", "AGENTS.md")
	projectPwdDir := filepath.Join(githubDir, "repo-b")

	// Seed a broad global file.read rule that would otherwise auto-allow the target.
	if err := state.AddAllowRule(state.AllowRule{
		Category: string(CategoryFileRead),
		Pattern:  filepath.Join(githubDir, "**"),
	}, stateOpts); err != nil {
		t.Fatalf("seed global allow rule: %v", err)
	}

	b := bus.New()

	e, err := New(EngineOptions{
		Bus:        b,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	// Close the bus so that any "ask" resolves immediately as ErrBusClosed —
	// that confirms the engine did NOT auto-allow via the global rule.
	b.Close()

	_, err = e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   outsideRepoFile,
		Pwd:      projectPwdDir,
	})
	// We expect ErrBusClosed (engine tried to ask the user) — NOT nil (allow).
	if err == nil {
		t.Fatal("Ask returned allow; global file.* rule must not apply in project sessions")
	}
	if !errors.Is(err, ErrBusClosed) {
		t.Errorf("err = %v, want ErrBusClosed (indicates ask stage was reached)", err)
	}
}

// TestProjectSession_GlobalNonFileRuleHonored verifies that non-file global
// rules (e.g. shell) still apply in project sessions.
//
// Success criterion 4 from the spec.
func TestProjectSession_GlobalNonFileRuleHonored(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}

	// Seed a global shell allow rule.
	if err := state.AddAllowRule(state.AllowRule{
		Category: string(CategoryShell),
		Pattern:  "go test ./...",
	}, stateOpts); err != nil {
		t.Fatalf("seed global shell rule: %v", err)
	}

	b := bus.New()
	t.Cleanup(b.Close)

	e, err := New(EngineOptions{
		Bus:        b,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "go test ./...",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("shell action = %v, want allow (global shell rule should still apply)", d.Action)
	}
	if d.Reason != "rule: state" {
		t.Errorf("Reason = %q, want rule: state (global shell rule source)", d.Reason)
	}
}

// TestNonProjectSession_GlobalFileRuleHonored verifies that when ProjectDir is
// empty, global file.read rules still apply as before.
//
// Success criterion 3 from the spec.
func TestNonProjectSession_GlobalFileRuleHonored(t *testing.T) {
	homeDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}
	base := t.TempDir()
	githubDir := filepath.Join(base, "github")
	outsideRepoFile := filepath.Join(githubDir, "repo-a", "AGENTS.md")
	projectPwdDir := filepath.Join(githubDir, "repo-b")

	if err := state.AddAllowRule(state.AllowRule{
		Category: string(CategoryFileRead),
		Pattern:  filepath.Join(githubDir, "**"),
	}, stateOpts); err != nil {
		t.Fatalf("seed global file rule: %v", err)
	}

	b := bus.New()
	t.Cleanup(b.Close)

	e, err := New(EngineOptions{
		Bus:    b,
		Config: defaultCfg(),
		State:  stateOpts,
		// ProjectDir intentionally empty.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   outsideRepoFile,
		Pwd:      projectPwdDir,
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("action = %v, want allow (global file rule must work when ProjectDir is empty)", d.Action)
	}
}

// TestProjectSession_ProjectFileRuleHonored verifies that project-scoped file
// rules (in .hygge/permissions.json) still apply even when global file rules
// are ignored.
//
// Success criterion 2 from the spec.
func TestProjectSession_ProjectFileRuleHonored(t *testing.T) {
	homeDir := t.TempDir()
	projectDir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: homeDir}
	base := t.TempDir()
	globalDir := filepath.Join(base, "global")
	projectSrcDir := filepath.Join(base, "project-src")

	// Seed a broad global file.read rule — it should be ignored.
	if err := state.AddAllowRule(state.AllowRule{
		Category: string(CategoryFileRead),
		Pattern:  filepath.Join(globalDir, "**"),
	}, stateOpts); err != nil {
		t.Fatalf("seed global rule: %v", err)
	}
	// Seed a project-scoped rule for a different pattern.
	if err := state.AddProjectAllowRule(state.AllowRule{
		Category: string(CategoryFileRead),
		Pattern:  filepath.Join(projectSrcDir, "**"),
	}, projectDir); err != nil {
		t.Fatalf("seed project rule: %v", err)
	}

	b := bus.New()
	t.Cleanup(b.Close)

	e, err := New(EngineOptions{
		Bus:        b,
		Config:     defaultCfg(),
		State:      stateOpts,
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	// Project rule must allow its target.
	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   filepath.Join(projectSrcDir, "main.go"),
		Pwd:      projectDir,
	})
	if err != nil {
		t.Fatalf("Ask project target: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("project target action = %v, want allow", d.Action)
	}
	if !strings.Contains(d.Reason, "project-state") {
		t.Errorf("Reason = %q, want mention of project-state", d.Reason)
	}

	// Global file rule must NOT apply.
	b.Close()
	_, err = e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   filepath.Join(globalDir, "secret.go"),
		Pwd:      projectDir,
	})
	if err == nil {
		t.Fatal("global file rule was honoured in project session; expected ask/deny")
	}
	if !errors.Is(err, ErrBusClosed) {
		t.Errorf("err = %v, want ErrBusClosed", err)
	}
}
