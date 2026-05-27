package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	appstate "github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/ui/styles"
)

// writeGitHEADForUI creates a minimal .git/HEAD inside dir for UI-layer tests.
func writeGitHEADForUI(t *testing.T, dir, content string) {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte(content), 0o644); err != nil {
		t.Fatalf("write HEAD: %v", err)
	}
}

// newBranchTestApp creates an App whose ProjectDir points at a real temp
// directory so gitBranch() can read .git/HEAD.  Returns the app, the bus, and
// the temp dir path.
func newBranchTestApp(t *testing.T, projectDir string) (*App, *bus.Bus) {
	t.Helper()
	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         styles.DefaultTheme(),
		ProjectDir:    projectDir,
		// Set HomeDir equal to ProjectDir so collapsedProjectPath() returns "~",
		// keeping the sidebar path short enough for branch display in view tests.
		HomeDir:       projectDir,
		ModelProvider: "provider-placeholder",
		ModelName:     "model-placeholder",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, b
}

// TestGitBranch_SendStartedInvalidatesCache verifies that sending a user
// message (sendStarted event) causes the sidebar to pick up a branch change
// rather than returning a stale cached value.
func TestGitBranch_SendStartedInvalidatesCache(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGitHEADForUI(t, dir, "ref: refs/heads/main\n")

	app, _ := newBranchTestApp(t, dir)

	// Warm the cache: first gitBranch() call populates it.
	first := app.gitBranch()
	if first != "main" {
		t.Fatalf("initial branch: got %q, want %q", first, "main")
	}

	// Simulate a git checkout while the app is running.
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644); err != nil {
		t.Fatalf("update HEAD: %v", err)
	}

	// Without any event, the cache is stale.
	cached := app.gitBranch()
	if cached != "main" {
		t.Errorf("before sendStarted: expected cached value %q, got %q", "main", cached)
	}

	// Sending a user message fires sendStarted which invalidates the cache.
	app.Update(sendStarted{UserInput: "hello", StartedAt: app.opts.Now()})

	// Now gitBranch() should return the fresh branch.
	fresh := app.gitBranch()
	if fresh != "feature" {
		t.Errorf("after sendStarted: got %q, want %q", fresh, "feature")
	}
}

// TestGitBranch_ViewContainsBranchAfterInvalidation verifies that the rendered
// sidebar contains the updated branch name after a sendStarted invalidation.
func TestGitBranch_ViewContainsBranchAfterInvalidation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGitHEADForUI(t, dir, "ref: refs/heads/initial\n")

	app, _ := newBranchTestApp(t, dir)

	// Force cache for initial branch.
	appstate.InvalidateBranchCache(dir)
	_ = app.gitBranch() // populates cache with "initial"

	// Simulate git checkout.
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/switched\n"), 0o644); err != nil {
		t.Fatalf("update HEAD: %v", err)
	}

	// Pre-sendStarted: sidebar should show "initial" (cached), not the
	// branch now on disk.
	viewBefore := app.View().Content
	plainBefore := ansiEscapeRE.ReplaceAllString(strings.ReplaceAll(viewBefore, "\r", ""), "")
	if !strings.Contains(plainBefore, "initial") {
		t.Errorf("sidebar before sendStarted: expected cached branch %q in view; got:\n%s", "initial", plainBefore)
	}
	if strings.Contains(plainBefore, "switched") {
		t.Errorf("sidebar before sendStarted should not show branch %q before invalidation; got:\n%s", "switched", plainBefore)
	}

	// Trigger sendStarted to invalidate.
	app.Update(sendStarted{UserInput: "hello", StartedAt: app.opts.Now()})

	// Re-render: branch cache is now fresh; View pulls gitBranch() which reads disk.
	viewAfter := app.View().Content
	plainAfter := strings.ReplaceAll(viewAfter, "\r", "")
	// Strip ANSI for plain-text assertion.
	plainAfter = ansiEscapeRE.ReplaceAllString(plainAfter, "")
	if !strings.Contains(plainAfter, "switched") {
		t.Errorf("sidebar after sendStarted: expected branch %q in view; got:\n%s", "switched", plainAfter)
	}
}

// TestGitBranch_NonGitDirUnaffected verifies that invalidation and re-read
// in a non-git directory does not panic and still returns "".
func TestGitBranch_NonGitDirUnaffected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir() // no .git directory

	app, _ := newBranchTestApp(t, dir)

	// Warm cache with "".
	first := app.gitBranch()
	if first != "" {
		t.Fatalf("non-git dir: got %q, want empty", first)
	}

	// Trigger invalidation via sendStarted: should not panic.
	app.Update(sendStarted{UserInput: "test", StartedAt: app.opts.Now()})

	after := app.gitBranch()
	if after != "" {
		t.Errorf("non-git dir after invalidation: got %q, want empty", after)
	}
}

// TestGitBranch_DetachedHEADUnaffected verifies that detached HEAD format is
// preserved through an invalidate/re-read cycle.
func TestGitBranch_DetachedHEADUnaffected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGitHEADForUI(t, dir, "a1b2c3d4e5f6g7h8\n") // detached HEAD

	app, _ := newBranchTestApp(t, dir)

	// Warm cache.
	appstate.InvalidateBranchCache(dir)
	first := app.gitBranch()
	if first != "@a1b2c3d" {
		t.Fatalf("detached HEAD: got %q, want %q", first, "@a1b2c3d")
	}

	// Trigger sendStarted invalidation; re-read should still return the detached SHA.
	app.Update(sendStarted{UserInput: "test", StartedAt: app.opts.Now()})

	second := app.gitBranch()
	if second != "@a1b2c3d" {
		t.Errorf("detached HEAD after invalidation: got %q, want %q", second, "@a1b2c3d")
	}
}
