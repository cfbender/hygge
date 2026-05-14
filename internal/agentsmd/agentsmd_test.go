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
		SourceProjectAgents: "project/.agents",
		SourceProjectRoot:   "project/root",
		Source(99):          "unknown(99)",
	}
	for src, want := range cases {
		if got := src.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", int(src), got, want)
		}
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
