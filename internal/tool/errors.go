package tool

import "fmt"

// Error codes for ToolError.Code.  These are intentionally small in number
// so callers can switch on them; new codes should be added sparingly.
const (
	// CodeInvalidArgs is returned when the JSON arguments fail schema
	// validation or cannot be decoded into the tool's args struct.
	CodeInvalidArgs = "invalid_args"

	// CodePermissionDenied is returned when an unrecoverable permission
	// engine failure occurs (engine closed, bus closed, context cancelled
	// while waiting for a reply).  An ordinary user "deny" decision is NOT
	// a ToolError — it is a Result with IsError: true.
	CodePermissionDenied = "permission_denied"

	// CodeExecutionFailed is returned for internal failures that prevent
	// the tool from running at all: panic recovery, missing dependencies,
	// or unrecoverable I/O setup errors.  An ordinary "file not found" or
	// "command exited non-zero" is NOT a ToolError — it is a Result with
	// IsError: true.
	CodeExecutionFailed = "execution_failed"
)

// ToolError is a transport-level failure that prevents the tool from
// producing a Result at all.  See the package documentation for the
// IsError-vs-ToolError distinction; in short:
//
//   - A Result with IsError: true is a normal outcome the model handles
//     (file not found, command failed, permission denied).
//   - A ToolError is an infrastructure problem (bad JSON arguments,
//     internal panic, engine offline) that the agent loop must surface
//     as a system-level failure.
//
//nolint:revive // exported name is part of the package's public API per the v0.1 spec
type ToolError struct {
	// Code is one of the Code* constants.
	Code string

	// Message is a short human-readable description.
	Message string

	// Wrapped is the underlying error if any.  Unwrap returns it so
	// errors.Is/As work across the boundary.
	Wrapped error
}

// Error implements the error interface.
func (e *ToolError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Wrapped != nil {
		return fmt.Sprintf("tool: %s: %s: %v", e.Code, e.Message, e.Wrapped)
	}
	return fmt.Sprintf("tool: %s: %s", e.Code, e.Message)
}

// Unwrap returns the wrapped error, if any.
func (e *ToolError) Unwrap() error { return e.Wrapped }

// newInvalidArgs builds a ToolError for argument-validation failures.
func newInvalidArgs(msg string, wrapped error) *ToolError {
	return &ToolError{Code: CodeInvalidArgs, Message: msg, Wrapped: wrapped}
}

// newPermissionFailure builds a ToolError for engine/bus infrastructure
// failures during permission evaluation.  A user "deny" is NOT this — it
// is an IsError Result.
func newPermissionFailure(msg string, wrapped error) *ToolError {
	return &ToolError{Code: CodePermissionDenied, Message: msg, Wrapped: wrapped}
}

// newExecutionFailed builds a ToolError for internal failures preventing
// execution.
func newExecutionFailed(msg string, wrapped error) *ToolError {
	return &ToolError{Code: CodeExecutionFailed, Message: msg, Wrapped: wrapped}
}
