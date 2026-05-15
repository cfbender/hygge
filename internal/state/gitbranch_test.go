package state

import (
	"os"
	"path/filepath"
	"testing"
)

// writeGitHEAD creates a minimal .git directory with a HEAD file under dir.
func writeGitHEAD(t *testing.T, dir, content string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(content), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
}

func TestReadGitBranch_SymbolicRef(t *testing.T) {
	dir := t.TempDir()
	writeGitHEAD(t, dir, "ref: refs/heads/main\n")
	got := readGitBranch(dir)
	if got != "main" {
		t.Errorf("got %q, want %q", got, "main")
	}
}

func TestReadGitBranch_FeatureBranch(t *testing.T) {
	dir := t.TempDir()
	writeGitHEAD(t, dir, "ref: refs/heads/feat/bubble-ui\n")
	got := readGitBranch(dir)
	if got != "feat/bubble-ui" {
		t.Errorf("got %q, want %q", got, "feat/bubble-ui")
	}
}

func TestReadGitBranch_DetachedHEAD(t *testing.T) {
	dir := t.TempDir()
	writeGitHEAD(t, dir, "a1b2c3d4e5f6g7h8\n")
	got := readGitBranch(dir)
	if got != "@a1b2c3d" {
		t.Errorf("got %q, want %q", got, "@a1b2c3d")
	}
}

func TestReadGitBranch_NoGitDir(t *testing.T) {
	dir := t.TempDir()
	got := readGitBranch(dir)
	if got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestReadGitBranch_WalksUp(t *testing.T) {
	// .git lives at the parent; the project path is a subdirectory.
	parent := t.TempDir()
	writeGitHEAD(t, parent, "ref: refs/heads/develop\n")
	sub := filepath.Join(parent, "subdir", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	got := readGitBranch(sub)
	if got != "develop" {
		t.Errorf("got %q, want %q", got, "develop")
	}
}

func TestReadGitBranch_EmptyHEAD(t *testing.T) {
	dir := t.TempDir()
	writeGitHEAD(t, dir, "")
	got := readGitBranch(dir)
	// Empty file: content after trim is "", less than 7 chars, returns "@"
	if got != "@" {
		t.Errorf("got %q, want %q", got, "@")
	}
}

func TestGitBranch_CachesResult(t *testing.T) {
	// Clear the cache before the test to avoid cross-test pollution.
	branchCacheMu.Lock()
	delete(branchCache, "")
	branchCacheMu.Unlock()

	dir := t.TempDir()
	writeGitHEAD(t, dir, "ref: refs/heads/cached\n")

	first := GitBranch(dir)
	if first != "cached" {
		t.Fatalf("first call: got %q, want %q", first, "cached")
	}

	// Overwrite HEAD — the cached result should still be returned.
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/changed\n"), 0o644); err != nil {
		t.Fatalf("overwrite HEAD: %v", err)
	}

	second := GitBranch(dir)
	if second != "cached" {
		t.Errorf("second call (should use cache): got %q, want %q", second, "cached")
	}
}
