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
