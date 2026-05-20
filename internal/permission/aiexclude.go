package permission

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

const aiExcludeFileName = ".aiexclude"

func aiExcludeRules(pwd string) []Rule {
	patterns := loadAIExcludePatterns(pwd)
	if len(patterns) == 0 {
		return nil
	}
	rules := make([]Rule, 0, len(patterns)*2)
	for _, pat := range patterns {
		rules = append(rules,
			Rule{Category: CategoryFileRead, Pattern: pat, Action: ActionDeny, Source: "aiexclude"},
			Rule{Category: CategoryFileWrite, Pattern: pat, Action: ActionDeny, Source: "aiexclude"},
		)
	}
	return rules
}

func aiExcludeDenyRule(req Request) *Rule {
	if req.Category != CategoryFileRead && req.Category != CategoryFileWrite {
		return nil
	}
	for _, r := range aiExcludeRules(req.Pwd) {
		if patternMatches(r.Pattern, req.Category, req.Target) {
			out := r
			return &out
		}
	}
	return nil
}

func loadAIExcludePatterns(pwd string) []string {
	if pwd == "" {
		return nil
	}
	root := filepath.Clean(pwd)
	path := filepath.Join(root, aiExcludeFileName)
	f, err := os.Open(path) //nolint:gosec // .aiexclude is intentionally read from the session project dir
	if err != nil {
		return nil
	}
	defer f.Close() //nolint:errcheck // read-only config file; close error is non-actionable

	var patterns []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		pat := strings.TrimSpace(s.Text())
		if pat == "" || strings.HasPrefix(pat, "#") {
			continue
		}
		patterns = append(patterns, aiExcludePattern(root, pat))
	}
	return patterns
}

func aiExcludePattern(root, pat string) string {
	dirPattern := strings.HasSuffix(pat, "/") || strings.HasSuffix(pat, string(filepath.Separator))
	pat = filepath.Clean(filepath.FromSlash(pat))
	if dirPattern {
		pat = filepath.Join(pat, "**")
	}
	if filepath.IsAbs(pat) {
		return pat
	}
	return filepath.Join(root, pat)
}
