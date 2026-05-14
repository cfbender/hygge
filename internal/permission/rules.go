package permission

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Rule is a single (category, pattern, action) decision unit.  Rules are
// evaluated in their declared order; the first match wins.  Patterns are
// doublestar globs; an empty pattern is treated as "**" — matches anything in
// the rule's category.
type Rule struct {
	// Category is the permission category the rule applies to.  When empty
	// the rule applies to every category (rare; used internally for cache
	// lookups and not exposed to user TOML).
	Category Category

	// Pattern is a doublestar glob.  For file.* categories it is matched
	// against the request's absolute Target path; for shell it is matched
	// against the full command string; for network against the URL or host.
	Pattern string

	// Action is the decision returned when the rule fires.
	Action Action

	// Source identifies the origin of the rule (e.g. "secrets-denylist",
	// "state", "config", "default").  Surfaced in Decision.Reason for
	// observability.
	Source string

	// AppliesOutsidePwdOnly, when true, restricts the rule so it only fires
	// when the request's Target is not inside the request's Pwd.  This is
	// how the configured "file_read_outside_pwd" mode is encoded as a
	// last-resort rule.  Internal; not used by user-supplied rules.
	AppliesOutsidePwdOnly bool

	// AppliesInsidePwdOnly is the symmetric flag used to express the
	// implicit "allow file reads under $PWD" default.
	AppliesInsidePwdOnly bool
}

// compiledRule is a Rule with its glob validated.  We do not store a compiled
// matcher object here because doublestar exposes only a Match function; we
// re-validate the glob at compile time so we fail fast on bad input.
type compiledRule struct {
	rule Rule
}

// Matcher evaluates a Request against a fixed, ordered list of rules.
// Matchers are immutable after construction and safe for concurrent use.
type Matcher struct {
	rules []compiledRule
}

// NewMatcher validates every rule's pattern up front and returns a Matcher
// ready to evaluate requests.  Returns [ErrInvalidPattern] wrapping the
// underlying glob error if any pattern is malformed.
func NewMatcher(rules []Rule) (*Matcher, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for i, r := range rules {
		pat := r.Pattern
		if pat == "" {
			pat = "**"
			r.Pattern = pat
		}
		if !doublestar.ValidatePattern(pat) {
			return nil, fmt.Errorf("%w: rule[%d] category=%q pattern=%q source=%q",
				ErrInvalidPattern, i, r.Category, pat, r.Source)
		}
		compiled = append(compiled, compiledRule{rule: r})
	}
	return &Matcher{rules: compiled}, nil
}

// Match returns the action of the first rule whose Category and Pattern
// match req, along with a pointer to a copy of the rule.  When no rule
// matches, Match returns ActionAsk and a nil rule.
func (m *Matcher) Match(req Request) (Action, *Rule) {
	for i := range m.rules {
		r := m.rules[i].rule
		if r.Category != "" && r.Category != req.Category {
			continue
		}
		if r.AppliesInsidePwdOnly && !isUnderPwd(req.Target, req.Pwd) {
			continue
		}
		if r.AppliesOutsidePwdOnly && isUnderPwd(req.Target, req.Pwd) {
			continue
		}
		if patternMatches(r.Pattern, req.Category, req.Target) {
			out := r
			return r.Action, &out
		}
	}
	return ActionAsk, nil
}

// patternMatches runs the doublestar match.  For file.* categories the target
// is a filesystem path; doublestar handles both / and OS-native separators
// internally on its "PathMatch" entry point.  For shell/network the pattern
// is treated as a plain glob over the Target string.
func patternMatches(pattern string, cat Category, target string) bool {
	switch cat {
	case CategoryFileRead, CategoryFileWrite:
		// Use PathMatch so doublestar normalises path separators.
		ok, err := doublestar.PathMatch(pattern, target)
		if err != nil {
			return false
		}
		return ok
	default:
		ok, err := doublestar.Match(pattern, target)
		if err != nil {
			return false
		}
		return ok
	}
}

// isUnderPwd reports whether target is the same as, or a descendant of, pwd.
// Both inputs are treated as filesystem paths and normalised with
// filepath.Clean.  An empty pwd is treated as "no PWD set" — the function
// returns false so that the "inside PWD" rule does not fire by accident.
func isUnderPwd(target, pwd string) bool {
	if pwd == "" || target == "" {
		return false
	}
	t := filepath.Clean(target)
	p := filepath.Clean(pwd)
	if t == p {
		return true
	}
	// Append separator so /home/foo does not match /home/foobar.
	prefix := p + string(filepath.Separator)
	return strings.HasPrefix(t, prefix)
}
