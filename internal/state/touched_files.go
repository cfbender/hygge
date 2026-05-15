package state

import (
	"path/filepath"
	"sort"
	"sync"
)

// TouchedFiles tracks absolute paths of files modified during the session.
// Paths are deduplicated; the set is safe for concurrent access.
type TouchedFiles struct {
	mu    sync.Mutex
	paths map[string]struct{}
}

// NewTouchedFiles returns an empty, ready-to-use TouchedFiles.
func NewTouchedFiles() *TouchedFiles {
	return &TouchedFiles{
		paths: make(map[string]struct{}),
	}
}

// Add registers absPath as touched.  If absPath is relative it is resolved
// against projectDir before storing.  Empty strings, bare ".", and paths that
// resolve to the project directory itself are silently ignored.
func (t *TouchedFiles) Add(absPath, projectDir string) {
	if absPath == "" || absPath == "." {
		return
	}
	p := absPath
	if !filepath.IsAbs(p) {
		if projectDir == "" {
			return
		}
		p = filepath.Join(projectDir, p)
	}
	// Ignore the project root itself (would come from a "." relative path
	// after resolution).
	if p == projectDir {
		return
	}
	t.mu.Lock()
	t.paths[p] = struct{}{}
	t.mu.Unlock()
}

// List returns all tracked paths, sorted lexicographically.
// The returned slice is a copy; callers may modify it freely.
func (t *TouchedFiles) List() []string {
	t.mu.Lock()
	out := make([]string, 0, len(t.paths))
	for p := range t.paths {
		out = append(out, p)
	}
	t.mu.Unlock()
	sort.Strings(out)
	return out
}

// Len returns the number of tracked paths.
func (t *TouchedFiles) Len() int {
	t.mu.Lock()
	n := len(t.paths)
	t.mu.Unlock()
	return n
}
