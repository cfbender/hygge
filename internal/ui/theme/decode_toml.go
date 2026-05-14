package theme

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	toml "github.com/pelletier/go-toml/v2"
)

// loadTOMLTheme reads a theme file at path and parses it into a *Theme.
// The file must declare every atom in AllAtoms() and must not declare
// unknown atoms.
func loadTOMLTheme(path string) (*Theme, error) {
	data, err := os.ReadFile(path) //nolint:gosec // intentional: user-supplied theme path
	if err != nil {
		return nil, err
	}
	return parseTOMLTheme(data)
}

// parseTOMLTheme decodes a TOML theme document from raw bytes.
func parseTOMLTheme(data []byte) (*Theme, error) {
	raw, err := decodeTOMLBytes(data)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	// Extract the theme name.
	name := ""
	if v, ok := raw["name"]; ok {
		if s, ok := v.(string); ok {
			name = s
		}
	}

	// Extract [colors] table.
	colorsRaw := map[string]any{}
	if v, ok := raw["colors"]; ok {
		if m, ok := v.(map[string]any); ok {
			colorsRaw = m
		}
	}

	// Parse each color value.
	parsed := make(map[Atom]Color, len(colorsRaw))
	for k, v := range colorsRaw {
		a := Atom(k)
		var s string
		switch val := v.(type) {
		case string:
			s = val
		case nil:
			s = ""
		default:
			return nil, &ErrInvalidColor{Atom: a, Value: fmt.Sprintf("%v", v)}
		}
		c, err := parseColor(a, s)
		if err != nil {
			return nil, err
		}
		parsed[a] = c
	}

	// Validate: check for unknown atoms first, then missing atoms.
	known := make(map[Atom]bool, len(allAtoms))
	for _, a := range allAtoms {
		known[a] = true
	}

	var unknowns []Atom
	for a := range parsed {
		if !known[a] {
			unknowns = append(unknowns, a)
		}
	}
	if len(unknowns) > 0 {
		sortAtoms(unknowns)
		return nil, &ErrUnknownAtom{Atoms: unknowns}
	}

	var missing []Atom
	for _, a := range allAtoms {
		if _, ok := parsed[a]; !ok {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		return nil, &ErrIncompleteTheme{Missing: missing}
	}

	// Validate inherit references point to known atoms and detect cycles.
	if err := validateInheritRefs(parsed); err != nil {
		return nil, err
	}

	return &Theme{Name: name, Colors: parsed}, nil
}

// validateInheritRefs checks that every inherit:<atom> reference points to a
// known atom and that no cycles exist.
func validateInheritRefs(colors map[Atom]Color) error {
	for a := range colors {
		if _, err := resolveColor(colors[a], colors); err != nil {
			return fmt.Errorf("atom %q: %w", a, err)
		}
	}
	return nil
}

// resolveThemePath returns the filesystem path for a user theme by name.
// It looks under opts.ConfigHome (or ~/.config when empty) /hygge/themes/<name>.toml.
func resolveThemePath(themeName string, opts LoadOptions) (string, error) {
	configHome := opts.ConfigHome
	if configHome == "" {
		home := opts.HomeDir
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("theme: get home dir: %w", err)
			}
		}
		configHome = filepath.Join(home, ".config")
	}

	path := filepath.Join(configHome, "hygge", "themes", themeName+".toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("%w: %s", ErrThemeNotFound, themeName)
	}

	return path, nil
}

// decodeTOMLBytes decodes a TOML document from raw bytes into map[string]any.
// This package does not import internal/config; it uses go-toml/v2 directly.
func decodeTOMLBytes(data []byte) (map[string]any, error) {
	dec := toml.NewDecoder(bytes.NewReader(data))
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// sortAtoms sorts a slice of Atom values lexicographically.
func sortAtoms(atoms []Atom) {
	sort.Slice(atoms, func(i, j int) bool {
		return atoms[i] < atoms[j]
	})
}
