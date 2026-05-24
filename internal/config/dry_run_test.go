package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIgnoreExternalSourcesUsesDefaultsOnly(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte("[model]\nprovider = \"openai\"\nname = \"gpt-5\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pwd := filepath.Join(home, "project")
	if err := os.MkdirAll(filepath.Join(pwd, ".hygge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pwd, ".hygge", "config.toml"), []byte("[model]\nprovider = \"openrouter\"\nname = \"deepseek/test\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, prov, err := Load(context.Background(), LoadOptions{
		HomeDir:               home,
		Pwd:                   pwd,
		EnvLookup:             makeEnvLookup(map[string]string{"HYGGE_model__provider": "openai", "HYGGE_model__name": "from-env"}),
		IgnoreExternalSources: true,
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// With IgnoreExternalSources no user/project config is loaded, so no
	// [model] section is merged in and no [[modes]] are present.
	// cfg.Model.Provider/Name remain empty; modes remain empty (no synthesis).
	if cfg.Model.Provider != "" || cfg.Model.Name != "" {
		t.Fatalf("model = %s/%s, want empty (external sources ignored)", cfg.Model.Provider, cfg.Model.Name)
	}
	if len(cfg.Modes) != 0 {
		t.Fatalf("Modes: got %d, want 0 (external sources ignored)", len(cfg.Modes))
	}
	if hasRealConfigSourceForTest(prov["model.provider"]) || hasRealConfigSourceForTest(prov["model.name"]) {
		t.Fatalf("provenance should only contain defaults, got provider=%v name=%v", prov["model.provider"], prov["model.name"])
	}
}

func hasRealConfigSourceForTest(sources []Source) bool {
	for _, src := range sources {
		if src.File != "" && src.File != "<defaults>" {
			return true
		}
	}
	return false
}
