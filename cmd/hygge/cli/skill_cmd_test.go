package cli

import (
	"bytes"
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
	if !strings.Contains(buf.String(), "no skills loaded") {
		t.Errorf("expected empty-state marker, got:\n%s", buf.String())
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
	if !strings.Contains(got, "name:        refactor") {
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
