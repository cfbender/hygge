package command

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoadBuiltinsAlwaysPresent(t *testing.T) {
	t.Parallel()
	reg, err := Load(LoadOptions{HomeDir: t.TempDir(), Pwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, name := range []string{"help", "clear", "compact", "cost", "sessions", "memory", "fork", "model", "reason", "remember", "forget", "version"} {
		c, ok := reg.Get(name)
		if !ok {
			t.Errorf("missing built-in %s", name)
			continue
		}
		if c.Source() != "builtin" {
			t.Errorf("%s source = %q, want builtin", name, c.Source())
		}
	}
}

func TestLoadUserLayerAddsTemplateCommand(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
[commands.review]
description = "Review code"
prompt = "Review:\n\n{{code}}"
args = [
  { name = "code", description = "code to review", required = true },
]
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, ok := reg.Get("review")
	if !ok {
		t.Fatal("missing /review")
	}
	if cmd.Source() != "user" {
		t.Errorf("source = %q, want user", cmd.Source())
	}
	out, err := cmd.Execute(context.Background(), nil, "x := 1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out.Message, "x := 1") {
		t.Errorf("message missing code: %q", out.Message)
	}
}

func TestLoadProjectOverridesUser(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
[commands.brief]
description = "user-level"
prompt = "USER: {{tail}}"
`)
	writeFile(t, filepath.Join(pwd, ".agents", "commands.toml"), `
[commands.brief]
description = "project-level"
prompt = "PROJECT: {{tail}}"
`)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, ok := reg.Get("brief")
	if !ok {
		t.Fatal("missing /brief")
	}
	if cmd.Source() != "project" {
		t.Errorf("source = %q, want project", cmd.Source())
	}
	if cmd.Description() != "project-level" {
		t.Errorf("description = %q, want project-level", cmd.Description())
	}
}

func TestLoadHyggeOverridesAgents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	pwd := t.TempDir()
	writeFile(t, filepath.Join(pwd, ".agents", "commands.toml"), `
[commands.review]
description = "agents-level"
prompt = "agents"
`)
	writeFile(t, filepath.Join(pwd, ".hygge", "commands.toml"), `
[commands.review]
description = "hygge-level"
prompt = "hygge"
`)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, _ := reg.Get("review")
	if cmd.Description() != "hygge-level" {
		t.Errorf(".hygge should override .agents at same project layer, got %q", cmd.Description())
	}
}

func TestLoadCanOverrideBuiltin(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
[commands.compact]
description = "user compact"
prompt = "custom compact prompt"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, _ := reg.Get("compact")
	if cmd.Source() != "user" {
		t.Errorf("expected override to flip source to user, got %q", cmd.Source())
	}
}

func TestLoadXDGConfigPath(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := t.TempDir()
	writeFile(t, filepath.Join(xdg, "hygge", "commands.toml"), `
[commands.explain]
description = "Explain a concept"
prompt = "Explain {{topic}}"
args = [{ name = "topic", required = true }]
`)
	reg, err := Load(LoadOptions{HomeDir: home, XDGConfigHome: xdg})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("explain"); !ok {
		t.Error("XDG-path command not loaded")
	}
}

func TestLoadMalformedFileWarnsButContinues(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
not = valid TOML for our schema
[commands.broken
`)
	// Even though parse fails, built-ins must still load.
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("help"); !ok {
		t.Error("built-ins should still load after malformed file")
	}
}

func TestLoadInvalidEntrySkippedWarns(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
[commands.good]
description = "ok"
prompt = "hi"

[commands.BAD]
description = "uppercase name not allowed"
prompt = "hi"

[commands.noprompt]
description = "missing prompt"

[commands.tail-reserved]
description = "uses reserved arg name"
prompt = "{{tail}}"
args = [{ name = "tail" }]
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("good"); !ok {
		t.Error("good should load")
	}
	if _, ok := reg.Get("BAD"); ok {
		t.Error("BAD should NOT load (invalid name)")
	}
	if _, ok := reg.Get("noprompt"); ok {
		t.Error("noprompt should NOT load (missing prompt)")
	}
	if _, ok := reg.Get("tail-reserved"); ok {
		t.Error("tail-reserved should NOT load (reserved arg name)")
	}
}

func TestLoadUnknownPlaceholderWarns(t *testing.T) {
	// We can't easily assert slog Warn here without setting up a
	// handler, but the command should still load and substitute
	// the known name + leave the unknown literal.
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
[commands.weird]
description = "weird"
prompt = "have {{topic}} and {{ghost}}"
args = [{ name = "topic", required = true }]
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cmd, ok := reg.Get("weird")
	if !ok {
		t.Fatal("weird should still load")
	}
	out, _ := cmd.Execute(context.Background(), nil, "monads")
	if !strings.Contains(out.Message, "have monads and {{ghost}}") {
		t.Errorf("unknown placeholder should remain literal; got %q", out.Message)
	}
}

func TestLoadHelpRegistryAttached(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
[commands.review]
description = "Review code"
prompt = "Review {{tail}}"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { AttachHelpRegistry(nil) })

	cmd, _ := reg.Get("help")
	out, _ := cmd.Execute(context.Background(), nil, "")
	if !strings.Contains(out.Notice, "/review") {
		t.Errorf("/help should list the TOML-loaded /review:\n%s", out.Notice)
	}
}

func TestLoadProjectWalkUpStopsAtGit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	// project root has .git
	proj := filepath.Join(home, "work", "proj")
	if err := os.MkdirAll(filepath.Join(proj, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// commands.toml deeper inside (under proj)
	writeFile(t, filepath.Join(proj, ".hygge", "commands.toml"), `
[commands.proj]
description = "from project"
prompt = "p"
`)
	// nested working dir
	pwd := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(pwd, 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("proj"); !ok {
		t.Error("project commands.toml should be discovered via walk-up")
	}
}

func TestLoadFromNilOptionsUsesHome(t *testing.T) {
	t.Parallel()
	// HomeDir auto-fills.  Empty Pwd skips project layer; we just
	// want this to not error.
	reg, err := Load(LoadOptions{})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() == 0 {
		t.Error("expected built-ins")
	}
}

func TestLoadUnknownTopLevelKey(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "commands.toml"), `
unknown_key = "ignored with warn"

[commands.ok]
description = "ok"
prompt = "x"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("ok"); !ok {
		t.Error("valid entry should still load alongside unknown top-level key")
	}
}
