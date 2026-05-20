package subagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cfbender/hygge/internal/config"
)

// writeFile is a tiny test helper for placing a subagents.toml under
// the right discovery layer.
func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestLoad_BuiltinAlwaysPresent(t *testing.T) {
	home := t.TempDir()
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reg.Get("general")
	if !ok {
		t.Fatal("expected built-in general type")
	}
	if got.Source != "builtin" {
		t.Fatalf("general.Source: got %q want builtin", got.Source)
	}
	if reg.Len() != 1 {
		t.Fatalf("Len: got %d want 1 (just general)", reg.Len())
	}
}

func TestLoad_UserLayerAddsType(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.searcher]
description = "Find things"
prompt = "You search."
tools = ["read", "grep", "glob"]
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// User-defined types displace the built-in "general".
	if reg.Len() != 1 {
		names := []string{}
		for _, t := range reg.List() {
			names = append(names, t.Name)
		}
		t.Fatalf("Len: got %d (%v) want 1", reg.Len(), names)
	}
	got, ok := reg.Get("searcher")
	if !ok {
		t.Fatal("missing 'searcher' type")
	}
	if got.Source != "user" {
		t.Fatalf("searcher.Source: got %q want user", got.Source)
	}
	if want := []string{"read", "grep", "glob"}; !equalSlices(got.Tools, want) {
		t.Fatalf("searcher.Tools: got %v want %v", got.Tools, want)
	}
}

func TestLoad_ProjectOverridesUser(t *testing.T) {
	home := t.TempDir()
	pwd := t.TempDir()

	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.search]
description = "user-level"
prompt = "user prompt"
`)
	writeFile(t, filepath.Join(pwd, ".agents", "subagents.toml"), `
[subagents.search]
description = "project-level"
prompt = "project prompt"
`)
	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reg.Get("search")
	if !ok {
		t.Fatal("missing 'search' type")
	}
	if got.Description != "project-level" {
		t.Fatalf("project did not override user: %q", got.Description)
	}
	if got.Source != "project" {
		t.Fatalf("Source: got %q want project", got.Source)
	}
}

func TestLoad_BuiltinCanBeOverridden(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.general]
description = "custom general"
prompt = "custom general prompt"
tools = ["read"]
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reg.Get("general")
	if !ok {
		t.Fatal("general missing after override")
	}
	if got.Description != "custom general" {
		t.Fatalf("override did not apply: %q", got.Description)
	}
	if got.Source != "user" {
		t.Fatalf("Source: got %q want user", got.Source)
	}
}

func TestLoad_SubagentToolStrippedFromTOML(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.evil]
description = "tries to recurse"
prompt = "go"
tools = ["read", "subagent", "grep"]
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reg.Get("evil")
	if !ok {
		t.Fatal("evil type missing")
	}
	for _, name := range got.Tools {
		if name == "subagent" {
			t.Fatalf("subagent tool not stripped from TOML: %v", got.Tools)
		}
	}
	if want := []string{"read", "grep"}; !equalSlices(got.Tools, want) {
		t.Fatalf("Tools: got %v want %v", got.Tools, want)
	}
}

func TestLoad_MalformedTOMLDoesNotAbort(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"),
		`this is not valid toml = [unclosed`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load should not fail on malformed TOML: %v", err)
	}
	if _, ok := reg.Get("general"); !ok {
		t.Fatal("built-in general missing after malformed user TOML")
	}
}

func TestLoad_InvalidEntryNameSkipped(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents."NotValid-Name"]
description = "bad name"
prompt = "x"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reg.Len() != 1 {
		t.Fatalf("invalid name should be skipped; got %d types", reg.Len())
	}
}

func TestLoad_MissingRequiredFieldsSkipped(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.no_desc]
prompt = "x"

[subagents.no_prompt]
description = "missing prompt"

[subagents.ok]
description = "ok"
prompt = "ok"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("ok"); !ok {
		t.Fatal("valid 'ok' type missing")
	}
	if _, ok := reg.Get("no_desc"); ok {
		t.Fatal("'no_desc' should have been skipped")
	}
	if _, ok := reg.Get("no_prompt"); ok {
		t.Fatal("'no_prompt' should have been skipped")
	}
}

func TestLoad_ModelOverrideParsedAndKept(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.fancy]
description = "wants a different model"
prompt = "go"
model = "anthropic/claude-haiku-4-5"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reg.Get("fancy")
	if !ok {
		t.Fatal("fancy missing")
	}
	if got.Model != "anthropic/claude-haiku-4-5" {
		t.Fatalf("model field not parsed: %q", got.Model)
	}
}

func TestLoad_MalformedModelOverrideDropped(t *testing.T) {
	// The entry is still loaded -- just without the override.  The
	// type falls back to the parent's model at runtime.
	tests := []struct {
		name  string
		model string
	}{
		{"no-slash", "anthropic-claude"},
		{"empty-provider", "/claude"},
		{"empty-model", "anthropic/"},
		{"uppercase-provider", "Anthropic/claude"},
		{"junk", "this is not a model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, filepath.Join(home, ".agents", "subagents.toml"),
				`
[subagents.fancy]
description = "x"
prompt = "x"
model = "`+tt.model+`"
`)
			reg, err := Load(LoadOptions{HomeDir: home})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			got, ok := reg.Get("fancy")
			if !ok {
				t.Fatal("type should still be loaded even with malformed model")
			}
			if got.Model != "" {
				t.Fatalf("malformed model not stripped: %q", got.Model)
			}
		})
	}
}

func TestRegistry_ListIsSortedAndCopied(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.zebra]
description = "z"
prompt = "z"

[subagents.alpha]
description = "a"
prompt = "a"
`)
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// User-defined types displace the built-in "general".
	all := reg.List()
	if len(all) != 2 {
		t.Fatalf("List len: got %d want 2", len(all))
	}
	want := []string{"alpha", "zebra"}
	for i, t2 := range all {
		if t2.Name != want[i] {
			t.Fatalf("List[%d]: got %q want %q", i, t2.Name, want[i])
		}
	}
	// Mutating the returned slice must not affect the registry.
	all[0].Name = "MUTATED"
	again := reg.List()
	if again[0].Name != "alpha" {
		t.Fatalf("List returned shared storage: now %q", again[0].Name)
	}
}

func TestRegistry_GetUnknownReturnsFalse(t *testing.T) {
	reg, err := Load(LoadOptions{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg.Get("does_not_exist"); ok {
		t.Fatal("Get returned true for unknown name")
	}
}

func TestRegistry_DefaultToolsCopied(t *testing.T) {
	reg, err := Load(LoadOptions{
		HomeDir:      t.TempDir(),
		DefaultTools: []string{"read", "grep"},
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := reg.DefaultTools()
	if want := []string{"read", "grep"}; !equalSlices(got, want) {
		t.Fatalf("DefaultTools: got %v want %v", got, want)
	}
	got[0] = "MUTATED"
	again := reg.DefaultTools()
	if again[0] != "read" {
		t.Fatal("DefaultTools shares storage")
	}
}

// ---------------------------------------------------------------------------
// config.toml subagent source tests
// ---------------------------------------------------------------------------

// TestLoad_ConfigSubagentsLoaded verifies that subagent types declared in
// config.toml (via opts.Config.Subagents) are loaded into the registry.
func TestLoad_ConfigSubagentsLoaded(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Subagents: map[string]config.SubagentEntry{
			"coder": {
				Description: "writes code",
				Prompt:      "You write code.",
				Tools:       []string{"read", "write"},
			},
		},
	}

	reg, err := Load(LoadOptions{HomeDir: home, Config: cfg})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := reg.Get("coder")
	if !ok {
		t.Fatal("expected 'coder' type from config")
	}
	if got.Source != "user" {
		t.Fatalf("coder.Source: got %q want user", got.Source)
	}
	if got.Description != "writes code" {
		t.Fatalf("coder.Description: got %q", got.Description)
	}
	if want := []string{"read", "write"}; !equalSlices(got.Tools, want) {
		t.Fatalf("coder.Tools: got %v want %v", got.Tools, want)
	}
}

// TestLoad_ConfigSubagentsDisplaceBuiltin verifies that when at least one
// user-defined type exists (here via config), the built-in "general" type is
// displaced — matching the existing subagents.toml behaviour.
func TestLoad_ConfigSubagentsDisplaceBuiltin(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Subagents: map[string]config.SubagentEntry{
			"helper": {
				Description: "helps with things",
				Prompt:      "You help.",
			},
		},
	}

	reg, err := Load(LoadOptions{HomeDir: home, Config: cfg})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Built-in general should be displaced.
	if t2, ok := reg.Get("general"); ok && t2.Source == "builtin" {
		t.Fatal("built-in general should be displaced by user-defined types from config")
	}
	if _, ok := reg.Get("helper"); !ok {
		t.Fatal("'helper' type from config missing")
	}
}

// TestLoad_ProjectOverridesConfigSubagent verifies that a project-level
// subagents.toml entry overrides a same-named entry from config.toml.
func TestLoad_ProjectOverridesConfigSubagent(t *testing.T) {
	home := t.TempDir()
	pwd := t.TempDir()

	// config.toml defines "search" at user level.
	cfg := &config.Config{
		Subagents: map[string]config.SubagentEntry{
			"search": {
				Description: "config-level search",
				Prompt:      "config prompt",
			},
		},
	}

	// Project-level subagents.toml overrides "search".
	writeFile(t, filepath.Join(pwd, ".hygge", "subagents.toml"), `
[subagents.search]
description = "project-level search"
prompt = "project prompt"
`)

	reg, err := Load(LoadOptions{HomeDir: home, Pwd: pwd, Config: cfg})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := reg.Get("search")
	if !ok {
		t.Fatal("'search' type missing")
	}
	if got.Description != "project-level search" {
		t.Fatalf("project did not override config: %q", got.Description)
	}
	if got.Source != "project" {
		t.Fatalf("Source: got %q want project", got.Source)
	}
}

// TestLoad_ConfigSubagentsOverrideUserTOML verifies that config.toml entries
// override same-named entries from the user-level subagents.toml (config is
// applied after the user TOML files in the load order).
func TestLoad_ConfigSubagentsOverrideUserTOML(t *testing.T) {
	home := t.TempDir()

	// User-level subagents.toml defines "search".
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.search]
description = "toml-level search"
prompt = "toml prompt"
`)

	// config.toml (opts.Config) overrides "search" — higher precedence.
	cfg := &config.Config{
		Subagents: map[string]config.SubagentEntry{
			"search": {
				Description: "config-level search",
				Prompt:      "config prompt",
			},
		},
	}

	reg, err := Load(LoadOptions{HomeDir: home, Config: cfg})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := reg.Get("search")
	if !ok {
		t.Fatal("'search' type missing")
	}
	if got.Description != "config-level search" {
		t.Fatalf("config did not override user TOML: %q", got.Description)
	}
}

// TestLoad_ProfileSubagents verifies that subagent types in a profile
// (which get merged into cfg.Subagents by config.Load) are available after
// loading, using the real config.Load + subagent.Load pipeline end-to-end.
func TestLoad_ProfileSubagents(t *testing.T) {
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "hygge")
	profilesDir := filepath.Join(cfgDir, "profiles")

	// Base user config: one subagent type.
	writeFile(t, filepath.Join(cfgDir, "config.toml"), `
[subagents.base_agent]
description = "base agent"
prompt = "base prompt"
`)

	// Profile adds another type and overrides base_agent description.
	writeFile(t, filepath.Join(profilesDir, "work.toml"), `
[subagents.base_agent]
description = "profile override"
prompt = "profile prompt"

[subagents.specialist]
description = "specialist agent"
prompt = "specialist prompt"
`)

	cfg, _, err := config.Load(context.Background(), config.LoadOptions{
		HomeDir:   home,
		Pwd:       home,
		Profile:   "work",
		EnvLookup: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	reg, err := Load(LoadOptions{
		HomeDir:       home,
		XDGConfigHome: filepath.Join(home, ".config"),
		Config:        cfg,
	})
	if err != nil {
		t.Fatalf("subagent.Load: %v", err)
	}

	// base_agent should reflect the profile override.
	base, ok := reg.Get("base_agent")
	if !ok {
		t.Fatal("'base_agent' type missing")
	}
	if base.Description != "profile override" {
		t.Fatalf("base_agent.Description: got %q want 'profile override'", base.Description)
	}

	// specialist from profile should be present.
	if _, ok := reg.Get("specialist"); !ok {
		t.Fatal("'specialist' type from profile missing")
	}
}

// TestLoad_LegacySubagentsTOMLStillWorks verifies that the legacy
// subagents.toml discovery path is unchanged when no Config is provided.
func TestLoad_LegacySubagentsTOMLStillWorks(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".agents", "subagents.toml"), `
[subagents.legacy]
description = "legacy agent"
prompt = "legacy prompt"
`)

	// No Config passed — old code path.
	reg, err := Load(LoadOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	got, ok := reg.Get("legacy")
	if !ok {
		t.Fatal("legacy type from subagents.toml missing")
	}
	if got.Source != "user" {
		t.Fatalf("Source: got %q want user", got.Source)
	}
}

// equalSlices is a tiny helper to compare two string slices.
func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
