package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if !strings.Contains(buf.String(), "no AGENTS.md") {
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
