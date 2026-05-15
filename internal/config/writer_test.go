package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteModelSelectionUpdatesExistingConfigPreservingUnrelatedFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "project", ".hygge", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	input := `[model]
provider = "anthropic"
name = "old"

[model.options]
api_key = "env"

[theme]
name = "nord"

[plugins.policy]
strict = true

[mcp.server]
command = "server"
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := WriteModelSelection(WriteModelOptions{Provenance: Provenance{
		"model.provider": {{File: "<defaults>"}, {File: path}},
	}}, "openrouter", "gpt-5")
	if err != nil {
		t.Fatalf("WriteModelSelection: %v", err)
	}
	if wrote != path {
		t.Fatalf("target = %q, want %q", wrote, path)
	}
	m, err := loadTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	model := m["model"].(map[string]any)
	if model["provider"] != "openrouter" || model["name"] != "gpt-5" {
		t.Fatalf("model = %#v", model)
	}
	if model["options"].(map[string]any)["api_key"] != "env" {
		t.Fatalf("model.options dropped: %#v", model["options"])
	}
	if m["theme"].(map[string]any)["name"] != "nord" || m["plugins"].(map[string]any)["policy"] == nil || m["mcp"].(map[string]any)["server"] == nil {
		t.Fatalf("unrelated config dropped: %#v", m)
	}
}

func TestWriteProviderAPIKeyPreservesModelOptions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	input := `[model]
provider = "openai"
name = "gpt-5"

[model.options]
base_url = "https://example.invalid/v1"
headers = { X-Test = "yes" }
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := WriteProviderAPIKey(WriteProviderAPIKeyOptions{Provenance: Provenance{"model.provider": {{File: path}}}}, "openai", "sk-fake-dialog")
	if err != nil {
		t.Fatalf("WriteProviderAPIKey: %v", err)
	}
	if wrote != path {
		t.Fatalf("wrote %q, want %q", wrote, path)
	}
	m, err := loadTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	options := m["model"].(map[string]any)["options"].(map[string]any)
	if options["api_key"] != "sk-fake-dialog" {
		t.Fatalf("api_key = %#v", options["api_key"])
	}
	if options["base_url"] != "https://example.invalid/v1" {
		t.Fatalf("base_url dropped: %#v", options)
	}
	if options["headers"].(map[string]any)["X-Test"] != "yes" {
		t.Fatalf("headers dropped: %#v", options)
	}
}

func TestWriteModelSelectionCreatesUserConfigWhenNoRealModelProvenance(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	wrote, err := WriteModelSelection(WriteModelOptions{
		HomeDir:       home,
		XDGConfigHome: xdg,
		Provenance: Provenance{
			"model.provider": {{File: "<defaults>"}, {File: "<env>"}},
		},
	}, "openai", "gpt-5")
	if err != nil {
		t.Fatalf("WriteModelSelection: %v", err)
	}
	want := filepath.Join(xdg, "hygge", "config.toml")
	if wrote != want {
		t.Fatalf("target = %q, want %q", wrote, want)
	}
	m, err := loadTOMLFile(want)
	if err != nil {
		t.Fatal(err)
	}
	model := m["model"].(map[string]any)
	if model["provider"] != "openai" || model["name"] != "gpt-5" {
		t.Fatalf("model = %#v", model)
	}
}

func TestWriteThemeSelectionPreservesUnrelatedFields(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	input := `[theme]
name = "shell"

[model]
provider = "openai"
name = "gpt-5"
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := WriteThemeSelection(WriteThemeSelectionOptions{Provenance: Provenance{"theme.name": {{File: path}}}}, "midnight")
	if err != nil {
		t.Fatalf("WriteThemeSelection: %v", err)
	}
	if wrote != path {
		t.Fatalf("wrote %q, want %q", wrote, path)
	}
	m, err := loadTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if m["theme"].(map[string]any)["name"] != "midnight" {
		t.Fatalf("theme not updated: %#v", m["theme"])
	}
	if m["model"].(map[string]any)["provider"] != "openai" {
		t.Fatalf("model dropped: %#v", m)
	}
}
