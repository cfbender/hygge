// Package config loads, merges, and validates Hygge configuration from
// multiple TOML sources.
//
// # Merge order (lowest → highest precedence)
//
//  1. Builtin defaults (defaults.go)
//  2. User config: $XDG_CONFIG_HOME/hygge/config.toml
//  3. Profile: $XDG_CONFIG_HOME/hygge/profiles/<name>.toml
//     Resolved recursively via the "extends" key (max depth 8, cycle-detected).
//  4. Walk-up .hygge/config.toml starting from opts.Pwd going up to $HOME.
//     Files closer to Pwd have higher precedence.
//  5. Environment variables: HYGGE_MODEL_PROVIDER → model.provider.
//     Nested keys use "_" as segment separator; values are best-effort coerced.
//  6. CLI flags: opts.Flags (dotted-path map[string]any), merged last.
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
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mitchellh/mapstructure"
)

// Config is the typed, fully-resolved configuration.
type Config struct {
	// Profile is the name of the active profile.  Not decoded from TOML;
	// set by Load after profile resolution.
	Profile string `mapstructure:"-"`

	Model      ModelConfig      `mapstructure:"model"`
	Permission PermissionConfig `mapstructure:"permission"`
	Theme      ThemeConfig      `mapstructure:"theme"`
}

// ModelConfig holds model selection and provider-specific knobs.
type ModelConfig struct {
	Provider string         `mapstructure:"provider"`
	Name     string         `mapstructure:"name"`
	Options  map[string]any `mapstructure:"options"`
}

// PermissionConfig controls which operations require user approval.
type PermissionConfig struct {
	FileReadOutsidePwd PermissionMode `mapstructure:"file_read_outside_pwd"`
	FileWrite          PermissionMode `mapstructure:"file_write"`
	Shell              PermissionMode `mapstructure:"shell"`
	Network            PermissionMode `mapstructure:"network"`
}

// ThemeConfig holds display-theme selection.
type ThemeConfig struct {
	Name string `mapstructure:"name"`
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
	profileName, err := resolveProfileName(opts, xdgStateHome)
	if err != nil {
		return nil, nil, err
	}

	// Enumerate all sources.
	sources, err := enumerateSources(ctx, opts, xdgConfigHome, profileName)
	if err != nil {
		return nil, nil, err
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
	envMap := buildEnvMap(opts)
	if len(envMap) > 0 {
		envSrc := Source{File: "<env>"}
		if err := deepMergeInto(merged, envMap, prov, envSrc); err != nil {
			return nil, nil, err
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
// opts.Profile → state file → "default".
func resolveProfileName(opts LoadOptions, xdgStateHome string) (string, error) {
	if opts.Profile != "" {
		return opts.Profile, nil
	}

	stateFile := filepath.Join(xdgStateHome, "hygge", "state.json")
	data, err := os.ReadFile(stateFile) //nolint:gosec // intentional: path is XDG state dir
	if err == nil {
		var s struct {
			Profile string `json:"profile"`
		}
		if jsonErr := json.Unmarshal(data, &s); jsonErr == nil && s.Profile != "" {
			return s.Profile, nil
		}
	}

	return "default", nil
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

	if strings.TrimSpace(cfg.Model.Provider) == "" {
		return &InvalidValueError{Key: "model.provider", Value: cfg.Model.Provider, Msg: "must not be empty"}
	}
	if strings.TrimSpace(cfg.Model.Name) == "" {
		return &InvalidValueError{Key: "model.name", Value: cfg.Model.Name, Msg: "must not be empty"}
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
