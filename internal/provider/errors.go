package provider

import "errors"

// Typed errors used to classify provider failures for the agent loop.  These
// are sentinel values returned (directly or wrapped) by Stream, CountTokens,
// and ListModels.  Callers use errors.Is to branch on the class.
//
// Retry policy is the agent's responsibility, not the adapter's.  These
// errors merely advertise whether a retry is plausibly worthwhile.
var (
	// ErrAuth indicates an authentication failure (HTTP 401, missing key,
	// unsupported key reference).  Not retriable without operator action.
	ErrAuth = errors.New("provider: authentication failed")

	// ErrAuthOpRefUnsupported is returned when an api_key option is an
	// op:// reference.  The 1Password CLI integration is a Task 10+ item;
	// the seam exists in v0.1 only so configs that use op:// fail with a
	// clear, recognisable error.
	ErrAuthOpRefUnsupported = errors.New("provider: op:// API key references are not supported yet")

	// ErrInvalidRequest indicates the upstream rejected the request as
	// malformed (HTTP 400).  Not retriable; a bug elsewhere.
	ErrInvalidRequest = errors.New("provider: invalid request")

	// ErrRateLimited indicates rate limiting (HTTP 429).  Retriable with
	// backoff.
	ErrRateLimited = errors.New("provider: rate limited")

	// ErrTransient indicates an upstream error that is plausibly worth
	// retrying (HTTP 5xx, mid-stream network failure, overloaded_error).
	ErrTransient = errors.New("provider: transient upstream error")

	// ErrUnknownProvider is returned by Get when no factory has been
	// registered under the requested name.
	ErrUnknownProvider = errors.New("provider: unknown provider")
)
