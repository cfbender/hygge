package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestContextShow_Empty(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"context", "show"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "no context files loaded") {
		t.Errorf("expected empty-state marker, got:\n%s", buf.String())
	}
}

func TestContextShow_Loaded(t *testing.T) {
	home := hermeticHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "AGENTS.md"),
		[]byte("user-level rule: tidy commits."), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"context", "show"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "## Project context") {
		t.Errorf("missing context header:\n%s", got)
	}
	if !strings.Contains(got, "user-level rule: tidy commits.") {
		t.Errorf("missing AGENTS.md body:\n%s", got)
	}
}

func TestContextPaths(t *testing.T) {
	home := hermeticHome(t)
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "AGENTS.md"),
		[]byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"context", "paths"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := strings.TrimSpace(buf.String())
	want := filepath.Join(home, ".agents", "AGENTS.md")
	if got != want {
		t.Errorf("paths output = %q, want %q", got, want)
	}
}

// TestContextList_Empty verifies that `hygge context list` reports
// the empty-state marker when no AGENTS.md / CLAUDE.md files exist.
func TestContextList_Empty(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"context", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "(no context files loaded)") {
		t.Errorf("expected empty-state marker, got:\n%s", buf.String())
	}
}

// TestContextList_TabularOutput verifies that a single planted
// user-level AGENTS.md is rendered with the header row and a data
// row whose SOURCE column is `user/.agents` and whose BYTES column
// matches len(content).
func TestContextList_TabularOutput(t *testing.T) {
	home := hermeticHome(t)
	body := "user-level rule: tidy commits."
	if err := os.MkdirAll(filepath.Join(home, ".agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "AGENTS.md"),
		[]byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"context", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	// Header row.
	for _, col := range []string{"SOURCE", "PATH", "BYTES", "LINES"} {
		if !strings.Contains(got, col) {
			t.Errorf("missing header column %q in:\n%s", col, got)
		}
	}
	// Data row.
	if !strings.Contains(got, "user/.agents") {
		t.Errorf("missing user/.agents source token:\n%s", got)
	}
	// Byte count (TrimSpace happens inside the loader, but the body
	// has no leading/trailing whitespace so the count matches).
	wantBytes := strconv.Itoa(len(body))
	if !strings.Contains(got, wantBytes) {
		t.Errorf("missing byte count %s in:\n%s", wantBytes, got)
	}
}

// TestContextList_SubdirAndRoot plants both a root AGENTS.md and a
// subdirectory AGENTS.md, verifies both are listed, and verifies
// that the subdir row uses `project/subdir` as SOURCE with a
// project-relative PATH (not absolute).
func TestContextList_SubdirAndRoot(t *testing.T) {
	home := hermeticHome(t)

	// Construct a project root one level below $HOME so the
	// project walk-up can find a marker without colliding with
	// the home-stop sentinel.
	root := filepath.Join(home, "work", "project")
	pwd := filepath.Join(root, "service")
	if err := os.MkdirAll(pwd, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mark the root with .git so findProjectRoot resolves there.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("root body"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "AGENTS.md"),
		[]byte("internal body"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Override the default hermetic Pwd so bootstrap sees the
	// project root.  Reuse the same XDG layout hermeticHome built.
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             pwd,
		ProviderFactory: fakeProviderFactory,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	cmdRoot := NewRootCmd()
	var buf bytes.Buffer
	cmdRoot.SetOut(&buf)
	cmdRoot.SetErr(&buf)
	cmdRoot.SetArgs([]string{"context", "list"})
	if err := cmdRoot.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()

	// Both source layers present.
	if !strings.Contains(got, "project/root") {
		t.Errorf("missing project/root row:\n%s", got)
	}
	if !strings.Contains(got, "project/subdir") {
		t.Errorf("missing project/subdir row:\n%s", got)
	}

	// The subdir PATH column must be the relative path, not absolute.
	wantRel := filepath.Join("internal", "AGENTS.md")
	if !strings.Contains(got, wantRel) {
		t.Errorf("missing relative subdir path %q:\n%s", wantRel, got)
	}
	// And must NOT contain the absolute subdir path.
	absSubdir := filepath.Join(root, "internal", "AGENTS.md")
	// Find the subdir line and assert it does not contain the abs path.
	for _, line := range strings.Split(got, "\n") {
		if strings.Contains(line, "project/subdir") &&
			strings.Contains(line, absSubdir) {
			t.Errorf("subdir row leaked absolute path:\n%s", line)
		}
	}
}
