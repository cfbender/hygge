package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// WriteModelOptions controls the narrow config writer used by runtime model
// selection.  It only writes model.provider and model.name.
type WriteModelOptions struct {
	HomeDir       string
	XDGConfigHome string
	Pwd           string
	Provenance    Provenance
}

// WriteProviderAPIKeyOptions controls the narrow config writer used for
// provider credentials. It shares the model writer's target-resolution inputs.
type WriteProviderAPIKeyOptions = WriteModelOptions

// WriteThemeSelectionOptions controls the narrow config writer used by runtime
// theme selection. It shares the model writer's target-resolution inputs.
type WriteThemeSelectionOptions = WriteModelOptions

// WritePluginSourcesOptions controls the narrow config writer used by
// `hygge plugins install/remove`. It shares the model writer's
// target-resolution inputs.
type WritePluginSourcesOptions = WriteModelOptions

// WriteDefaultProfileOptions controls the narrow config writer used by
// `hygge profile use`. It always writes the user config, not project config.
type WriteDefaultProfileOptions = WriteModelOptions

// WriteModelSelection persists provider/name to one deterministic writable
// file. Target policy: if the winning model provenance already comes from a
// real config file, update that file; otherwise create/update the user config
// at $XDG_CONFIG_HOME/hygge/config.toml. Env/flag/default-only selections are
// never rewritten in place. Existing TOML is decoded to a generic map before
// writing so unrelated keys and unknown sections are preserved; comments may
// be reformatted by go-toml.
func WriteModelSelection(opts WriteModelOptions, providerName, modelName string) (string, error) {
	if providerName == "" || modelName == "" {
		return "", fmt.Errorf("config: model provider and name are required")
	}
	target := modelWriteTarget(opts)
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read model target: %w", err)
	}

	model, ok := m["model"].(map[string]any)
	if !ok {
		model = map[string]any{}
		m["model"] = model
	}
	model["provider"] = providerName
	model["name"] = modelName

	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(m); err != nil {
		return target, fmt.Errorf("config: encode model target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return target, fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o600); err != nil {
		return target, fmt.Errorf("config: write model target: %w", err)
	}
	return target, nil
}

// WriteProviderAPIKey persists apiKey into model.options.api_key while
// preserving unrelated config fields and existing model options.
func WriteProviderAPIKey(opts WriteProviderAPIKeyOptions, providerName, apiKey string) (string, error) {
	if strings.TrimSpace(providerName) == "" || strings.TrimSpace(apiKey) == "" {
		return "", fmt.Errorf("config: provider and api key are required")
	}
	target := providerAPIKeyWriteTarget(opts)
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read api key target: %w", err)
	}
	model, ok := m["model"].(map[string]any)
	if !ok {
		model = map[string]any{}
		m["model"] = model
	}
	if existing, ok := model["provider"].(string); ok && existing != "" && existing != providerName {
		return target, fmt.Errorf("config: target model provider is %q, not %q", existing, providerName)
	}
	model["provider"] = providerName
	options, ok := model["options"].(map[string]any)
	if !ok {
		options = map[string]any{}
		model["options"] = options
	}
	options["api_key"] = apiKey
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return target, fmt.Errorf("config: encode api key target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return target, fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o600); err != nil {
		return target, fmt.Errorf("config: write api key target: %w", err)
	}
	return target, nil
}

// WriteThemeSelection persists theme.name while preserving unrelated config
// fields. Target policy mirrors the model writer: update the winning real
// theme provenance when known, otherwise write the user config.
func WriteThemeSelection(opts WriteThemeSelectionOptions, themeName string) (string, error) {
	if strings.TrimSpace(themeName) == "" {
		return "", fmt.Errorf("config: theme name is required")
	}
	target := themeWriteTarget(opts)
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read theme target: %w", err)
	}
	theme, ok := m["theme"].(map[string]any)
	if !ok {
		theme = map[string]any{}
		m["theme"] = theme
	}
	theme["name"] = strings.TrimSpace(themeName)
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return target, fmt.Errorf("config: encode theme target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return target, fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o600); err != nil {
		return target, fmt.Errorf("config: write theme target: %w", err)
	}
	return target, nil
}

// WriteDefaultProfile persists default_profile in the user config while
// preserving unrelated config fields. CLI --profile still overrides this value.
func WriteDefaultProfile(opts WriteDefaultProfileOptions, profileName string) (string, error) {
	profileName = strings.TrimSpace(profileName)
	if profileName == "" {
		return "", fmt.Errorf("config: default profile name is required")
	}
	target := filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read default profile target: %w", err)
	}
	m["default_profile"] = profileName

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return target, fmt.Errorf("config: encode default profile target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return target, fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o600); err != nil {
		return target, fmt.Errorf("config: write default profile target: %w", err)
	}
	return target, nil
}

// WritePluginSources persists [plugins].sources while preserving unrelated
// config fields and per-plugin [plugins.<name>] tables. Target policy mirrors
// the other narrow writers: update the winning real plugins.sources file when
// known; otherwise create/update the user config.
func WritePluginSources(opts WritePluginSourcesOptions, sources []string) (string, error) {
	target := pluginSourcesWriteTarget(opts)
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read plugin sources target: %w", err)
	}

	plugins, ok := m["plugins"].(map[string]any)
	if !ok {
		plugins = map[string]any{}
		m["plugins"] = plugins
	}
	plugins["sources"] = append([]string(nil), sources...)

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return target, fmt.Errorf("config: encode plugin sources target: %w", err)
	}
	if err := atomicWriteConfig(target, buf.Bytes()); err != nil {
		return target, fmt.Errorf("config: write plugin sources target: %w", err)
	}
	return target, nil
}

func providerAPIKeyWriteTarget(opts WriteProviderAPIKeyOptions) string {
	for _, key := range []string{"model.options.api_key", "model.provider"} {
		if path := lastRealSource(opts.Provenance[key]); path != "" {
			return path
		}
	}
	return filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
}

func modelWriteTarget(opts WriteModelOptions) string {
	for _, key := range []string{"model.provider", "model.name"} {
		if path := lastRealSource(opts.Provenance[key]); path != "" {
			return path
		}
	}
	return filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
}

func themeWriteTarget(opts WriteThemeSelectionOptions) string {
	if path := lastRealSource(opts.Provenance["theme.name"]); path != "" {
		return path
	}
	return filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
}

func pluginSourcesWriteTarget(opts WritePluginSourcesOptions) string {
	if path := lastRealSource(opts.Provenance["plugins.sources"]); path != "" {
		return path
	}
	return filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
}

func atomicWriteConfig(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // intentional config path resolved by writer target policy
		return fmt.Errorf("create config dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() //nolint:gosec // temp path returned by os.CreateTemp in config dir

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil { //nolint:gosec // intentional atomic replacement of resolved config path
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func lastRealSource(sources []Source) string {
	for i := len(sources) - 1; i >= 0; i-- {
		file := sources[i].File
		if file == "" || strings.HasPrefix(file, "<") {
			continue
		}
		return file
	}
	return ""
}

// WriteInitStyleOptions controls the writer used by `hygge init`.
type WriteInitStyleOptions = WriteModelOptions

// InitStyleConfig is a complete mode/subagent preset selected by `hygge init`.
type InitStyleConfig struct {
	Modes     []ModeConfig
	Subagents []OnboardingSubagent
}

// WriteInitStyle persists all modes for an init style to the user config and
// any subagents to the user subagents.toml. Existing unrelated config fields
// are preserved. Existing modes are replaced because init is a bootstrap/reset
// operation for the user's agent layout.
func WriteInitStyle(opts WriteInitStyleOptions, style InitStyleConfig) (string, error) {
	if len(style.Modes) == 0 {
		return "", fmt.Errorf("config: init style requires at least one mode")
	}
	for _, mode := range style.Modes {
		if strings.TrimSpace(mode.Name) == "" || strings.TrimSpace(mode.Provider) == "" || strings.TrimSpace(mode.Model) == "" {
			return "", fmt.Errorf("config: init style mode requires name, provider, and model")
		}
	}

	target := filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read init target: %w", err)
	}

	first := style.Modes[0]
	modelMap, ok := m["model"].(map[string]any)
	if !ok {
		modelMap = map[string]any{}
		m["model"] = modelMap
	}
	modelMap["provider"] = first.Provider
	modelMap["name"] = first.Model
	if first.Reasoning != "" {
		modelMap["reasoning"] = first.Reasoning
	}

	modes := make([]any, 0, len(style.Modes))
	for _, mode := range style.Modes {
		entry := map[string]any{
			"name":     mode.Name,
			"provider": mode.Provider,
			"model":    mode.Model,
		}
		if mode.Prompt != "" {
			entry["prompt"] = mode.Prompt
		}
		if mode.Description != "" {
			entry["description"] = mode.Description
		}
		if mode.Color != "" {
			entry["color"] = mode.Color
		}
		if mode.Reasoning != "" {
			entry["reasoning"] = mode.Reasoning
		}
		modes = append(modes, entry)
	}
	m["modes"] = modes

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return target, fmt.Errorf("config: encode init target: %w", err)
	}
	if err := atomicWriteConfig(target, buf.Bytes()); err != nil {
		return target, fmt.Errorf("config: write init target: %w", err)
	}
	if err := WriteSubagentsToml(WriteSubagentsTomlOptions{HomeDir: opts.HomeDir, XDGConfigHome: opts.XDGConfigHome}, style.Subagents); err != nil {
		return target, err
	}
	return target, nil
}

// WriteOnboardingModeOptions controls the narrow config writer for the
// onboarding wizard's mode result. It shares the model writer's target-
// resolution inputs.
type WriteOnboardingModeOptions = WriteModelOptions

// WriteSubagentsTomlOptions configures WriteSubagentsToml.
// Only HomeDir and XDGConfigHome are used.
type WriteSubagentsTomlOptions struct {
	HomeDir       string
	XDGConfigHome string
}

// OnboardingSubagent is a minimal sub-agent descriptor used during onboarding.
// It carries only what the wizard collects; the runtime loads the canonical
// subagent.Type after bootstrap.
type OnboardingSubagent struct {
	Name        string
	Description string
	Prompt      string
	Model       string // optional "<provider>/<model>" ref; empty = inherit
}

// WriteOnboardingMode persists a single [[modes]] entry produced by the
// onboarding wizard into the user config at $XDG_CONFIG_HOME/hygge/config.toml.
// It always targets the user config (no provenance reuse) because onboarding
// only ever runs when no real model/auth config exists.
// The first call creates the file; subsequent calls from mode editing
// replace the matching entry.
func WriteOnboardingMode(opts WriteOnboardingModeOptions, mode ModeConfig) (string, error) {
	if mode.Name == "" || mode.Provider == "" || mode.Model == "" {
		return "", fmt.Errorf("config: onboarding mode requires name, provider, and model")
	}
	target := filepath.Join(resolveWriterXDGConfig(opts), "hygge", "config.toml")
	m := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		parsed, err := parseTOMLBytes(data)
		if err != nil {
			return target, &ParseError{File: target, Err: err}
		}
		m = parsed
	} else if !os.IsNotExist(err) {
		return target, fmt.Errorf("config: read onboarding target: %w", err)
	}

	// Write model.provider and model.name so bare `hygge` works without
	// an explicit [[modes]] section in configs that rely on defaults.
	modelMap, ok := m["model"].(map[string]any)
	if !ok {
		modelMap = map[string]any{}
		m["model"] = modelMap
	}
	modelMap["provider"] = mode.Provider
	modelMap["name"] = mode.Model

	// Encode the mode as a TOML table that can sit in an [[modes]] array.
	// We find or replace by Name inside any existing "modes" slice.
	newEntry := map[string]any{
		"name":     mode.Name,
		"provider": mode.Provider,
		"model":    mode.Model,
	}
	if mode.Prompt != "" {
		newEntry["prompt"] = mode.Prompt
	}
	if mode.Description != "" {
		newEntry["description"] = mode.Description
	}
	if mode.Color != "" {
		newEntry["color"] = mode.Color
	}
	if mode.Reasoning != "" {
		newEntry["reasoning"] = mode.Reasoning
	}

	existing, _ := m["modes"].([]any)
	kept := make([]any, 0, len(existing)+1)
	replaced := false
	for _, raw := range existing {
		tbl, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		name, _ := tbl["name"].(string)
		switch name {
		case mode.Name:
			kept = append(kept, newEntry)
			replaced = true
		case "General":
			// Onboarding creates an explicit first mode, so drop the synthesized
			// fallback mode if it was previously written to config.
			continue
		default:
			kept = append(kept, raw)
		}
	}
	if !replaced {
		kept = append(kept, newEntry)
	}
	m["modes"] = kept

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(m); err != nil {
		return target, fmt.Errorf("config: encode onboarding target: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return target, fmt.Errorf("config: create config dir: %w", err)
	}
	if err := os.WriteFile(target, buf.Bytes(), 0o600); err != nil {
		return target, fmt.Errorf("config: write onboarding target: %w", err)
	}
	return target, nil
}

// WriteSubagentsToml appends or replaces OnboardingSubagent entries in the
// user-level $XDG_CONFIG_HOME/hygge/subagents.toml file.  Entries sharing a
// Name are updated in-place; new names are appended.  Existing entries for
// other names are left untouched.
func WriteSubagentsToml(opts WriteSubagentsTomlOptions, agents []OnboardingSubagent) error {
	if len(agents) == 0 {
		return nil
	}
	xdg := opts.XDGConfigHome
	if xdg == "" {
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			xdg = v
		} else {
			home := opts.HomeDir
			if home == "" {
				var err error
				if home, err = os.UserHomeDir(); err != nil {
					return fmt.Errorf("config: subagents toml: home dir: %w", err)
				}
			}
			xdg = filepath.Join(home, ".config")
		}
	}
	target := filepath.Join(xdg, "hygge", "subagents.toml")

	// Read or initialise the TOML map.
	raw := map[string]any{}
	if data, err := os.ReadFile(target); err == nil { //nolint:gosec // intentional config path
		if err2 := toml.Unmarshal(data, &raw); err2 != nil {
			return fmt.Errorf("config: parse subagents.toml: %w", err2)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("config: read subagents.toml: %w", err)
	}

	// Ensure top-level [subagents] table exists.
	subMap, ok := raw["subagents"].(map[string]any)
	if !ok {
		subMap = map[string]any{}
		raw["subagents"] = subMap
	}

	for _, ag := range agents {
		if ag.Name == "" {
			continue
		}
		entry := map[string]any{
			"description": ag.Description,
			"prompt":      ag.Prompt,
		}
		if ag.Model != "" {
			entry["model"] = ag.Model
		}
		subMap[ag.Name] = entry
	}

	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(raw); err != nil {
		return fmt.Errorf("config: encode subagents.toml: %w", err)
	}
	if err := atomicWriteConfig(target, buf.Bytes()); err != nil {
		return fmt.Errorf("config: write subagents.toml: %w", err)
	}
	return nil
}

func resolveWriterXDGConfig(opts WriteModelOptions) string {
	if opts.XDGConfigHome != "" {
		return opts.XDGConfigHome
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return v
	}
	home := opts.HomeDir
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return filepath.Join(home, ".config")
}
