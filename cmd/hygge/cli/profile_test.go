package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/state"
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

	// Verify the state file now has ActiveProfile=work.
	st, err := state.Load(state.LoadOptions{
		HomeDir:      home,
		XDGStateHome: filepath.Join(home, ".local", "state"),
	})
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if st.ActiveProfile != "work" {
		t.Errorf("ActiveProfile = %q, want work", st.ActiveProfile)
	}
}
