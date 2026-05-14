package subagent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/cfbender/hygge/internal/provider"
)

// ProviderResolver constructs (or fetches a cached) provider for the
// model identifier supplied.  The identifier is conventionally
// "<provider>/<model-id>", e.g. "anthropic/claude-haiku-4-5".
//
// Returns the constructed provider and the bare model id (no provider
// prefix) -- that's what gets passed into the agent loop via
// session.Model.Name.
//
// Implementations MUST be safe for concurrent calls.  The CLI
// bootstrap supplies a resolver that caches a single instance per
// provider name; tests may supply trivial closures.
type ProviderResolver func(ctx context.Context, modelRef string) (provider.Provider, string, error)

// modelRefRe is the validation pattern for model overrides parsed
// from subagents.toml.  We deliberately keep it loose on the
// model-id side (anything non-empty) while pinning the provider
// segment to the same shape used by the provider registry.
var modelRefRe = regexp.MustCompile(`^[a-z][a-z0-9_]*/.+$`)

// ErrInvalidModelRef is returned by [ParseModelRef] (and resolvers
// that delegate to it) when the supplied string is not of the form
// "<provider>/<model-id>".  Callers use errors.Is to branch on it so
// they can fall back to the parent provider on malformed input.
var ErrInvalidModelRef = errors.New("subagent: invalid model reference")

// IsValidModelRef reports whether s matches the canonical
// "<provider>/<model-id>" shape.  Empty strings return false: an
// empty Type.Model means "use parent's", and is checked separately
// by the caller before invoking this helper.
func IsValidModelRef(s string) bool {
	return modelRefRe.MatchString(s)
}

// ParseModelRef splits a model reference into its provider name and
// bare model id.  Returns [ErrInvalidModelRef] (wrapped) when the
// input does not match the canonical shape.  Whitespace at the
// boundaries is trimmed before validation so trailing newlines from
// hand-edited TOML never trip the regex.
func ParseModelRef(ref string) (providerName, modelID string, err error) {
	ref = strings.TrimSpace(ref)
	if !modelRefRe.MatchString(ref) {
		return "", "", fmt.Errorf("%w: %q (want <provider>/<model-id>)", ErrInvalidModelRef, ref)
	}
	// Split on the first slash; model ids may contain slashes
	// (openrouter does this) so we deliberately do not use a greedy
	// split.
	idx := strings.IndexByte(ref, '/')
	return ref[:idx], ref[idx+1:], nil
}
