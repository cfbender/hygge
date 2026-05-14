package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// fileMu serialises in-process writes to the auth file.  Two
// goroutines calling Set/Remove concurrently would otherwise race on
// load-modify-save: each reads the same prior state, applies its
// mutation, and writes back — clobbering the other update.  This
// mutex matches the single-process safety guarantee documented in the
// package doc.  Cross-process races are still possible and are an
// accepted v0.1 limitation, identical to [internal/state].
var fileMu sync.Mutex

// Path returns the resolved path to auth.json for the given options.
// The file need not exist.
func Path(opts LoadOptions) (string, error) {
	dir, err := resolveStateDir(opts)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hygge", "auth.json"), nil
}

// Load reads the auth file from disk.  If the file does not exist,
// Load returns an empty Store (with an initialised Providers map) and
// a nil error — a missing file on first run is not an error.  If the
// file exists but cannot be decoded, Load returns [ErrCorrupt]
// wrapping the underlying error.
func Load(opts LoadOptions) (*Store, error) {
	path, err := Path(opts)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // intentional: path is XDG state dir
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Providers: map[string]Credential{}}, nil
		}
		return nil, fmt.Errorf("%w: read %s: %s", ErrCorrupt, path, err.Error())
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty file at %s", ErrCorrupt, path)
	}

	var s Store
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if decErr := dec.Decode(&s); decErr != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrCorrupt, path, decErr.Error())
	}
	if s.Providers == nil {
		s.Providers = map[string]Credential{}
	}
	return &s, nil
}

// Get returns the credential stored for provider and a found flag.
// The second return value is false when the provider is not in the
// store.
func (s *Store) Get(provider string) (Credential, bool) {
	if s == nil || s.Providers == nil {
		return Credential{}, false
	}
	c, ok := s.Providers[provider]
	return c, ok
}

// List returns the provider names in the store, sorted ascending.
func (s *Store) List() []string {
	if s == nil || len(s.Providers) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.Providers))
	for k := range s.Providers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Set writes (or replaces) the credential for provider and persists
// the store to disk atomically.  If cred.AddedAt is the zero time,
// Set fills it with the current time.  Returns an error if loading
// the prior store fails or the disk write fails.
func Set(provider string, cred Credential, opts LoadOptions) error {
	if provider == "" {
		return fmt.Errorf("auth: Set: empty provider name")
	}
	fileMu.Lock()
	defer fileMu.Unlock()

	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("auth: Set: %w", err)
	}
	if cred.AddedAt.IsZero() {
		cred.AddedAt = time.Now()
	}
	if s.Providers == nil {
		s.Providers = map[string]Credential{}
	}
	s.Providers[provider] = cred
	return save(s, opts)
}

// Remove deletes the credential for provider and persists the store.
// Idempotent — removing an absent provider is not an error.
func Remove(provider string, opts LoadOptions) error {
	fileMu.Lock()
	defer fileMu.Unlock()

	s, err := Load(opts)
	if err != nil {
		return fmt.Errorf("auth: Remove: %w", err)
	}
	if _, ok := s.Providers[provider]; !ok {
		return nil
	}
	delete(s.Providers, provider)
	return save(s, opts)
}

// save writes s to disk atomically.  The target directory is created
// with mode 0o700 if it does not exist.  The auth file is written
// with mode 0o600.
//
// The caller must already hold fileMu — Save itself does not lock so
// that mutators which already hold the mutex can call it directly.
func save(s *Store, opts LoadOptions) error {
	path, err := Path(opts)
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("auth: create dir %s: %w", dir, err)
	}

	// Marshal the store.  We always serialise Providers as an object,
	// never null — initialise it here in case the caller cleared the
	// map.
	if s.Providers == nil {
		s.Providers = map[string]Credential{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // 0o600 intentional
	if err != nil {
		return fmt.Errorf("auth: open tmp %s: %w", tmp, err)
	}

	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()

	if writeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("auth: write tmp: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("auth: sync tmp: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("auth: close tmp: %w", closeErr)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("auth: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// resolveStateDir computes the base state directory (without the
// "hygge/auth.json" suffix) using the same precedence as
// [internal/state]:
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
			return "", fmt.Errorf("auth: get home directory: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state"), nil
}
