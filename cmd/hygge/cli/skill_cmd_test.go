package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, dir, name, description, whenToUse, body string) {
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

func TestSkillsListEmpty(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The hygge built-in skill is always present; "no skills loaded" must
	// NOT appear.
	got := buf.String()
	if strings.Contains(got, "no skills loaded") {
		t.Errorf("unexpected empty-state marker when builtin should be present:\n%s", got)
	}
	if !strings.Contains(got, "hygge") {
		t.Errorf("built-in hygge skill not in listing:\n%s", got)
	}
	if !strings.Contains(got, "builtin") {
		t.Errorf("source label 'builtin' not in listing:\n%s", got)
	}
}

func TestSkillsListWithFiles(t *testing.T) {
	home := hermeticHome(t)
	writeSkillFile(t, filepath.Join(home, ".agents", "skills"),
		"refactor", "Refactor a handler", "when refactoring", "body")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "refactor") {
		t.Errorf("output missing skill name:\n%s", got)
	}
	if !strings.Contains(got, "user/.agents") {
		t.Errorf("output missing source label:\n%s", got)
	}
}

func TestSkillsShow_Found(t *testing.T) {
	home := hermeticHome(t)
	writeSkillFile(t, filepath.Join(home, ".agents", "skills"),
		"refactor", "Refactor a handler", "when refactoring", "step 1\nstep 2")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "show", "refactor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "name: refactor") {
		t.Errorf("missing name line:\n%s", got)
	}
	if !strings.Contains(got, "step 1") {
		t.Errorf("missing body:\n%s", got)
	}
}

func TestSkillsShow_NotFound(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "show", "nope"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for missing skill")
	}
	if !strings.Contains(buf.String(), "no skill named") {
		t.Errorf("missing error message:\n%s", buf.String())
	}
}

func TestSkillsDoctor_NoProblems(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "doctor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "no problems detected") {
		t.Errorf("expected clean diagnosis, got:\n%s", buf.String())
	}
}

func TestSkillsDoctor_ReportsBadFile(t *testing.T) {
	home := hermeticHome(t)
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Frontmatter without closing fence.
	body := "---\nname: bad\ndescription: x\nwhen_to_use: y\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "doctor"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "skipped") {
		t.Errorf("expected 'skipped' marker for bad file:\n%s", got)
	}
	if !strings.Contains(got, "issue(s) detected") {
		t.Errorf("expected issue count:\n%s", got)
	}
}

// TestSkillsShowHyggeBuiltin verifies that the built-in "hygge" skill is
// shown by `hygge skills show hygge` and its body covers key topics.
func TestSkillsShowHyggeBuiltin(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"skills", "show", "hygge"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"name: hygge",
		"source: builtin",
		"config.toml",
		"permissions",
		"MCP",
		"plugins",
		"Troubleshooting",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("skills show hygge: output missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestSkillsListBuiltinInSystemPrompt verifies that the hygge built-in skill
// is reflected in the assembled system prompt at bootstrap time.
func TestSkillsListBuiltinInSystemPrompt(t *testing.T) {
	hermeticHome(t)

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if _, ok := rt.Skills.Get("hygge"); !ok {
		t.Fatal("rt.Skills missing built-in hygge skill")
	}
	if !strings.Contains(rt.SystemPrompt, "<name>hygge</name>") {
		t.Errorf("SystemPrompt missing <name>hygge</name>:\n%s", rt.SystemPrompt)
	}
}
