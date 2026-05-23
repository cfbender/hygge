package permission

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/state"
)

func TestEngineYoloReportsStateAndIgnoresClosedEngine(t *testing.T) {
	e, _, _ := newEngine(t, defaultCfg())
	if e.Yolo() {
		t.Fatal("new engine should not start in yolo mode")
	}

	e.SetYolo(true)
	if !e.Yolo() {
		t.Fatal("Yolo() = false after SetYolo(true)")
	}

	e.Close()
	if e.Yolo() {
		t.Fatal("closed engine should report yolo disabled")
	}
	e.SetYolo(false) // should be a no-op on a closed engine.
}

func TestNewRequiresBus(t *testing.T) {
	_, err := New(EngineOptions{})
	if err == nil {
		t.Fatal("New without Bus succeeded")
	}
	if !strings.Contains(err.Error(), "Bus is required") {
		t.Fatalf("New error = %v, want Bus is required", err)
	}
}

func TestNewPropagatesStateLoadError(t *testing.T) {
	b := bus.New()
	defer b.Close()
	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}
	statePath, err := state.Path(stateOpts)
	if err != nil {
		t.Fatalf("state.Path: %v", err)
	}
	if err := writeStateFile(statePath, `{"allowed_rules":[{"category":"shell","pattern":"["}]}`); err != nil {
		t.Fatalf("writeStateFile: %v", err)
	}

	_, err = New(EngineOptions{Bus: b, State: stateOpts})
	if !errors.Is(err, ErrInvalidPattern) {
		t.Fatalf("New error = %v, want ErrInvalidPattern", err)
	}
}

func TestAskReturnsUnknownActionError(t *testing.T) {
	b := bus.New()
	defer b.Close()
	m, err := NewMatcher([]Rule{{Category: CategoryShell, Pattern: "**", Action: Action("bogus"), Source: "test"}})
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	e := &Engine{
		bus:          b,
		clock:        time.Now,
		matcher:      m,
		sessionCache: make(map[sessionCacheKey]Decision),
	}

	_, err = e.Ask(context.Background(), Request{Category: CategoryShell, Target: "echo hi"})
	if err == nil || !strings.Contains(err.Error(), `unknown action "bogus"`) {
		t.Fatalf("Ask error = %v, want unknown action", err)
	}
}

func TestAskReturnsBusClosed(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())
	b.Close()

	_, err := e.Ask(context.Background(), Request{Category: CategoryShell, Target: "echo hi"})
	if !errors.Is(err, ErrBusClosed) {
		t.Fatalf("Ask error = %v, want ErrBusClosed", err)
	}
}

func TestSecretDenyRuleOnlyAppliesToFileCategories(t *testing.T) {
	if got := secretDenyRule(Request{Category: CategoryShell, Target: "/repo/.env"}); got != nil {
		t.Fatalf("secretDenyRule(shell) = %+v, want nil", got)
	}
	if got := secretDenyRule(Request{Category: CategoryFileRead, Target: "/repo/main.go"}); got != nil {
		t.Fatalf("secretDenyRule(non-secret file) = %+v, want nil", got)
	}
	if got := secretDenyRule(Request{Category: CategoryFileWrite, Target: "/repo/.env"}); got == nil {
		t.Fatal("secretDenyRule(secret write) = nil, want rule")
	}
}

func TestPromoteTarget(t *testing.T) {
	// Relative and non-file cases do not hit os.Stat so they are table-driven.
	for _, tc := range []struct {
		name   string
		cat    Category
		target string
		want   string
	}{
		{name: "shell unchanged", cat: CategoryShell, target: "go test ./...", want: "go test ./..."},
		{name: "empty file target", cat: CategoryFileRead, target: "", want: ""},
		{name: "relative dotdot project", cat: CategoryFileRead, target: "../crush/internal/cli/foo.go", want: "../crush/**"},
		{name: "relative same project", cat: CategoryFileRead, target: "src/main.go", want: "src/**"},
		{name: "relative current dir file", cat: CategoryFileWrite, target: "main.go", want: "./**"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := promoteTarget(tc.cat, tc.target); got != tc.want {
				t.Fatalf("promoteTarget(%q, %q) = %q, want %q", tc.cat, tc.target, got, tc.want)
			}
		})
	}

	// Absolute-path cases require real filesystem entries so os.Stat works.
	t.Run("absolute regular file", func(t *testing.T) {
		dir := t.TempDir()
		f, err := os.CreateTemp(dir, "main*.go")
		if err != nil {
			t.Fatalf("create temp file: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close temp file: %v", err)
		}
		want := filepath.Join(dir, "**")
		if got := promoteTarget(CategoryFileWrite, f.Name()); got != want {
			t.Fatalf("promoteTarget(file) = %q, want %q", got, want)
		}
	})

	t.Run("absolute directory", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, "**")
		if got := promoteTarget(CategoryFileRead, dir); got != want {
			t.Fatalf("promoteTarget(dir) = %q, want %q", got, want)
		}
	})

	// Regression for unclean absolute directory targets: avoid producing /path//**.
	t.Run("absolute directory with trailing separator", func(t *testing.T) {
		dir := t.TempDir()
		want := filepath.Join(dir, "**")
		if got := promoteTarget(CategoryFileRead, dir+string(os.PathSeparator)); got != want {
			t.Fatalf("promoteTarget(dir/) = %q, want %q", got, want)
		}
	})

	t.Run("root-level absolute file", func(t *testing.T) {
		d := t.TempDir()
		file := filepath.Join(d, "hygge-promote-root-file-test")
		if err := os.WriteFile(file, []byte("test"), 0o600); err != nil {
			t.Fatalf("create test file %s: %v", file, err)
		}

		want := filepath.Join(filepath.Dir(file), "**")
		if got := promoteTarget(CategoryFileRead, file); got != want {
			t.Fatalf("promoteTarget(root file) = %q, want %q", got, want)
		}
	})

	t.Run("absolute unknown (non-existent)", func(t *testing.T) {
		d := t.TempDir()
		target := filepath.Join(d, "no", "such", "path", "crush")
		if got := promoteTarget(CategoryFileRead, target); got != target {
			t.Fatalf("promoteTarget(unknown) = %q, want exact target %q", got, target)
		}
	})
}

func TestSplitPath(t *testing.T) {
	for _, tc := range []struct {
		path string
		want []string
	}{
		{path: "../crush/internal/cli/foo.go", want: []string{"..", "crush", "internal", "cli", "foo.go"}},
		{path: "/", want: nil},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := splitPath(tc.path)
			if len(got) != len(tc.want) {
				t.Fatalf("splitPath len = %d (%v), want %d", len(got), got, len(tc.want))
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("splitPath[%d] = %q, want %q (all: %v)", i, got[i], tc.want[i], got)
				}
			}
		})
	}
}

func TestReasonFromRule(t *testing.T) {
	if got := reasonFromRule(nil); got != "" {
		t.Fatalf("reasonFromRule(nil) = %q, want empty", got)
	}
	if got := reasonFromRule(&Rule{}); got != "" {
		t.Fatalf("reasonFromRule(no source) = %q, want empty", got)
	}
	if got := reasonFromRule(&Rule{Source: "state"}); got != "rule: state" {
		t.Fatalf("reasonFromRule(state) = %q", got)
	}
}

func TestMatcherEmptyPatternAndBadMatchPattern(t *testing.T) {
	m, err := NewMatcher([]Rule{{Category: CategoryShell, Pattern: "", Action: ActionAllow, Source: "test"}})
	if err != nil {
		t.Fatalf("NewMatcher empty pattern: %v", err)
	}
	action, rule := m.Match(Request{Category: CategoryShell, Target: "anything"})
	if action != ActionAllow || rule == nil || rule.Pattern != "**" {
		t.Fatalf("Match empty-pattern rule = action %v rule %+v, want allow pattern **", action, rule)
	}

	if patternMatches("[", CategoryShell, "anything") {
		t.Fatal("patternMatches invalid shell glob = true, want false")
	}
	if patternMatches("[", CategoryFileRead, "/tmp/x") {
		t.Fatal("patternMatches invalid path glob = true, want false")
	}
}

func TestDefaultRulesAgentAndPlugin(t *testing.T) {
	cfg := &config.Config{Permission: config.PermissionConfig{Subagent: config.PermDeny}}
	m, err := NewMatcher(defaultRules(cfg))
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	action, _ := m.Match(Request{Category: CategoryAgent, Target: "dispatch"})
	if action != ActionDeny {
		t.Fatalf("agent action = %v, want deny", action)
	}
	action, _ = m.Match(Request{Category: CategoryPlugin, Target: "plugin.tool"})
	if action != ActionAsk {
		t.Fatalf("plugin action = %v, want ask", action)
	}
}

func TestStoreSessionClosedEngineNoops(t *testing.T) {
	e, _, _ := newEngine(t, defaultCfg())
	e.Close()
	e.storeSession(Request{Category: CategoryShell, Target: "echo hi"}, Decision{Action: ActionAllow, Scope: ScopeSession})
	if _, ok := e.lookupSession(Request{Category: CategoryShell, Target: "echo hi"}); ok {
		t.Fatal("lookupSession found entry after storeSession on closed engine")
	}
}

// TestOutsidePWD_DirAllowDoesNotMatchSiblingRepo is a regression test for the
// over-broad file permission promotion bug.  Approving an outside-PWD directory
// target like filepath.Join(base, "github", "repo-a") should persist as
// filepath.Join(base, "github", "repo-a", "**"), NOT as the parent github
// glob, so it cannot accidentally allow reads from a sibling repo.
func TestOutsidePWD_DirAllowDoesNotMatchSiblingRepo(t *testing.T) {
	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}

	base := t.TempDir()
	approvedRepo := filepath.Join(base, "github", "repo-a")
	siblingRepo := filepath.Join(base, "github", "repo-b")
	otherProject := filepath.Join(base, "other", "project")

	// Simulate what handleReply now persists for a directory target:
	// promoteTarget(CategoryFileRead, approvedRepo) → filepath.Join(approvedRepo, "**")
	// (The test uses a literal pattern to be independent of real os.Stat calls.)
	if err := state.AddAllowRule(state.AllowRule{
		Category: string(CategoryFileRead),
		Pattern:  filepath.Join(approvedRepo, "**"),
	}, stateOpts); err != nil {
		t.Fatalf("seed allow rule: %v", err)
	}

	b := bus.New()
	t.Cleanup(b.Close)
	e, err := New(EngineOptions{
		Bus:    b,
		Config: defaultCfg(),
		State:  stateOpts,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	// The approved directory pattern must allow files inside crush itself.
	dAllowed, err := e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   filepath.Join(approvedRepo, "internal", "cli", "main.go"),
		Pwd:      otherProject,
	})
	if err != nil {
		t.Fatalf("Ask approved repo file: %v", err)
	}
	if dAllowed.Action != ActionAllow {
		t.Errorf("approved repo file: got %v, want allow (covered by %s)", dAllowed.Action, filepath.Join(approvedRepo, "**"))
	}

	// A sibling repo must NOT be covered by the approved repo rule — it should be
	// denied or asked, but never silently allowed via a parent glob.
	// Default policy for outside-PWD file.read is "ask", so we expect ActionAsk
	// to be resolved by bus.  We don't have a responder, so close the bus to get
	// ErrBusClosed — that confirms the engine reached the ask stage, not allow.
	b.Close()
	_, err = e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   filepath.Join(siblingRepo, "AGENTS.md"),
		Pwd:      otherProject,
	})
	if err == nil {
		t.Fatal("sibling repo file: Ask succeeded (returned allow), want ask/deny — sibling must not be covered by crush rule")
	}
	// ErrBusClosed confirms the engine tried to ask the user (correct) rather
	// than returning an allow from the persisted crush rule.
	if !errors.Is(err, ErrBusClosed) {
		t.Errorf("sibling repo file: err = %v, want ErrBusClosed (indicating ask, not pre-allowed)", err)
	}
}
