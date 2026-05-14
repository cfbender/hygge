package permission

import (
	"errors"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/config"
)

func TestMatcher_SecretsDenylist(t *testing.T) {
	rules, err := buildRulesNoState(testConfig(config.PermAllow, config.PermAllow, config.PermAllow, config.PermAllow))
	if err != nil {
		t.Fatalf("buildRules: %v", err)
	}
	m, err := NewMatcher(rules)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}

	for _, c := range []struct {
		cat    Category
		target string
	}{
		{CategoryFileRead, "/home/me/proj/.env"},
		{CategoryFileWrite, "/home/me/proj/.env"},
		{CategoryFileRead, "/home/me/proj/.env.local"},
		{CategoryFileRead, "/home/me/.aws/credentials"},
		{CategoryFileRead, "/home/me/.ssh/id_rsa"},
		{CategoryFileRead, "/etc/foo/server.key"},
		{CategoryFileRead, "/path/to/cert.pem"},
		{CategoryFileWrite, "/home/me/keepass.kdbx"},
	} {
		action, rule := m.Match(Request{Category: c.cat, Target: c.target, Pwd: "/home/me/proj"})
		if action != ActionDeny {
			t.Errorf("Match(%v, %q): got %v, want deny", c.cat, c.target, action)
		}
		if rule == nil || rule.Source != "secrets-denylist" {
			t.Errorf("Match(%v, %q): got rule=%+v, want source=secrets-denylist", c.cat, c.target, rule)
		}
	}
}

func TestMatcher_SecretsDenylist_DoesNotApplyToShell(t *testing.T) {
	// A shell command that mentions ".env" is gated by the shell category,
	// not file.read.  The denylist must not constrain it.
	rules, _ := buildRulesNoState(testConfig(config.PermAllow, config.PermAllow, config.PermAllow, config.PermDeny))
	m, _ := NewMatcher(rules)

	action, _ := m.Match(Request{
		Category: CategoryShell,
		Target:   "cat .env",
		Pwd:      "/home/me/proj",
	})
	if action != ActionAllow {
		t.Errorf("shell command mentioning .env: got %v, want allow", action)
	}
}

func TestMatcher_StateAllowRule(t *testing.T) {
	rules := []Rule{
		{Category: CategoryFileWrite, Pattern: "/repo/src/**", Action: ActionAllow, Source: "state"},
		{Category: CategoryFileWrite, Pattern: "**", Action: ActionAsk, Source: "default"},
	}
	m, err := NewMatcher(rules)
	if err != nil {
		t.Fatalf("NewMatcher: %v", err)
	}
	action, rule := m.Match(Request{Category: CategoryFileWrite, Target: "/repo/src/main.go"})
	if action != ActionAllow {
		t.Errorf("expected allow, got %v", action)
	}
	if rule == nil || rule.Source != "state" {
		t.Errorf("expected source=state, got %+v", rule)
	}
}

func TestMatcher_ConfigRuleOverridesDefault(t *testing.T) {
	// A "config"-source rule placed before "default" wins.
	rules := []Rule{
		{Category: CategoryShell, Pattern: "git *", Action: ActionAllow, Source: "config"},
		{Category: CategoryShell, Pattern: "**", Action: ActionAsk, Source: "default"},
	}
	m, _ := NewMatcher(rules)

	action, rule := m.Match(Request{Category: CategoryShell, Target: "git status"})
	if action != ActionAllow {
		t.Errorf("git rule allow: got %v", action)
	}
	if rule == nil || rule.Source != "config" {
		t.Errorf("rule source: got %+v, want config", rule)
	}

	// Unrelated command falls through to the default.
	action, rule = m.Match(Request{Category: CategoryShell, Target: "rm -rf /"})
	if action != ActionAsk {
		t.Errorf("default fallthrough: got %v, want ask", action)
	}
	if rule == nil || rule.Source != "default" {
		t.Errorf("fallthrough rule source: got %+v, want default", rule)
	}
}

func TestMatcher_DefaultPolicy_FileReadInsidePwd(t *testing.T) {
	rules, _ := buildRulesNoState(testConfig(config.PermAsk, config.PermAsk, config.PermAsk, config.PermDeny))
	m, _ := NewMatcher(rules)

	// Inside PWD → allow.
	action, rule := m.Match(Request{
		Category: CategoryFileRead,
		Target:   "/repo/main.go",
		Pwd:      "/repo",
	})
	if action != ActionAllow {
		t.Errorf("inside PWD: got %v, want allow", action)
	}
	if rule == nil || rule.Source != "default" {
		t.Errorf("rule: got %+v, want default", rule)
	}

	// Outside PWD → ask (because file_read_outside_pwd = ask).
	action, _ = m.Match(Request{
		Category: CategoryFileRead,
		Target:   "/etc/passwd",
		Pwd:      "/repo",
	})
	if action != ActionAsk {
		t.Errorf("outside PWD with ask mode: got %v, want ask", action)
	}
}

func TestMatcher_DefaultPolicy_FileReadOutsidePwdDeny(t *testing.T) {
	rules, _ := buildRulesNoState(testConfig(config.PermDeny, config.PermAsk, config.PermAsk, config.PermDeny))
	m, _ := NewMatcher(rules)

	action, _ := m.Match(Request{
		Category: CategoryFileRead,
		Target:   "/etc/passwd",
		Pwd:      "/repo",
	})
	if action != ActionDeny {
		t.Errorf("outside PWD with deny mode: got %v, want deny", action)
	}
}

func TestMatcher_DefaultPolicy_Network(t *testing.T) {
	rules, _ := buildRulesNoState(testConfig(config.PermAsk, config.PermAsk, config.PermAsk, config.PermDeny))
	m, _ := NewMatcher(rules)

	action, _ := m.Match(Request{Category: CategoryNetwork, Target: "https://example.com"})
	if action != ActionDeny {
		t.Errorf("network default: got %v, want deny", action)
	}
}

func TestMatcher_DefaultPolicy_MCP(t *testing.T) {
	rules, _ := buildRulesNoState(testConfig(config.PermAsk, config.PermAsk, config.PermAsk, config.PermDeny))
	m, _ := NewMatcher(rules)

	action, rule := m.Match(Request{Category: CategoryMCP, Target: "github_create_issue"})
	if action != ActionAsk {
		t.Errorf("mcp default: got %v, want ask", action)
	}
	if rule == nil || rule.Source != "default" {
		t.Errorf("rule: got %+v, want default", rule)
	}
}

func TestMatcher_NilConfigCoversMCP(t *testing.T) {
	rules, _ := buildRulesNoState(nil)
	m, _ := NewMatcher(rules)
	action, _ := m.Match(Request{Category: CategoryMCP, Target: "any"})
	if action != ActionAsk {
		t.Errorf("nil-config MCP fallback: got %v, want ask", action)
	}
}

func TestMatcher_InvalidPattern(t *testing.T) {
	// doublestar rejects unclosed character classes.
	_, err := NewMatcher([]Rule{
		{Category: CategoryShell, Pattern: "[unclosed", Action: ActionAllow, Source: "config"},
	})
	if err == nil {
		t.Fatal("expected error for invalid pattern")
	}
	if !errors.Is(err, ErrInvalidPattern) {
		t.Errorf("expected ErrInvalidPattern, got %v", err)
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error should name source: %v", err)
	}
}

func TestMatcher_EmptyCategoryMatchesAny(t *testing.T) {
	// A rule with empty Category matches any category — used internally,
	// but worth covering.
	rules := []Rule{
		{Category: "", Pattern: "**", Action: ActionDeny, Source: "internal"},
	}
	m, _ := NewMatcher(rules)
	for _, cat := range []Category{CategoryFileRead, CategoryShell, CategoryNetwork} {
		action, _ := m.Match(Request{Category: cat, Target: "x"})
		if action != ActionDeny {
			t.Errorf("cat=%v: got %v, want deny", cat, action)
		}
	}
}

func TestMatcher_NoMatchReturnsAsk(t *testing.T) {
	// A matcher with rules that all miss returns ActionAsk.
	rules := []Rule{
		{Category: CategoryShell, Pattern: "git *", Action: ActionAllow, Source: "config"},
	}
	m, _ := NewMatcher(rules)
	action, rule := m.Match(Request{Category: CategoryFileRead, Target: "/foo"})
	if action != ActionAsk {
		t.Errorf("got %v, want ask", action)
	}
	if rule != nil {
		t.Errorf("rule: got %+v, want nil", rule)
	}
}

func TestIsUnderPwd(t *testing.T) {
	cases := []struct {
		target, pwd string
		want        bool
	}{
		{"/repo/src/main.go", "/repo", true},
		{"/repo", "/repo", true},
		{"/repo/", "/repo", true},
		{"/repofoo", "/repo", false}, // prefix-but-not-descendant guard
		{"/etc/passwd", "/repo", false},
		{"", "/repo", false},
		{"/repo", "", false},
	}
	for _, c := range cases {
		got := isUnderPwd(c.target, c.pwd)
		if got != c.want {
			t.Errorf("isUnderPwd(%q, %q) = %v, want %v", c.target, c.pwd, got, c.want)
		}
	}
}

func TestModeToAction(t *testing.T) {
	cases := []struct {
		in  config.PermissionMode
		out Action
	}{
		{config.PermAllow, ActionAllow},
		{config.PermDeny, ActionDeny},
		{config.PermAsk, ActionAsk},
		{config.PermissionMode("garbage"), ActionAsk},
		{config.PermissionMode(""), ActionAsk},
	}
	for _, c := range cases {
		if got := modeToAction(c.in); got != c.out {
			t.Errorf("modeToAction(%q) = %v, want %v", c.in, got, c.out)
		}
	}
}

// Helpers ---------------------------------------------------------------------

// testConfig builds a config.Config with the four legacy permission
// scalars set.  MCP defaults to "ask" (zero PermissionMode -> ActionAsk).
func testConfig(readOutside, write, shell, network config.PermissionMode) *config.Config {
	return &config.Config{
		Permission: config.PermissionConfig{
			FileReadOutsidePwd: readOutside,
			FileWrite:          write,
			Shell:              shell,
			Network:            network,
		},
	}
}

// buildRulesNoState assembles the same rule layers as buildRules but without
// consulting the on-disk state — useful for matcher-only tests.
func buildRulesNoState(cfg *config.Config) ([]Rule, error) {
	var rules []Rule
	for _, pat := range SecretsDenylist {
		rules = append(rules,
			Rule{Category: CategoryFileRead, Pattern: pat, Action: ActionDeny, Source: "secrets-denylist"},
			Rule{Category: CategoryFileWrite, Pattern: pat, Action: ActionDeny, Source: "secrets-denylist"},
		)
	}
	rules = append(rules, defaultRules(cfg)...)
	return rules, nil
}
