package permission

import (
	"context"
	"errors"
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
	for _, tc := range []struct {
		name   string
		cat    Category
		target string
		want   string
	}{
		{name: "shell unchanged", cat: CategoryShell, target: "go test ./...", want: "go test ./..."},
		{name: "empty file target", cat: CategoryFileRead, target: "", want: ""},
		{name: "absolute file", cat: CategoryFileWrite, target: "/repo/src/main.go", want: "/repo/src/**"},
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
