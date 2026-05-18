package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteOnboardingModeCreatesUserConfigAndPreservesUnrelatedFields(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")
	target := filepath.Join(xdg, "hygge", "config.toml")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	input := `[theme]
name = "shell"

[[modes]]
name = "General"
provider = "anthropic"
model = "claude-sonnet-4-5"

[[modes]]
name = "existing"
provider = "openai"
model = "gpt-4"
`
	if err := os.WriteFile(target, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := WriteOnboardingMode(WriteOnboardingModeOptions{HomeDir: home, XDGConfigHome: xdg}, ModeConfig{
		Name:        "builder",
		Provider:    "openai",
		Model:       "gpt-5",
		Prompt:      "Build carefully.",
		Description: "Created during onboarding",
	})
	if err != nil {
		t.Fatalf("WriteOnboardingMode: %v", err)
	}
	if wrote != target {
		t.Fatalf("wrote %q, want %q", wrote, target)
	}
	m, err := loadTOMLFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if m["theme"].(map[string]any)["name"] != "shell" {
		t.Fatalf("theme dropped: %#v", m)
	}
	model := m["model"].(map[string]any)
	if model["provider"] != "openai" || model["name"] != "gpt-5" {
		t.Fatalf("model = %#v", model)
	}
	modes := m["modes"].([]any)
	if len(modes) != 2 {
		t.Fatalf("modes len = %d, want 2", len(modes))
	}
	for _, raw := range modes {
		mode := raw.(map[string]any)
		if mode["name"] == "General" {
			t.Fatalf("General mode should be removed after onboarding: %#v", modes)
		}
	}
	mode := modes[1].(map[string]any)
	if mode["name"] != "builder" || mode["prompt"] != "Build carefully." {
		t.Fatalf("mode = %#v", mode)
	}
}

func TestWriteSubagentsTomlCreatesUserHyggeSubagents(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	xdg := filepath.Join(home, ".config")

	if err := WriteSubagentsToml(WriteSubagentsTomlOptions{HomeDir: home, XDGConfigHome: xdg}, []OnboardingSubagent{{
		Name:        "search_agent",
		Description: "Search the codebase",
		Prompt:      "Find relevant files and summarize them.",
		Model:       "openai/gpt-5-mini",
	}}); err != nil {
		t.Fatalf("WriteSubagentsToml: %v", err)
	}
	target := filepath.Join(xdg, "hygge", "subagents.toml")
	m, err := loadTOMLFile(target)
	if err != nil {
		t.Fatal(err)
	}
	subs := m["subagents"].(map[string]any)
	entry := subs["search_agent"].(map[string]any)
	if entry["description"] != "Search the codebase" || entry["prompt"] != "Find relevant files and summarize them." || entry["model"] != "openai/gpt-5-mini" {
		t.Fatalf("entry = %#v", entry)
	}
}
