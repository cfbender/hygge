// Package config loads, merges, and validates Hygge configuration from
// multiple TOML sources.
//
// # Merge order (lowest → highest precedence)
//
//  1. Builtin defaults (defaults.go)
//  2. User config: $XDG_CONFIG_HOME/hygge/config.toml, then hygge.toml
//     (hygge.toml values win over config.toml values within the user scope)
//  3. Profile: $XDG_CONFIG_HOME/hygge/profiles/<name>.toml
//     Resolved recursively via the "extends" key (max depth 8, cycle-detected).
//  4. Walk-up starting from opts.Pwd going up to $HOME.
//     Each directory is checked for .hygge/config.toml then .hygge/hygge.toml;
//     hygge.toml values win within the same directory.
//     Directories closer to Pwd have higher precedence overall.
//  5. PWD-level files (loaded directly from opts.Pwd, not from .hygge/):
//     hygge.toml is loaded first; hygge.local.toml is loaded second and wins.
//     Both have higher precedence than all walk-up project config files.
//  6. Environment variables: HYGGE_model__provider → model.provider.
//     Uses "__" (double underscore) as path-segment separator; single
//     underscores within a segment are preserved as part of the key name.
//     Format: HYGGE_<segment>__<segment>__<segment>=<value>
//     Each segment is case-folded to lowercase for config key lookup.
//     Examples:
//     HYGGE_model__provider=openai         → model.provider
//     HYGGE_permission__file_write=allow   → permission.file_write
//     Values are best-effort coerced.
//  7. CLI flags: opts.Flags (dotted-path map[string]any), merged last.
//
// # Unknown-keys policy
//
// The underlying merged map retains every key from every source so future
// tasks can pick up new sections without breaking existing user configs.
// The typed Config struct is decoded with ErrorUnused=false so extra keys in
// the map are silently ignored during struct decode. This lets users have
// e.g. an [mcp] section today without getting errors — it will be picked up
// once that package is implemented.  Validation of known keys is rigorous.
//
// # Unset sentinel
//
// A string value of "__hygge_unset__" removes the key from the merged result.
// Use this in a higher-precedence source to clear a key set by a lower one.
//
// # Env-var interpolation
//
// String values of the form $VAR or ${VAR} are expanded using opts.EnvLookup
// at merge time.  If the variable is not set the literal string is preserved
// and a slog.Warn message is emitted.
package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"

	"github.com/cfbender/hygge/internal/state"
)

// SubagentEntry is the per-type configuration that can appear inside the
// [subagents.<name>] table in config.toml (or in a profile file).  The schema
// is identical to the entries accepted by subagents.toml so both sources are
// interchangeable.
type SubagentEntry struct {
	Description string   `mapstructure:"description"`
	Prompt      string   `mapstructure:"prompt"`
	Tools       []string `mapstructure:"tools"`
	Model       string   `mapstructure:"model"`
}

// Config is the typed, fully-resolved configuration.
type Config struct {
	// Profile is the name of the active profile.  Not decoded from TOML;
	// set by Load after profile resolution.
	Profile string `mapstructure:"-"`

	// ProfileDir is the resolved directory for the active profile.
	// For a flat profile ($XDG_CONFIG_HOME/hygge/profiles/<name>.toml) this
	// is $XDG_CONFIG_HOME/hygge/profiles/<name> (which may not exist on disk).
	// For a directory profile ($XDG_CONFIG_HOME/hygge/profiles/<name>/config.toml)
	// this is that directory.  Empty when no profile is active (e.g. "default"
	// with no profile file).  Not decoded from TOML; set by Load.
	ProfileDir string `mapstructure:"-"`

	Model         ModelConfig         `mapstructure:"model"`
	Permission    PermissionConfig    `mapstructure:"permission"`
	Theme         ThemeConfig         `mapstructure:"theme"`
	UI            UIConfig            `mapstructure:"ui"`
	Compaction    CompactionConfig    `mapstructure:"compaction"`
	Session       SessionConfig       `mapstructure:"session"`
	Catalog       CatalogConfig       `mapstructure:"catalog"`
	Notifications NotificationsConfig `mapstructure:"notifications"`

	// Subagents holds subagent type definitions declared directly in
	// config.toml (or a profile).  Each key is the type name; the value
	// carries the same fields as a subagents.toml entry.  These are merged
	// through the normal profile machinery, so a profile can add or override
	// individual types.  The subagent registry loader consults this map in
	// addition to the legacy subagents.toml files.
	Subagents map[string]SubagentEntry `mapstructure:"subagents"`
	// Modes defines the agent modes available to the user. The first mode is
	// the default when modes exist. Each mode specifies its provider and model;
	// [[modes]] is the canonical source for active provider/model selection.
	// Empty modes are valid after Load() and signal that startup should route to
	// onboarding instead of synthesizing a fallback mode.
	Modes []ModeConfig `mapstructure:"modes"`

	// Plugins holds plugin source and per-plugin configuration.
	// Sources is the list of plugin source URIs declared in [plugins].sources.
	// PluginSettings maps plugin names to their [plugins.<name>] config tables.
	Plugins PluginsConfig `mapstructure:"plugins"`

	// raw is the full merged config map, preserved so PluginSettings can
	// extract dynamic [plugins.<name>] tables that mapstructure cannot
	// decode because the keys are not known at compile time.
	raw map[string]any
}

// RawPluginSettings returns the per-plugin config tables from the raw merged
// config.  Each key is a plugin name (from [plugins.<name>] in config.toml).
// Returns nil when no plugin-specific tables are set.
//
// Example TOML:
//
//	[plugins.policy-guard]
//	strict = true
//	blocked_patterns = ["rm -rf", "sudo"]
func (c *Config) RawPluginSettings() map[string]map[string]any {
	return PluginSettings(c.raw)
}

// PluginsConfig is the [plugins] section of config.toml.
type PluginsConfig struct {
	// Sources is the list of plugin source URIs.
	// Each entry must be a valid source URI (github: or local:).
	Sources []string `mapstructure:"sources"`
}

// PluginSettings returns the per-plugin config tables from the raw merged
// config map.  Each key is a plugin name (from [plugins.<name>] in
// config.toml) and the value is the free-form table content.
//
// The raw map is needed here because mapstructure cannot decode dynamic map
// keys; we extract them from the merged map directly.
func PluginSettings(raw map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any)
	pluginsRaw, ok := raw["plugins"]
	if !ok {
		return out
	}
	pluginsMap, ok := pluginsRaw.(map[string]any)
	if !ok {
		return out
	}
	for k, v := range pluginsMap {
		if k == "sources" {
			continue // not a plugin config table
		}
		if tbl, ok := v.(map[string]any); ok {
			out[k] = tbl
		}
	}
	return out
}

// SessionConfig controls session-lifecycle behaviour.
type SessionConfig struct {
	// ResumeDefault controls the bare `hygge` invocation's default
	// behaviour when no --continue or --new flag is set:
	//
	//   "new"      — always start a fresh session (default).
	//   "continue" — resume the cwd's most recent session when one
	//                exists; fall back to new when none does.
	//   "ask"      — open the resume picker on launch.
	//
	// The comparison is case-insensitive.  Any other value warns and
	// resets to "new".
	ResumeDefault string `mapstructure:"resume_default"`
}

// CatalogConfig controls the shared model catalog.
type CatalogConfig struct {
	// RefreshInterval is a Go duration string (e.g. "24h", "1h30m") that,
	// when non-empty, schedules a periodic background refresh of the
	// Catwalk catalog. Empty string (the default) means no periodic
	// refresh — the one-shot startup refresh still fires.
	//
	// Values that cannot be parsed as a duration, or that are negative,
	// are treated as empty with a slog.Warn.
	RefreshInterval string `mapstructure:"refresh_interval"`
}

// RefreshIntervalDuration parses the RefreshInterval string into a
// time.Duration.  Returns 0 when the string is empty.  Logs a Warn and
// returns 0 when the value is unparseable or negative.
func (c CatalogConfig) RefreshIntervalDuration() time.Duration {
	if c.RefreshInterval == "" {
		return 0
	}
	d, err := time.ParseDuration(c.RefreshInterval)
	if err != nil {
		slog.Warn("config: invalid catalog.refresh_interval, disabling periodic refresh",
			"value", c.RefreshInterval)
		return 0
	}
	if d < 0 {
		slog.Warn("config: negative catalog.refresh_interval, disabling periodic refresh",
			"value", c.RefreshInterval)
		return 0
	}
	return d
}

// CompactionConfig controls the compaction suggestion banner.
type CompactionConfig struct {
	// ThresholdPct is the percentage of the model's context window at which
	// the advisory suggestion banner appears.  0 disables the suggestion
	// entirely.  Valid range: 0–99.  Values ≥ 100 warn and reset to the
	// default (80).  Default: 80.
	ThresholdPct float64 `mapstructure:"threshold_pct"`
}

// ModelConfig holds model selection and provider-specific knobs.
type ModelConfig struct {
	Provider      string         `mapstructure:"provider"`
	Name          string         `mapstructure:"name"`
	SmallProvider string         `mapstructure:"small_provider"`
	SmallModel    string         `mapstructure:"small_model"`
	Options       map[string]any `mapstructure:"options"`
	// Reasoning is the session-default reasoning knob.  Allowed
	// values: "" / "off" (no reasoning), "low", "medium", "high".
	// Invalid values are reset to "" with a warning at load time so
	// a typo in a profile cannot block startup.
	Reasoning string `mapstructure:"reasoning"`
	// ReasoningBudget is an explicit Anthropic-style token budget
	// for extended thinking.  Zero means "derive from Reasoning"
	// (the standard low/medium/high mapping).  Negative values are
	// reset to zero with a warning.  Ignored by OpenAI-family
	// adapters; only the discrete effort knob affects their wire
	// format.
	ReasoningBudget int `mapstructure:"reasoning_budget"`
}

// ModeConfig defines a named agent mode. The app runs in a mode when one is
// configured; each mode specifies a provider, model, and optional reasoning
// level, system prompt, and accent color. Modes are declared as [[modes]]
// array-of-tables in TOML. When no modes are configured, startup routes to
// onboarding instead of synthesizing a fallback mode.
type ModeConfig struct {
	// Name is the display name for the mode (e.g. "smart", "rush", "deep").
	Name string `mapstructure:"name"`
	// Provider is the provider for this mode (e.g. "anthropic", "openrouter").
	// Required when modes are explicitly declared.
	Provider string `mapstructure:"provider"`
	// Model is the model name for this mode (e.g. "claude-sonnet-4-5").
	// Required when modes are explicitly declared.
	Model string `mapstructure:"model"`
	// Reasoning is the reasoning level for this mode ("off"/"low"/"medium"/"high").
	// Empty means no reasoning.
	Reasoning string `mapstructure:"reasoning"`
	// Prompt is an optional system prompt appended to the base system prompt
	// when this mode is active. Use "file:path" to read from a file (relative
	// paths resolve against the config directory, ~/... is expanded).
	Prompt string `mapstructure:"prompt"`
	// Description is a short human-readable description shown in the mode picker.
	Description string `mapstructure:"description"`
	// Color is the hex accent color for bubbles rendered in this mode.
	// Empty uses the theme default.
	Color string `mapstructure:"color"`
}

// PermissionConfig controls which operations require user approval.
type PermissionConfig struct {
	FileReadOutsidePwd PermissionMode `mapstructure:"file_read_outside_pwd"`
	FileWrite          PermissionMode `mapstructure:"file_write"`
	Shell              PermissionMode `mapstructure:"shell"`
	Network            PermissionMode `mapstructure:"network"`
	// MCP gates MCP tool invocations.  Defaults to "ask".  Servers
	// may override per-server via mcp.toml's permission_category.
	MCP PermissionMode `mapstructure:"mcp"`
	// Subagent gates subagent dispatch.  Defaults to "allow" because
	// subagents inherit the session's tool-level permissions.
	Subagent PermissionMode `mapstructure:"subagent"`
}

// ThemeConfig holds display-theme selection.
type ThemeConfig struct {
	Name string `mapstructure:"name"`
}

// UIConfig holds UI behaviour knobs.
type UIConfig struct {
	// NerdFonts controls whether nerd-font glyphs are used in the TUI.
	// Disable if your terminal font does not include nerd-font glyphs; we'll
	// render plain ASCII alternatives (e.g. ":main" instead of " main").
	// Default: true.
	NerdFonts bool `mapstructure:"nerd_fonts"`
}

// NotificationsConfig controls desktop notification behaviour.
type NotificationsConfig struct {
	// Enabled controls whether any desktop notifications are sent.
	// When false, all notification types are suppressed regardless of
	// the other fields.  Default: true.
	Enabled bool `mapstructure:"enabled"`
	// PermissionAsk, when true, sends a notification when the agent
	// requests permission to execute a tool.  Useful when the user
	// has switched to another window and wants to be alerted.
	// Default: true.
	PermissionAsk bool `mapstructure:"permission_ask"`
	// TurnComplete, when true, sends a notification when the agent
	// finishes a full turn.  Off by default to avoid notification
	// fatigue on short interactive sessions.  Default: false.
	TurnComplete bool `mapstructure:"turn_complete"`
}

// Source identifies where a config key value originated.
type Source struct {
	// File is the absolute path to the source file.
	// Special values: "<defaults>", "<env>", "<flag>".
	File string

	// Line is the 1-based line number within File, or 0 when unknown
	// (e.g. for defaults, env vars, and CLI flags).
	Line int
}

// Provenance maps a dotted-path config key (e.g. "model.name") to the
// ordered list of Sources that contributed to it.  For scalar leaves the
// last entry is the winning source.  For maps/arrays all contributing
// sources are listed in merge order.
type Provenance map[string][]Source

// LoadOptions controls the behaviour of Load.
type LoadOptions struct {
	// Pwd is the starting directory for .hygge/config.toml walk-up.
	// Defaults to os.Getwd() when empty.
	Pwd string

	// Profile is the explicit profile name to load.  When empty, Load
	// consults the state file and falls back to "default".
	Profile string

	// EnvLookup is used for environment-variable lookups and $VAR
	// interpolation.  Defaults to os.LookupEnv.  Override in tests.
	EnvLookup func(key string) (string, bool)

	// Flags contains pre-parsed CLI flag overrides keyed by dotted path.
	// Pass nil when there are no flag overrides.
	Flags map[string]any

	// HomeDir overrides $HOME for XDG path computation.  Useful in tests.
	HomeDir string

	// IgnoreExternalSources loads only built-in defaults and explicit Flags.
	// It skips user config, profiles, walk-up config, and HYGGE_* env overrides.
	IgnoreExternalSources bool
}

// Load resolves configuration from all sources and returns the merged,
// validated Config together with its Provenance map.
func Load(ctx context.Context, opts LoadOptions) (*Config, Provenance, error) {
	// Fill in defaults for LoadOptions fields that weren't set.
	if opts.EnvLookup == nil {
		opts.EnvLookup = os.LookupEnv
	}
	if opts.Pwd == "" {
		var err error
		opts.Pwd, err = os.Getwd()
		if err != nil {
			return nil, nil, fmt.Errorf("config: get working directory: %w", err)
		}
	}
	if opts.HomeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, fmt.Errorf("config: get home directory: %w", err)
		}
		opts.HomeDir = home
	}

	xdgConfigHome := xdgConfigDir(opts)
	xdgStateHome := xdgStateDir(opts)

	// Resolve profile name.
	profileName := "default"
	var err error
	if !opts.IgnoreExternalSources {
		profileName, err = resolveProfileName(opts, xdgConfigHome, xdgStateHome)
		if err != nil {
			return nil, nil, err
		}
	}

	// Enumerate all sources.
	var sources []configSource
	var profileDir string
	if !opts.IgnoreExternalSources {
		sources, profileDir, err = enumerateSources(ctx, opts, xdgConfigHome, profileName)
		if err != nil {
			return nil, nil, err
		}
	}

	// Start with builtin defaults.
	merged := defaultConfig()
	prov := make(Provenance)
	recordProvenance(prov, merged, Source{File: "<defaults>"}, "")

	// Merge each source in order.
	for _, src := range sources {
		m, err := loadTOMLFile(src.path)
		if err != nil {
			return nil, nil, &ParseError{File: src.path, Err: err}
		}
		// Strip the "extends" key — it's profile metadata, not config.
		delete(m, "extends")

		if err := deepMergeInto(merged, m, prov, src.source); err != nil {
			return nil, nil, err
		}
	}

	// Merge environment variables.
	if !opts.IgnoreExternalSources {
		envMap := buildEnvMap(opts)
		if len(envMap) > 0 {
			envSrc := Source{File: "<env>"}
			if err := deepMergeInto(merged, envMap, prov, envSrc); err != nil {
				return nil, nil, err
			}
		}
	}

	// Merge CLI flags.
	if len(opts.Flags) > 0 {
		flagMap := dottedToNested(opts.Flags)
		flagSrc := Source{File: "<flag>"}
		if err := deepMergeInto(merged, flagMap, prov, flagSrc); err != nil {
			return nil, nil, err
		}
	}

	// Apply env-var interpolation across all string leaves.
	interpolate(merged, opts.EnvLookup)

	// Decode to typed struct.
	cfg, err := decodeToStruct(merged)
	if err != nil {
		return nil, nil, err
	}
	cfg.Profile = profileName
	cfg.ProfileDir = profileDir
	cfg.raw = merged

	// Validate known fields.
	if err := validateConfig(cfg); err != nil {
		return nil, nil, err
	}

	return cfg, prov, nil
}

// xdgConfigDir returns $XDG_CONFIG_HOME or ~/.config.
func xdgConfigDir(opts LoadOptions) string {
	if v, ok := opts.EnvLookup("XDG_CONFIG_HOME"); ok && v != "" {
		return v
	}
	return filepath.Join(opts.HomeDir, ".config")
}

// xdgStateDir returns $XDG_STATE_HOME or ~/.local/state.
func xdgStateDir(opts LoadOptions) string {
	if v, ok := opts.EnvLookup("XDG_STATE_HOME"); ok && v != "" {
		return v
	}
	return filepath.Join(opts.HomeDir, ".local", "state")
}

// resolveProfileName picks the active profile name via the precedence:
// opts.Profile → PWD local default_profile → user config default_profile → state file (active_profile) → "default".
//
// "PWD local default_profile" is the highest-precedence file signal: it reads
// opts.Pwd/hygge.toml and then opts.Pwd/hygge.local.toml (in that order so
// hygge.local.toml wins), mirroring the file-merge order in enumerateSources.
func resolveProfileName(opts LoadOptions, xdgConfigHome, xdgStateHome string) (string, error) {
	if opts.Profile != "" {
		return opts.Profile, nil
	}

	// Consult PWD-level files first (highest-precedence file sources).
	// hygge.toml is checked before hygge.local.toml so that hygge.local.toml
	// wins when both declare default_profile.
	pwdProfile, err := defaultProfileFromPWDFiles(opts.Pwd)
	if err != nil {
		return "", err
	}
	if pwdProfile != "" {
		return pwdProfile, nil
	}

	profileName, err := defaultProfileFromUserConfig(xdgConfigHome)
	if err != nil {
		return "", err
	}
	if profileName != "" {
		return profileName, nil
	}

	st, err := state.Load(state.LoadOptions{
		HomeDir:      opts.HomeDir,
		XDGStateHome: xdgStateHome,
	})
	if err != nil {
		// A corrupt or unreadable state file is non-fatal for profile
		// resolution: fall through to "default" and log a warning so the
		// user knows something unexpected happened.
		slog.Warn("config: could not read state file, using default profile",
			"err", err)
		return "default", nil
	}
	if st.ActiveProfile != "" {
		return st.ActiveProfile, nil
	}

	return "default", nil
}

// defaultProfileFromPWDFiles reads default_profile from opts.Pwd/hygge.toml
// and opts.Pwd/hygge.local.toml (in that order).  The last non-empty value
// wins, so hygge.local.toml beats hygge.toml.  Returns "" when neither file
// exists or neither sets default_profile.
func defaultProfileFromPWDFiles(pwd string) (string, error) {
	result := ""
	for _, name := range []string{"hygge.toml", "hygge.local.toml"} {
		path := filepath.Join(pwd, name)
		data, err := os.ReadFile(path) //nolint:gosec // intentional config path
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("config: read PWD config for default profile: %w", err)
		}
		m, err := parseTOMLBytes(data)
		if err != nil {
			return "", &ParseError{File: path, Err: err}
		}
		if profileName, _ := m["default_profile"].(string); strings.TrimSpace(profileName) != "" {
			result = strings.TrimSpace(profileName)
		}
	}
	return result, nil
}

func defaultProfileFromUserConfig(xdgConfigHome string) (string, error) {
	hyggeDir := filepath.Join(xdgConfigHome, "hygge")
	// Check config.toml first, then hygge.toml; the latter wins when both exist
	// because it is checked last and its non-empty result takes precedence.
	result := ""
	for _, name := range []string{"config.toml", "hygge.toml"} {
		path := filepath.Join(hyggeDir, name)
		data, err := os.ReadFile(path) //nolint:gosec // intentional config path
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", fmt.Errorf("config: read user config for default profile: %w", err)
		}
		m, err := parseTOMLBytes(data)
		if err != nil {
			return "", &ParseError{File: path, Err: err}
		}
		if profileName, _ := m["default_profile"].(string); strings.TrimSpace(profileName) != "" {
			result = strings.TrimSpace(profileName)
		}
	}
	return result, nil
}

// decodeToStruct converts the merged map to a typed Config using mapstructure.
// ErrorUnused=false: extra keys in the map are fine (future expandability).
// ErrorUnset=false: struct fields without a corresponding map key are fine.
// WeaklyTypedInput=true: best-effort coercion from env-var strings.
func decodeToStruct(m map[string]any) (*Config, error) {
	var cfg Config
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:           &cfg,
		WeaklyTypedInput: true,
		ErrorUnused:      false,
		ErrorUnset:       false,
		TagName:          "mapstructure",
	})
	if err != nil {
		return nil, fmt.Errorf("config: create decoder: %w", err)
	}
	if err := dec.Decode(m); err != nil {
		return nil, fmt.Errorf("config: decode config: %w", err)
	}
	return &cfg, nil
}

// validateConfig validates the values of known fields.
func validateConfig(cfg *Config) error {
	valid := map[PermissionMode]bool{
		PermAllow: true,
		PermAsk:   true,
		PermDeny:  true,
	}
	type pf struct {
		key string
		val PermissionMode
	}
	perms := []pf{
		{"permission.file_read_outside_pwd", cfg.Permission.FileReadOutsidePwd},
		{"permission.file_write", cfg.Permission.FileWrite},
		{"permission.shell", cfg.Permission.Shell},
		{"permission.network", cfg.Permission.Network},
		{"permission.mcp", cfg.Permission.MCP},
		{"permission.subagent", cfg.Permission.Subagent},
	}
	for _, p := range perms {
		if !valid[p.val] {
			return &InvalidValueError{
				Key:   p.key,
				Value: p.val,
				Msg:   `must be one of "allow", "ask", "deny"`,
			}
		}
	}

	// Reasoning is best-effort: invalid values warn and reset to ""
	// rather than fail the load.  Applies to model.reasoning when set
	// (backward compat: [model] reasoning still accepted alongside modes).
	switch strings.ToLower(strings.TrimSpace(cfg.Model.Reasoning)) {
	case "", "off", "low", "medium", "high":
		cfg.Model.Reasoning = strings.ToLower(strings.TrimSpace(cfg.Model.Reasoning))
	default:
		slog.Warn("config: invalid model.reasoning, resetting to off",
			"value", cfg.Model.Reasoning)
		cfg.Model.Reasoning = ""
	}
	if cfg.Model.ReasoningBudget < 0 {
		slog.Warn("config: invalid model.reasoning_budget, resetting to 0",
			"value", cfg.Model.ReasoningBudget)
		cfg.Model.ReasoningBudget = 0
	}

	// [[modes]] is now the canonical source for active provider/model.
	//
	// Empty modes are allowed — the TUI detects the missing config and
	// opens the onboarding wizard.
	//
	// When modes are present every mode must declare both provider and
	// model; missing fields are a hard validation error so operators
	// get a clear message instead of a silent fallback.
	for i := range cfg.Modes {
		if strings.TrimSpace(cfg.Modes[i].Provider) == "" {
			return &InvalidValueError{
				Key:   fmt.Sprintf("modes[%d].provider", i),
				Value: "",
				Msg:   fmt.Sprintf("mode %q must declare a provider", cfg.Modes[i].Name),
			}
		}
		if strings.TrimSpace(cfg.Modes[i].Model) == "" {
			return &InvalidValueError{
				Key:   fmt.Sprintf("modes[%d].model", i),
				Value: "",
				Msg:   fmt.Sprintf("mode %q must declare a model", cfg.Modes[i].Name),
			}
		}
	}

	// Compaction threshold: 0 is valid (disables suggestion); ≥ 100 warns and
	// resets to the default 80.
	if cfg.Compaction.ThresholdPct >= 100 {
		slog.Warn("config: invalid compaction.threshold_pct, resetting to 80",
			"value", cfg.Compaction.ThresholdPct)
		cfg.Compaction.ThresholdPct = 80
	}

	// Session resume_default: accepts "new", "continue", "ask"
	// case-insensitively.  Anything else warns and resets to "new".
	switch strings.ToLower(strings.TrimSpace(cfg.Session.ResumeDefault)) {
	case "", "new":
		cfg.Session.ResumeDefault = "new"
	case "continue":
		cfg.Session.ResumeDefault = "continue"
	case "ask":
		cfg.Session.ResumeDefault = "ask"
	default:
		slog.Warn("config: invalid session.resume_default, resetting to new",
			"value", cfg.Session.ResumeDefault)
		cfg.Session.ResumeDefault = "new"
	}

	// Catalog refresh_interval: validate by parsing; bad values warn and
	// are reset to "" (disabled) so startup is never blocked.
	if cfg.Catalog.RefreshInterval != "" {
		d, err := time.ParseDuration(cfg.Catalog.RefreshInterval)
		if err != nil {
			slog.Warn("config: invalid catalog.refresh_interval, disabling periodic refresh",
				"value", cfg.Catalog.RefreshInterval)
			cfg.Catalog.RefreshInterval = ""
		} else if d < 0 {
			slog.Warn("config: negative catalog.refresh_interval, disabling periodic refresh",
				"value", cfg.Catalog.RefreshInterval)
			cfg.Catalog.RefreshInterval = ""
		}
	}

	return nil
}

// recordProvenance adds a Source entry for every leaf key in m under the
// given dot-prefix.
func recordProvenance(prov Provenance, m map[string]any, src Source, prefix string) {
	for k, v := range m {
		key := k
		if prefix != "" {
			key = prefix + "." + k
		}
		switch val := v.(type) {
		case map[string]any:
			recordProvenance(prov, val, src, key)
		default:
			prov[key] = append(prov[key], src)
		}
	}
}

// interpolate walks a map[string]any and replaces $VAR / ${VAR} patterns
// in string values using the provided lookup function.
func interpolate(m map[string]any, lookup func(string) (string, bool)) {
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			interpolate(val, lookup)
		case string:
			m[k] = expandEnvValue(val, lookup)
		}
	}
}

// expandEnvValue expands $VAR and ${VAR} references in a single string.
// If the referenced variable is not set, the literal $VAR is preserved and
// a warning is logged.
// TODO: add 1Password op:// resolution here in a future task.
func expandEnvValue(s string, lookup func(string) (string, bool)) string {
	result := os.Expand(s, func(varName string) string {
		if val, ok := lookup(varName); ok {
			return val
		}
		slog.Warn("config: env var not set, keeping literal", "var", varName)
		return "$" + varName
	})
	return result
}

// dottedToNested converts a flat dotted-path map to a nested map[string]any.
// e.g. {"model.name": "x"} → {"model": {"name": "x"}}
func dottedToNested(flat map[string]any) map[string]any {
	out := map[string]any{}
	for dotted, val := range flat {
		parts := strings.Split(dotted, ".")
		cur := out
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = val
			} else {
				if _, exists := cur[part]; !exists {
					cur[part] = map[string]any{}
				}
				next, ok := cur[part].(map[string]any)
				if !ok {
					next = map[string]any{}
					cur[part] = next
				}
				cur = next
			}
		}
	}
	return out
}
