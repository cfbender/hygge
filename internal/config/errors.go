package config

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by this package.
var (
	// ErrProfileNotFound is returned when a named profile file does not exist.
	// Loading the "default" profile when its file is absent is NOT an error —
	// it is treated as a clean first-run where no profile layer is applied.
	ErrProfileNotFound = errors.New("profile not found")

	// ErrCyclicProfile is returned when profile extends chains form a cycle.
	ErrCyclicProfile = errors.New("cyclic profile extends chain")

	// ErrProfileDepth is returned when the profile extends chain exceeds maxProfileDepth.
	ErrProfileDepth = errors.New("profile extends chain too deep")
)

// maxProfileDepth is the maximum number of profile files that may be chained
// via extends before Load returns ErrProfileDepth.
const maxProfileDepth = 8

// ParseError wraps a TOML parse failure with the originating file path.
type ParseError struct {
	File string
	Err  error
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parse error in %s: %v", e.File, e.Err)
}

func (e *ParseError) Unwrap() error { return e.Err }

// MergeTypeError is returned when two sources provide different concrete types
// for the same key.
type MergeTypeError struct {
	Key      string
	LowFile  string
	HighFile string
	LowType  string
	HighType string
}

func (e *MergeTypeError) Error() string {
	return fmt.Sprintf(
		"type mismatch for key %q: %s provided %s, %s provided %s",
		e.Key, e.LowFile, e.LowType, e.HighFile, e.HighType,
	)
}

// UnknownKeyError is returned when strict struct decoding encounters an unexpected key.
type UnknownKeyError struct {
	Key  string
	File string
}

func (e *UnknownKeyError) Error() string {
	if e.File != "" {
		return fmt.Sprintf("unknown config key %q in %s", e.Key, e.File)
	}
	return fmt.Sprintf("unknown config key %q", e.Key)
}

// InvalidValueError is returned when a value fails validation (e.g. bad
// PermissionMode string).
type InvalidValueError struct {
	Key   string
	Value any
	Msg   string
}

func (e *InvalidValueError) Error() string {
	return fmt.Sprintf("invalid value for %q (%v): %s", e.Key, e.Value, e.Msg)
}
