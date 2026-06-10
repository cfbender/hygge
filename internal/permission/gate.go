package permission

import (
	"context"
	"errors"
	"fmt"
)

// ErrDenied is the sentinel wrapped by every denial returned from [Gate].
// Callers distinguish "the user/policy said no" from an Ask infrastructure
// failure via errors.Is(err, ErrDenied).
var ErrDenied = errors.New("permission denied")

// Asker is the single-method surface of [Engine] that [Gate] needs.
// It exists so gated call sites can be exercised with fakes in tests.
type Asker interface {
	Ask(ctx context.Context, req Request) (Decision, error)
}

var _ Asker = (*Engine)(nil)

// DeniedError is the concrete error [Gate] returns for a denial.  It wraps
// [ErrDenied] and carries the request fields plus the engine's reason so
// callers can build their own user-facing messages.
type DeniedError struct {
	// ToolName is the tool that asked, copied from the request.
	ToolName string

	// Category and Target identify what was denied, copied from the request.
	Category Category
	Target   string

	// Reason is the engine-provided explanation.  May be empty.
	Reason string
}

func (e *DeniedError) Error() string {
	reason := e.Reason
	if reason == "" {
		reason = "denied by policy"
	}
	if e.ToolName != "" {
		return fmt.Sprintf("permission denied: %s: %s", e.ToolName, reason)
	}
	return fmt.Sprintf("permission denied: %s", reason)
}

func (e *DeniedError) Unwrap() error { return ErrDenied }

// Gate asks eng to evaluate req and maps the decision to an error:
//
//   - nil eng: allow.  Only test setups run without an engine; production
//     wiring always supplies one.
//   - Ask fails: the error is returned as-is so callers can surface
//     infrastructure failures distinctly from denials.
//   - denied: a *DeniedError wrapping [ErrDenied].
//   - allowed: nil.
func Gate(ctx context.Context, eng Asker, req Request) error {
	if eng == nil {
		return nil
	}
	d, err := eng.Ask(ctx, req)
	if err != nil {
		return err
	}
	if d.Action == ActionDeny {
		return &DeniedError{
			ToolName: req.ToolName,
			Category: req.Category,
			Target:   req.Target,
			Reason:   d.Reason,
		}
	}
	return nil
}
