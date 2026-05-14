package plugin

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Loader is the extension point that decides which plugin runtime to use.
//
// For v0.3 only LuaLoader is provided.  A future SubprocessLoader would
// handle non-.lua entrypoints via a JSON-RPC subprocess protocol.
//
// The Registry tries each registered Loader in order and uses the first one
// that reports CanLoad = true.  If no loader matches, plugin load fails with
// a clear error.
type Loader interface {
	// CanLoad inspects the manifest and the files at dir and returns true
	// if this loader handles them.  Must not read the entrypoint file.
	CanLoad(dir string, m Manifest) bool

	// Load creates and initialises a Plugin.  Called after CanLoad returns
	// true.  The returned Plugin has not yet had Load(ctx, h) called on it;
	// that is the caller's responsibility.
	Load(name, source, dir string, m Manifest) (Plugin, error)
}

// LuaLoader is the concrete loader for .lua-based plugins.
type LuaLoader struct{}

// CanLoad returns true when the manifest's Entrypoint ends in ".lua", or when
// no manifest exists and a plugin.lua file is present in dir.
func (l LuaLoader) CanLoad(_ string, m Manifest) bool {
	ep := m.Entrypoint
	if ep == "" {
		ep = "plugin.lua"
	}
	if strings.HasSuffix(ep, ".lua") {
		return true
	}
	return false
}

// Load creates a luaPlugin. The script is not executed until Plugin.Load is
// called.
func (l LuaLoader) Load(name, source, dir string, m Manifest) (Plugin, error) {
	ep := m.Entrypoint
	if ep == "" {
		ep = "plugin.lua"
	}
	scriptPath := filepath.Join(dir, ep)
	if _, err := os.Stat(scriptPath); err != nil {
		return nil, fmt.Errorf("plugin: Lua loader: entrypoint %q not found in %q: %w", ep, dir, err)
	}
	return newLuaPlugin(name, source, scriptPath, m), nil
}

// resolveLoader picks the first Loader that CanLoad the given manifest.
// Returns an error if none match.
func resolveLoader(loaders []Loader, dir string, m Manifest) (Loader, error) {
	for _, l := range loaders {
		if l.CanLoad(dir, m) {
			return l, nil
		}
	}
	ep := m.Entrypoint
	if ep == "" {
		ep = "plugin.lua"
	}
	return nil, fmt.Errorf(
		"plugin: no loader can handle entrypoint %q for plugin %q; "+
			"Lua (.lua) is the only supported runtime in v0.3. "+
			"Subprocess JSON-RPC plugins are architecturally reserved — see README",
		ep, m.Name,
	)
}

// defaultLoaders returns the ordered set of loaders for the Registry.
func defaultLoaders() []Loader {
	return []Loader{
		LuaLoader{},
		subprocessLoader{}, // always errors — architectural placeholder
	}
}

// resolveCacheDir returns $XDG_STATE_HOME/hygge/plugins (or ~/.local/state/hygge/plugins
// when XDG_STATE_HOME is not set).
func resolveCacheDir() string {
	if v, ok := os.LookupEnv("XDG_STATE_HOME"); ok && v != "" {
		return filepath.Join(v, "hygge", "plugins")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("plugin: cannot resolve home dir; using /tmp for cache", "err", err)
		return filepath.Join("/tmp", "hygge-plugins")
	}
	return filepath.Join(home, ".local", "state", "hygge", "plugins")
}

// findOrSynthesiseManifest looks for a plugin.toml in dir.  When absent, it
// synthesises a trivial manifest with the given fallback name.
func findOrSynthesiseManifest(dir, fallbackName string) (Manifest, error) {
	path := filepath.Join(dir, "plugin.toml")
	data, err := os.ReadFile(path) //nolint:gosec // controlled path
	if os.IsNotExist(err) {
		// Single-file plugin — check that plugin.lua exists.
		luaPath := filepath.Join(dir, "plugin.lua")
		if _, err2 := os.Stat(luaPath); err2 != nil {
			return Manifest{}, fmt.Errorf(
				"plugin: neither plugin.toml nor plugin.lua found in %q; "+
					"a plugin directory must contain at least one of these files", dir)
		}
		return SynthesiseManifest(fallbackName), nil
	}
	if err != nil {
		return Manifest{}, fmt.Errorf("plugin: read manifest: %w", err)
	}
	return ParseManifest(data)
}

// sourceBaseName returns a filesystem-safe name derived from a source URI.
// e.g. "github:cfbender/hygge-policy-guard" → "hygge-policy-guard"
// e.g. "local:/Users/cfb/my-plugin" → "my-plugin"
func sourceBaseName(source string) string {
	_, path, _ := strings.Cut(source, ":")
	// Strip ref/branch suffixes.
	path, _, _ = strings.Cut(path, "@")
	path, _, _ = strings.Cut(path, "#")
	// Use only the last segment.
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return "plugin"
	}
	name := parts[len(parts)-1]
	if name == "" && len(parts) > 1 {
		name = parts[len(parts)-2]
	}
	// Sanitise to match nameRe by forcing lowercase and replacing bad chars.
	name = strings.ToLower(name)
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('-')
		}
	}
	name = strings.Trim(sb.String(), "-_")
	if name == "" {
		return "plugin"
	}
	// Ensure first char is a letter.
	if name[0] >= '0' && name[0] <= '9' {
		name = "p" + name
	}
	return name
}
