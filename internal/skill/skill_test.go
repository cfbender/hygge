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

// writeDirSkill writes a directory-style skill (`.agents` standard):
// dir/<name>/SKILL.md with the given description (and no when_to_use,
// matching the convention in the wild).
func writeDirSkill(t *testing.T, dir, name, description, body string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := "---\nname: " + name + "\ndescription: " + description + "\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(contents), 0o600); err != nil {
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
	// Built-in skills (e.g. "hygge") are always present, so the registry
	// is never completely empty.  Verify at least the hygge built-in loaded.
	if _, ok := reg.Get("hygge"); !ok {
		t.Error("built-in skill 'hygge' not present in empty registry")
	}
	// All() must return a non-nil slice because built-ins are loaded.
	if got := reg.All(); len(got) == 0 {
		t.Error("All() returned empty slice; expected at least the built-in hygge skill")
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

func TestLoad_ClaudeCompatibleSkillDirs(t *testing.T) {
	home, pwd := fakeHome(t)
	writeDirSkill(t, filepath.Join(home, ".claude", "skills"),
		"user-claude", "from user claude", "body")
	writeDirSkill(t, filepath.Join(pwd, ".claude", "skills"),
		"project-claude", "from project claude", "body")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := map[string]Source{
		"user-claude":    SourceUserClaude,
		"project-claude": SourceProjectClaude,
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
	// The README.md (no frontmatter) must be skipped; only "good" user
	// skill + the built-in hygge skill should be present.
	if _, ok := reg.Get("good"); !ok {
		t.Error("good skill was not loaded")
	}
	if _, ok := reg.Get("README"); ok {
		t.Error("README.md was loaded as a skill, expected skip")
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
	// Built-ins (hygge) are always present; user skills alpha/bravo/charlie
	// must also appear and All() must be sorted ascending.
	wantContained := []string{"alpha", "bravo", "charlie", "hygge"}
	if len(all) < len(wantContained) {
		t.Fatalf("len(All) = %d, want >= %d", len(all), len(wantContained))
	}
	// Verify the four expected names are present and sorted relative to each other.
	nameSet := make(map[string]bool, len(all))
	for _, sk := range all {
		nameSet[sk.Name] = true
	}
	for _, name := range wantContained {
		if !nameSet[name] {
			t.Errorf("All() missing expected skill %q", name)
		}
	}
	// Verify the returned slice is globally sorted.
	for i := 1; i < len(all); i++ {
		if all[i].Name < all[i-1].Name {
			t.Errorf("All() not sorted: %q < %q at index %d", all[i].Name, all[i-1].Name, i)
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
		SourceBuiltin:       "builtin",
		SourceUserClaude:    "user/.claude",
		SourceUserAgents:    "user/.agents",
		SourceUserHygge:     "user/hygge",
		SourceProjectClaude: "project/.claude",
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
	for _, want := range []string{
		"Skills provide specialized instructions and workflows for specific tasks.",
		"Use the skill tool to load a skill when a task matches its description.",
		"<available_skills>",
		"<name>alpha</name>",
		"<description>alpha desc</description>",
		"<when_to_use>alpha when</when_to_use>",
		"<name>bravo</name>",
		"</available_skills>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("BuildSystemPromptAdditions missing %q.\n--- got ---\n%s", want, got)
		}
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
	// Built-in skills are always loaded regardless of the home dir.
	if _, ok := reg.Get("hygge"); !ok {
		t.Error("built-in hygge skill not present after home-dir fallback load")
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

// TestLoad_DirStyleSkill verifies the `.agents` standard directory
// layout (~/.agents/skills/<name>/SKILL.md) is discovered by Load.
// This is the layout used in the wild — every Claude-/.agents-style
// skill ships as a directory containing SKILL.md plus optional
// auxiliary files.
func TestLoad_DirStyleSkill(t *testing.T) {
	home, pwd := fakeHome(t)
	writeDirSkill(t, filepath.Join(home, ".agents", "skills"),
		"adapt", "Adapt designs across contexts.", "# Adapt\n\nbody.")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("adapt")
	if !ok {
		t.Fatalf("Get(adapt): not found; directory-style skill was not discovered")
	}
	wantDir := filepath.Join(home, ".agents", "skills", "adapt")
	if sk.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", sk.Dir, wantDir)
	}
	if !strings.HasSuffix(sk.Path, "SKILL.md") {
		t.Errorf("Path = %q, want it to end in SKILL.md", sk.Path)
	}
	if sk.WhenToUse != "" {
		t.Errorf("WhenToUse = %q, want empty for .agents-style skill", sk.WhenToUse)
	}
}

// TestLoad_BothLayoutsCoexist verifies that flat-style and directory-
// style skills in the same directory both load.
func TestLoad_BothLayoutsCoexist(t *testing.T) {
	home, pwd := fakeHome(t)
	skillsDir := filepath.Join(home, ".agents", "skills")
	writeSkill(t, skillsDir, "flat-one", "flat description", "when", "body")
	writeDirSkill(t, skillsDir, "dir-one", "dir description", "body")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("flat-one"); !ok {
		t.Error("flat-one not loaded")
	}
	if _, ok := reg.Get("dir-one"); !ok {
		t.Error("dir-one not loaded")
	}
	if _, ok := reg.Get("hygge"); !ok {
		t.Error("builtin hygge not loaded")
	}
	// flat-one + dir-one + the built-in hygge skill, plus any future builtins.
	if got := reg.Len(); got < 3 {
		t.Errorf("Len = %d, want >= 3 (flat-one + dir-one + builtin hygge)", got)
	}
}

// TestLoad_DirNameMismatchSkipped verifies that a directory-style
// skill whose directory name does not match the frontmatter `name` is
// skipped with a warning, not silently included.
func TestLoad_DirNameMismatchSkipped(t *testing.T) {
	home, pwd := fakeHome(t)
	skillsRoot := filepath.Join(home, ".agents", "skills")
	misnamed := filepath.Join(skillsRoot, "wrong-name")
	if err := os.MkdirAll(misnamed, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: original\ndescription: x\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(misnamed, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// The misnamed dir-style skill must be skipped; only the builtin hygge
	// skill should be present (it is always loaded).
	if _, ok := reg.Get("original"); ok {
		t.Error("dir-name-mismatched skill 'original' was loaded; expected skip")
	}
	if _, ok := reg.Get("wrong-name"); ok {
		t.Error("dir-name-mismatched skill 'wrong-name' was loaded; expected skip")
	}
	if _, ok := reg.Get("hygge"); !ok {
		t.Error("built-in hygge skill not present")
	}
}

// ---------------------------------------------------------------------------
// Built-in skill tests (HYGGE-11)
// ---------------------------------------------------------------------------

// TestLoad_BuiltinHyggeAlwaysPresent verifies the hygge built-in skill is
// discoverable in every fresh load, regardless of user / project paths.
func TestLoad_BuiltinHyggeAlwaysPresent(t *testing.T) {
	home, pwd := fakeHome(t)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("hygge")
	if !ok {
		t.Fatal("Get(hygge): not found; built-in skill must always be present")
	}
	if sk.Source != SourceBuiltin {
		t.Errorf("Source = %v, want SourceBuiltin", sk.Source)
	}
	if sk.Description == "" {
		t.Error("Description is empty")
	}
	if sk.WhenToUse == "" {
		t.Error("WhenToUse is empty")
	}
	if sk.Body == "" {
		t.Error("Body is empty")
	}
}

// TestLoad_BuiltinBodyContainsKeyTopics verifies the hygge built-in skill
// body covers the topics required by the spec.
func TestLoad_BuiltinBodyContainsKeyTopics(t *testing.T) {
	home, pwd := fakeHome(t)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("hygge")
	if !ok {
		t.Fatal("Get(hygge): not found")
	}
	requiredTopics := []string{
		"config.toml",     // config files
		"permissions",     // permissions
		"MCP",             // MCP servers
		"plugins",         // plugins
		"skills",          // skills
		"hooks",           // hooks
		"Troubleshooting", // troubleshooting section
	}
	for _, topic := range requiredTopics {
		if !strings.Contains(sk.Body, topic) {
			t.Errorf("Body missing required topic %q", topic)
		}
	}
}

// TestLoad_UserSkillOverridesBuiltin verifies that a user skill with the
// same name as a built-in skill replaces the built-in (higher priority).
func TestLoad_UserSkillOverridesBuiltin(t *testing.T) {
	home, pwd := fakeHome(t)
	writeSkill(t, filepath.Join(home, ".agents", "skills"),
		"hygge", "user-override description", "user-when", "user body")

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("hygge")
	if !ok {
		t.Fatal("Get(hygge): not found")
	}
	if sk.Description != "user-override description" {
		t.Errorf("Description = %q, want user-override description (user should override builtin)",
			sk.Description)
	}
	if sk.Source != SourceUserAgents {
		t.Errorf("Source = %v, want SourceUserAgents (user overrides builtin)", sk.Source)
	}
}

// TestLoad_BuiltinPath verifies the builtin hygge skill has no filesystem
// Path and uses only a virtual Dir token for diagnostics.
func TestLoad_BuiltinPath(t *testing.T) {
	home, pwd := fakeHome(t)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	sk, ok := reg.Get("hygge")
	if !ok {
		t.Fatal("Get(hygge): not found")
	}
	if sk.Path != "" {
		t.Errorf("Path = %q, want empty for embedded builtin", sk.Path)
	}
	if sk.Dir != "builtin" {
		t.Errorf("Dir = %q, want \"builtin\"", sk.Dir)
	}
}
