// Package auth manages per-machine credential storage for hygge.
//
// # What lives here
//
// Provider authentication material — API keys today, OAuth tokens in a
// future revision.  These secrets are intentionally *not* part of the
// human-edited TOML config files, which often end up checked in to
// dotfiles repositories.  They live in a per-machine JSON file under
// $XDG_STATE_HOME.
//
// # Storage format
//
// JSON at $XDG_STATE_HOME/hygge/auth.json (fallback:
// $HOME/.local/state/hygge/auth.json).  The parent directory is created
// with mode 0o700; the file itself is written with mode 0o600.  Atomic
// writes via temp-file + rename, matching the [internal/state] package.
//
// # Single-process semantics
//
// Like [internal/state], v0.1 does not coordinate writes across
// processes.  Concurrent hygge invocations that mutate the auth store
// in overlapping windows may lose updates.  This is acceptable for v0.1
// where hygge is run as a single foreground process.
package auth

import (
	"errors"
	"time"
)

// CredentialType discriminates the shape of a stored [Credential].
type CredentialType string

const (
	// CredAPIKey selects the API-key shape: the [Credential.APIKey] field
	// is populated; the OAuth-related fields are zero.
	CredAPIKey CredentialType = "api_key"

	// CredOAuth selects the OAuth shape: the AccessToken, RefreshToken,
	// and ExpiresAt fields are populated.  v0.1 does not implement the
	// OAuth flow itself — see [ErrOAuthUnsupported].
	CredOAuth CredentialType = "oauth"
)

// Credential is one provider's stored auth material.  Only one of the
// two shape-specific field groups is populated, determined by Type.
type Credential struct {
	Type CredentialType `json:"type"`

	// API key shape:
	APIKey string `json:"api_key,omitempty"`

	// OAuth shape:
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`

	// Common:
	AddedAt time.Time `json:"added_at"`
}

// Store is the loaded auth file.  Construct via [Load].  The zero value
// is valid and represents an empty store.
type Store struct {
	// Providers maps provider name → credential.
	Providers map[string]Credential `json:"providers"`
}

// LoadOptions configures [Load], [Save], [Path], [Set], and [Remove].
// The zero value uses real environment variables and the real home
// directory.  Mirrors [internal/state].LoadOptions.
type LoadOptions struct {
	// HomeDir overrides $HOME for XDG fallback computation.  Empty =
	// real HOME.
	HomeDir string

	// XDGStateHome overrides $XDG_STATE_HOME.  Empty = real env or
	// fallback.
	XDGStateHome string
}

// ErrCorrupt is returned (wrapped) when the auth file exists but
// cannot be decoded as valid JSON conforming to the known schema.  It
// covers empty files, malformed JSON, and files containing unknown
// top-level fields.
var ErrCorrupt = errors.New("auth: corrupt file")

// ErrOAuthUnsupported is returned by the OAuth stubs in this package.
// v0.1 only supports API-key credentials end-to-end; OAuth wiring is
// scaffolded but not yet functional.
var ErrOAuthUnsupported = errors.New("auth: oauth flow not yet supported")
