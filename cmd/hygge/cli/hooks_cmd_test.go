package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeHooksToml writes a hooks.toml under dir/.agents/ and creates a
// .git marker so walk-up terminates at dir.
func writeHooksToml(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".agents"), 0o700); err != nil {
		t.Fatalf("mkdir .agents: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".agents", "hooks.toml"), []byte(content), 0o600); err != nil {
		t.Fatalf("write hooks.toml: %v", err)
	}
}

func TestHooksList_NoHooks(t *testing.T) {
	home := hermeticHome(t)
	// Just a .git so walk-up stops; no hooks.toml.
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o700); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	// Repoint Pwd at home so discovery starts there.
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             home,
		ProviderFactory: stubProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
	})

	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"hooks", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "no hooks") {
		t.Fatalf("want 'no hooks' message, got: %q", buf.String())
	}
}

func TestHooksList_WithHooks(t *testing.T) {
	home := hermeticHome(t)
	content := `
[hooks.guard]
description = "Policy check"
events = ["pre_tool"]
command = "/usr/bin/true"
`
	writeHooksToml(t, home, content)
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             home,
		ProviderFactory: stubProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
	})

	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"hooks", "list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "guard") {
		t.Fatalf("want 'guard' in output, got: %q", out)
	}
	if !strings.Contains(out, "pre_tool") {
		t.Fatalf("want 'pre_tool' in output, got: %q", out)
	}
}

func TestHooksList_EventFilter(t *testing.T) {
	home := hermeticHome(t)
	content := `
[hooks.pre-guard]
description = "pre_tool hook"
events = ["pre_tool"]
command = "/usr/bin/true"

[hooks.post-telemetry]
description = "post_tool hook"
events = ["post_tool"]
command = "/usr/bin/true"
mode = "async"
`
	writeHooksToml(t, home, content)
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             home,
		ProviderFactory: stubProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
	})

	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"hooks", "list", "--event", "pre_tool"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "pre-guard") {
		t.Fatalf("want 'pre-guard' in filtered output, got: %q", out)
	}
	if strings.Contains(out, "post-telemetry") {
		t.Fatalf("post-telemetry should be filtered out, got: %q", out)
	}
}

func TestHooksShow_Found(t *testing.T) {
	home := hermeticHome(t)
	content := `
[hooks.policy]
description = "Policy guard hook"
events = ["pre_tool"]
command = "/usr/local/bin/policy-check"
timeout = "3s"
`
	writeHooksToml(t, home, content)
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             home,
		ProviderFactory: stubProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
	})

	cmd := NewRootCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"hooks", "show", "policy"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "policy") {
		t.Fatalf("want hook name in output, got: %q", out)
	}
	if !strings.Contains(out, "pre_tool") {
		t.Fatalf("want events in output, got: %q", out)
	}
	if !strings.Contains(out, "3s") {
		t.Fatalf("want timeout in output, got: %q", out)
	}
}

func TestHooksShow_NotFound(t *testing.T) {
	home := hermeticHome(t)
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             home,
		ProviderFactory: stubProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
	})

	cmd := NewRootCmd()
	var buf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"hooks", "show", "nonexistent"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for unknown hook name")
	}
}
