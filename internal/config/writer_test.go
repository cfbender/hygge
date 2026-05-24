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
small_model = "haiku"

[model.options]
api_key = "env"

[theme]
name = "nord"

[plugins.policy]
strict = true

[mcp.server]
command = "server"

[[modes]]
name = "smart"
provider = "anthropic"
model = "old"
prompt = "Keep this prompt."
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := WriteModelSelection(WriteModelOptions{Provenance: Provenance{
		"modes": {{File: path}},
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
	if _, ok := model["provider"]; ok {
		t.Fatalf("top-level model.provider written: %#v", model)
	}
	if _, ok := model["name"]; ok {
		t.Fatalf("top-level model.name written: %#v", model)
	}
	if model["small_model"] != "haiku" {
		t.Fatalf("model.small_model dropped: %#v", model)
	}
	if model["options"].(map[string]any)["api_key"] != "env" {
		t.Fatalf("model.options dropped: %#v", model["options"])
	}
	modes := m["modes"].([]any)
	firstMode := modes[0].(map[string]any)
	if firstMode["name"] != "smart" || firstMode["provider"] != "openrouter" || firstMode["model"] != "gpt-5" || firstMode["prompt"] != "Keep this prompt." {
		t.Fatalf("first mode = %#v", firstMode)
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
small_model = "gpt-4o-mini"

[model.options]
base_url = "https://example.invalid/v1"
headers = { X-Test = "yes" }
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := WriteProviderAPIKey(WriteProviderAPIKeyOptions{Provenance: Provenance{"model.options.api_key": {{File: path}}}}, "openai", "sk-fake-dialog")
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
	model := m["model"].(map[string]any)
	if _, ok := model["provider"]; ok {
		t.Fatalf("top-level model.provider written: %#v", model)
	}
	if _, ok := model["name"]; ok {
		t.Fatalf("top-level model.name written: %#v", model)
	}
	if model["small_model"] != "gpt-4o-mini" {
		t.Fatalf("small_model dropped: %#v", model)
	}
	options := model["options"].(map[string]any)
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
			"modes": {{File: "<defaults>"}, {File: "<env>"}},
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
	if _, ok := m["model"]; ok {
		t.Fatalf("top-level model section created: %#v", m["model"])
	}
	modes := m["modes"].([]any)
	if len(modes) != 1 {
		t.Fatalf("modes len = %d, want 1", len(modes))
	}
	mode := modes[0].(map[string]any)
	if mode["name"] != "General" || mode["provider"] != "openai" || mode["model"] != "gpt-5" {
		t.Fatalf("mode = %#v", mode)
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

func TestWriteDefaultProfileCreatesUserConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	wrote, err := WriteDefaultProfile(WriteDefaultProfileOptions{HomeDir: home, XDGConfigHome: xdg}, "work")
	if err != nil {
		t.Fatalf("WriteDefaultProfile: %v", err)
	}
	want := filepath.Join(xdg, "hygge", "config.toml")
	if wrote != want {
		t.Fatalf("target = %q, want %q", wrote, want)
	}
	m, err := loadTOMLFile(want)
	if err != nil {
		t.Fatal(err)
	}
	if m["default_profile"] != "work" {
		t.Fatalf("default_profile = %#v", m["default_profile"])
	}
}

func TestWriteDefaultProfilePreservesUnrelatedFields(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	path := filepath.Join(xdg, "hygge", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteDefaultProfile(WriteDefaultProfileOptions{HomeDir: home, XDGConfigHome: xdg}, "work"); err != nil {
		t.Fatalf("WriteDefaultProfile: %v", err)
	}
	m, err := loadTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if m["default_profile"] != "work" {
		t.Fatalf("default_profile = %#v", m["default_profile"])
	}
	model := m["model"].(map[string]any)
	if model["provider"] != "anthropic" || model["name"] != "claude-sonnet-4-5" {
		t.Fatalf("model dropped: %#v", model)
	}
}

func TestWritePluginSourcesPreservesPerPluginTables(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	input := `[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[plugins]
sources = ["local:/old"]

[plugins.policy]
strict = true
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := WritePluginSources(WritePluginSourcesOptions{Provenance: Provenance{"plugins.sources": {{File: path}}}}, []string{"local:/old", "local:/new"})
	if err != nil {
		t.Fatalf("WritePluginSources: %v", err)
	}
	if wrote != path {
		t.Fatalf("wrote %q, want %q", wrote, path)
	}
	m, err := loadTOMLFile(path)
	if err != nil {
		t.Fatal(err)
	}
	plugins := m["plugins"].(map[string]any)
	sources := plugins["sources"].([]any)
	if len(sources) != 2 || sources[0] != "local:/old" || sources[1] != "local:/new" {
		t.Fatalf("sources = %#v", sources)
	}
	if plugins["policy"].(map[string]any)["strict"] != true {
		t.Fatalf("per-plugin config dropped: %#v", plugins)
	}
	if m["model"].(map[string]any)["provider"] != "anthropic" {
		t.Fatalf("unrelated config dropped: %#v", m)
	}
}

func TestWritePluginSourcesCreatesUserConfigWhenNoRealProvenance(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	wrote, err := WritePluginSources(WritePluginSourcesOptions{
		HomeDir:       home,
		XDGConfigHome: xdg,
		Provenance:    Provenance{"plugins.sources": {{File: "<env>"}}},
	}, []string{"local:/plugin"})
	if err != nil {
		t.Fatalf("WritePluginSources: %v", err)
	}
	want := filepath.Join(xdg, "hygge", "config.toml")
	if wrote != want {
		t.Fatalf("target = %q, want %q", wrote, want)
	}
	m, err := loadTOMLFile(want)
	if err != nil {
		t.Fatal(err)
	}
	sources := m["plugins"].(map[string]any)["sources"].([]any)
	if len(sources) != 1 || sources[0] != "local:/plugin" {
		t.Fatalf("sources = %#v", sources)
	}
}
