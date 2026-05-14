package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/auth"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
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
	for k, v := range opts {
		cp[k] = v
	}
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
	for k, v := range c.last {
		cp[k] = v
	}
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

	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   xdgConfig,
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: fakeProviderFactory,
		Now:             func() time.Time { return time.Unix(0, 0).UTC() },
		SkipTea:         true,
	})
	t.Cleanup(func() { SetTestOverrides(nil) })
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

// TestBootstrap_AuthStoreInjectsAPIKey verifies that with no
// model.options.api_key in config and no env var set, a stored
// auth.json credential is injected into the provider factory's opts.
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

	captured := &optsCapture{}
	// Override the provider factory installed by hermeticHome so we
	// capture the merged options.
	SetTestOverrides(&bootstrapOptions{
		HomeDir:         home,
		XDGConfigHome:   filepath.Join(home, ".config"),
		XDGStateHome:    xdgState,
		Pwd:             home,
		ProviderFactory: captured.factory,
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

	if rt.Skills == nil || rt.Skills.Len() != 1 {
		t.Fatalf("Skills.Len() = %d, want 1", rt.Skills.Len())
	}
	if !strings.Contains(rt.SystemPrompt, "## Available skills") {
		t.Errorf("SystemPrompt missing skills header:\n%s", rt.SystemPrompt)
	}
	if !strings.Contains(rt.SystemPrompt, "- foo:") {
		t.Errorf("SystemPrompt missing foo entry:\n%s", rt.SystemPrompt)
	}
	if _, ok := rt.Tools.Get("skill"); !ok {
		t.Error("skill tool not registered when a skill registry is present")
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
