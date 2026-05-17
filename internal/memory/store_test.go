package memory

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/session"
)

func TestFileStoreRememberProjectMemoryRoundTrip(t *testing.T) {
	projectDir := t.TempDir()
	now := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	st := NewFileStore(FileStoreOptions{ProjectDir: projectDir, HomeDir: t.TempDir(), Now: func() time.Time { return now }})

	mem, err := st.Remember(context.Background(), session.MemoryScopeProject, "Use mise run precommit before final status")
	if err != nil {
		t.Fatalf("Remember: %v", err)
	}
	wantPath := filepath.Join(projectDir, ".hygge", "memory", "use-mise-run-precommit-before-final-status.md")
	if mem.Path != wantPath {
		t.Fatalf("path = %q, want %q", mem.Path, wantPath)
	}

	got, err := st.List(context.Background(), session.MemoryScopeProject)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Scope != session.MemoryScopeProject || got[0].Title != "Use mise run precommit before final status" || got[0].Body != "Use mise run precommit before final status" {
		t.Fatalf("memories = %+v", got)
	}
}

func TestProjectMemoryGitignoreWarningWhenHyggeNotIgnored(t *testing.T) {
	projectDir := initGitRepo(t)
	st := NewFileStore(FileStoreOptions{ProjectDir: projectDir, HomeDir: t.TempDir()})
	warning, err := st.ProjectMemoryGitignoreWarning(context.Background())
	if err != nil {
		t.Fatalf("ProjectMemoryGitignoreWarning: %v", err)
	}
	if !strings.Contains(warning, ".hygge/ is not ignored") {
		t.Fatalf("warning = %q, want .hygge guidance", warning)
	}
}

func TestProjectMemoryGitignoreWarningSkippedWhenIgnoredOrNotFirst(t *testing.T) {
	projectDir := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(projectDir, ".gitignore"), []byte(".hygge/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := NewFileStore(FileStoreOptions{ProjectDir: projectDir, HomeDir: t.TempDir()})
	warning, err := st.ProjectMemoryGitignoreWarning(context.Background())
	if err != nil {
		t.Fatalf("ProjectMemoryGitignoreWarning ignored: %v", err)
	}
	if warning != "" {
		t.Fatalf("warning = %q, want none when ignored", warning)
	}

	projectDir = initGitRepo(t)
	st = NewFileStore(FileStoreOptions{ProjectDir: projectDir, HomeDir: t.TempDir()})
	if _, err := st.Remember(context.Background(), session.MemoryScopeProject, "Existing memory"); err != nil {
		t.Fatalf("Remember: %v", err)
	}
	warning, err = st.ProjectMemoryGitignoreWarning(context.Background())
	if err != nil {
		t.Fatalf("ProjectMemoryGitignoreWarning existing: %v", err)
	}
	if warning != "" {
		t.Fatalf("warning = %q, want none after first memory", warning)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestFileStoreListMemoriesOrdersGlobalProject(t *testing.T) {
	projectDir := t.TempDir()
	xdg := t.TempDir()
	st := NewFileStore(FileStoreOptions{ProjectDir: projectDir, XDGConfigHome: xdg})
	if _, err := st.Remember(context.Background(), session.MemoryScopeProject, "Project preference"); err != nil {
		t.Fatalf("Remember project: %v", err)
	}
	if _, err := st.Remember(context.Background(), session.MemoryScopeGlobal, "Global preference"); err != nil {
		t.Fatalf("Remember global: %v", err)
	}

	got, err := st.ListMemories(context.Background())
	if err != nil {
		t.Fatalf("ListMemories: %v", err)
	}
	if len(got) != 2 || got[0].Scope != session.MemoryScopeGlobal || got[1].Scope != session.MemoryScopeProject {
		t.Fatalf("memory order = %+v, want global then project", got)
	}
	if !strings.Contains(got[0].Path, filepath.Join(xdg, "hygge", "memory")) {
		t.Fatalf("global path = %q", got[0].Path)
	}
}

func TestFileStoreRememberRejectsSecrets(t *testing.T) {
	st := NewFileStore(FileStoreOptions{ProjectDir: t.TempDir(), HomeDir: t.TempDir()})
	_, err := st.Remember(context.Background(), session.MemoryScopeProject, "password=super-secret")
	if !errors.Is(err, ErrSecret) {
		t.Fatalf("err = %v, want ErrSecret", err)
	}
}

func TestFileStoreRememberAddsCollisionSuffix(t *testing.T) {
	st := NewFileStore(FileStoreOptions{ProjectDir: t.TempDir(), HomeDir: t.TempDir()})
	first, err := st.Remember(context.Background(), session.MemoryScopeProject, "Same title")
	if err != nil {
		t.Fatalf("Remember first: %v", err)
	}
	second, err := st.Remember(context.Background(), session.MemoryScopeProject, "Same title")
	if err != nil {
		t.Fatalf("Remember second: %v", err)
	}
	if first.Path == second.Path || !strings.HasSuffix(second.Path, "same-title-2.md") {
		t.Fatalf("paths first=%q second=%q, want collision suffix", first.Path, second.Path)
	}
}
