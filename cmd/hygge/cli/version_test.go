package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionSubcommand(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "hygge") {
		t.Errorf("output missing 'hygge': %q", got)
	}
	if !strings.Contains(got, Version) {
		t.Errorf("output missing version %q: %q", Version, got)
	}
}

func TestRootVersionFlag(t *testing.T) {
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, Version) {
		t.Errorf("--version output missing %q: %q", Version, got)
	}
}
