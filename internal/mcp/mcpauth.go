package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// mcpAuthFileMu serialises in-process writes to the mcp-auth file,
// matching the single-mutex pattern used by internal/auth.
var mcpAuthFileMu sync.Mutex

// AuthEntry holds the auth material for one MCP server.  It is
// intentionally generic — any key/value pairs the user needs to inject
// as HTTP headers (e.g. "Authorization", "X-API-Key") are stored here
// rather than in the shared mcp.toml, which is often committed to VCS.
type AuthEntry struct {
	// Headers is the map of header-name → value that will be injected
	// at runtime.  Values are stored in plaintext with mode 0o600 on
	// the auth file; they should contain literal secret values, not
	// $VAR references (those belong in mcp.toml).
	Headers map[string]string `json:"headers,omitempty"`

	// AddedAt is the time the entry was first recorded.
	AddedAt time.Time `json:"added_at"`
}

// AuthStore is the loaded mcp-auth.json file.
type AuthStore struct {
	// Servers maps server name → auth entry.
	Servers map[string]AuthEntry `json:"servers"`
}

// AuthLoadOptions mirrors auth.LoadOptions so callers can redirect
// the file into a tempdir without touching real $HOME.
type AuthLoadOptions struct {
	// HomeDir overrides $HOME for XDG fallback computation.
	HomeDir string

	// XDGStateHome overrides $XDG_STATE_HOME.
	XDGStateHome string
}

// AuthPath returns the resolved path to mcp-auth.json for the given
// options.  The file need not exist.  It lives next to auth.json under
// $XDG_STATE_HOME/hygge/.
func AuthPath(opts AuthLoadOptions) (string, error) {
	dir, err := resolveMCPAuthStateDir(opts)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hygge", "mcp-auth.json"), nil
}

// LoadAuth reads mcp-auth.json.  Missing file → empty store, nil
// error.  Corrupt file → wrapped error.
func LoadAuth(opts AuthLoadOptions) (*AuthStore, error) {
	path, err := AuthPath(opts)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is XDG state dir
	if err != nil {
		if os.IsNotExist(err) {
			return &AuthStore{Servers: map[string]AuthEntry{}}, nil
		}
		return nil, fmt.Errorf("mcp-auth: read %s: %w", path, err)
	}
	if len(data) == 0 {
		return &AuthStore{Servers: map[string]AuthEntry{}}, nil
	}
	var s AuthStore
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("mcp-auth: corrupt %s: %w", path, err)
	}
	if s.Servers == nil {
		s.Servers = map[string]AuthEntry{}
	}
	return &s, nil
}

// GetAuth returns the auth entry for the named server and a found flag.
func (s *AuthStore) GetAuth(serverName string) (AuthEntry, bool) {
	if s == nil || s.Servers == nil {
		return AuthEntry{}, false
	}
	e, ok := s.Servers[serverName]
	return e, ok
}

// SetAuth writes (or replaces) the auth entry for serverName and
// persists the store to disk atomically.
func SetAuth(serverName string, entry AuthEntry, opts AuthLoadOptions) error {
	if serverName == "" {
		return fmt.Errorf("mcp-auth: SetAuth: empty server name")
	}
	mcpAuthFileMu.Lock()
	defer mcpAuthFileMu.Unlock()

	s, err := LoadAuth(opts)
	if err != nil {
		return fmt.Errorf("mcp-auth: SetAuth: %w", err)
	}
	if entry.AddedAt.IsZero() {
		entry.AddedAt = time.Now()
	}
	if s.Servers == nil {
		s.Servers = map[string]AuthEntry{}
	}
	s.Servers[serverName] = entry
	return saveMCPAuth(s, opts)
}

// RemoveAuth deletes the auth entry for serverName.  Idempotent.
func RemoveAuth(serverName string, opts AuthLoadOptions) error {
	mcpAuthFileMu.Lock()
	defer mcpAuthFileMu.Unlock()

	s, err := LoadAuth(opts)
	if err != nil {
		return fmt.Errorf("mcp-auth: RemoveAuth: %w", err)
	}
	if _, ok := s.Servers[serverName]; !ok {
		return nil
	}
	delete(s.Servers, serverName)
	return saveMCPAuth(s, opts)
}

// saveMCPAuth writes s to disk atomically (temp-file + rename), mode 0o600.
// Caller must hold mcpAuthFileMu.
func saveMCPAuth(s *AuthStore, opts AuthLoadOptions) error {
	path, err := AuthPath(opts)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mcp-auth: create dir %s: %w", dir, err)
	}
	if s.Servers == nil {
		s.Servers = map[string]AuthEntry{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("mcp-auth: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // 0o600 intentional
	if err != nil {
		return fmt.Errorf("mcp-auth: open tmp %s: %w", tmp, err)
	}
	_, writeErr := f.Write(data)
	syncErr := f.Sync()
	closeErr := f.Close()

	if writeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mcp-auth: write tmp: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mcp-auth: sync tmp: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mcp-auth: close tmp: %w", closeErr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mcp-auth: rename %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// resolveMCPAuthStateDir resolves the XDG state base directory, matching
// auth.resolveStateDir semantics exactly.
func resolveMCPAuthStateDir(opts AuthLoadOptions) (string, error) {
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
			return "", fmt.Errorf("mcp-auth: get home directory: %w", err)
		}
	}
	return filepath.Join(home, ".local", "state"), nil
}
