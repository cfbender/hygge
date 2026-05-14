package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkill writes a skill file with the given name into dir.  The
// frontmatter `name` equals the filename stem so the parser accepts it.
func writeSkill(t *testing.T, dir, name, description, whenToUse, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: " + name + "\ndescription: " + description +
		"\nwhen_to_use: " + whenToUse + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeHome builds a tempdir tree with the four discovery roots ready
// to be populated.  Returns home and pwd; xdgConfig is home/.config.
func fakeHome(t *testing.T) (home, pwd string) {
	t.Helper()
	home = t.TempDir()
	pwd = filepath.Join(home, "work", "project")
	if err := os.MkdirAll(pwd, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, pwd
}

func TestLoad_Empty(t *testing.T) {
	home, pwd := fakeHome(t)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() != 0 {
		t.Errorf("Len = %d, want 0", reg.Len())
	}
	if got := reg.All(); got != nil {
		t.Errorf("All = %v, want nil", got)
	}
}

func TestLoad_SingleUserAgentsSkill(t *testing.T) {
	home, pwd := fakeHome(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills"),
		"foo", "user-agents foo", "when-foo", "body foo")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("Len = %d, want 1", reg.Len())
	}
	sk, ok := reg.Get("foo")
	if !ok {
		t.Fatal("Get(foo): not found")
	}
	if sk.Description != "user-agents foo" {
		t.Errorf("Description = %q", sk.Description)
	}
	if sk.Source != SourceUserAgents {
		t.Errorf("Source = %v, want SourceUserAgents", sk.Source)
	}
}

func TestLoad_HyggeUserOverridesAgentsUser(t *testing.T) {
	home, pwd := fakeHome(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills"),
		"foo", "X", "wx", "body X")
	writeSkill(t, filepath.Join(home, ".config", "hygge", "skills"),
		"foo", "Y", "wy", "body Y")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("foo")
	if !ok {
		t.Fatal("Get(foo): not found")
	}
	if sk.Description != "Y" {
		t.Errorf("Description = %q, want Y (hygge overrides .agents)", sk.Description)
	}
	if sk.Source != SourceUserHygge {
		t.Errorf("Source = %v, want SourceUserHygge", sk.Source)
	}
}

func TestLoad_ProjectAgentsOverridesUserHygge(t *testing.T) {
	home, pwd := fakeHome(t)
	writeSkill(t, filepath.Join(home, ".config", "hygge", "skills"),
		"foo", "user-hygge", "w1", "b1")
	writeSkill(t, filepath.Join(pwd, ".agents", "skills"),
		"foo", "project-agents", "w2", "b2")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("foo")
	if !ok {
		t.Fatal("Get(foo): not found")
	}
	if sk.Description != "project-agents" {
		t.Errorf("Description = %q", sk.Description)
	}
	if sk.Source != SourceProjectAgents {
		t.Errorf("Source = %v, want SourceProjectAgents", sk.Source)
	}
}

func TestLoad_WalkUpFindsHyggeAtAncestor(t *testing.T) {
	home := t.TempDir()
	deep := filepath.Join(home, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// Put .hygge/skills/ at home/a, three levels above deep.
	writeSkill(t, filepath.Join(home, "a", ".hygge", "skills"),
		"foo", "from-ancestor", "w", "body")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: deep})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("foo")
	if !ok {
		t.Fatal("Get(foo): not found")
	}
	if sk.Source != SourceProjectHygge {
		t.Errorf("Source = %v, want SourceProjectHygge", sk.Source)
	}
}

func TestLoad_WalkUpStopsAtGit(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "monorepo")
	inner := filepath.Join(root, "service", "subdir")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	// Place a .git directory at root — the walk-up must stop here.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Put a skill ABOVE the .git boundary; it must NOT be loaded.
	writeSkill(t, filepath.Join(home, ".hygge", "skills"),
		"above", "above-git", "w", "b")
	// Put a skill INSIDE the .git boundary — at root — to confirm the
	// walk still finds files at the boundary itself.
	writeSkill(t, filepath.Join(root, ".hygge", "skills"),
		"inside", "inside-git", "w", "b")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: inner})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("above"); ok {
		t.Error("skill above .git boundary was loaded; walk-up should have halted")
	}
	if _, ok := reg.Get("inside"); !ok {
		t.Error("skill at .git boundary was not loaded; walk-up halted too early")
	}
}

func TestLoad_MalformedSkippedOthersLoaded(t *testing.T) {
	home, pwd := fakeHome(t)
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Write a malformed file (frontmatter never closes).
	bad := "---\nname: bad\ndescription: x\nwhen_to_use: y\n\nstill in frontmatter\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "good", "gd", "gu", "gb")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() != 1 {
		t.Errorf("Len = %d, want 1", reg.Len())
	}
	if _, ok := reg.Get("good"); !ok {
		t.Error("good skill was not loaded")
	}
}

func TestLoad_StemNameMismatchSkipped(t *testing.T) {
	home, pwd := fakeHome(t)
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// File is named `renamed.md` but frontmatter says `original`.
	body := "---\nname: original\ndescription: x\nwhen_to_use: y\n---\n"
	if err := os.WriteFile(filepath.Join(dir, "renamed.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "good", "gd", "gu", "gb")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("original"); ok {
		t.Error("stem-mismatched file was loaded")
	}
	if _, ok := reg.Get("good"); !ok {
		t.Error("good skill was not loaded alongside the skipped one")
	}
}

func TestLoad_NoFrontmatterSkipped(t *testing.T) {
	home, pwd := fakeHome(t)
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"),
		[]byte("# Just notes\nno frontmatter here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, dir, "good", "gd", "gu", "gb")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() != 1 {
		t.Errorf("Len = %d, want 1", reg.Len())
	}
}

func TestRegistry_AllSorted(t *testing.T) {
	home, pwd := fakeHome(t)
	dir := filepath.Join(home, ".agents", "skills")
	for _, name := range []string{"charlie", "alpha", "bravo"} {
		writeSkill(t, dir, name, "d-"+name, "w-"+name, "b-"+name)
	}
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	all := reg.All()
	want := []string{"alpha", "bravo", "charlie"}
	if len(all) != len(want) {
		t.Fatalf("len(All) = %d, want %d", len(all), len(want))
	}
	for i, sk := range all {
		if sk.Name != want[i] {
			t.Errorf("All[%d].Name = %q, want %q", i, sk.Name, want[i])
		}
	}
}

func TestRegistry_SourceValuesPerLayer(t *testing.T) {
	home, pwd := fakeHome(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills"),
		"ua", "d", "w", "b")
	writeSkill(t, filepath.Join(home, ".config", "hygge", "skills"),
		"uh", "d", "w", "b")
	writeSkill(t, filepath.Join(pwd, ".agents", "skills"),
		"pa", "d", "w", "b")
	writeSkill(t, filepath.Join(pwd, ".hygge", "skills"),
		"ph", "d", "w", "b")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := map[string]Source{
		"ua": SourceUserAgents,
		"uh": SourceUserHygge,
		"pa": SourceProjectAgents,
		"ph": SourceProjectHygge,
	}
	for name, want := range cases {
		sk, ok := reg.Get(name)
		if !ok {
			t.Errorf("Get(%s): not found", name)
			continue
		}
		if sk.Source != want {
			t.Errorf("Get(%s).Source = %v, want %v", name, sk.Source, want)
		}
	}
}

func TestSourceString(t *testing.T) {
	cases := map[Source]string{
		SourceUserAgents:    "user/.agents",
		SourceUserHygge:     "user/hygge",
		SourceProjectAgents: "project/.agents",
		SourceProjectHygge:  "project/hygge",
		Source(99):          "unknown(99)",
	}
	for src, want := range cases {
		if got := src.String(); got != want {
			t.Errorf("Source(%d).String() = %q, want %q", int(src), got, want)
		}
	}
}

func TestBuildSystemPromptAdditions_Empty(t *testing.T) {
	if got := BuildSystemPromptAdditions(nil); got != "" {
		t.Errorf("nil registry: got %q, want empty", got)
	}
	reg := &Registry{byName: map[string]Skill{}}
	if got := BuildSystemPromptAdditions(reg); got != "" {
		t.Errorf("empty registry: got %q, want empty", got)
	}
}

func TestBuildSystemPromptAdditions_Format(t *testing.T) {
	home, pwd := fakeHome(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills"),
		"alpha", "alpha desc", "alpha when", "alpha body")
	writeSkill(t, filepath.Join(home, ".agents", "skills"),
		"bravo", "bravo desc", "bravo when", "bravo body")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := BuildSystemPromptAdditions(reg)
	want := "## Available skills\n\n" +
		"Skills provide specialized instructions for specific tasks. Load a " +
		"skill by name via the `skill` tool when the task matches its description.\n\n" +
		"- alpha: alpha desc\n" +
		"  alpha when\n" +
		"- bravo: bravo desc\n" +
		"  bravo when"
	if got != want {
		t.Errorf("BuildSystemPromptAdditions mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestLoad_HomeDirFallback(t *testing.T) {
	// Empty HomeDir should fall back to os.UserHomeDir; we don't want to
	// touch the real home so we set HOME via t.Setenv.
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	reg, err := Load(LoadOptions{Pwd: tmp})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() != 0 {
		t.Errorf("Len = %d", reg.Len())
	}
}

func TestLoad_XDGConfigOverride(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	pwd := home
	writeSkill(t, filepath.Join(xdg, "hygge", "skills"),
		"xdg-foo", "from-xdg", "w", "b")

	reg, err := Load(LoadOptions{HomeDir: home, XDGConfigHome: xdg, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("xdg-foo")
	if !ok {
		t.Fatalf("Get(xdg-foo): not found; XDGConfigHome was not honoured")
	}
	if !strings.Contains(sk.Path, xdg) {
		t.Errorf("Path = %q; want it to contain %q", sk.Path, xdg)
	}
}
