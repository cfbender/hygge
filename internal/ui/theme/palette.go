package theme

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// colorKind distinguishes the three concrete kinds a Color can hold.
type colorKind int

const (
	colorKindDefault colorKind = iota // empty string: use terminal default
	colorKindANSI                     // ANSI palette index 0-255
	colorKindHex                      // hex string "#RRGGBB" or "#RGB"
	colorKindInherit                  // inherit:<atom>
)

// Color is an abstract color reference resolved at Style() call time.
// The zero value represents "no color" (terminal default).
type Color struct {
	kind        colorKind
	raw         string // the normalized string passed to lipgloss.Color(), or ""
	inheritAtom Atom   // only valid when kind == colorKindInherit
}

// IsDefault reports whether this Color means "use the terminal default"
// (empty-string case).
func (c Color) IsDefault() bool { return c.kind == colorKindDefault }

// String returns a human-readable description suitable for FormatTheme output.
func (c Color) String() string {
	switch c.kind {
	case colorKindDefault:
		return "(default)"
	case colorKindANSI:
		return "ansi:" + c.raw
	case colorKindHex:
		return c.raw
	case colorKindInherit:
		return "inherit:" + string(c.inheritAtom)
	default:
		return "(unknown)"
	}
}

// lipglossColor returns the lipgloss.Color for this Color.
// Callers must resolve inherit references before calling this.
func (c Color) lipglossColor() lipgloss.Color {
	return lipgloss.Color(c.raw)
}

// parseColor parses a color value string from a theme file into a Color.
// Accepted forms:
//
//	""              → colorKindDefault (no override)
//	"#RRGGBB"       → colorKindHex
//	"#RGB"          → colorKindHex
//	"N"             → colorKindANSI  (N is a decimal integer 0–255)
//	"ansi:N"        → colorKindANSI
//	"inherit:<atom>" → colorKindInherit
//
// Any other value returns ErrInvalidColor.
func parseColor(atom Atom, s string) (Color, error) {
	if s == "" {
		return Color{kind: colorKindDefault}, nil
	}

	// inherit:<atom>
	if strings.HasPrefix(s, "inherit:") {
		target := strings.TrimPrefix(s, "inherit:")
		if target == "" {
			return Color{}, &ErrInvalidColor{Atom: atom, Value: s}
		}
		return Color{kind: colorKindInherit, inheritAtom: Atom(target)}, nil
	}

	// ansi:N
	if strings.HasPrefix(s, "ansi:") {
		rest := strings.TrimPrefix(s, "ansi:")
		if _, err := strconv.Atoi(rest); err != nil {
			return Color{}, &ErrInvalidColor{Atom: atom, Value: s}
		}
		return Color{kind: colorKindANSI, raw: rest}, nil
	}

	// hex
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		if len(hex) != 3 && len(hex) != 6 {
			return Color{}, &ErrInvalidColor{Atom: atom, Value: s}
		}
		for _, ch := range hex {
			if !isHexDigit(ch) {
				return Color{}, &ErrInvalidColor{Atom: atom, Value: s}
			}
		}
		return Color{kind: colorKindHex, raw: s}, nil
	}

	// bare integer → ANSI index
	if _, err := strconv.Atoi(s); err == nil {
		return Color{kind: colorKindANSI, raw: s}, nil
	}

	return Color{}, &ErrInvalidColor{Atom: atom, Value: s}
}

func isHexDigit(r rune) bool {
	return (r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'f') ||
		(r >= 'A' && r <= 'F')
}

// resolveColor resolves a Color — following inherit chains — against the
// theme's Colors map.  Returns the final concrete Color.
func resolveColor(start Color, colors map[Atom]Color) (Color, error) {
	return resolveColorDepth(start, colors, 0)
}

func resolveColorDepth(c Color, colors map[Atom]Color, depth int) (Color, error) {
	if c.kind != colorKindInherit {
		return c, nil
	}
	if depth >= maxInheritDepth {
		slog.Warn("theme: inherit chain exceeded max depth, truncating",
			"depth", depth, "max", maxInheritDepth)
		return Color{}, fmt.Errorf("%w: chain exceeded %d hops", ErrInheritCycle, maxInheritDepth)
	}

	target, ok := colors[c.inheritAtom]
	if !ok {
		// Atom not in the map at all — treat as error.
		return Color{}, fmt.Errorf("%w: atom %q referenced by inherit not found", ErrInheritCycle, c.inheritAtom)
	}
	if target.kind == colorKindInherit && target.inheritAtom == c.inheritAtom {
		// Self-reference.
		return Color{}, fmt.Errorf("%w: atom %q inherits itself", ErrInheritCycle, c.inheritAtom)
	}

	return resolveColorDepth(target, colors, depth+1)
}
