package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeCommandsToml(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "commands.toml"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommandsList_BuiltinsAlwaysShow(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"/help", "/clear", "/compact", "/cost", "/sessions", "/fork", "/model", "/reason", "/version", "builtin"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

func TestCommandsList_IncludesTOMLEntries(t *testing.T) {
	home := hermeticHome(t)
	writeCommandsToml(t, filepath.Join(home, ".agents"), `
[commands.review]
description = "Review code"
prompt = "Review {{tail}}"
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "/review") {
		t.Errorf("output missing /review:\n%s", got)
	}
	if !strings.Contains(got, "user") {
		t.Errorf("output missing user source label:\n%s", got)
	}
}

func TestCommandsList_SourceFilter(t *testing.T) {
	home := hermeticHome(t)
	writeCommandsToml(t, filepath.Join(home, ".agents"), `
[commands.review]
description = "user-defined"
prompt = "{{tail}}"
`)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "list", "--source", "user"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "/review") {
		t.Errorf("expected /review in --source=user output:\n%s", got)
	}
	if strings.Contains(got, "/help") {
		t.Errorf("--source=user should not show built-ins:\n%s", got)
	}
}

func TestCommandsShow_Builtin(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "show", "model"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "/model") {
		t.Errorf("missing name line:\n%s", got)
	}
	if !strings.Contains(got, "builtin") {
		t.Errorf("missing source:\n%s", got)
	}
}

func TestCommandsShow_AcceptsLeadingSlash(t *testing.T) {
	hermeticHome(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "show", "/help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(buf.String(), "/help") {
		t.Errorf("expected /help in output:\n%s", buf.String())
	}
}

func TestCommandsShow_NotFound(t *testing.T) {
	hermeticHome(t)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "show", "nope"})
	if err := root.Execute(); err == nil {
		t.Fatal("expected error for unknown name")
	}
	if !strings.Contains(buf.String(), "no command named") {
		t.Errorf("expected friendly error, got:\n%s", buf.String())
	}
}

func TestCommandsList_ShowsArgsColumn(t *testing.T) {
	home := hermeticHome(t)
	writeCommandsToml(t, filepath.Join(home, ".agents"), `
[commands.explain]
description = "Explain something"
prompt = "Explain {{topic}}"
args = [
  { name = "topic", description = "what to explain", required = true },
]
`)
	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"commands", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "<topic>") {
		t.Errorf("expected required-arg marker in args column:\n%s", got)
	}
}
