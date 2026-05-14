package tool

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cfbender/hygge/internal/permission"
)

// resolvePath produces an absolute, cleaned path from a possibly-relative
// arg.  Relative paths are joined to ec.Pwd; absolute paths are cleaned.
// An empty path returns an empty string so callers can distinguish "not
// supplied" from "explicitly empty".
func resolvePath(ec ExecContext, raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	if ec.Pwd == "" {
		// No Pwd configured — fall back to absolutising against the
		// process cwd via filepath.Abs.
		return filepath.Abs(raw)
	}
	return filepath.Clean(filepath.Join(ec.Pwd, raw)), nil
}

// askPermission wraps ec.Permission.Ask, returning either the granted
// decision (caller proceeds), an IsError Result (caller returns the
// Result unchanged), or a *ToolError (caller propagates).
func askPermission(ctx context.Context, ec ExecContext, req permission.Request) (permission.Decision, *Result, error) {
	if ec.Permission == nil {
		return permission.Decision{}, nil, newExecutionFailed("permission engine not configured", nil)
	}
	req.SessionID = ec.SessionID
	req.Pwd = ec.Pwd
	d, err := ec.Permission.Ask(ctx, req)
	if err != nil {
		return permission.Decision{}, nil, newPermissionFailure(
			fmt.Sprintf("permission ask failed: %v", err), err)
	}
	if d.Action == permission.ActionDeny {
		reason := d.Reason
		if reason == "" {
			reason = "denied by policy"
		}
		return d, &Result{
			IsError: true,
			Content: fmt.Sprintf("permission denied: %s", reason),
			Metadata: map[string]any{
				"permission":        "denied",
				"permission_reason": reason,
				"category":          string(req.Category),
				"target":            req.Target,
			},
		}, nil
	}
	return d, nil, nil
}

// decodeArgs strictly decodes raw into out; returns a *ToolError on failure.
// strict means DisallowUnknownFields — JSON Schema additionalProperties=false
// is the model-facing contract; this is the runtime gate.
func decodeArgs(raw []byte, out any) error {
	if len(raw) == 0 {
		// Treat empty as "{}" so optional-only tools still work.
		raw = []byte("{}")
	}
	dec := newStrictDecoder(raw)
	if err := dec.Decode(out); err != nil {
		return newInvalidArgs(fmt.Sprintf("decode args: %v", err), err)
	}
	return nil
}

// rawHasKey reports whether the top-level JSON object in raw contains key.
// Used by tools whose required fields are types Go cannot distinguish from
// "missing" once decoded (e.g. an empty-string is a valid Content but JSON
// Schema requires it to be present).
func rawHasKey(raw []byte, key string) bool {
	if len(raw) == 0 {
		return false
	}
	var m map[string]any
	if err := newStrictDecoder(raw).Decode(&m); err != nil {
		// On decode error rawHasKey returns false; the strict decode
		// path will surface the real error to the caller.
		return false
	}
	_, ok := m[key]
	return ok
}
