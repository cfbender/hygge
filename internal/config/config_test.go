package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/state"
)

// --- helpers -----------------------------------------------------------------

// makeEnvLookup returns an EnvLookup that reads from the provided map.
func makeEnvLookup(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

// writeTOML writes toml content to path, creating directories as needed.
func writeTOML(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// hermeticOpts returns LoadOptions pointing entirely into a tmp dir, with
// a hermetic EnvLookup that returns nothing unless populated.
func hermeticOpts(t *testing.T, tmpDir string, env map[string]string) LoadOptions {
	t.Helper()
	if env == nil {
		env = map[string]string{}
	}
	return LoadOptions{
		HomeDir:   tmpDir,
		Pwd:       tmpDir,
		EnvLookup: makeEnvLookup(env),
	}
}

// --- test 1: defaults-only load ----------------------------------------------

func TestLoad_DefaultsOnly(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(t, tmp, nil)

	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Model.Provider != "anthropic" {
		t.Errorf("model.provider: got %q, want %q", cfg.Model.Provider, "anthropic")
	}
	if cfg.Model.Name != "claude-sonnet-4-5" {
		t.Errorf("model.name: got %q, want %q", cfg.Model.Name, "claude-sonnet-4-5")
	}
	if cfg.Permission.Shell != PermAsk {
		t.Errorf("permission.shell: got %q, want %q", cfg.Permission.Shell, PermAsk)
	}
	if cfg.Permission.Network != PermDeny {
		t.Errorf("permission.network: got %q, want %q", cfg.Permission.Network, PermDeny)
	}
	if cfg.Theme.Name != "shell" {
		t.Errorf("theme.name: got %q, want %q", cfg.Theme.Name, "shell")
	}

	// Provenance for every key should point to <defaults>.
	for _, key := range []string{"model.provider", "model.name", "permission.shell", "theme.name"} {
		sources, ok := prov[key]
		if !ok {
			t.Errorf("no provenance for %q", key)
			continue
		}
		if len(sources) == 0 || sources[0].File != "<defaults>" {
			t.Errorf("provenance[%q]: expected <defaults>, got %v", key, sources)
		}
	}
}

// --- test 2: user config overrides defaults ----------------------------------

func TestLoad_UserConfigOverridesDefaults(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(t, tmp, nil)

	// XDG_CONFIG_HOME is not set so the path is tmp/.config
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "openai"
name = "gpt-4o"

[permission]
shell = "deny"
`)

	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Model.Provider != "openai" {
		t.Errorf("model.provider: got %q, want openai", cfg.Model.Provider)
	}
	if cfg.Model.Name != "gpt-4o" {
		t.Errorf("model.name: got %q, want gpt-4o", cfg.Model.Name)
	}
	if cfg.Permission.Shell != PermDeny {
		t.Errorf("permission.shell: got %q, want deny", cfg.Permission.Shell)
	}

	// Provenance for model.provider should include both defaults and user config.
	sources := prov["model.provider"]
	if len(sources) < 2 {
		t.Errorf("expected at least 2 sources for model.provider, got %v", sources)
	}
	last := sources[len(sources)-1]
	if !strings.HasSuffix(last.File, "config.toml") {
		t.Errorf("winning source for model.provider should be user config, got %q", last.File)
	}
}

// --- test 3: profile overrides user config; extends chain + cycle detection --

func TestLoad_ProfileOverridesUserConfig(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(t, tmp, nil)
	opts.Profile = "work"

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	profilesDir := filepath.Join(cfgDir, "profiles")

	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "openai"
name = "gpt-4o"
`)

	writeTOML(t, filepath.Join(profilesDir, "base.toml"), `
[permission]
shell = "allow"
`)

	writeTOML(t, filepath.Join(profilesDir, "work.toml"), `
extends = "base"

[model]
name = "claude-opus-4-5"
`)

	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// profile 'work' sets model.name; extends 'base' sets permission.shell
	if cfg.Model.Name != "claude-opus-4-5" {
		t.Errorf("model.name: got %q, want claude-opus-4-5", cfg.Model.Name)
	}
	if cfg.Permission.Shell != PermAllow {
		t.Errorf("permission.shell: got %q, want allow", cfg.Permission.Shell)
	}
	// provider still comes from user config
	if cfg.Model.Provider != "openai" {
		t.Errorf("model.provider: got %q, want openai", cfg.Model.Provider)
	}
	if cfg.Profile != "work" {
		t.Errorf("Profile: got %q, want work", cfg.Profile)
	}

	_ = prov
}

func TestLoad_ProfileCycleDetected(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(t, tmp, nil)
	opts.Profile = "a"

	profilesDir := filepath.Join(tmp, ".config", "hygge", "profiles")
	writeTOML(t, filepath.Join(profilesDir, "a.toml"), `extends = "b"`)
	writeTOML(t, filepath.Join(profilesDir, "b.toml"), `extends = "a"`)

	_, _, err := Load(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error for cyclic profile chain")
	}
	if !errors.Is(err, ErrCyclicProfile) {
		t.Errorf("expected ErrCyclicProfile, got: %v", err)
	}
}

func TestLoad_ProfileNotFound(t *testing.T) {
	tmp := t.TempDir()
	opts := hermeticOpts(t, tmp, nil)
	opts.Profile = "nonexistent"

	_, _, err := Load(context.Background(), opts)
	if err == nil {
		t.Fatal("expected ErrProfileNotFound")
	}
	if !errors.Is(err, ErrProfileNotFound) {
		t.Errorf("expected ErrProfileNotFound, got: %v", err)
	}
}

func TestLoad_DefaultProfileMissingIsOK(t *testing.T) {
	// No profiles directory at all — should succeed silently.
	tmp := t.TempDir()
	opts := hermeticOpts(t, tmp, nil)
	// Profile defaults to "default" when not specified.

	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Profile != "default" {
		t.Errorf("Profile: got %q, want default", cfg.Profile)
	}
}

// --- test 4: walk-up picks up multiple .hygge/config.toml files --------------

func TestLoad_WalkupPrecedence(t *testing.T) {
	tmp := t.TempDir()

	// Layout:
	//   tmp/                          <- treated as "home"
	//   tmp/root/.hygge/config.toml   <- lower precedence
	//   tmp/root/pkg-a/.hygge/config.toml  <- higher precedence (closer to Pwd)

	rootDir := filepath.Join(tmp, "root")
	pkgADir := filepath.Join(tmp, "root", "pkg-a")

	writeTOML(t, filepath.Join(rootDir, ".hygge", "config.toml"), `
[model]
provider = "openai"
name = "gpt-4o"

[permission]
shell = "deny"
`)

	writeTOML(t, filepath.Join(pkgADir, ".hygge", "config.toml"), `
[model]
name = "gpt-4o-mini"

[permission]
shell = "ask"
`)

	opts := LoadOptions{
		HomeDir:   tmp,
		Pwd:       pkgADir,
		EnvLookup: makeEnvLookup(nil),
	}

	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// pkg-a overrides name and shell
	if cfg.Model.Name != "gpt-4o-mini" {
		t.Errorf("model.name: got %q, want gpt-4o-mini", cfg.Model.Name)
	}
	if cfg.Permission.Shell != PermAsk {
		t.Errorf("permission.shell: got %q, want ask", cfg.Permission.Shell)
	}
	// provider set by root only
	if cfg.Model.Provider != "openai" {
		t.Errorf("model.provider: got %q, want openai", cfg.Model.Provider)
	}

	// provenance for model.name should have at least 3 entries (defaults, root, pkg-a)
	sources := prov["model.name"]
	if len(sources) < 3 {
		t.Errorf("model.name provenance: expected >=3, got %d: %v", len(sources), sources)
	}
	last := sources[len(sources)-1]
	if !strings.Contains(last.File, "pkg-a") {
		t.Errorf("winning source for model.name should be pkg-a, got %q", last.File)
	}
}

// --- test 5: env-var overrides walk-up ---------------------------------------

func TestLoad_EnvVarOverridesWalkup(t *testing.T) {
	tmp := t.TempDir()

	rootDir := filepath.Join(tmp, "root")
	writeTOML(t, filepath.Join(rootDir, ".hygge", "config.toml"), `
[permission]
shell = "allow"
`)

	// Simulate HYGGE_permission__shell=deny via t.Setenv (which updates os.Environ).
	t.Setenv("HYGGE_permission__shell", "deny")

	opts := LoadOptions{
		HomeDir:   tmp,
		Pwd:       rootDir,
		EnvLookup: os.LookupEnv,
	}

	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Permission.Shell != PermDeny {
		t.Errorf("permission.shell: got %q, want deny (env should win)", cfg.Permission.Shell)
	}

	sources := prov["permission.shell"]
	last := sources[len(sources)-1]
	if last.File != "<env>" {
		t.Errorf("winning source should be <env>, got %q", last.File)
	}
}

// --- test 6: flag overrides env ----------------------------------------------

func TestLoad_FlagOverridesEnv(t *testing.T) {
	tmp := t.TempDir()

	t.Setenv("HYGGE_permission__shell", "deny")

	opts := LoadOptions{
		HomeDir:   tmp,
		Pwd:       tmp,
		EnvLookup: os.LookupEnv,
		Flags:     map[string]any{"permission.shell": "ask"},
	}

	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Permission.Shell != PermAsk {
		t.Errorf("permission.shell: got %q, want ask (flag should win)", cfg.Permission.Shell)
	}

	sources := prov["permission.shell"]
	last := sources[len(sources)-1]
	if last.File != "<flag>" {
		t.Errorf("winning source should be <flag>, got %q", last.File)
	}
}

// --- test 7: parse error includes file:line:col ------------------------------

func TestLoad_ParseErrorIncludesPosition(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `[model
provider = "anthropic"
`)

	opts := hermeticOpts(t, tmp, nil)
	_, _, err := Load(context.Background(), opts)
	if err == nil {
		t.Fatal("expected parse error")
	}

	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if !strings.Contains(pe.File, "config.toml") {
		t.Errorf("ParseError.File should contain config.toml, got %q", pe.File)
	}
	// The inner error from go-toml should have position info.
	inner := pe.Err.Error()
	if !strings.Contains(inner, ":") {
		t.Errorf("inner error should contain position (row:col), got %q", inner)
	}
}

// --- test 8: unknown keys tolerated in underlying map but typed struct is fine

func TestLoad_UnknownKeysTolerated(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[unknown_section]
foo = "bar"
`)

	opts := hermeticOpts(t, tmp, nil)
	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load should tolerate unknown keys, got: %v", err)
	}

	// Known keys must still decode correctly.
	if cfg.Model.Provider != "anthropic" {
		t.Errorf("model.provider: got %q", cfg.Model.Provider)
	}
}

// --- test 9: $VAR interpolation ----------------------------------------------

func TestLoad_EnvInterpolation(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[model.options]
api_key = "$MY_API_KEY"
`)

	env := map[string]string{
		"MY_API_KEY": "sk-test-12345",
	}
	opts := hermeticOpts(t, tmp, env)

	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Model.Options["api_key"] != "sk-test-12345" {
		t.Errorf("api_key: got %v, want sk-test-12345", cfg.Model.Options["api_key"])
	}
}

func TestLoad_EnvInterpolation_UnsetVarKeptLiteral(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[model.options]
api_key = "$UNSET_VAR"
`)

	opts := hermeticOpts(t, tmp, nil) // empty env — var not set

	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Literal kept as-is when var is not set.
	if cfg.Model.Options["api_key"] != "$UNSET_VAR" {
		t.Errorf("api_key: got %v, want $UNSET_VAR", cfg.Model.Options["api_key"])
	}
}

// --- test 10: type mismatch error carries both source files ------------------

func TestLoad_TypeMismatchError(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	// User config sets model.name to an integer — type mismatch with defaults (string).
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = 12345
`)

	opts := hermeticOpts(t, tmp, nil)
	_, _, err := Load(context.Background(), opts)
	if err == nil {
		t.Fatal("expected type mismatch error")
	}

	var te *MergeTypeError
	if !errors.As(err, &te) {
		t.Fatalf("expected *MergeTypeError, got %T: %v", err, err)
	}
	if te.Key != "model.name" {
		t.Errorf("expected key=model.name, got %q", te.Key)
	}
	if te.LowFile == "" || te.HighFile == "" {
		t.Errorf("both source files should be populated: low=%q high=%q", te.LowFile, te.HighFile)
	}
}

// --- test 11: explain output -------------------------------------------------

func TestExplain_PermissionShell(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[permission]
shell = "deny"
`)

	opts := hermeticOpts(t, tmp, nil)
	cfg, prov, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	formatted, sources, err := Explain(prov, cfg, "permission.shell")
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}

	if !strings.Contains(formatted, "permission.shell") {
		t.Errorf("output should contain key name")
	}
	if !strings.Contains(formatted, "<defaults>") {
		t.Errorf("output should mention <defaults>")
	}
	if !strings.Contains(formatted, "config.toml") {
		t.Errorf("output should mention config.toml")
	}

	if len(sources) < 2 {
		t.Errorf("expected at least 2 sources, got %d", len(sources))
	}
}

func TestExplain_UnknownKey(t *testing.T) {
	prov := Provenance{}
	_, _, err := Explain(prov, &Config{}, "nonexistent.key")
	if err == nil {
		t.Fatal("expected error for unknown key in provenance")
	}
}

// --- test 12: profile depth limit -------------------------------------------

func TestLoad_ProfileDepthLimit(t *testing.T) {
	tmp := t.TempDir()
	profilesDir := filepath.Join(tmp, ".config", "hygge", "profiles")

	// Create a chain of 9 profiles (a→b→c→...→i) which exceeds maxProfileDepth=8.
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	for idx, name := range names {
		var content string
		if idx < len(names)-1 {
			content = "extends = \"" + names[idx+1] + "\"\n"
		} else {
			content = "[model]\nprovider = \"x\"\n"
		}
		writeTOML(t, filepath.Join(profilesDir, name+".toml"), content)
	}

	opts := hermeticOpts(t, tmp, nil)
	opts.Profile = "a"

	_, _, err := Load(context.Background(), opts)
	if err == nil {
		t.Fatal("expected ErrProfileDepth")
	}
	if !errors.Is(err, ErrProfileDepth) {
		t.Errorf("expected ErrProfileDepth, got: %v", err)
	}
}

// --- test 13: unset sentinel via walk-up -------------------------------------

func TestLoad_UnsetSentinelClearsKey(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "openai"
name = "gpt-4o"
`)

	// Walk-up config clears model.provider via sentinel.
	writeTOML(t, filepath.Join(tmp, ".hygge", "config.toml"), `
[model]
name = "local-model"
`)

	opts := LoadOptions{
		HomeDir:   tmp,
		Pwd:       tmp,
		EnvLookup: makeEnvLookup(nil),
	}

	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Model.Name != "local-model" {
		t.Errorf("model.name: got %q, want local-model", cfg.Model.Name)
	}
}

// --- test 14: invalid permission value rejected ------------------------------

func TestLoad_InvalidPermissionValue(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[permission]
shell = "maybe"
`)

	opts := hermeticOpts(t, tmp, nil)
	_, _, err := Load(context.Background(), opts)
	if err == nil {
		t.Fatal("expected validation error for invalid permission value")
	}
	var ive *InvalidValueError
	if !errors.As(err, &ive) {
		t.Errorf("expected *InvalidValueError, got %T: %v", err, err)
	}
}

// --- test 15: ${ } form interpolation ----------------------------------------

func TestLoad_BraceEnvInterpolation(t *testing.T) {
	tmp := t.TempDir()

	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"

[model.options]
api_key = "${BRACE_VAR}"
`)

	env := map[string]string{"BRACE_VAR": "resolved-value"}
	opts := hermeticOpts(t, tmp, env)

	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Model.Options["api_key"] != "resolved-value" {
		t.Errorf("api_key: got %v, want resolved-value", cfg.Model.Options["api_key"])
	}
}

// --- test 16: state file sets profile ----------------------------------------

func TestLoad_StateFileProfile(t *testing.T) {
	tmp := t.TempDir()

	// Write state using the state package so the field name and format are correct.
	if err := state.Save(&state.State{ActiveProfile: "work"},
		state.LoadOptions{HomeDir: tmp}); err != nil {
		t.Fatalf("state.Save: %v", err)
	}

	profilesDir := filepath.Join(tmp, ".config", "hygge", "profiles")
	writeTOML(t, filepath.Join(profilesDir, "work.toml"), `
[model]
name = "from-state-profile"
`)

	opts := hermeticOpts(t, tmp, nil)
	// opts.Profile is empty — state file should be consulted.

	cfg, _, err := Load(context.Background(), opts)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Model.Name != "from-state-profile" {
		t.Errorf("model.name: got %q, want from-state-profile", cfg.Model.Name)
	}
	if cfg.Profile != "work" {
		t.Errorf("Profile: got %q, want work", cfg.Profile)
	}
}

// --- Reasoning fields --------------------------------------------------------

// TestLoad_Reasoning_ValidValues exercises the happy path: each
// recognised reasoning value passes through unchanged.
func TestLoad_Reasoning_ValidValues(t *testing.T) {
	for _, val := range []string{"", "off", "low", "medium", "high"} {
		t.Run("value="+val, func(t *testing.T) {
			tmp := t.TempDir()
			cfgDir := filepath.Join(tmp, ".config", "hygge")
			writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
reasoning = "`+val+`"
reasoning_budget = 3000
`)
			cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Model.Reasoning != val {
				t.Errorf("model.reasoning: got %q, want %q", cfg.Model.Reasoning, val)
			}
			if cfg.Model.ReasoningBudget != 3000 {
				t.Errorf("model.reasoning_budget: got %d, want 3000", cfg.Model.ReasoningBudget)
			}
		})
	}
}

// TestLoad_Reasoning_InvalidValueWarnsAndResets confirms an invalid
// reasoning string does NOT fail the load — it warns and is reset to "".
func TestLoad_Reasoning_InvalidValueWarnsAndResets(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
reasoning = "extreme"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Reasoning != "" {
		t.Errorf("invalid reasoning value should reset to \"\", got %q", cfg.Model.Reasoning)
	}
}

// TestLoad_Reasoning_NegativeBudgetResets confirms a negative
// reasoning_budget is clamped to zero rather than failing the load.
func TestLoad_Reasoning_NegativeBudgetResets(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
reasoning_budget = -500
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.ReasoningBudget != 0 {
		t.Errorf("negative reasoning_budget should reset to 0, got %d", cfg.Model.ReasoningBudget)
	}
}

// TestLoad_Reasoning_CaseInsensitive verifies upper-case TOML values
// are normalised to lower-case so adapters can match on a fixed
// vocabulary without string-folding at every callsite.
func TestLoad_Reasoning_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[model]
provider = "anthropic"
name = "claude-sonnet-4-5"
reasoning = "MEDIUM"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Model.Reasoning != "medium" {
		t.Errorf("reasoning value should be lower-cased, got %q", cfg.Model.Reasoning)
	}
}

// ---------------------------------------------------------------------------
// Compaction threshold config tests (T2.3)
// ---------------------------------------------------------------------------

// TestLoad_Compaction_Default verifies that the default threshold_pct is 80.
func TestLoad_Compaction_Default(t *testing.T) {
	tmp := t.TempDir()
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Compaction.ThresholdPct != 80 {
		t.Errorf("compaction.threshold_pct default: got %v, want 80", cfg.Compaction.ThresholdPct)
	}
}

// TestLoad_Compaction_ZeroDisables verifies that threshold_pct=0 is valid
// (it disables the suggestion) and is accepted without a warning.
func TestLoad_Compaction_ZeroDisables(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[compaction]
threshold_pct = 0
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Compaction.ThresholdPct != 0 {
		t.Errorf("compaction.threshold_pct: got %v, want 0", cfg.Compaction.ThresholdPct)
	}
}

// TestLoad_Compaction_ValidRange verifies an in-range value (e.g. 70) parses
// correctly.
func TestLoad_Compaction_ValidRange(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[compaction]
threshold_pct = 70
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Compaction.ThresholdPct != 70 {
		t.Errorf("compaction.threshold_pct: got %v, want 70", cfg.Compaction.ThresholdPct)
	}
}

// TestLoad_Compaction_InvalidHighValueReset verifies that a value ≥100 does
// NOT fail the load — it warns and resets to 80.
func TestLoad_Compaction_InvalidHighValueReset(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[compaction]
threshold_pct = 110
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load should not fail for out-of-range threshold_pct, got: %v", err)
	}
	if cfg.Compaction.ThresholdPct != 80 {
		t.Errorf("out-of-range threshold_pct should reset to 80, got %v", cfg.Compaction.ThresholdPct)
	}
}

// ---------------------------------------------------------------------------
// Session resume_default config tests (T2.4)
// ---------------------------------------------------------------------------

// TestLoad_Session_Default verifies that resume_default defaults to "new".
func TestLoad_Session_Default(t *testing.T) {
	tmp := t.TempDir()
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.ResumeDefault != "new" {
		t.Errorf("session.resume_default default: got %q, want \"new\"", cfg.Session.ResumeDefault)
	}
}

// TestLoad_Session_Continue verifies resume_default = "continue" parses correctly.
func TestLoad_Session_Continue(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[session]
resume_default = "continue"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.ResumeDefault != "continue" {
		t.Errorf("session.resume_default: got %q, want \"continue\"", cfg.Session.ResumeDefault)
	}
}

// TestLoad_Session_Ask verifies resume_default = "ask" parses correctly.
func TestLoad_Session_Ask(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[session]
resume_default = "ask"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.ResumeDefault != "ask" {
		t.Errorf("session.resume_default: got %q, want \"ask\"", cfg.Session.ResumeDefault)
	}
}

// TestLoad_Session_InvalidValueWarnsAndResets confirms an invalid
// resume_default does NOT fail the load — it warns and resets to "new".
func TestLoad_Session_InvalidValueWarnsAndResets(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[session]
resume_default = "always"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load should not fail for invalid resume_default, got: %v", err)
	}
	if cfg.Session.ResumeDefault != "new" {
		t.Errorf("invalid resume_default should reset to \"new\", got %q", cfg.Session.ResumeDefault)
	}
}

// TestLoad_Session_CaseInsensitive verifies upper-case TOML values are
// normalised to lower-case.
func TestLoad_Session_CaseInsensitive(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[session]
resume_default = "CONTINUE"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.ResumeDefault != "continue" {
		t.Errorf("resume_default should be lower-cased, got %q", cfg.Session.ResumeDefault)
	}
}

// ---------------------------------------------------------------------------
// Catalog refresh_interval config tests (T3.3)
// ---------------------------------------------------------------------------

// TestLoad_CatalogRefreshInterval_Empty verifies that an absent
// refresh_interval defaults to the empty string (disabled).
func TestLoad_CatalogRefreshInterval_Empty(t *testing.T) {
	tmp := t.TempDir()
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Catalog.RefreshInterval != "" {
		t.Errorf("default catalog.refresh_interval: got %q, want \"\"", cfg.Catalog.RefreshInterval)
	}
	// Zero duration for empty string.
	if d := cfg.Catalog.RefreshIntervalDuration(); d != 0 {
		t.Errorf("RefreshIntervalDuration() for empty: got %v, want 0", d)
	}
}

// TestLoad_CatalogRefreshInterval_Valid verifies that a valid duration
// string is stored as-is.
func TestLoad_CatalogRefreshInterval_Valid(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[catalog]
refresh_interval = "24h"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Catalog.RefreshInterval != "24h" {
		t.Errorf("catalog.refresh_interval: got %q, want \"24h\"", cfg.Catalog.RefreshInterval)
	}
	if d := cfg.Catalog.RefreshIntervalDuration(); d != 24*60*60*1e9 {
		t.Errorf("RefreshIntervalDuration(): got %v, want 24h", d)
	}
}

// TestLoad_CatalogRefreshInterval_Invalid verifies that an unparseable
// refresh_interval is reset to "" rather than failing the load.
func TestLoad_CatalogRefreshInterval_Invalid(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[catalog]
refresh_interval = "not-a-duration"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load should not fail for invalid refresh_interval, got: %v", err)
	}
	if cfg.Catalog.RefreshInterval != "" {
		t.Errorf("invalid refresh_interval should reset to \"\", got %q", cfg.Catalog.RefreshInterval)
	}
}

// TestLoad_CatalogRefreshInterval_Negative verifies that a negative
// duration string is reset to "" (disabled) with a warn.
func TestLoad_CatalogRefreshInterval_Negative(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "hygge")
	writeTOML(t, filepath.Join(cfgDir, "config.toml"), `
[catalog]
refresh_interval = "-1h"
`)
	cfg, _, err := Load(context.Background(), hermeticOpts(t, tmp, nil))
	if err != nil {
		t.Fatalf("Load should not fail for negative refresh_interval, got: %v", err)
	}
	if cfg.Catalog.RefreshInterval != "" {
		t.Errorf("negative refresh_interval should reset to \"\", got %q", cfg.Catalog.RefreshInterval)
	}
}

// TestCatalogConfig_RefreshIntervalDuration_DirectParsing tests the helper
// directly (without going through config.Load) for all edge cases.
func TestCatalogConfig_RefreshIntervalDuration_DirectParsing(t *testing.T) {
	cases := []struct {
		input string
		want  string // "disabled" or a valid duration string
	}{
		{"", "disabled"},
		{"1h", "1h0m0s"},
		{"30m", "30m0s"},
		{"24h", "24h0m0s"},
		{"bad", "disabled"},
		{"-5m", "disabled"},
	}
	for _, c := range cases {
		t.Run("input="+c.input, func(t *testing.T) {
			cc := CatalogConfig{RefreshInterval: c.input}
			d := cc.RefreshIntervalDuration()
			if c.want == "disabled" {
				if d != 0 {
					t.Errorf("expected 0, got %v", d)
				}
			} else {
				if d.String() != c.want {
					t.Errorf("got %v, want %s", d, c.want)
				}
			}
		})
	}
}
