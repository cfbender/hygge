package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/catalog"
)

func TestInitRequiresConfiguredProvider(t *testing.T) {
	hermeticHome(t)

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "general", "--provider", "definitely-missing", "--model", "claude-test"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("expected init to require provider auth")
	}
	if !strings.Contains(out.String(), "hygge provider auth") {
		t.Fatalf("output missing provider auth guidance:\n%s", out.String())
	}
}

func TestInitGeneralWritesPromptFileAndSingleMode(t *testing.T) {
	home := hermeticHome(t)
	seedInitAuth(t, home, "anthropic")

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "general", "--provider", "anthropic", "--model", "claude-test"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	configPath := filepath.Join(home, ".config", "hygge", "config.toml")
	data, err := os.ReadFile(configPath) //nolint:gosec // hermetic test config path
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(data)
	for _, want := range []string{`provider = 'anthropic'`, `model = 'claude-test'`, `name = 'general'`, `prompt = 'file:prompts/general/general.md'`} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}
	if strings.Contains(config, "[[subagents]]") {
		t.Fatalf("general style should not write subagents into config:\n%s", config)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "hygge", "subagents.toml")); !os.IsNotExist(err) {
		t.Fatalf("general style should not create subagents.toml, err=%v", err)
	}
	promptPath := filepath.Join(home, ".config", "hygge", "prompts", "general", "general.md")
	prompt, err := os.ReadFile(promptPath) //nolint:gosec // hermetic prompt path
	if err != nil {
		t.Fatalf("read prompt: %v", err)
	}
	if !strings.Contains(string(prompt), "general engineering mode") {
		t.Fatalf("unexpected prompt:\n%s", string(prompt))
	}
}

func TestInitAmpWritesModesAndConfigSubagents(t *testing.T) {
	home := hermeticHome(t)
	seedInitAuth(t, home, "anthropic")

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "amp", "--provider", "anthropic", "--model", "claude-test"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	configData, err := os.ReadFile(filepath.Join(home, ".config", "hygge", "config.toml")) //nolint:gosec // hermetic test config path
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(configData)
	for _, want := range []string{`name = 'smart'`, `name = 'rush'`, `name = 'deep'`, `reasoning = 'high'`, `prompt = 'file:prompts/amp/smart.md'`} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}

	for _, want := range []string{`[subagents.carpenter]`, `model = 'anthropic/claude-test'`, `prompt = 'file:prompts/amp/carpenter.md'`, `[subagents.search]`} {
		if !strings.Contains(config, want) {
			t.Fatalf("config subagents missing %q:\n%s", want, config)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "hygge", "subagents.toml")); !os.IsNotExist(err) {
		t.Fatalf("init should not create legacy subagents.toml, err=%v", err)
	}
}

func TestInitWritesBuiltInInspiredDefaults(t *testing.T) {
	home := hermeticHome(t)
	seedInitAuth(t, home, "openai")

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"init", "opencode", "--provider", "openai", "--model", "gpt-test"})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v\n%s", err, out.String())
	}

	configData, err := os.ReadFile(filepath.Join(home, ".config", "hygge", "config.toml")) //nolint:gosec // hermetic test config path
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	config := string(configData)
	for _, want := range []string{`name = 'build'`, `name = 'plan'`, `reasoning = 'low'`, `prompt = 'file:prompts/opencode/plan.md'`} {
		if !strings.Contains(config, want) {
			t.Fatalf("config missing %q:\n%s", want, config)
		}
	}

	for _, want := range []string{`[subagents.general]`, `[subagents.explore]`, `[subagents.scout]`, `model = 'openai/gpt-test'`} {
		if !strings.Contains(config, want) {
			t.Fatalf("config subagents missing %q:\n%s", want, config)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "hygge", "subagents.toml")); !os.IsNotExist(err) {
		t.Fatalf("init should not create legacy subagents.toml, err=%v", err)
	}
}

func TestMaterializeInitStyleAssignsPerComponentModels(t *testing.T) {
	home := t.TempDir()
	style := initStyle{
		Name: "general",
		Modes: []initModeDefault{{
			Name:       "general",
			PromptFile: "general/general.md",
		}},
		Subagents: []initSubagentDefault{{
			Name:       "helper",
			PromptFile: "general/general.md",
		}},
	}
	resolved, err := materializeInitStyle(filepath.Join(home, ".config"), style, "anthropic", map[string]string{
		initModeKey("general"):    "claude-main",
		initSubagentKey("helper"): "claude-helper",
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if got := resolved.Modes[0].Model; got != "claude-main" {
		t.Fatalf("mode model = %q, want claude-main", got)
	}
	if got := resolved.Subagents[0].Model; got != "anthropic/claude-helper" {
		t.Fatalf("subagent model = %q, want anthropic/claude-helper", got)
	}
}

func TestInitPickerModelsCancelOnCtrlC(t *testing.T) {
	styleModel := initStyleSelectModel{list: list.New([]list.Item{initStyleSelectItem{style: availableInitStyles()[0]}}, list.NewDefaultDelegate(), 40, 8)}
	updatedStyle, _ := styleModel.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	gotStyle, ok := updatedStyle.(initStyleSelectModel)
	if !ok || !gotStyle.cancelled {
		t.Fatalf("style picker did not cancel on ctrl+c: %#v", updatedStyle)
	}

	providerModel := initProviderSelectModel{list: list.New([]list.Item{initProviderSelectItem{name: "anthropic"}}, list.NewDefaultDelegate(), 40, 8)}
	updatedProvider, _ := providerModel.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	gotProvider, ok := updatedProvider.(initProviderSelectModel)
	if !ok || !gotProvider.cancelled {
		t.Fatalf("provider picker did not cancel on ctrl+c: %#v", updatedProvider)
	}

	modelModel := initModelSelectModel{list: list.New([]list.Item{initModelSelectItem{entry: catalog.Entry{ID: "claude-test"}}}, list.NewDefaultDelegate(), 40, 8)}
	updatedModel, _ := modelModel.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	gotModel, ok := updatedModel.(initModelSelectModel)
	if !ok || !gotModel.cancelled {
		t.Fatalf("model picker did not cancel on ctrl+c: %#v", updatedModel)
	}
}

func seedInitAuth(t *testing.T, home, providerName string) {
	t.Helper()
	if err := auth.Set(providerName, auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-test", AddedAt: time.Now()}, authOptsFor(home)); err != nil {
		t.Fatalf("seed auth: %v", err)
	}
}
