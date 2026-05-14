package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSubagentsToml(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subagents.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentsList_BuiltinAlwaysShows(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"subagents", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "general") {
		t.Errorf("output missing builtin general:\n%s", got)
	}
	if !strings.Contains(got, "builtin") {
		t.Errorf("output missing builtin source label:\n%s", got)
	}
}

func TestSubagentsList_IncludesTOMLEntries(t *testing.T) {
	home := hermeticHome(t)
	writeSubagentsToml(t, filepath.Join(home, ".agents"), `
[subagents.searcher]
description = "Find files"
prompt = "search"
tools = ["read", "grep"]
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"subagents", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "searcher") {
		t.Errorf("output missing searcher:\n%s", got)
	}
	if !strings.Contains(got, "user") {
		t.Errorf("output missing user source label:\n%s", got)
	}
}

func TestSubagentsShow_BuiltinGeneral(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"subagents", "show", "general"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "name:        general") {
		t.Errorf("missing name line:\n%s", got)
	}
	if !strings.Contains(got, "system prompt") {
		t.Errorf("missing system prompt section:\n%s", got)
	}
	if !strings.Contains(got, "isolation") {
		t.Errorf("expected builtin prompt body in output, got:\n%s", got)
	}
}

func TestSubagentsShow_NotFound(t *testing.T) {
	hermeticHome(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"subagents", "show", "nope"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unknown name")
	}
	if !strings.Contains(buf.String(), "no sub-agent type named") {
		t.Errorf("expected helpful error message, got:\n%s", buf.String())
	}
}
