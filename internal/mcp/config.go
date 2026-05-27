package mcp

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Source describes which discovery layer a ServerConfig came from.
// Values are ordered by precedence (lower-priority first): later
// values override earlier values on name collisions.
type Source int

// Source values, ordered by precedence (lower-priority first).
const (
	// SourceUserAgents is ~/.agents/mcp.toml.
	SourceUserAgents Source = iota
	// SourceUserHygge is ~/.config/hygge/mcp.toml.
	SourceUserHygge
	// SourceProjectAgents is <pwd>/.agents/mcp.toml (walk-up).
	SourceProjectAgents
	// SourceProjectHygge is <pwd>/.hygge/mcp.toml (walk-up).
	SourceProjectHygge
)

// String returns a short diagnostic token for the source.
func (s Source) String() string {
	switch s {
	case SourceUserAgents:
		return "user/.agents"
	case SourceUserHygge:
		return "user/hygge"
	case SourceProjectAgents:
		return "project/.agents"
	case SourceProjectHygge:
		return "project/hygge"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// validTransports is the set of transport identifiers accepted in
// mcp.toml.  Anything else is rejected at load time.
//
// Supported values:
//
//   - "stdio"    — subprocess over stdin/stdout (default).
//   - "sse"      — SSE transport (2024-11-05 spec); use `url` to point at
//     the SSE endpoint.
//   - "http"     — Streamable HTTP transport (2025-03-26 spec, preferred
//     for new servers); use `url` to point at the single MCP
//     endpoint URL.
var validTransports = map[string]bool{
	"stdio": true,
	"sse":   true,
	"http":  true,
}

// validPermissionCategories is the set of permission category names
// accepted in mcp.toml.  An invalid value falls back to "mcp" with a
// slog.Warn so misconfigured files still produce a working client.
var validPermissionCategories = map[string]bool{
	"mcp":        true,
	"shell":      true,
	"network":    true,
	"file.read":  true,
	"file.write": true,
}

// ServerConfig is one [[servers]] entry from mcp.toml.
type ServerConfig struct {
	Name      string
	Transport string
	Command   string
	Args      []string
	Env       map[string]string
	Dir       string

	// URL is the SSE or Streamable HTTP endpoint URL.  Required when
	// Transport is "sse" or "http".
	URL string

	// Headers are sent on every HTTP request for SSE and Streamable
	// HTTP transports. Values may reference $VAR / ${VAR} which are
	// expanded at load time via os.LookupEnv.
	Headers map[string]string

	// OAuth enables OAuth bearer-token injection from mcp-auth.json for
	// SSE and Streamable HTTP transports and optionally carries OAuth
	// client configuration.
	OAuth OAuthConfig

	// Enabled defaults to true when unset.  Disabled servers are
	// returned from LoadConfigs so `hygge mcp list` can show them,
	// but bootstrap skips spawning them.
	Enabled bool

	// PermissionCategory is the permission category used when this
	// server's tools are invoked.  Defaults to "mcp".
	PermissionCategory string

	// Source identifies which discovery layer this config came from.
	Source Source

	// Path is the absolute path of the mcp.toml file this config
	// was read from.  Diagnostics only.
	Path string
}

// LoadOptions configures LoadConfigs.  At least Pwd should be set if
// the caller wants project-level discovery; HomeDir falls back to
// os.UserHomeDir() when zero.
type LoadOptions struct {
	HomeDir       string
	XDGConfigHome string
	Pwd           string

	// EnvLookup is the function used for $VAR interpolation in
	// Command, each Args element, and each Env value.  Falls back to
	// os.Getenv when nil.
	EnvLookup func(string) string
}

// tomlSchema is the surface shape of mcp.toml.  Unknown top-level keys
// trigger a slog.Warn but do not fail the load.
type tomlSchema struct {
	Servers []tomlServer `toml:"servers"`
}

type tomlServer struct {
	Name               string            `toml:"name"`
	Transport          string            `toml:"transport"`
	Command            string            `toml:"command"`
	Args               []string          `toml:"args"`
	Env                map[string]string `toml:"env"`
	Dir                string            `toml:"dir"`
	URL                string            `toml:"url"`
	Headers            map[string]string `toml:"headers"`
	OAuth              any               `toml:"oauth"`
	Enabled            *bool             `toml:"enabled"`
	PermissionCategory string            `toml:"permission_category"`
}

// LoadConfigs walks the four .agents/.hygge layers and returns every
// declared MCP server.  Servers with the same Name are deduped using
// precedence order (later layers win).  Missing files are ignored;
// malformed files are reported as load errors so the user sees them
// instead of silently dropping servers.
func LoadConfigs(opts LoadOptions) ([]ServerConfig, error) {
	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("mcp: home dir: %w", err)
		}
		homeDir = h
	}
	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}
	envLookup := opts.EnvLookup
	if envLookup == nil {
		envLookup = os.Getenv
	}

	byName := make(map[string]ServerConfig)
	order := []string{} // preserves first-seen order; later overrides update in place

	// Layer 1: ~/.agents/mcp.toml
	if err := loadOneFile(byName, &order, filepath.Join(homeDir, ".agents", "mcp.toml"), SourceUserAgents, envLookup); err != nil {
		return nil, err
	}
	// Layer 2: ~/.config/hygge/mcp.toml
	if err := loadOneFile(byName, &order, filepath.Join(xdgConfig, "hygge", "mcp.toml"), SourceUserHygge, envLookup); err != nil {
		return nil, err
	}

	// Layers 3 + 4: project walk-up.
	if opts.Pwd != "" {
		if p := findProjectFile(opts.Pwd, filepath.Join(".agents", "mcp.toml"), homeDir); p != "" {
			if err := loadOneFile(byName, &order, p, SourceProjectAgents, envLookup); err != nil {
				return nil, err
			}
		}
		if p := findProjectFile(opts.Pwd, filepath.Join(".hygge", "mcp.toml"), homeDir); p != "" {
			if err := loadOneFile(byName, &order, p, SourceProjectHygge, envLookup); err != nil {
				return nil, err
			}
		}
	}

	out := make([]ServerConfig, 0, len(order))
	for _, name := range order {
		out = append(out, byName[name])
	}
	return out, nil
}

// loadOneFile parses path (if present) and merges its entries into
// byName.  Missing files are silently skipped.  Parse / validation
// errors fail the whole load — a malformed mcp.toml is the user's
// problem to fix.
func loadOneFile(
	byName map[string]ServerConfig,
	order *[]string,
	path string,
	src Source,
	envLookup func(string) string,
) error {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from controlled discovery roots
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("mcp: read %s: %w", path, err)
	}

	// First pass: detect unknown top-level keys for forward-compat
	// warnings.
	var rawTop map[string]any
	if err := toml.Unmarshal(data, &rawTop); err == nil {
		for k := range rawTop {
			if k != "servers" {
				slog.Warn("mcp: unknown top-level key in mcp.toml; ignored",
					"path", path, "key", k)
			}
		}
	}

	var schema tomlSchema
	dec := toml.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&schema); err != nil {
		// Retry without strict mode so we don't bail on a forward-
		// compatible unknown key.  We logged the unknown top-level
		// key above; per-server unknowns surface here.
		var fallback tomlSchema
		if err2 := toml.Unmarshal(data, &fallback); err2 != nil {
			return fmt.Errorf("mcp: parse %s: %w", path, err2)
		}
		slog.Warn("mcp: mcp.toml contained unknown fields; proceeding (forward-compat)",
			"path", path, "err", err.Error())
		schema = fallback
	}

	for i, s := range schema.Servers {
		cfg, err := normalizeServer(s, src, path, envLookup)
		if err != nil {
			return fmt.Errorf("mcp: %s server[%d]: %w", path, i, err)
		}
		if _, exists := byName[cfg.Name]; !exists {
			*order = append(*order, cfg.Name)
		}
		byName[cfg.Name] = cfg
	}
	return nil
}

// normalizeServer validates one [[servers]] entry, applies defaults,
// and interpolates $VAR references.
func normalizeServer(s tomlServer, src Source, path string, envLookup func(string) string) (ServerConfig, error) {
	name := strings.TrimSpace(s.Name)
	if name == "" {
		return ServerConfig{}, fmt.Errorf("name is required")
	}
	transport := strings.TrimSpace(s.Transport)
	if transport == "" {
		transport = "stdio"
	}
	if !validTransports[transport] {
		return ServerConfig{}, fmt.Errorf("unknown transport %q (supported: stdio, sse, http)", transport)
	}

	// Transport-specific validation.
	command := interpolate(s.Command, envLookup)
	sseURL := interpolate(s.URL, envLookup)
	oauthConfig, err := normalizeOAuthConfig(s.OAuth)
	if err != nil {
		return ServerConfig{}, err
	}

	switch transport {
	case "stdio":
		if strings.TrimSpace(command) == "" {
			return ServerConfig{}, fmt.Errorf("command is required for transport %q", transport)
		}
		if sseURL != "" {
			slog.Warn("mcp: url field is ignored for stdio transport",
				"path", path, "server", name)
		}
		if len(s.Headers) > 0 {
			slog.Warn("mcp: headers field is ignored for stdio transport",
				"path", path, "server", name)
		}
		if oauthConfig.Enabled {
			return ServerConfig{}, fmt.Errorf("oauth cannot be used with transport %q", transport)
		}
	case "sse", "http":
		if strings.TrimSpace(sseURL) == "" {
			return ServerConfig{}, fmt.Errorf("url is required for transport %q", transport)
		}
		if command != "" {
			slog.Warn("mcp: command field is ignored for http/sse transport",
				"path", path, "server", name)
		}
	}

	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = interpolate(a, envLookup)
	}
	env := make(map[string]string, len(s.Env))
	for k, v := range s.Env {
		env[k] = interpolate(v, envLookup)
	}

	// Expand header values using the provided envLookup.  For SSE
	// headers this is the primary mechanism for injecting tokens such
	// as Bearer tokens from the environment.
	headers := make(map[string]string, len(s.Headers))
	for k, v := range s.Headers {
		expanded := os.Expand(v, func(key string) string {
			val := envLookup(key)
			if val == "" {
				if _, ok := os.LookupEnv(key); !ok {
					slog.Warn("mcp: header references unset env var",
						"path", path, "server", name, "header", k, "var", key)
				}
			}
			return val
		})
		headers[k] = expanded
	}

	enabled := true
	if s.Enabled != nil {
		enabled = *s.Enabled
	}

	permCat := strings.TrimSpace(s.PermissionCategory)
	if permCat == "" {
		permCat = "mcp"
	}
	if !validPermissionCategories[permCat] {
		slog.Warn("mcp: unknown permission_category; falling back to \"mcp\"",
			"path", path, "server", name, "value", permCat)
		permCat = "mcp"
	}

	return ServerConfig{
		Name:               name,
		Transport:          transport,
		Command:            command,
		Args:               args,
		Env:                env,
		Dir:                interpolate(s.Dir, envLookup),
		URL:                sseURL,
		Headers:            headers,
		OAuth:              oauthConfig,
		Enabled:            enabled,
		PermissionCategory: permCat,
		Source:             src,
		Path:               path,
	}, nil
}

// interpolate replaces $VAR and ${VAR} occurrences in s with the value
// from envLookup.  Unknown vars are replaced with the empty string —
// the same behaviour os.ExpandEnv ships with.  Backslash-escaped
// dollars are preserved literally to give users an escape hatch.
func normalizeOAuthConfig(raw any) (OAuthConfig, error) {
	if raw == nil {
		return OAuthConfig{}, nil
	}
	switch v := raw.(type) {
	case bool:
		return OAuthConfig{Enabled: v}, nil
	case map[string]any:
		cfg := OAuthConfig{Enabled: true}
		if value, ok := v["enabled"]; ok {
			b, ok := value.(bool)
			if !ok {
				return OAuthConfig{}, fmt.Errorf("oauth.enabled must be a bool")
			}
			cfg.Enabled = b
		}
		if value, ok := v["client_id"]; ok {
			s, ok := value.(string)
			if !ok {
				return OAuthConfig{}, fmt.Errorf("oauth.client_id must be a string")
			}
			cfg.ClientID = strings.TrimSpace(s)
		}
		if value, ok := v["client_secret"]; ok {
			s, ok := value.(string)
			if !ok {
				return OAuthConfig{}, fmt.Errorf("oauth.client_secret must be a string")
			}
			cfg.ClientSecret = strings.TrimSpace(s)
		}
		if value, ok := v["scope"]; ok {
			s, ok := value.(string)
			if !ok {
				return OAuthConfig{}, fmt.Errorf("oauth.scope must be a string")
			}
			cfg.Scope = strings.TrimSpace(s)
		}
		if value, ok := v["redirect_uri"]; ok {
			s, ok := value.(string)
			if !ok {
				return OAuthConfig{}, fmt.Errorf("oauth.redirect_uri must be a string")
			}
			cfg.RedirectURI = strings.TrimSpace(s)
		}
		return cfg, nil
	case map[string]string:
		return OAuthConfig{Enabled: true, ClientID: strings.TrimSpace(v["client_id"]), ClientSecret: strings.TrimSpace(v["client_secret"]), Scope: strings.TrimSpace(v["scope"]), RedirectURI: strings.TrimSpace(v["redirect_uri"])}, nil
	default:
		return OAuthConfig{}, fmt.Errorf("oauth must be a boolean or table")
	}
}

func interpolate(s string, envLookup func(string) string) string {
	if !strings.ContainsRune(s, '$') {
		return s
	}
	return os.Expand(s, envLookup)
}

// findProjectFile walks parents of start looking for a file at the
// relative path rel.  The walk halts at the first directory containing
// `.git`, the first match found, $HOME, or the filesystem root —
// whichever comes first.  This mirrors internal/skill's findProjectDir
// behaviour for file targets.
func findProjectFile(start, rel, homeStop string) string {
	dir := filepath.Clean(start)
	homeStop = filepath.Clean(homeStop)
	for {
		if homeStop != "" && dir == homeStop {
			return ""
		}
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		// Stop at .git boundary.
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
