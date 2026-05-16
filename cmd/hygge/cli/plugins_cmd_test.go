package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// testPluginsDir returns the absolute path to internal/plugin/testdata/plugins.
func testPluginsDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// Go up from cmd/hygge/cli to repo root, then into internal/plugin/testdata.
	repoRoot := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return filepath.Join(repoRoot, "internal", "plugin", "testdata", "plugins")
}

// TestPluginsList_empty verifies that list shows "(no plugins installed)" when
// no plugins are configured.
func TestPluginsList_empty(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	got := buf.String()
	if !strings.Contains(got, "no plugins") {
		t.Errorf("expected 'no plugins' in output, got:\n%s", got)
	}
}

// TestPluginsList_withPlugin verifies that a plugin loaded from a local path
// appears in the list output.
func TestPluginsList_withPlugin(t *testing.T) {
	home := hermeticHome(t)
	dir := testPluginsDir(t)

	// Write a config with the local plugin source.
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgBody := `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[plugins]
sources = ["local:` + dir + `/hello"]
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "list"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}
	got := buf.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("expected 'hello' plugin in output, got:\n%s", got)
	}
	if !strings.Contains(got, "loaded") {
		t.Errorf("expected 'loaded' status in output, got:\n%s", got)
	}
}

// TestPluginsInstall_invalidURI verifies that an invalid URI is rejected.
func TestPluginsInstall_invalidURI(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "install", "npm:some-package"})
	err := root.Execute()
	if err == nil {
		t.Fatal("expected error for npm: URI")
	}
}

func TestPluginsInstallWritesConfig(t *testing.T) {
	home := hermeticHome(t)
	source := "local:" + filepath.Join(testPluginsDir(t), "hello")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "install", source})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	data, err := os.ReadFile(filepath.Join(home, ".config", "hygge", "config.toml")) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "[plugins]") || !strings.Contains(got, source) {
		t.Fatalf("config missing plugin source %q:\n%s", source, got)
	}
}

func TestPluginsRemoveWritesConfig(t *testing.T) {
	home := hermeticHome(t)
	source := "local:" + filepath.Join(testPluginsDir(t), "hello")
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfgBody := `[plugins]
sources = ["` + source + `"]
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "remove", "hello"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	data, err := os.ReadFile(filepath.Join(cfgDir, "config.toml")) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), source) {
		t.Fatalf("config still contains removed source %q:\n%s", source, string(data))
	}
}

func TestPluginsTypesInstallWritesLuaLSFiles(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "types", "install", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	typesPath := filepath.Join(dir, ".hygge", "types", "hygge.lua")
	typesData, err := os.ReadFile(typesPath) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read types: %v", err)
	}
	if got := string(typesData); !strings.Contains(got, "---@class Hygge") || !strings.Contains(got, "function hygge.register_tool") {
		t.Fatalf("types file missing Hygge definitions:\n%s", got)
	}

	luarcPath := filepath.Join(dir, ".luarc.json")
	luarcData, err := os.ReadFile(luarcPath) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read .luarc.json: %v", err)
	}
	luarc := string(luarcData)
	for _, want := range []string{`"runtime.version": "Lua 5.1"`, `"workspace.library"`, `".hygge/types"`, `"hygge"`} {
		if !strings.Contains(luarc, want) {
			t.Fatalf(".luarc.json missing %s:\n%s", want, luarc)
		}
	}
}

func TestPluginsTypesInstallPreservesExistingLuaRC(t *testing.T) {
	hermeticHome(t)
	dir := t.TempDir()
	luarcPath := filepath.Join(dir, ".luarc.json")
	if err := os.WriteFile(luarcPath, []byte(`{"existing": true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "types", "install", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	luarcData, err := os.ReadFile(luarcPath) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read .luarc.json: %v", err)
	}
	if got := string(luarcData); got != `{"existing": true}` {
		t.Fatalf("expected existing .luarc.json to be preserved, got %q", got)
	}
	if !strings.Contains(buf.String(), "left existing LuaLS config unchanged") {
		t.Fatalf("expected preservation notice, got:\n%s", buf.String())
	}
}

func TestPluginsDevInitWritesScaffoldAndTypes(t *testing.T) {
	hermeticHome(t)
	dir := filepath.Join(t.TempDir(), "My Plugin")

	root := NewRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs([]string{"plugins", "dev", "init", dir})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\noutput: %s", err, buf.String())
	}

	manifestData, err := os.ReadFile(filepath.Join(dir, "plugin.toml")) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read plugin.toml: %v", err)
	}
	if got := string(manifestData); !strings.Contains(got, `name = "my-plugin"`) || !strings.Contains(got, `entrypoint = "plugin.lua"`) {
		t.Fatalf("unexpected plugin.toml:\n%s", got)
	}

	pluginData, err := os.ReadFile(filepath.Join(dir, "plugin.lua")) // #nosec G304 -- hermetic test path under t.TempDir
	if err != nil {
		t.Fatalf("read plugin.lua: %v", err)
	}
	if !strings.Contains(string(pluginData), "hygge.register_tool") {
		t.Fatalf("plugin.lua missing starter tool:\n%s", string(pluginData))
	}

	if _, err := os.Stat(filepath.Join(dir, ".hygge", "types", "hygge.lua")); err != nil {
		t.Fatalf("expected types file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".luarc.json")); err != nil {
		t.Fatalf("expected .luarc.json: %v", err)
	}
}
