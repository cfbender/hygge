package plugin

import (
	"regexp"
)

// nameRe is the validation pattern for plugin names.  Mirrors the pattern used
// by command and subagent registries.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
