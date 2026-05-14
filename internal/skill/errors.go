package skill

import (
	"errors"
	"fmt"
)

// ErrNoFrontmatter is returned by ParseFile when the file does not start
// with the `---\n` opening delimiter.  The loader treats this as a
// "this file is not a skill" signal and skips the file with a warning;
// it is not a fatal error.
var ErrNoFrontmatter = errors.New("skill: file has no frontmatter")

// ParseError is the error type returned by ParseFile for malformed skill
// files: missing closing `---`, invalid name, missing required keys, or
// a filename-stem / frontmatter-name mismatch.
//
// Path is the absolute path the parser was reading.  Reason is a short
// human-readable explanation.
type ParseError struct {
	Path   string
	Reason string
}

// Error implements the error interface.
func (e *ParseError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("skill: parse %q: %s", e.Path, e.Reason)
}
