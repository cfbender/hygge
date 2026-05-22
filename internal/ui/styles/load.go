package styles

import (
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	toml "github.com/pelletier/go-toml/v2"
)

// ErrThemeNotFound is returned when the requested theme name is not the
// built-in default and no matching file exists on disk.
var ErrThemeNotFound = errors.New("theme not found")

// LoadOptions controls theme loading.
type LoadOptions struct {
	// ConfigHome overrides $XDG_CONFIG_HOME; "" uses the real environment.
	ConfigHome string
	// HomeDir overrides $HOME; "" uses the real home directory.
	HomeDir string
}

// Load returns the Styles for the given theme name.
//
//   - "" or "claret" → the built-in Claret theme.
//   - any other name → ~/.config/hygge/themes/<name>.toml.
//
// User themes override Claret's palette role-by-role; any role not specified
// in the TOML inherits the Claret value.
func Load(name string, opts LoadOptions) (*Styles, error) {
	if name == "" || name == "claret" {
		return DefaultTheme(), nil
	}
	path, err := resolveThemePath(name, opts)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // intentional: user-supplied theme path
	if err != nil {
		return nil, err
	}
	s, err := parseTOMLTheme(data)
	if err != nil {
		return nil, fmt.Errorf("styles: load %q from %s: %w", name, path, err)
	}
	if s.Name == "" {
		s.Name = name
	}
	return s, nil
}

// KnownNames returns the set of theme names available to Load: the built-in
// "claret" plus any *.toml files under <config>/hygge/themes/.
func KnownNames(opts LoadOptions) []string {
	names := map[string]bool{"claret": true}
	if configHome := resolveConfigHome(opts); configHome != "" {
		entries, err := os.ReadDir(filepath.Join(configHome, "hygge", "themes"))
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".toml" {
					continue
				}
				names[strings.TrimSuffix(entry.Name(), ".toml")] = true
			}
		}
	}
	out := make([]string, 0, len(names))
	for n := range names {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func resolveConfigHome(opts LoadOptions) string {
	if opts.ConfigHome != "" {
		return opts.ConfigHome
	}
	home := opts.HomeDir
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = h
	}
	return filepath.Join(home, ".config")
}

func resolveThemePath(name string, opts LoadOptions) (string, error) {
	configHome := resolveConfigHome(opts)
	if configHome == "" {
		return "", fmt.Errorf("styles: no config home available")
	}
	path := filepath.Join(configHome, "hygge", "themes", name+".toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", fmt.Errorf("%w: %s", ErrThemeNotFound, name)
	}
	return path, nil
}

// themeFile is the TOML schema for a user theme.
//
//	name = "my-theme"
//	[palette]
//	primary   = "#cba6f7"
//	secondary = "#f9e2af"
//	accent    = "#94e2d5"
//	# ... see quickStyleOpts for the full set of role names.
//	[atoms]
//	# optional fine-grained overrides keyed by atom name
//	"bubble.user.border" = "#5fafff"
type themeFile struct {
	Name    string            `toml:"name"`
	Palette map[string]string `toml:"palette"`
	Atoms   map[string]string `toml:"atoms"`
}

// parseTOMLTheme decodes a user theme document and folds its overrides into
// a Styles built atop the Claret default palette.
func parseTOMLTheme(data []byte) (*Styles, error) {
	var f themeFile
	dec := toml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	opts := claretOpts()
	for role, raw := range f.Palette {
		c, err := parseColorString(raw)
		if err != nil {
			return nil, fmt.Errorf("palette.%s: %w", role, err)
		}
		if !applyPaletteRole(&opts, role, c) {
			return nil, fmt.Errorf("palette: unknown role %q", role)
		}
	}

	s := quickStyle(opts)

	// Atom-level overrides applied after the palette roll-up so users can
	// pin a specific surface independently of its role mapping.
	for k, raw := range f.Atoms {
		atom := Atom(k)
		if !isKnownAtom(atom) {
			return nil, fmt.Errorf("atoms: unknown atom %q", k)
		}
		c, err := parseColorString(raw)
		if err != nil {
			return nil, fmt.Errorf("atoms.%s: %w", k, err)
		}
		s.Colors[atom] = c
	}

	s.Name = f.Name
	return &s, nil
}

// parseColorString accepts a hex string ("#aabbcc" or "#abc"). Empty input
// returns nil (meaning "no override / inherit"). Anything else errors.
func parseColorString(s string) (color.Color, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if !strings.HasPrefix(s, "#") {
		return nil, fmt.Errorf("expected hex color (#RRGGBB), got %q", s)
	}
	hex := s[1:]
	if len(hex) != 3 && len(hex) != 6 {
		return nil, fmt.Errorf("expected #RGB or #RRGGBB, got %q", s)
	}
	for _, ch := range hex {
		if !isHexDigit(ch) {
			return nil, fmt.Errorf("invalid hex digit in %q", s)
		}
	}
	return lipgloss.Color(s), nil
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'f') ||
		(r >= 'A' && r <= 'F')
}

func isKnownAtom(a Atom) bool {
	return slices.Contains(allAtoms, a)
}

// applyPaletteRole sets the named palette role on opts. Returns false for
// unknown role names so the loader can surface a clear error.
func applyPaletteRole(o *quickStyleOpts, role string, c color.Color) bool {
	if c == nil {
		return true // empty value = leave default
	}
	switch role {
	case "primary":
		o.primary = c
	case "secondary":
		o.secondary = c
	case "accent":
		o.accent = c
	case "fg_base":
		o.fgBase = c
	case "fg_subtle":
		o.fgSubtle = c
	case "fg_more_subtle":
		o.fgMoreSubtle = c
	case "fg_most_subtle":
		o.fgMostSubtle = c
	case "on_primary":
		o.onPrimary = c
	case "bg_base":
		o.bgBase = c
	case "bg_least_visible":
		o.bgLeastVisible = c
	case "bg_less_visible":
		o.bgLessVisible = c
	case "bg_most_visible":
		o.bgMostVisible = c
	case "separator":
		o.separator = c
	case "destructive":
		o.destructive = c
	case "error":
		o.error = c
	case "warning":
		o.warning = c
	case "warning_subtle":
		o.warningSubtle = c
	case "busy":
		o.busy = c
	case "info":
		o.info = c
	case "info_more_subtle":
		o.infoMoreSubtle = c
	case "info_most_subtle":
		o.infoMostSubtle = c
	case "success":
		o.success = c
	case "success_more_subtle":
		o.successMoreSubtle = c
	case "success_most_subtle":
		o.successMostSubtle = c
	default:
		return false
	}
	return true
}

// claretOpts returns the palette options used to build the Claret default.
// Kept in sync with themes.go DefaultTheme.
func claretOpts() quickStyleOpts {
	return quickStyleOpts{
		primary:   hex("#C75B7A"),
		secondary: hex("#D4A76A"),
		accent:    hex("#8FA86E"),

		fgBase:       hex("#DDD3C7"),
		fgSubtle:     hex("#BDB3A7"),
		fgMoreSubtle: hex("#9E9288"),
		fgMostSubtle: hex("#71685E"),

		onPrimary: hex("#180810"),

		bgBase:         hex("#180810"),
		bgLeastVisible: hex("#211618"),
		bgLessVisible:  hex("#2B1F22"),
		bgMostVisible:  hex("#3A2E25"),

		separator: hex("#3A2E25"),

		destructive:       hex("#C44536"),
		error:             hex("#C44536"),
		warning:           hex("#D4A76A"),
		warningSubtle:     hex("#C5975B"),
		busy:              hex("#D4A76A"),
		info:              hex("#8995A8"),
		infoMoreSubtle:    hex("#6E7A90"),
		infoMostSubtle:    hex("#6E7A90"),
		success:           hex("#8FA86E"),
		successMoreSubtle: hex("#7A9460"),
		successMostSubtle: hex("#7A9460"),
	}
}
