package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeProject builds a tempdir tree with a fake $HOME and a project
// living three levels below it.  Returns home, pwd, and the project
// root dir.
func makeProject(t *testing.T) (home, pwd, root string) {
	t.Helper()
	home = t.TempDir()
	root = filepath.Join(home, "work", "project")
	pwd = filepath.Join(root, "service", "subdir")
	if err := os.MkdirAll(pwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mark the project root with .git so findProjectRoot has a marker.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home, pwd, root
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil { //nolint:gosec // test fixture, path is t.TempDir-rooted
		t.Fatal(err)
	}
}

func TestLoad_NoneFound(t *testing.T) {
	home, pwd, _ := makeProject(t)
	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 0 {
		t.Errorf("len(blocks) = %d, want 0", len(blocks))
	}
}

func TestLoad_OnlyUserAgents(t *testing.T) {
	home, pwd, _ := makeProject(t)
	writeFile(t, filepath.Join(home, ".agents", "AGENTS.md"), "user-agents body")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Source != SourceUserAgents {
		t.Errorf("Source = %v, want SourceUserAgents", blocks[0].Source)
	}
	if blocks[0].Content != "user-agents body" {
		t.Errorf("Content = %q", blocks[0].Content)
	}
}

func TestLoad_ProjectRootSibling(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "project root body")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Source != SourceProjectRoot {
		t.Errorf("Source = %v, want SourceProjectRoot", blocks[0].Source)
	}
}

func TestLoad_BothProjectLayers(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "project .agents body")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "project root body")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2", len(blocks))
	}
	// Precedence order: project/.agents before project/root.
	if blocks[0].Source != SourceProjectAgents {
		t.Errorf("blocks[0].Source = %v, want SourceProjectAgents", blocks[0].Source)
	}
	if blocks[1].Source != SourceProjectRoot {
		t.Errorf("blocks[1].Source = %v, want SourceProjectRoot", blocks[1].Source)
	}
}

func TestLoad_AboveGitNotLoaded(t *testing.T) {
	home, pwd, _ := makeProject(t)
	// Place an AGENTS.md ABOVE the .git boundary — at $HOME/work/.
	// findProjectRoot should stop at the project root (which has .git)
	// and not consider this file.
	writeFile(t, filepath.Join(home, "work", "AGENTS.md"), "above-git body")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, b := range blocks {
		if strings.Contains(b.Path, filepath.Join(home, "work", "AGENTS.md")) {
			t.Errorf("file above .git boundary was loaded: %s", b.Path)
		}
	}
}

func TestLoad_EmptyFileStillReturned(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Content != "" {
		t.Errorf("Content = %q, want empty", blocks[0].Content)
	}
}

func TestLoad_AllFourLayers(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(home, ".agents", "AGENTS.md"), "user-agents")
	writeFile(t, filepath.Join(home, ".config", "hygge", "AGENTS.md"), "user-hygge")
	writeFile(t, filepath.Join(root, ".agents", "AGENTS.md"), "project-agents")
	writeFile(t, filepath.Join(root, "AGENTS.md"), "project-root")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 4 {
		t.Fatalf("len(blocks) = %d, want 4", len(blocks))
	}
	wantOrder := []Source{
		SourceUserAgents,
		SourceUserHygge,
		SourceProjectAgents,
		SourceProjectRoot,
	}
	for i, want := range wantOrder {
		if blocks[i].Source != want {
			t.Errorf("blocks[%d].Source = %v, want %v", i, blocks[i].Source, want)
		}
	}
}

func TestBuildSystemPromptAdditions_Empty(t *testing.T) {
	if got := BuildSystemPromptAdditions(nil); got != "" {
		t.Errorf("nil: %q", got)
	}
	if got := BuildSystemPromptAdditions([]Block{}); got != "" {
		t.Errorf("empty: %q", got)
	}
}

func TestBuildSystemPromptAdditions_TwoBlocks(t *testing.T) {
	blocks := []Block{
		{Path: "/home/u/.agents/AGENTS.md", Source: SourceUserAgents, Content: "user rules"},
		{Path: "/proj/AGENTS.md", Source: SourceProjectRoot, Content: "project rules"},
	}
	got := BuildSystemPromptAdditions(blocks)
	if !strings.HasPrefix(got, "## Project context") {
		t.Errorf("missing header in: %q", got)
	}
	if !strings.Contains(got, "user rules") {
		t.Error("missing first content body")
	}
	if !strings.Contains(got, "project rules") {
		t.Error("missing second content body")
	}
	if !strings.Contains(got, "\n---\n") {
		t.Error("missing separator between blocks")
	}
	if !strings.Contains(got, "/home/u/.agents/AGENTS.md") {
		t.Error("missing first source path in comment")
	}
	if !strings.Contains(got, "/proj/AGENTS.md") {
		t.Error("missing second source path in comment")
	}
	if !strings.Contains(got, "<!-- source: user/.agents:") {
		t.Error("missing user/.agents source token")
	}
}

func TestSourceString(t *testing.T) {
	cases := map[Source]string{
		SourceUserAgents:    "user/.agents",
		SourceUserHygge:     "user/hygge",
		SourceUserClaude:    "user/.claude",
		SourceProjectAgents: "project/.agents",
		SourceProjectRoot:   "project/root",
		SourceProjectSubdir: "project/subdir",
		Source(99):          "unknown(99)",
	}
	for src, want := range cases {
		if got := src.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", int(src), got, want)
		}
	}
}

// TestLoad_UserClaude verifies that ~/.claude/CLAUDE.md is picked up
// at the user-level layer.
func TestLoad_UserClaude(t *testing.T) {
	home, pwd, _ := makeProject(t)
	writeFile(t, filepath.Join(home, ".claude", "CLAUDE.md"), "user-level claude rules")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Source != SourceUserClaude {
		t.Errorf("Source = %v, want SourceUserClaude", blocks[0].Source)
	}
}

// TestLoad_ProjectClaudeAndLocal verifies that <root>/CLAUDE.md and
// <root>/CLAUDE.local.md both load as SourceProjectRoot alongside
// AGENTS.md.
func TestLoad_ProjectClaudeAndLocal(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "agents body")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "claude body")
	writeFile(t, filepath.Join(root, "CLAUDE.local.md"), "local override body")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 3 {
		t.Fatalf("len(blocks) = %d, want 3", len(blocks))
	}
	for _, b := range blocks {
		if b.Source != SourceProjectRoot {
			t.Errorf("block %q: Source = %v, want SourceProjectRoot", b.Path, b.Source)
		}
	}
}

// TestLoad_RecursiveSubdirAgents verifies that AGENTS.md files in
// project subdirectories are picked up.
func TestLoad_RecursiveSubdirAgents(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "internal", "skill", "AGENTS.md"), "skill subdir context")
	writeFile(t, filepath.Join(root, "cmd", "hygge", "AGENTS.md"), "cmd subdir context")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 2 {
		t.Fatalf("len(blocks) = %d, want 2; got %+v", len(blocks), blocks)
	}
	for _, b := range blocks {
		if b.Source != SourceProjectSubdir {
			t.Errorf("block %q: Source = %v, want SourceProjectSubdir", b.Path, b.Source)
		}
		if b.RelPath == "" {
			t.Errorf("block %q: RelPath empty", b.Path)
		}
	}
	// Order: by RelPath ascending.  "cmd/..." < "internal/..."
	if !strings.HasPrefix(blocks[0].RelPath, "cmd") {
		t.Errorf("blocks[0].RelPath = %q, want cmd/... first", blocks[0].RelPath)
	}
}

// TestLoad_RecursiveSubdirClaude verifies CLAUDE.md is also picked up
// during recursive descent.
func TestLoad_RecursiveSubdirClaude(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "internal", "CLAUDE.md"), "claude in subdir")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Source != SourceProjectSubdir {
		t.Errorf("Source = %v, want SourceProjectSubdir", blocks[0].Source)
	}
}

// TestLoad_RecursiveSkipsExcludedDirs verifies that node_modules /
// .git / .agents / .hygge subtrees are pruned from the recursive walk.
func TestLoad_RecursiveSkipsExcludedDirs(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "node_modules", "pkg", "AGENTS.md"), "should not load")
	writeFile(t, filepath.Join(root, ".hygge", "skills", "x", "AGENTS.md"), "should not load")
	writeFile(t, filepath.Join(root, "vendor", "sub", "AGENTS.md"), "should not load")
	writeFile(t, filepath.Join(root, "internal", "AGENTS.md"), "should load")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, b := range blocks {
		if strings.Contains(b.Path, "node_modules") ||
			strings.Contains(b.Path, "vendor") ||
			strings.Contains(b.Path, ".hygge") {
			t.Errorf("excluded path was loaded: %s", b.Path)
		}
	}
	// At least the internal/AGENTS.md should appear.
	found := false
	for _, b := range blocks {
		if strings.HasSuffix(b.Path, filepath.Join("internal", "AGENTS.md")) {
			found = true
		}
	}
	if !found {
		t.Error("internal/AGENTS.md was not loaded")
	}
}

// TestLoad_RootFilesNotDoubleLoaded verifies that AGENTS.md / CLAUDE.md
// at the project root are loaded as SourceProjectRoot ONCE and not
// re-added by the recursive descent.
func TestLoad_RootFilesNotDoubleLoaded(t *testing.T) {
	home, pwd, root := makeProject(t)
	writeFile(t, filepath.Join(root, "AGENTS.md"), "root agents")
	writeFile(t, filepath.Join(root, "CLAUDE.md"), "root claude")

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rootCount := 0
	subdirCount := 0
	for _, b := range blocks {
		switch b.Source {
		case SourceProjectRoot:
			rootCount++
		case SourceProjectSubdir:
			subdirCount++
		}
	}
	if rootCount != 2 {
		t.Errorf("rootCount = %d, want 2 (AGENTS.md + CLAUDE.md)", rootCount)
	}
	if subdirCount != 0 {
		t.Errorf("subdirCount = %d, want 0 (no double-load)", subdirCount)
	}
}

func TestLoad_TestdataFixture(t *testing.T) {
	// Sanity: the bundled testdata file parses through Load when planted
	// in a fake home.
	home := t.TempDir()
	pwd := home
	src, err := os.ReadFile(filepath.Join("testdata", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, ".agents", "AGENTS.md"), string(src))

	blocks, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Content == "" {
		t.Error("Content empty")
	}
}
