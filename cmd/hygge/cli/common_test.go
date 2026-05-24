package cli

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/llm"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/state"
)

// fakeProvider is a no-network provider stand-in used by every test
// that bootstraps the CLI runtime.  It returns ListModels with a
// realistic context window so the agent's pct-used math has a
// denominator to work with, and Stream returns a closed channel
// immediately (the tests under cmd/hygge/cli never drive Send through
// to a real model — Task 11's agent tests already cover that path).
type fakeProvider struct{}

func (fakeProvider) Name() string { return "anthropic" }
func (fakeProvider) Stream(_ context.Context, _ provider.Request) (<-chan provider.Event, error) {
	ch := make(chan provider.Event)
	close(ch)
	return ch, nil
}
func (fakeProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (fakeProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return []provider.Model{{
		Name:           "claude-sonnet-4-5",
		ContextWindow:  200_000,
		MaxOutput:      8192,
		SupportsTools:  true,
		SupportsImages: true,
	}}, nil
}

// fakeProviderFactory is the bootstrap-injectable factory variant.
func fakeProviderFactory(_ map[string]any) (provider.Provider, error) {
	return fakeProvider{}, nil
}

type fakeFantasyLanguageModel struct{}

func (fakeFantasyLanguageModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, nil
}
func (fakeFantasyLanguageModel) Stream(context.Context, fantasy.Call) (fantasy.StreamResponse, error) {
	return nil, nil
}
func (fakeFantasyLanguageModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, nil
}
func (fakeFantasyLanguageModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, nil
}
func (fakeFantasyLanguageModel) Provider() string { return "test" }
func (fakeFantasyLanguageModel) Model() string    { return "test-model" }

// optsCapture is a thread-safe map[string]any sink used by tests that
// need to assert on what was passed to the provider factory.
type optsCapture struct {
	mu   sync.Mutex
	last map[string]any
}

func (c *optsCapture) factory(opts map[string]any) (provider.Provider, error) {
	c.mu.Lock()
	// Shallow-copy so subsequent bootstrap mutations cannot taint the
	// captured value.
	cp := make(map[string]any, len(opts))
	maps.Copy(cp, opts)
	c.last = cp
	c.mu.Unlock()
	return fakeProvider{}, nil
}

func (c *optsCapture) snapshot() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.last == nil {
		return nil
	}
	cp := make(map[string]any, len(c.last))
	maps.Copy(cp, c.last)
	return cp
}

// hermeticHome returns a fresh tempdir with the standard .config and
// .local/state subdirectories, plus a SetTestOverrides hook installed
// so every CLI command sees the same hermetic paths.  The hook is
// cleared in t.Cleanup.
func hermeticHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	xdgConfig := filepath.Join(home, ".config")
	xdgState := filepath.Join(home, ".local", "state")

	seedHermeticAuth(t, home, xdgState)

	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   xdgConfig,
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: fakeProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})
	t.Cleanup(func() { SetTestOverrides(nil) })
	return home
}

// seedHermeticAuth writes a fake anthropic credential into auth.json
// under the given hermetic home/state so hasAnyProviderAuth (the TUI
// entrypoint gate) is satisfied.  Tests that swap to a different
// XDGStateHome via SetTestOverrides must call this for the new state
// dir if they exercise runTUI.
func seedHermeticAuth(t *testing.T, home, xdgState string) {
	t.Helper()
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-hermetic-test"},
		auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("seedHermeticAuth: %v", err)
	}
}

func seedHermeticModelConfig(t *testing.T, home string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := []byte("[model]\nprovider = \"anthropic\"\nname = \"claude-sonnet-4-5\"\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), body, 0o600); err != nil {
		t.Fatalf("write model config: %v", err)
	}
}

// seedHermeticModeConfig writes a canonical [[modes]] entry into the hermetic
// config dir.  Use this (instead of seedHermeticModelConfig) when the test
// needs the bootstrap to have an active provider/model via the new
// [[modes]]-canonical path.
func seedHermeticModeConfig(t *testing.T, home string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := []byte("[[modes]]\nname = \"General\"\nprovider = \"anthropic\"\nmodel = \"claude-sonnet-4-5\"\n")
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), body, 0o600); err != nil {
		t.Fatalf("write mode config: %v", err)
	}
}

func hermeticHomeWithModel(t *testing.T) string {
	t.Helper()
	home := hermeticHome(t)
	seedHermeticModelConfig(t, home)
	return home
}

// seedSession creates a session row in the store via a one-shot
// bootstrap — useful for tests that need the store to be non-empty.
// Returns the session id.
func seedSession(t *testing.T, projectDir string) string {
	t.Helper()
	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	sess, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: projectDir,
		Model: session.ModelRef{
			Provider: "anthropic",
			Name:     "claude-sonnet-4-5",
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess.ID
}

func TestBootstrapBuildsAllComponents(t *testing.T) {
	hermeticHome(t)

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.Config == nil {
		t.Error("Config nil")
	}
	if rt.Provenance == nil {
		t.Error("Provenance nil")
	}
	if rt.State == nil {
		t.Error("State nil")
	}
	if rt.Bus == nil {
		t.Error("Bus nil")
	}
	if rt.Store == nil {
		t.Error("Store nil")
	}
	if rt.Provider == nil {
		t.Error("Provider nil")
	}
	if rt.Permission == nil {
		t.Error("Permission nil")
	}
	if rt.Tools == nil {
		t.Error("Tools nil")
	}
	if rt.Catalog == nil {
		t.Error("Catalog nil")
	}
	if rt.Agent == nil {
		t.Error("Agent nil")
	}
	if rt.Theme == nil {
		t.Error("Theme nil")
	}
}

func TestBootstrapAutoloadsPluginDirectories(t *testing.T) {
	home := hermeticHome(t)

	plugins := []struct {
		root string
		name string
	}{
		{root: filepath.Join(home, ".config", "hygge", "plugins"), name: "xonsh"},
		{root: filepath.Join(home, ".hygge", "plugins"), name: "project"},
	}
	for _, p := range plugins {
		dir := filepath.Join(p.root, p.name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir plugin dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "plugin.lua"), []byte("hygge.log('info', 'loaded')\n"), 0o600); err != nil {
			t.Fatalf("write plugin.lua: %v", err)
		}
	}

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	for _, want := range []string{"xonsh", "project"} {
		if _, ok := rt.Plugins.Get(want); !ok {
			t.Fatalf("autoloaded plugin %q not loaded", want)
		}
	}
}

// TestBootstrapWithoutProviderAuthSucceeds verifies the change in v0.x:
// bootstrap (used by every CLI command) no longer fails when no
// provider credential is configured.  Inspection commands such as
// `hygge provider list` or `hygge config explain` must run on a fresh
// install where the user has not yet authenticated.
func TestBootstrapWithoutProviderAuthSucceeds(t *testing.T) {
	home := t.TempDir()
	for _, providerName := range knownProviders() {
		if envName := providerEnvVar(providerName); envName != "" {
			t.Setenv(envName, "")
		}
	}

	rt, err := bootstrap(context.Background(), bootstrapOptions{
		HomeDir:       home,
		XDGConfigHome: filepath.Join(home, ".config"),
		XDGStateHome:  filepath.Join(home, ".local", "state"),
		Pwd:           home,
		Now:           func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:       true,
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// The credential gate now lives on the TUI entrypoint.
	if hasAnyProviderAuth(rt.StateOpts) {
		t.Fatal("hasAnyProviderAuth = true; want false when no env var or auth.json entry")
	}
}

// TestRunTUIWithoutProviderAuthOpensOnboarding verifies that the TUI
// entrypoint opens even when no provider has credentials; onboarding handles
// first-run setup inside the app.
func TestRunTUIWithoutProviderAuthOpensOnboarding(t *testing.T) {
	home := t.TempDir()
	for _, providerName := range knownProviders() {
		if envName := providerEnvVar(providerName); envName != "" {
			t.Setenv(envName, "")
		}
	}
	seedHermeticModelConfig(t, home)

	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             home,
		ProviderFactory: fakeProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})
	t.Cleanup(func() { SetTestOverrides(nil) })

	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{})
	if err := root.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if strings.Contains(out.String(), "ANTHROPIC_API_KEY") {
		t.Fatalf("output leaked ANTHROPIC_API_KEY: %v", out.String())
	}
}

func TestBootstrapYoloEnablesPermissionBypass(t *testing.T) {
	hermeticHome(t)

	rt, err := bootstrap(context.Background(), bootstrapOptions{Yolo: true})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if rt.Permission == nil || !rt.Permission.Yolo() {
		t.Fatal("permission engine yolo mode not enabled")
	}
}

func TestFantasyModelResolverDoesNotReuseParentAfterConfigChange(t *testing.T) {
	cfg := &config.Config{
		Model: config.ModelConfig{Options: map[string]any{
			"api_key":  "sk-test",
			"base_url": "https://first.invalid/v1",
		}},
		Modes: []config.ModeConfig{{Name: "General", Provider: "test-provider", Model: "model-a"}},
	}
	resolver := buildFantasyModelResolver(cfg, stateLoadOptionsForTest(), nil, fakeFantasyLanguageModel{}, llm.ProviderBuildOptions{})

	cfg.Model.Options["base_url"] = "https://second.invalid/v1"
	got, err := resolver(context.Background(), "test-provider", "model-a")
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if got == nil {
		t.Fatal("resolver returned nil model")
	}
	if _, reused := got.(fakeFantasyLanguageModel); reused {
		t.Fatal("resolver reused parent model instead of rebuilding with current options")
	}
}

func stateLoadOptionsForTest() state.LoadOptions {
	return state.LoadOptions{}
}

// TestBootstrap_AuthStoreInjectsAPIKey verifies that with no
// model.options.api_key in config and no env var set, a stored
// auth.json credential is injected into the provider factory's opts.
func TestBootstrapDryRunIgnoresConfigAndAuth(t *testing.T) {
	home := t.TempDir()
	xdgState := filepath.Join(home, ".local", "state")
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env-should-be-ignored")
	seedHermeticModelConfig(t, home)
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-from-store-should-be-ignored"},
		auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("auth.Set: %v", err)
	}

	captured := &optsCapture{}
	rt, err := bootstrap(context.Background(), bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: captured.factory,
		FantasyModel:    nil,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		DryRun:          true,
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if !rt.DryRun {
		t.Fatal("rt.DryRun = false, want true")
	}
	// DryRun ignores external sources, so no [model] section is loaded and
	// no [[modes]] are present.  cfg.Model.Provider/Name are empty.
	if rt.Config.Model.Provider != "" || rt.Config.Model.Name != "" {
		t.Fatalf("dry-run model = %s/%s, want empty (external sources ignored)", rt.Config.Model.Provider, rt.Config.Model.Name)
	}
	if hasConfiguredModel(rt.Config, rt.Provenance) {
		t.Fatalf("hasConfiguredModel = true; no modes with dry-run (external sources ignored)")
	}
	if got := captured.snapshot(); got != nil {
		t.Fatalf("provider factory was called with opts %#v; want no provider/auth load", got)
	}
}

func TestBootstrap_AuthStoreInjectsAPIKey(t *testing.T) {
	home := hermeticHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "")

	// Seed the auth store with an anthropic API key.
	xdgState := filepath.Join(home, ".local", "state")
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-from-store-1234"},
		auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("auth.Set: %v", err)
	}

	// Seed a [[modes]] entry so the bootstrap has an active provider.
	seedHermeticModeConfig(t, home)

	captured := &optsCapture{}
	// Override the provider factory installed by hermeticHome so we
	// capture the merged options.
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: captured.factory,
		FantasyModel:    fakeFantasyLanguageModel{},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	got := captured.snapshot()
	if got == nil {
		t.Fatal("factory was never called")
	}
	if got["api_key"] != "sk-from-store-1234" {
		t.Errorf("opts[api_key]: got %v, want sk-from-store-1234", got["api_key"])
	}
}

// TestBootstrap_EnvVarBeatsAuthStore verifies that when the canonical
// env var is set, the auth store is not consulted — the adapter's own
// env fallback gets to run.
func TestAuthConfiguredProvidersIncludesEnvAndAuthStore(t *testing.T) {
	home := hermeticHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env-9999")
	t.Setenv("OPENAI_API_KEY", "")
	xdgState := filepath.Join(home, ".local", "state")
	if err := auth.Set("openrouter",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-from-store-1234"},
		auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("auth.Set: %v", err)
	}

	got := authConfiguredProviders(state.LoadOptions{HomeDir: home, XDGStateHome: xdgState})
	want := []string{"anthropic", "openrouter"}
	if !slices.Equal(got, want) {
		t.Fatalf("authConfiguredProviders = %v, want %v", got, want)
	}
}

func TestBootstrap_EnvVarBeatsAuthStore(t *testing.T) {
	home := hermeticHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env-9999")

	xdgState := filepath.Join(home, ".local", "state")
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-from-store-1234"},
		auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("auth.Set: %v", err)
	}

	captured := &optsCapture{}
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: captured.factory,
		FantasyModel:    fakeFantasyLanguageModel{},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	got := captured.snapshot()
	if got == nil {
		t.Fatal("factory was never called")
	}
	if v, ok := got["api_key"]; ok {
		t.Errorf("opts[api_key]: got %v, want absent (env var should win and adapter handles it)", v)
	}
}

// TestBootstrap_ConfigBeatsEnvAndStore verifies that an explicit
// model.options.api_key in the user's config file overrides both the
// env var and the auth store.
func TestBootstrap_ConfigBeatsEnvAndStore(t *testing.T) {
	home := hermeticHome(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env-9999")

	xdgState := filepath.Join(home, ".local", "state")
	if err := auth.Set("anthropic",
		auth.Credential{Type: auth.CredAPIKey, APIKey: "sk-from-store-1234"},
		auth.LoadOptions{HomeDir: home, XDGStateHome: xdgState}); err != nil {
		t.Fatalf("auth.Set: %v", err)
	}

	// Write a user config with an explicit api_key.
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("mkdir cfg dir: %v", err)
	}
	cfgBody := `[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[model.options]
api_key = "sk-from-config-explicit"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfgBody), 0o644); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	captured := &optsCapture{}
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: captured.factory,
		FantasyModel:    fakeFantasyLanguageModel{},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	got := captured.snapshot()
	if got == nil {
		t.Fatal("factory was never called")
	}
	if got["api_key"] != "sk-from-config-explicit" {
		t.Errorf("opts[api_key]: got %v, want sk-from-config-explicit", got["api_key"])
	}
}

// TestBootstrap_SkillsAppearInSystemPrompt verifies that a skill file
// dropped under .agents/skills/ is auto-discovered and surfaces in
// the assembled system prompt.
func TestBootstrap_SkillsAppearInSystemPrompt(t *testing.T) {
	home := hermeticHome(t)

	// Drop a skill at ~/.agents/skills/foo.md.
	dir := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "---\nname: foo\ndescription: foo desc\nwhen_to_use: foo when\n---\nfoo body\n"
	if err := os.WriteFile(filepath.Join(dir, "foo.md"), []byte(body), 0o600); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// The user wrote "foo" under ~/.agents/skills; the hygge built-in is
	// always also present, so we expect at least 2 skills (hygge + foo).
	skillsLen := 0
	if rt.Skills != nil {
		skillsLen = rt.Skills.Len()
	}
	if skillsLen < 2 {
		t.Fatalf("Skills.Len() = %d, want >= 2 (builtin hygge + user foo)", skillsLen)
	}
	if _, ok := rt.Skills.Get("foo"); !ok {
		t.Fatal("Skills missing user-written 'foo' skill")
	}
	if !strings.Contains(rt.SystemPrompt, "## Available skills") {
		t.Errorf("SystemPrompt missing skills header:\n%s", rt.SystemPrompt)
	}
	if !strings.Contains(rt.SystemPrompt, "<name>foo</name>") {
		t.Errorf("SystemPrompt missing foo entry:\n%s", rt.SystemPrompt)
	}
	if _, ok := rt.Tools.Get("skill"); !ok {
		t.Error("skill tool not registered when a skill registry is present")
	}
}

func TestComposeModeSystemPromptDoesNotAccumulatePreviousModes(t *testing.T) {
	base := "base\n\nskills\n\nproject context"
	first := composeModeSystemPrompt(base, "mode one")
	second := composeModeSystemPrompt(base, "mode two")

	if first != "base\n\nskills\n\nproject context\n\nmode one" {
		t.Fatalf("first prompt = %q", first)
	}
	if second != "base\n\nskills\n\nproject context\n\nmode two" {
		t.Fatalf("second prompt = %q", second)
	}
	if strings.Contains(second, "mode one") {
		t.Fatalf("second prompt retained previous mode: %q", second)
	}
}

func TestBootstrap_AppendsDefaultModePrompt(t *testing.T) {
	home := hermeticHome(t)
	cfgDir := filepath.Join(home, ".config", "hygge")
	if err := os.MkdirAll(filepath.Join(cfgDir, "prompts"), 0o755); err != nil {
		t.Fatalf("mkdir prompts: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "prompts", "smart.md"), []byte("smart mode: delegate read-only codebase mapping to @agent:search"), 0o600); err != nil {
		t.Fatalf("write smart prompt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(`
[[modes]]
name = "smart"
provider = "anthropic"
model = "claude-sonnet-4-5"
prompt = "file:prompts/smart.md"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if !strings.Contains(rt.SystemPrompt, "smart mode: delegate read-only codebase mapping to @agent:search") {
		t.Fatalf("SystemPrompt missing default mode prompt:\n%s", rt.SystemPrompt)
	}
}

func TestDefaultSystemPromptGuidesToolNarration(t *testing.T) {
	if !strings.Contains(defaultSystemPrompt, "Before using tools") {
		t.Fatalf("defaultSystemPrompt should ask the model to narrate tool usage:\n%s", defaultSystemPrompt)
	}
	if !strings.Contains(defaultSystemPrompt, "inspect or change") {
		t.Fatalf("defaultSystemPrompt should describe pre-tool context:\n%s", defaultSystemPrompt)
	}
	if !strings.Contains(defaultSystemPrompt, "concise, natural language") || !strings.Contains(defaultSystemPrompt, "Avoid terse shorthand") {
		t.Fatalf("defaultSystemPrompt should require natural-language tool narration:\n%s", defaultSystemPrompt)
	}
	for _, want := range []string{"Hygge", "subagents", "yolo-mode", "permission prompts", "verified without evidence"} {
		if !strings.Contains(defaultSystemPrompt, want) {
			t.Fatalf("defaultSystemPrompt missing %q:\n%s", want, defaultSystemPrompt)
		}
	}
	for _, want := range []string{"<hygge_system_contract>", "<instruction_precedence>", "<memory_policy>", "<untrusted_context_policy>", "irreversible actions", "propose memories", "session-scoped memories autonomously", "explicit user confirmation before saving inferred project/global memories"} {
		if !strings.Contains(defaultSystemPrompt, want) {
			t.Fatalf("defaultSystemPrompt missing hardened section/detail %q:\n%s", want, defaultSystemPrompt)
		}
	}
}

// TestDefaultSystemPromptContainsCompactInstruction verifies that the
// default system prompt instructs the model to use the compact tool at
// ~90% context usage and when switching topics.
func TestDefaultSystemPromptContainsCompactInstruction(t *testing.T) {
	for _, want := range []string{
		"<context_management>",
		"compact",
		"90%",
		"context window",
		"subject",
	} {
		if !strings.Contains(defaultSystemPrompt, want) {
			t.Fatalf("defaultSystemPrompt missing compact instruction %q:\n%s", want, defaultSystemPrompt)
		}
	}
}

// TestBootstrap_AgentsMDAppearsInSystemPrompt verifies that an
// AGENTS.md sitting next to .git in the project root is loaded and
// appended to the system prompt under "## Project context".
func TestBootstrap_AgentsMDAppearsInSystemPrompt(t *testing.T) {
	home := hermeticHome(t)

	// Build a project root with .git and an AGENTS.md sibling.
	root := filepath.Join(home, "work", "proj")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"),
		[]byte("project rule: be conservative."), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}

	// Repoint the test override's Pwd at the new project.
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    filepath.Join(home, ".local", "state"),
		Pwd:             root,
		ProviderFactory: fakeProviderFactory,
		FantasyModel:    fakeFantasyLanguageModel{},
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	if len(rt.AgentsBlocks) != 1 {
		t.Fatalf("len(AgentsBlocks) = %d, want 1", len(rt.AgentsBlocks))
	}
	if !strings.Contains(rt.SystemPrompt, "## Project context") {
		t.Errorf("SystemPrompt missing project-context header:\n%s", rt.SystemPrompt)
	}
	if !strings.Contains(rt.SystemPrompt, "project rule: be conservative.") {
		t.Errorf("SystemPrompt missing AGENTS.md body:\n%s", rt.SystemPrompt)
	}
}

// TestResolveReasoning_OverrideAndConfig exercises the precedence
// matrix between config.Model.Reasoning and the --reasoning CLI
// override.
func TestResolveReasoning_OverrideAndConfig(t *testing.T) {
	cases := []struct {
		name       string
		cfgEffort  string
		cfgBudget  int
		override   string
		wantEffort string
		wantBudget int
	}{
		{name: "both empty", cfgEffort: "", override: "", wantEffort: ""},
		{name: "config only", cfgEffort: "medium", override: "", wantEffort: "medium"},
		{name: "override wins", cfgEffort: "medium", override: "high", wantEffort: "high"},
		{name: "override to off clears config", cfgEffort: "high", override: "off", wantEffort: "off"},
		{name: "invalid override falls back to config", cfgEffort: "low", override: "extreme", wantEffort: "low"},
		{name: "case-insensitive override", cfgEffort: "", override: "HIGH", wantEffort: "high"},
		{name: "trims whitespace", cfgEffort: "", override: "  medium  ", wantEffort: "medium"},
		{name: "config budget forwarded", cfgEffort: "high", cfgBudget: 12000, wantEffort: "high", wantBudget: 12000},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Model.Reasoning = c.cfgEffort
			cfg.Model.ReasoningBudget = c.cfgBudget
			r := resolveReasoning(cfg, c.override)
			if r.Effort != c.wantEffort {
				t.Errorf("Effort=%q, want %q", r.Effort, c.wantEffort)
			}
			if r.BudgetTokens != c.wantBudget {
				t.Errorf("BudgetTokens=%d, want %d", r.BudgetTokens, c.wantBudget)
			}
		})
	}
}

// TestResolveReasoning_NilConfigSafe ensures resolveReasoning does
// not panic when handed a nil *config.Config (defensive: bootstrap
// always passes a non-nil one in production but the helper should
// remain safe in isolation).
func TestResolveReasoning_NilConfigSafe(t *testing.T) {
	r := resolveReasoning(nil, "low")
	if r.Effort != "low" {
		t.Errorf("Effort=%q, want low", r.Effort)
	}
}

// ---------------------------------------------------------------------------
// findResumableSession tests (T2.4)
// ---------------------------------------------------------------------------

// TestFindResumableSession_NoSessions returns "" when the store is empty.
func TestFindResumableSession_NoSessions(t *testing.T) {
	hermeticHome(t)
	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	sid, err := findResumableSession(context.Background(), rt, rt.Pwd, false)
	if err != nil {
		t.Fatalf("findResumableSession: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty sid, got %q", sid)
	}
}

// TestFindResumableSession_ScopedToCwd only returns sessions whose
// ProjectDir matches the current Pwd.
func TestFindResumableSession_ScopedToCwd(t *testing.T) {
	home := hermeticHome(t)
	otherDir := t.TempDir()

	// Seed a session for a DIFFERENT project directory.
	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	_, err = rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: otherDir,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession (other): %v", err)
	}

	// findResumableSession scoped to home (rt.Pwd) should return nothing.
	sid, err := findResumableSession(context.Background(), rt, home, false)
	if err != nil {
		t.Fatalf("findResumableSession: %v", err)
	}
	if sid != "" {
		t.Errorf("expected empty sid for different project dir, got %q", sid)
	}
}

// TestFindResumableSession_ReturnsLatestForCwd returns the most recent
// session whose ProjectDir matches the Pwd.
func TestFindResumableSession_ReturnsLatestForCwd(t *testing.T) {
	home := hermeticHome(t)

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// Seed two sessions for the same project dir.
	_, err = rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession #1: %v", err)
	}
	sess2, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession #2: %v", err)
	}

	sid, err := findResumableSession(context.Background(), rt, home, false)
	if err != nil {
		t.Fatalf("findResumableSession: %v", err)
	}
	// ListSessions returns newest-first, so the second session should be returned.
	if sid != sess2.ID {
		t.Errorf("expected latest session id %q, got %q", sess2.ID, sid)
	}
}

// TestFindResumableSession_AllowAny ignores the project dir filter and
// returns the globally most recent primary session.
func TestFindResumableSession_AllowAny(t *testing.T) {
	home := hermeticHome(t)
	otherDir := t.TempDir()

	rt, err := bootstrap(context.Background(), bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	defer func() { _ = rt.Close() }()

	// Seed a session for home, then a newer one for otherDir.
	_, err = rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: home,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession (home): %v", err)
	}
	latest, err := rt.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: otherDir,
		Model:      session.ModelRef{Provider: "anthropic", Name: "claude-sonnet-4-5"},
	})
	if err != nil {
		t.Fatalf("CreateSession (other): %v", err)
	}

	// With allowAny=true, we expect the globally latest session.
	sid, err := findResumableSession(context.Background(), rt, home, true)
	if err != nil {
		t.Fatalf("findResumableSession (allowAny): %v", err)
	}
	if sid != latest.ID {
		t.Errorf("expected global latest %q, got %q", latest.ID, sid)
	}
}

func TestKnownProvidersUsesCatwalkProviderIDs(t *testing.T) {
	providers := knownProviders()
	if !slices.Contains(providers, "gemini") {
		t.Fatalf("knownProviders() missing Catwalk provider %q; got %v", "gemini", providers)
	}
	if slices.Contains(providers, "google") {
		t.Fatalf("knownProviders() includes non-Catwalk provider %q; provider picker must use Catwalk provider IDs only: %v", "google", providers)
	}
}
