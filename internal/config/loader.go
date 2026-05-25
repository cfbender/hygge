package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// configSource pairs a file path with its Source metadata.
type configSource struct {
	path   string
	source Source
}

// enumerateSources returns the ordered list of config file sources to load,
// in merge order (lowest to highest precedence, excluding defaults).
// Defaults are applied separately in Load.
//
// Order:
//  1. User config (config.toml first, hygge.toml second so hygge.toml wins)
//  2. Profile (chain via extends, base-first)
//  3. Walk-up .hygge/config.toml and .hygge/hygge.toml files (root-first, so pwd-nearest wins last)
//
// Within each scope (user config, each walk-up directory) config.toml is
// loaded first and hygge.toml second, so hygge.toml values win over
// config.toml values from the same directory.  This lets projects that
// already own config.toml introduce a hygge.toml without renaming existing
// files.
//
// The second return value is the resolved profile directory (see
// resolveProfileChain for the definition).
func enumerateSources(
	_ context.Context,
	opts LoadOptions,
	xdgConfigHome string,
	profileName string,
) ([]configSource, string, error) {
	var sources []configSource

	// 1. User config — load config.toml first, then hygge.toml so that
	//    hygge.toml values win within the user config scope.
	hyggeDir := filepath.Join(xdgConfigHome, "hygge")
	for _, name := range []string{"config.toml", "hygge.toml"} {
		p := filepath.Join(hyggeDir, name)
		if _, err := os.Stat(p); err == nil {
			sources = append(sources, configSource{
				path:   p,
				source: Source{File: p},
			})
		}
	}

	// 2. Profile chain.
	profileSources, profileDir, err := resolveProfileChain(profileName, xdgConfigHome)
	if err != nil {
		return nil, "", err
	}
	sources = append(sources, profileSources...)

	// 3. Walk-up .hygge/config.toml files.
	walkSources, err := collectWalkupSources(opts.Pwd, opts.HomeDir)
	if err != nil {
		return nil, "", err
	}
	sources = append(sources, walkSources...)

	return sources, profileDir, nil
}

// resolveProfileChain builds the ordered list of profile sources for
// profileName, following extends chains (base profile first, so the named
// profile's values win last).
//
// The second return value is the resolved profile directory for the *named*
// profile (not its ancestors).  This is:
//   - the directory containing a flat profile file
//     ($profilesDir/<name>.toml → $profilesDir/<name>), or
//   - the directory profile directory itself
//     ($profilesDir/<name>/config.toml → $profilesDir/<name>).
//
// The directory may not exist on disk (flat profile case).  An empty string
// is returned when no profile file was found (e.g. missing "default").
func resolveProfileChain(profileName, xdgConfigHome string) ([]configSource, string, error) {
	if profileName == "" {
		return nil, "", nil
	}

	profilesDir := filepath.Join(xdgConfigHome, "hygge", "profiles")

	// Traverse extends chain, collecting file paths from bottom to top.
	// We'll reverse them at the end so base comes first.
	type entry struct {
		name string
		path string
	}

	var chain []entry
	// profileDir records the directory for the named profile (the first entry
	// in the walk; later entries are extended parents).
	profileDir := ""
	visited := make(map[string]bool)
	current := profileName

	for {
		if visited[current] {
			return nil, "", fmt.Errorf("%w: %s", ErrCyclicProfile, current)
		}
		if len(chain) >= maxProfileDepth {
			return nil, "", fmt.Errorf("%w: max %d", ErrProfileDepth, maxProfileDepth)
		}

		// Prefer the flat file; fall back to the directory form.
		// Requirement 3: flat file wins when both exist (backward compat).
		flatPath := filepath.Join(profilesDir, current+".toml")
		dirPath := filepath.Join(profilesDir, current, "config.toml")

		var profilePath string
		switch {
		case fileExists(flatPath):
			profilePath = flatPath
		case fileExists(dirPath):
			profilePath = dirPath
		default:
			if current == "default" {
				// Missing "default" profile is not an error on first run.
				// Break the for loop — profileDir stays "".
				goto done
			}
			return nil, "", fmt.Errorf("%w: %s", ErrProfileNotFound, current)
		}

		// Record the directory for the named (first) profile only.
		// Both flat and directory forms resolve to profiles/<name>.
		if len(chain) == 0 {
			profileDir = filepath.Join(profilesDir, current)
		}

		visited[current] = true
		chain = append(chain, entry{name: current, path: profilePath})

		// Peek at the file to see if it has an extends key.
		m, err := loadTOMLFile(profilePath)
		if err != nil {
			return nil, "", &ParseError{File: profilePath, Err: err}
		}
		extendsVal, ok := m["extends"]
		if !ok {
			break
		}
		extendsName, ok := extendsVal.(string)
		if !ok || extendsName == "" {
			break
		}
		current = extendsName
	}
done:

	// Reverse so the base (earliest in the extends chain) is applied first,
	// giving the named profile the highest precedence within the chain.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	sources := make([]configSource, 0, len(chain))
	for _, e := range chain {
		sources = append(sources, configSource{
			path:   e.path,
			source: Source{File: e.path},
		})
	}
	return sources, profileDir, nil
}

// fileExists reports whether a regular file exists at path.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// collectWalkupSources walks up from dir toward homeDir, collecting
// .hygge/config.toml and .hygge/hygge.toml files.  Files are returned in
// root-first order so that the file closest to dir has the highest precedence
// (is last in the returned slice → merged last).
//
// Within each directory both config.toml and hygge.toml are loaded when
// present, with config.toml first so that hygge.toml values win within the
// same directory.
func collectWalkupSources(dir, homeDir string) ([]configSource, error) {
	// dirSources collects per-directory pairs [config.toml?, hygge.toml?].
	// Each element is a slice of sources for one directory (0, 1, or 2 entries).
	type dirEntry []configSource
	var dirs []dirEntry

	current := dir
	for {
		var entry dirEntry
		for _, name := range []string{"config.toml", "hygge.toml"} {
			p := filepath.Join(current, ".hygge", name)
			if _, err := os.Stat(p); err == nil {
				entry = append(entry, configSource{
					path:   p,
					source: Source{File: p},
				})
			}
		}
		if len(entry) > 0 {
			dirs = append(dirs, entry)
		}

		// Stop at home directory.
		if current == homeDir {
			break
		}

		parent := filepath.Dir(current)
		if parent == current {
			// Filesystem root reached.
			break
		}
		current = parent
	}

	// dirs is collected nearest-first; reverse to root-first so the nearest
	// directory ends up highest precedence after all merges.
	for i, j := 0, len(dirs)-1; i < j; i, j = i+1, j-1 {
		dirs[i], dirs[j] = dirs[j], dirs[i]
	}

	var files []configSource
	for _, entry := range dirs {
		files = append(files, entry...)
	}
	return files, nil
}

// buildEnvMap reads HYGGE_* environment variables and converts them to a
// nested map[string]any for merging.
//
// Mapping uses "__" (double underscore) as the path-segment separator.
// Single underscores within a segment are preserved as part of the key name.
//
//	HYGGE_model__provider=openai          → model.provider = "openai"
//	HYGGE_permission__file_write=allow    → permission.file_write = "allow"
//	HYGGE_model__options__thinking_budget=8000 → model.options.thinking_budget = 8000
//
// The "HYGGE_" prefix is stripped first (single underscore separates the
// prefix from the first path segment), then the remainder is split on "__".
// Each segment is case-folded to lowercase for map key lookup.
//
// If splitting produces an empty segment (e.g. "HYGGE___foo"), the env var
// is skipped with a slog.Warn and processing continues.
//
// Values are best-effort coerced: "true"/"false" → bool, integer strings →
// int64, float strings → float64, else string.
func buildEnvMap(opts LoadOptions) map[string]any {
	const prefix = "HYGGE_"
	result := map[string]any{}

	// We need to iterate over all env vars with the prefix.
	// Since opts.EnvLookup only does single-key lookups, we instead probe
	// the known key space.  For full env iteration we use os.Environ when
	// EnvLookup is the default; otherwise we rely on the caller to inject
	// HYGGE_ vars via a wrapper that also handles iteration.
	//
	// For testability, callers that inject EnvLookup should also call
	// buildEnvMapFromKeys with explicit keys.  In production we use
	// os.Environ().
	//
	// NOTE: This is a deliberate design: we don't call os.Environ() directly
	// so that tests can inject a hermetic environment.  Tests use
	// buildEnvMapFromKeys or set t.Setenv.
	for _, env := range listEnvVars(opts.EnvLookup) {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		rest := strings.TrimPrefix(env, prefix)
		parts := strings.Split(strings.ToLower(rest), "__")

		// Skip env vars that produce empty segments (e.g. HYGGE___foo).
		valid := true
		if slices.Contains(parts, "") {
			slog.Warn("config: skipping malformed env var (empty segment)", "var", env)
			valid = false
		}
		if !valid {
			continue
		}

		val, ok := opts.EnvLookup(env)
		if !ok {
			continue
		}

		// Navigate/create nested maps.
		cur := result
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = coerceEnvValue(val)
			} else {
				if _, exists := cur[part]; !exists {
					cur[part] = map[string]any{}
				}
				next, ok := cur[part].(map[string]any)
				if !ok {
					// Conflict: prior key was scalar, now we need a map.
					// Higher nesting wins — replace.
					next = map[string]any{}
					cur[part] = next
				}
				cur = next
			}
		}
	}
	return result
}

// listEnvVars returns the names of environment variables.  When lookup is
// os.LookupEnv we use os.Environ(); otherwise we fall back to probing the
// known HYGGE_ key set (test mode).
//
// In test mode callers inject HYGGE_ vars by calling t.Setenv so os.Environ
// picks them up even with a custom EnvLookup — therefore we always read from
// os.Environ here and just use lookup for the value.
func listEnvVars(_ func(string) (string, bool)) []string {
	raw := os.Environ()
	names := make([]string, 0, len(raw))
	for _, kv := range raw {
		if idx := strings.IndexByte(kv, '='); idx > 0 {
			names = append(names, kv[:idx])
		}
	}
	return names
}

// coerceEnvValue converts an environment-variable string to bool/int64/float64
// where unambiguous, otherwise returns the original string.
func coerceEnvValue(s string) any {
	// Boolean
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	// Integer
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		return i
	}
	// Float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// buildEnvMapFromKeys is a testable variant of buildEnvMap that works from
// an explicit list of HYGGE_* key names.  Uses "__" (double underscore) as
// the path-segment separator; see buildEnvMap for full semantics.
func buildEnvMapFromKeys(keys []string, lookup func(string) (string, bool)) map[string]any {
	const prefix = "HYGGE_"
	result := map[string]any{}

	for _, env := range keys {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		val, ok := lookup(env)
		if !ok {
			continue
		}
		rest := strings.TrimPrefix(env, prefix)
		parts := strings.Split(strings.ToLower(rest), "__")

		// Skip env vars that produce empty segments (e.g. HYGGE___foo).
		valid := true
		if slices.Contains(parts, "") {
			slog.Warn("config: skipping malformed env var (empty segment)", "var", env)
			valid = false
		}
		if !valid {
			continue
		}

		cur := result
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = coerceEnvValue(val)
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
	return result
}
