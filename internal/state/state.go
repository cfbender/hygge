// Package state manages persisted runtime data for a hygge installation.
//
// # What lives here
//
// Runtime mutable data that is machine-written (not human-edited): the active
// profile name, last-used model, recent session identifiers, and per-file
// trust decisions.  This is distinct from config-as-source (TOML files in
// $XDG_CONFIG_HOME/hygge/), which belongs in package config.
//
// # Storage format
//
// JSON, stored at $XDG_STATE_HOME/hygge/state.json (fallback:
// $HOME/.local/state/hygge/state.json).  The directory is created with mode
// 0o700 on first write; the file itself is written with mode 0o600.
//
// # Atomic writes
//
// Save writes to a sibling .tmp file first, syncs, closes, then renames to
// the real path.  Rename is atomic on POSIX filesystems, so readers always
// see either the previous complete file or the new complete file — never a
// partial write.
//
// # Single-instance limitation
//
// v0.1 provides no daemon or advisory-lock coordination.  Each call to Load
// and Save does a discrete file operation; concurrent processes on the same
// state file may overwrite each other's changes if they read, modify, and
// save the state in overlapping windows.  This is acceptable for v0.1 where
// hygge is typically run as a single process.  A future version may add
// advisory locking or a version/sequence field to detect and reject stale
// writes.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrCorruptState is returned when the state file exists but cannot be
// decoded as valid JSON conforming to the known schema.  This covers empty
// files, malformed JSON, and files that contain unknown top-level fields
// (which indicate a state file written by a newer version of hygge).
var ErrCorruptState = errors.New("state file is corrupt or from a newer version")

// MaxRecentSessions is the maximum number of session IDs retained in
// [State.RecentSessions].  Older entries are dropped when the cap is reached.
const MaxRecentSessions = 20

// State is the persisted runtime data for a hygge installation.
//
// All fields are optional (omitempty); a zero-value State is valid and
// represents a clean first-run installation.
type State struct {
	ActiveProfile  string            `json:"active_profile,omitempty"`
	LastUsedModel  *ModelRef         `json:"last_used_model,omitempty"`
	RecentSessions []string          `json:"recent_sessions,omitempty"`
	TrustedConfigs map[string]string `json:"trusted_configs,omitempty"` // absolute path -> sha256 hex
}

// ModelRef identifies a provider and model name.
type ModelRef struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

// LoadOptions controls how Load, Save, and Path resolve the state file path.
// The zero value uses real environment variables and the real home directory.
type LoadOptions struct {
	// HomeDir overrides $HOME for XDG fallback computation.  Empty = real HOME.
	HomeDir string

	// XDGStateHome overrides $XDG_STATE_HOME.  Empty = real env or fallback.
	XDGStateHome string
}

// Path returns the resolved path to state.json for the given options.  The
// file need not exist.
func Path(opts LoadOptions) (string, error) {
	dir, err := resolveStateDir(opts)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hygge", "state.json"), nil
}

// Load reads state from disk.  If the file does not exist, Load returns a
// zero-valued State and a nil error — missing state on first run is not an
// error.  If the file exists but cannot be decoded, Load returns
// [ErrCorruptState] wrapping the underlying error.
func Load(opts LoadOptions) (*State, error) {
	path, err := Path(opts)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // intentional: path is XDG state dir
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, fmt.Errorf("state: read %s: %w", path, err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty file at %s", ErrCorruptState, path)
	}

	var s State
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if decErr := dec.Decode(&s); decErr != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrCorruptState, path, decErr.Error())
	}

	return &s, nil
}

// Save writes s to disk atomically.  The target directory is created with
// mode 0o700 if it does not exist.  The state file is written with mode
// 0o600.
func Save(s *State, opts LoadOptions) error {
	path, err := Path(opts)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: create dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal: %w", err)
	}
	// Append trailing newline for clean text-file hygiene.
	data = append(data, '\n')

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // 0o600 intentional
	if err != nil {
		return fmt.Errorf("state: open tmp %s: %w", tmp, err)
	}

	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()

	if writeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: write tmp: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: sync tmp: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: close tmp: %w", closeErr)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: rename %s -> %s: %w", tmp, path, err)
	}

	return nil
}

// resolveStateDir computes the base state directory (without the
// "hygge/state.json" suffix) using this precedence:
//
//  1. opts.XDGStateHome if non-empty.
//  2. $XDG_STATE_HOME env var if set and non-empty.
//  3. opts.HomeDir + "/.local/state" if opts.HomeDir is non-empty.
//  4. os.UserHomeDir() + "/.local/state" as the final fallback.
func resolveStateDir(opts LoadOptions) (string, error) {
	if opts.XDGStateHome != "" {
		return opts.XDGStateHome, nil
	}
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return v, nil
	}
	home := opts.HomeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("state: get home directory: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state"), nil
}
