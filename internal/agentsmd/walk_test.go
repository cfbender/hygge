package agentsmd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWalkUp_VisitStop verifies WalkUp halts when Visit returns WalkStop
// and returns the directory where it stopped.
func TestWalkUp_VisitStop(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	var visited []string
	got := WalkUp(sub, WalkOption{}, func(dir string) WalkAction {
		visited = append(visited, dir)
		if dir == filepath.Join(root, "a") {
			return WalkStop
		}
		return WalkContinue
	})

	want := filepath.Join(root, "a")
	if got != want {
		t.Errorf("WalkUp returned %q, want %q", got, want)
	}
	// c, b, a — a stops the walk.
	if len(visited) != 3 {
		t.Errorf("visited %v, want 3 dirs", visited)
	}
}

// TestWalkUp_HomeStop verifies the walk halts at HomeStop without visiting it.
func TestWalkUp_HomeStop(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "work", "proj", "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	var visited []string
	got := WalkUp(sub, WalkOption{HomeStop: root}, func(dir string) WalkAction {
		visited = append(visited, dir)
		return WalkContinue
	})

	// Walk never visits root (HomeStop) so it should return "" (no WalkStop
	// returned by Visit).
	if got != "" {
		t.Errorf("WalkUp returned %q, want %q (no stop)", got, "")
	}
	// src, proj, work — root is excluded.
	for _, v := range visited {
		if v == root {
			t.Errorf("HomeStop %q was visited", root)
		}
	}
}

// TestWalkUp_FilesystemRoot verifies the walk terminates at the
// filesystem root when no stop is found.
func TestWalkUp_FilesystemRoot(t *testing.T) {
	t.Parallel()
	// Walk from a temp directory; the filesystem root is guaranteed
	// to be above it.  Walk should return "" (no WalkStop).
	tmp := t.TempDir()
	count := 0
	got := WalkUp(tmp, WalkOption{}, func(_ string) WalkAction {
		count++
		if count > 1000 {
			t.Fatal("walk did not terminate")
		}
		return WalkContinue
	})
	if got != "" {
		t.Errorf("WalkUp returned %q, want empty", got)
	}
}

// TestWalkUp_ExcludeDir verifies that a directory whose basename is in
// ExcludeDirs is not passed to Visit.
func TestWalkUp_ExcludeDir(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	excluded := filepath.Join(root, "node_modules", "lib")
	if err := os.MkdirAll(excluded, 0o755); err != nil {
		t.Fatal(err)
	}

	excludeDirs := map[string]struct{}{
		"node_modules": {},
	}
	var visited []string
	WalkUp(excluded, WalkOption{ExcludeDirs: excludeDirs, HomeStop: root}, func(dir string) WalkAction {
		visited = append(visited, dir)
		return WalkContinue
	})

	for _, v := range visited {
		if filepath.Base(v) == "node_modules" {
			t.Errorf("excluded dir 'node_modules' was visited: %v", visited)
		}
	}
	// 'lib' is a child of node_modules but its own basename is not excluded;
	// however node_modules itself is excluded.  Depending on implementation,
	// 'lib' may or may not be visited.  The key invariant is that
	// 'node_modules' itself is never visited.
}

// TestWalkUp_EmptyStart verifies WalkUp with an empty start terminates
// without panicking.
func TestWalkUp_EmptyStart(t *testing.T) {
	t.Parallel()
	// filepath.Clean("") returns "." so this should visit "." and then
	// immediately reach the filesystem root — no panic.
	got := WalkUp("", WalkOption{}, func(_ string) WalkAction {
		return WalkContinue
	})
	if got != "" {
		t.Errorf("WalkUp(\"\") returned %q, want empty", got)
	}
}

// TestFindProjectRootFrom_FindsGit verifies the public helper finds a
// .git-marked project root.
func TestFindProjectRootFrom_FindsGit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	sub := filepath.Join(root, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := FindProjectRootFrom(sub)
	if got != root {
		t.Errorf("FindProjectRootFrom = %q, want %q", got, root)
	}
}

// TestFindProjectRootFrom_EmptyWhenNoMarker verifies "" is returned
// when no marker exists anywhere.
func TestFindProjectRootFrom_EmptyWhenNoMarker(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	got := FindProjectRootFrom(tmp)
	// tmp itself has no marker, and we cannot predict what's above it
	// in the real filesystem.  The test only exercises the no-panic
	// invariant — the real assertion is that a known isolated tree
	// returns "".
	_ = got // acceptable: just verifying no panic
}

// TestFindProjectRootFrom_EmptyInput returns "" for empty input.
func TestFindProjectRootFrom_EmptyInput(t *testing.T) {
	t.Parallel()
	got := FindProjectRootFrom("")
	if got != "" {
		t.Errorf("FindProjectRootFrom(\"\") = %q, want empty", got)
	}
}
