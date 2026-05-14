package theme

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned by this package.
var (
	// ErrThemeNotFound is returned when the requested theme name is not a
	// builtin and no matching file exists on disk.
	ErrThemeNotFound = errors.New("theme not found")

	// ErrInheritCycle is returned when an "inherit:<atom>" chain forms a
	// cycle or exceeds maxInheritDepth.
	ErrInheritCycle = errors.New("inherit cycle detected")
)

// maxInheritDepth is the maximum length of an inherit chain before
// Load returns ErrInheritCycle.
const maxInheritDepth = 8

// ErrIncompleteTheme is returned when a parsed theme file is missing one or
// more atoms from AllAtoms().
type ErrIncompleteTheme struct {
	Missing []Atom
}

func (e *ErrIncompleteTheme) Error() string {
	strs := make([]string, len(e.Missing))
	for i, a := range e.Missing {
		strs[i] = string(a)
	}
	return fmt.Sprintf("theme is missing required atoms: %s", strings.Join(strs, ", "))
}

// ErrUnknownAtom is returned when a theme file contains keys that are not in
// AllAtoms().
type ErrUnknownAtom struct {
	Atoms []Atom
}

func (e *ErrUnknownAtom) Error() string {
	strs := make([]string, len(e.Atoms))
	for i, a := range e.Atoms {
		strs[i] = string(a)
	}
	return fmt.Sprintf("theme contains unknown atoms: %s", strings.Join(strs, ", "))
}

// ErrInvalidColor is returned when a color value string is not in any
// recognized form.
type ErrInvalidColor struct {
	Atom  Atom
	Value string
}

func (e *ErrInvalidColor) Error() string {
	return fmt.Sprintf("invalid color value %q for atom %q: expected hex (#RRGGBB), ANSI index (0-255), ansi:N, inherit:<atom>, or empty string", e.Value, e.Atom)
}
