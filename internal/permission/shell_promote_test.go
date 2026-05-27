package permission

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/state"
)

// TestPromoteShellTarget covers the issue #44 requirement that shell command
// targets are promoted to reusable glob patterns for filesystem-ish arguments.
func TestPromoteShellTarget(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		// Issue examples.
		{name: "cat_file_in_dir", input: "cat internal/main.go", want: "cat internal/**/*"},
		{name: "ls_dot", input: "ls .", want: "ls **/*"},

		// Flags preserved verbatim.
		{name: "ls_flag_only", input: "ls -la", want: "ls -la"},
		{name: "ls_flag_and_path", input: "ls -la ./src", want: "ls -la src/**/*"},

		// Go wildcard already present — do not double-promote.
		{name: "go_test_all", input: "go test ./...", want: "go test ./..."},

		// Non-path args kept verbatim.
		{name: "git_status", input: "git status", want: "git status"},
		{name: "echo_string", input: "echo hello", want: "echo hello"},

		// Relative paths with explicit ./
		{name: "cat_dotslash", input: "cat ./foo/bar.go", want: "cat foo/**/*"},

		// Nested dir component.
		{name: "wc_nested", input: "wc -l internal/foo/bar.go", want: "wc -l internal/foo/**/*"},

		// Bare filename (no directory component) — NOT promoted (conservative:
		// could be a subcommand arg, not necessarily a path).
		{name: "cat_bare_file", input: "cat main.go", want: "cat main.go"},

		// dotdot path.
		{name: "cat_dotdot", input: "cat ../other/file.go", want: "cat ../other/**/*"},

		// Empty command — returned unchanged.
		{name: "empty", input: "", want: ""},

		// Shell metacharacters — returned unchanged (conservative).
		{name: "pipe", input: "ls | grep foo", want: "ls | grep foo"},
		{name: "redirect", input: "cat file.go > out.txt", want: "cat file.go > out.txt"},
		{name: "semicolon", input: "ls; echo done", want: "ls; echo done"},

		// Already-promoted pattern with glob — kept as-is.
		{name: "already_glob", input: "cat internal/**/*", want: "cat internal/**/*"},

		// Multiple path args.
		{name: "diff_two_files", input: "diff a/foo.go b/foo.go", want: "diff a/**/* b/**/*"},

		// Tilde path.
		{name: "cat_tilde", input: "cat ~/.config/foo.toml", want: "cat ~/.config/**/*"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := promoteShellTarget(tc.input)
			if got != tc.want {
				t.Fatalf("promoteShellTarget(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestPromoteShellArg exercises the per-argument helper directly.
func TestPromoteShellArg(t *testing.T) {
	for _, tc := range []struct {
		arg  string
		want string
	}{
		{".", "**/*"},
		{"..", "**/*"},
		{"./", "**/*"},
		{"./src/", "src/**/*"},
		{"src/main.go", "src/**/*"},
		{"main.go", "main.go"}, // bare filename — not promoted (conservative)
		{"-v", "-v"},
		{"--flag", "--flag"},
		{"./...", "./..."}, // go recursive wildcard — kept
		{"**/*", "**/*"},   // already glob — kept
		{"foo/bar", "foo/**/*"},
		{"", ""},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			got := promoteShellArg(tc.arg)
			if got != tc.want {
				t.Fatalf("promoteShellArg(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}

// TestShellPromotion_PromoteTargetShellCategory checks that promoteTarget
// delegates to promoteShellTarget for CategoryShell.
func TestShellPromotion_PromoteTargetShellCategory(t *testing.T) {
	got := promoteTarget(CategoryShell, "cat internal/main.go")
	want := "cat internal/**/*"
	if got != want {
		t.Fatalf("promoteTarget(shell, ...) = %q, want %q", got, want)
	}
}

// TestShellPromotion_SessionCacheHit verifies that once a shell command is
// approved (session scope), a similar command in the same directory tree is
// served from the session cache without re-prompting.
func TestShellPromotion_SessionCacheHit(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "session", At: time.Now()}
	})
	defer stop()

	// First command: triggers a bus prompt.
	d1, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "cat internal/main.go",
	})
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}
	if d1.Action != ActionAllow {
		t.Errorf("first: got %v, want allow", d1.Action)
	}

	// Second command in the same directory tree: must be served from cache.
	d2, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "cat internal/other.go",
	})
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if d2.Action != ActionAllow {
		t.Errorf("second: got %v, want allow (cached)", d2.Action)
	}
	if got := asks.Load(); got != 1 {
		t.Errorf("bus asks: got %d, want 1 (second should be cached)", got)
	}
}

// TestShellPromotion_AlwaysScopePersistsPattern checks that an "always" shell
// approval persists the promoted glob pattern, and that a subsequent engine
// created with that state allows matching commands without prompting.
func TestShellPromotion_AlwaysScopePersistsPattern(t *testing.T) {
	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}

	b := bus.New()
	t.Cleanup(b.Close)

	e, err := New(EngineOptions{
		Bus:    b,
		Config: defaultCfg(),
		State:  stateOpts,
		Clock:  func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})

	// First Ask — triggers bus prompt, response is "always".
	d, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "cat internal/main.go",
	})
	stop()
	e.Close()
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow || d.Scope != ScopeAlways {
		t.Errorf("first decision: got %+v, want allow/always", d)
	}

	// Verify persisted pattern is the promoted glob.
	s, err := state.Load(stateOpts)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if len(s.AllowedRules) != 1 {
		t.Fatalf("AllowedRules: got %d, want 1", len(s.AllowedRules))
	}
	r := s.AllowedRules[0]
	wantPattern := "cat internal/**/*"
	if r.Category != "shell" || r.Pattern != wantPattern {
		t.Errorf("persisted rule: got {category:%q pattern:%q}, want shell@%q", r.Category, r.Pattern, wantPattern)
	}

	// New engine with same state: similar command must not prompt.
	b2 := bus.New()
	t.Cleanup(b2.Close)
	e2, err := New(EngineOptions{
		Bus:    b2,
		Config: defaultCfg(),
		State:  stateOpts,
	})
	if err != nil {
		t.Fatalf("New e2: %v", err)
	}
	t.Cleanup(e2.Close)

	askedSub := bus.Subscribe[bus.PermissionAsked](b2, bus.SubscribeOptions{BufferSize: 4})
	defer askedSub.Unsubscribe()

	d2, err := e2.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "cat internal/other.go",
	})
	if err != nil {
		t.Fatalf("Ask e2 sibling: %v", err)
	}
	if d2.Action != ActionAllow {
		t.Errorf("sibling: got %v, want allow (covered by persisted glob)", d2.Action)
	}
	select {
	case asked := <-askedSub.C():
		t.Errorf("bus was asked unexpectedly for sibling command: %+v", asked)
	case <-time.After(50 * time.Millisecond):
		// correct — no prompt
	}
}

// TestShellPromotion_DifferentDirDoesNotMatch verifies that approving a shell
// command in one directory does NOT grant approval for commands in a different
// directory tree.
func TestShellPromotion_DifferentDirDoesNotMatch(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "session", At: time.Now()}
	})
	defer stop()

	// Approve cat in "internal/".
	_, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "cat internal/main.go",
	})
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}

	// cat in a different directory must trigger a fresh prompt.
	_, err = e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "cat cmd/main.go",
	})
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if got := asks.Load(); got != 2 {
		t.Errorf("bus asks: got %d, want 2 (different dir must re-prompt)", got)
	}
}

// TestShellPromotion_NoPathArg_NoGlobCoverage verifies that a command with no
// path-like args (e.g. "git status") is stored exactly and does not
// accidentally match unrelated commands via glob.
func TestShellPromotion_NoPathArg_NoGlobCoverage(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "session", At: time.Now()}
	})
	defer stop()

	// Approve "git status".
	_, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "git status",
	})
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}

	// "git diff" must still prompt — different command, no glob broadening.
	_, err = e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "git diff",
	})
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if got := asks.Load(); got != 2 {
		t.Errorf("bus asks: got %d, want 2 (git diff must not be covered by git status)", got)
	}
}

func TestShellPromotion_DirectoryArgCoversTree(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "session", At: time.Now()}
	})
	defer stop()

	_, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "ls -la ./src",
	})
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}

	_, err = e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "ls -la src/deep/file.go",
	})
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if got := asks.Load(); got != 1 {
		t.Errorf("bus asks: got %d, want 1 (directory arg should cover tree)", got)
	}
}

func TestShellPromotion_UnpromotedGlobMetacharactersDoNotMatchAsGlob(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "session", At: time.Now()}
	})
	defer stop()

	_, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "curl https://api.example.test/v1?limit=10",
	})
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}

	_, err = e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "curl https://api.example.test/v1Xlimit=10",
	})
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if got := asks.Load(); got != 2 {
		t.Errorf("bus asks: got %d, want 2 (literal ? must not act as a glob)", got)
	}
}
