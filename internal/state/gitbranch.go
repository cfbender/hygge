package state

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// branchCache holds a once-computed branch name per project directory, keyed
// by the absolute project path.  Populated on first call; the session-lifetime
// cache avoids re-reading .git/HEAD on every render tick.
var (
	branchCacheMu sync.Mutex
	branchCache   = map[string]string{}
)

// GitBranch returns the current branch (or detached-HEAD short SHA) for the
// repository rooted at or above projectPath.  The result is cached per
// projectPath for the lifetime of the process.
//
// Detection strategy (no git subprocess):
//  1. Walk up from projectPath to the filesystem root looking for a .git directory.
//  2. Read .git/HEAD.
//     - If the content begins with "ref: refs/heads/", strip that prefix and
//     return the branch name.
//     - Otherwise assume a detached HEAD: return "@" + first 7 hex characters of
//     the SHA (e.g. "@a1b2c3d").
//  3. If no .git directory is found, return "".
func GitBranch(projectPath string) string {
	branchCacheMu.Lock()
	if cached, ok := branchCache[projectPath]; ok {
		branchCacheMu.Unlock()
		return cached
	}
	branchCacheMu.Unlock()

	branch := readGitBranch(projectPath)

	branchCacheMu.Lock()
	branchCache[projectPath] = branch
	branchCacheMu.Unlock()

	return branch
}

// readGitBranch does the actual .git/HEAD walk without any caching.
// Exported only for tests that need to call it without the cache.
func readGitBranch(projectPath string) string {
	gitDir := findGitDir(projectPath)
	if gitDir == "" {
		return ""
	}
	headPath := filepath.Join(gitDir, "HEAD")
	data, err := os.ReadFile(headPath) //nolint:gosec // user-controlled path is intentional
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	const refPrefix = "ref: refs/heads/"
	if after, ok := strings.CutPrefix(content, refPrefix); ok {
		return after
	}
	// Detached HEAD — return first 7 chars of the SHA prefixed by "@".
	if len(content) >= 7 {
		return "@" + content[:7]
	}
	return "@" + content
}

// findGitDir walks up from dir looking for a .git directory or file.  Returns
// the path to the .git directory when found, or "" when the filesystem root is
// reached without finding one.
func findGitDir(dir string) string {
	for {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		if err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached the filesystem root.
			return ""
		}
		dir = parent
	}
}
