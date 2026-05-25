package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigExplainNoKey(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()

	// New format: grouped TOML view.  The [permission] section header
	// must appear and the shell leaf must be present inside it.
	if !strings.Contains(got, "[permission]") {
		t.Errorf("output missing [permission] section header:\n%s", got)
	}
	if !strings.Contains(got, "shell =") {
		t.Errorf("output missing permission.shell leaf line:\n%s", got)
	}

	// The output should have section-grouped inline comments showing
	// the winning source — at minimum the defaults source must appear.
	if !strings.Contains(got, "<defaults>") {
		t.Errorf("output should reference <defaults> source:\n%s", got)
	}

	// model.provider is not in defaults so with no user config it should
	// either be absent or shown as an empty string.  Either way it must
	// not appear as a raw "model.provider" dotted-path label (the new
	// format uses section headers + leaf names, not dotted paths).
	if strings.Contains(got, "model.provider") {
		t.Errorf("output should not contain raw dotted path 'model.provider':\n%s", got)
	}
}

func TestConfigExplainNoKey_SectionsPresent(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()

	// All default-backed core sections must be present in the merged view.
	for _, section := range []string{"[permission]", "[theme]", "[ui]", "[compaction]", "[session]", "[notifications]"} {
		if !strings.Contains(got, section) {
			t.Errorf("output missing section %s:\n%s", section, got)
		}
	}
}

func TestConfigExplainNoKey_OverrideChain(t *testing.T) {
	home := hermeticHome(t)

	// Write a user config that overrides permission.shell.
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := []byte("[permission]\nshell = \"deny\"\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), body, 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()

	// The shell line should show the override comment.
	if !strings.Contains(got, "overrides") {
		t.Errorf("output should mention 'overrides' for a key with multiple sources:\n%s", got)
	}
	// The winning value must be shown.
	if !strings.Contains(got, `"deny"`) {
		t.Errorf("output missing winning value \"deny\":\n%s", got)
	}
}

func TestConfigExplainNoKey_FullMergedConfigAndNoANSIForBuffer(t *testing.T) {
	home := hermeticHome(t)

	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := []byte(`
[model]
provider = "openai"
name = "gpt-4o"

[[modes]]
name = "smart"
provider = "anthropic"
model = "claude-sonnet-4-5"

[subagents.reviewer]
description = "Reviews code"
prompt = "Be strict"
tools = ["read", "grep"]

[mcp.github]
command = "github-mcp"
args = ["stdio"]
`)
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), body, 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()

	for _, want := range []string{"[[modes]]", "[subagents.reviewer]", "[mcp.github]"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "provider = \"openai\"") || strings.Contains(got, "name = \"gpt-4o\"") {
		t.Errorf("output should omit deprecated top-level model provider/name:\n%s", got)
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("buffer output should not contain ANSI escapes:\n%q", got)
	}
}

func TestConfigExplainKey(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"config", "explain", "permission.shell"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "permission.shell") {
		t.Errorf("output missing key:\n%s", got)
	}
	if !strings.Contains(got, "set by:") {
		t.Errorf("output missing provenance chain:\n%s", got)
	}
	if !strings.Contains(got, "<defaults>") {
		t.Errorf("output should reference the defaults source:\n%s", got)
	}
}
