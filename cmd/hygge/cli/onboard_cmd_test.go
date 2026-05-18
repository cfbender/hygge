package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOnboardWritesGeneralModelToUserConfig(t *testing.T) {
	home := hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"onboard", "--provider", "openrouter", "--model", "openai/gpt-4o-mini"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}

	configPath := filepath.Join(home, ".config", "hygge", "config.toml")
	data, err := os.ReadFile(configPath) //nolint:gosec // test reads the hermetic config path it just created
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)
	if !strings.Contains(config, "[model]") {
		t.Fatalf("config missing [model] section:\n%s", config)
	}
	if !strings.Contains(config, `provider = 'openrouter'`) && !strings.Contains(config, `provider = "openrouter"`) {
		t.Fatalf("config missing openrouter provider:\n%s", config)
	}
	if !strings.Contains(config, `name = 'openai/gpt-4o-mini'`) && !strings.Contains(config, `name = "openai/gpt-4o-mini"`) {
		t.Fatalf("config missing selected model:\n%s", config)
	}
	if !strings.Contains(out.String(), "Configured General agent") {
		t.Fatalf("output missing success message:\n%s", out.String())
	}
}
