package plugin_test

import (
	"testing"

	"github.com/cfbender/hygge/internal/plugin"
)

// TestParseManifest_valid tests normal manifest parsing.
func TestParseManifest_valid(t *testing.T) {
	toml := []byte(`
name = "my-plugin"
version = "1.0.0"
description = "A test plugin"
entrypoint = "init.lua"

[capabilities]
hooks = true
`)
	m, err := plugin.ParseManifest(toml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Name != "my-plugin" {
		t.Errorf("Name = %q, want %q", m.Name, "my-plugin")
	}
	if m.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", m.Version, "1.0.0")
	}
	if m.Entrypoint != "init.lua" {
		t.Errorf("Entrypoint = %q, want %q", m.Entrypoint, "init.lua")
	}
	if !m.Capabilities.Hooks {
		t.Error("Capabilities.Hooks should be true")
	}
	if m.Synthesised() {
		t.Error("Synthesised() should be false for a parsed manifest")
	}
}

// TestParseManifest_missingName ensures we get an error when name is missing.
func TestParseManifest_missingName(t *testing.T) {
	_, err := plugin.ParseManifest([]byte(`version = "1.0.0"`))
	if err == nil {
		t.Fatal("expected error for missing name")
	}
}

// TestParseManifest_invalidName tests name validation.
func TestParseManifest_invalidName(t *testing.T) {
	cases := []struct {
		toml string
		desc string
	}{
		{`name = "123bad"`, "starts with digit"},
		{`name = "UPPER"`, "uppercase"},
		{`name = ""`, "empty string"},
	}
	for _, tc := range cases {
		_, err := plugin.ParseManifest([]byte(tc.toml))
		if err == nil {
			t.Errorf("expected error for %s", tc.desc)
		}
	}
}

// TestSynthesiseManifest checks the trivial manifest construction.
func TestSynthesiseManifest(t *testing.T) {
	m := plugin.SynthesiseManifest("my-plugin")
	if m.Name != "my-plugin" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.Entrypoint != "plugin.lua" {
		t.Errorf("Entrypoint = %q", m.Entrypoint)
	}
	if !m.Synthesised() {
		t.Error("Synthesised() should be true")
	}
}
