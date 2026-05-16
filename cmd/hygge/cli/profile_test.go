package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfileListEmpty(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "no profiles") {
		t.Errorf("expected 'no profiles' marker, got:\n%s", got)
	}
}

func TestProfileListWithFiles(t *testing.T) {
	home := hermeticHome(t)

	dir := filepath.Join(home, ".config", "hygge", "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"default.toml", "work.toml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "default") {
		t.Errorf("output missing 'default':\n%s", got)
	}
	if !strings.Contains(got, "work") {
		t.Errorf("output missing 'work':\n%s", got)
	}
	if !strings.Contains(got, "* default") {
		t.Errorf("expected default to be marked active:\n%s", got)
	}
}

func TestProfileUse(t *testing.T) {
	home := hermeticHome(t)

	// Create profile files so the loader doesn't error when use re-bootstraps.
	dir := filepath.Join(home, ".config", "hygge", "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "work.toml"), []byte(""), 0o644); err != nil {
		t.Fatalf("write work.toml: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "use", "work"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Verify the user config now sets default_profile=work.
	data, err := os.ReadFile(filepath.Join(home, ".config", "hygge", "config.toml")) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if !strings.Contains(string(data), `default_profile = 'work'`) && !strings.Contains(string(data), `default_profile = "work"`) {
		t.Errorf("config missing default_profile: %s", data)
	}
}

func TestProfileListMarksConfigDefaultProfile(t *testing.T) {
	home := hermeticHome(t)
	dir := filepath.Join(home, ".config", "hygge", "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"default.toml", "work.toml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(""), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".config", "hygge", "config.toml"), []byte(`default_profile = "work"`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"profile", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "* work") {
		t.Errorf("expected work to be marked default:\n%s", got)
	}
	if strings.Contains(got, "* default") {
		t.Errorf("default should not be marked when config default_profile is work:\n%s", got)
	}
}
