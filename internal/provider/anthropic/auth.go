package anthropic

import (
	"fmt"
	"os"
	"strings"

	"github.com/cfbender/hygge/internal/provider"
)

// resolveAPIKey applies the precedence chain documented in the package: an
// opts["api_key"] string (literal, $ENVVAR reference, or op:// reference),
// then ANTHROPIC_API_KEY, then a typed ErrAuth failure.
func resolveAPIKey(opts map[string]any) (string, error) {
	if raw, ok := opts["api_key"]; ok {
		if s, ok := raw.(string); ok && s != "" {
			if strings.HasPrefix(s, "op://") {
				return "", fmt.Errorf("%w: %s", provider.ErrAuthOpRefUnsupported, s)
			}
			if strings.HasPrefix(s, "$") {
				name := strings.TrimPrefix(s, "$")
				if v := os.Getenv(name); v != "" {
					return v, nil
				}
				return "", fmt.Errorf("%w: env %s referenced by api_key is empty", provider.ErrAuth, name)
			}
			return s, nil
		}
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("%w: no Anthropic API key found; set ANTHROPIC_API_KEY or model.options.api_key", provider.ErrAuth)
}
