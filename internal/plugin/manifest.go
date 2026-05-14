package plugin

import (
	"fmt"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// Manifest is the parsed content of a plugin.toml manifest file.
//
// Single-file plugins (a lone plugin.lua at the root of a repo) do not
// require a manifest; the loader synthesises a trivial one with Name =
// basename of the source URI and Entrypoint = "plugin.lua".
type Manifest struct {
	// Name is the unique plugin identifier.  Derived from the manifest
	// file or synthesised from the source URI.
	Name string `toml:"name"`

	// Version is a human-facing version string (e.g. "1.2.3").
	// Optional.
	Version string `toml:"version"`

	// Description is the one-line summary.  Optional.
	Description string `toml:"description"`

	// Entrypoint is the path to the entry .lua file, relative to the
	// manifest directory.  Defaults to "plugin.lua" when absent.
	Entrypoint string `toml:"entrypoint"`

	// Capabilities is an optional declaration of which hygge APIs the
	// plugin uses.  UX-only; registrations happen at runtime regardless.
	Capabilities CapabilityDecl `toml:"capabilities"`

	// synthesised is true when this manifest was not loaded from a file
	// but constructed by the loader for a single-file plugin.
	synthesised bool
}

// CapabilityDecl is the optional [capabilities] block in plugin.toml.
type CapabilityDecl struct {
	Tools     bool `toml:"tools"`
	Hooks     bool `toml:"hooks"`
	Commands  bool `toml:"commands"`
	Subagents bool `toml:"subagents"`
	Messages  bool `toml:"messages"`
}

// Synthesised reports whether this manifest was created by the loader
// (single-file plugin, no explicit plugin.toml).
func (m Manifest) Synthesised() bool { return m.synthesised }

// ParseManifest reads and validates a plugin.toml file.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("plugin: parse manifest: %w", err)
	}
	if err := validateManifest(m); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// SynthesiseManifest creates a minimal Manifest for a single-file plugin.
// name is the plugin name (typically the source URI basename).
func SynthesiseManifest(name string) Manifest {
	return Manifest{
		Name:        name,
		Entrypoint:  "plugin.lua",
		synthesised: true,
	}
}

// validateManifest checks required fields.
func validateManifest(m Manifest) error {
	name := strings.TrimSpace(m.Name)
	if name == "" {
		return fmt.Errorf("plugin: manifest: name is required")
	}
	if !nameRe.MatchString(name) {
		return fmt.Errorf("plugin: manifest: name %q must match [a-z][a-z0-9_-]*", name)
	}
	return nil
}
