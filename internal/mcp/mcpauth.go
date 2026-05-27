package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	// at runtime. Values are stored in plaintext with mode 0o600 on
	// the auth file; they should contain literal secret values, not
	// $VAR references (those belong in mcp.toml).
	Headers map[string]string `json:"headers,omitempty"`

	// OAuth holds the legacy bearer-token shape for MCP servers that support
	// OAuth-based authorization. New auth flows populate Tokens with token
	// sets (access token, refresh token, expiry, scope) and ClientInfo with
	// OAuth client metadata; OAuthCredential is retained for compatibility.
	OAuth *OAuthCredential `json:"oauth,omitempty"`

	// Tokens and ClientInfo are the current OAuth auth-flow storage shape.
	Tokens       *OAuthTokens     `json:"tokens,omitempty"`
	ClientInfo   *OAuthClientInfo `json:"client_info,omitempty"`
	CodeVerifier string           `json:"code_verifier,omitempty"`
	OAuthState   string           `json:"oauth_state,omitempty"`
	ServerURL    string           `json:"server_url,omitempty"`

	// AddedAt is the time the entry was first recorded.
	AddedAt time.Time `json:"added_at"`
}

// OAuthCredential is OAuth bearer-token material for one MCP server.
type OAuthCredential struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	ClientID     string `json:"client_id,omitempty"`
	ClientSecret string `json:"client_secret,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // Unix milliseconds.
	Scope        string `json:"scope,omitempty"`
}

// OAuthTokens is the current OAuth token shape stored for an MCP server.
type OAuthTokens struct {
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenURL     string `json:"token_url,omitempty"`
	ExpiresAt    int64  `json:"expires_at,omitempty"` // Unix milliseconds.
	Scope        string `json:"scope,omitempty"`
}

// OAuthClientInfo is cached dynamic-client-registration state.
type OAuthClientInfo struct {
	ClientID              string `json:"client_id,omitempty"`
	ClientSecret          string `json:"client_secret,omitempty"`
	ClientIDIssuedAt      int64  `json:"client_id_issued_at,omitempty"`
	ClientSecretExpiresAt int64  `json:"client_secret_expires_at,omitempty"`
}

// UnmarshalJSON decodes OAuthTokens while accepting expires_at in either
// the canonical numeric form (Unix milliseconds) or as a JSON string. The
// string form covers files written by other MCP clients or earlier shapes
// that serialised expiry as an RFC3339 timestamp or a decimal-string Unix
// epoch. Decoded values are normalised to Unix milliseconds; the field is
// always marshalled back as an integer.
func (t *OAuthTokens) UnmarshalJSON(data []byte) error {
	type alias OAuthTokens
	aux := struct {
		ExpiresAt json.RawMessage `json:"expires_at,omitempty"`
		*alias
	}{alias: (*alias)(t)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	ms, err := parseExpiresAtMillis(aux.ExpiresAt)
	if err != nil {
		return fmt.Errorf("expires_at: %w", err)
	}
	t.ExpiresAt = ms
	return nil
}

// UnmarshalJSON decodes OAuthCredential with the same expires_at
// tolerance as [OAuthTokens.UnmarshalJSON].
func (c *OAuthCredential) UnmarshalJSON(data []byte) error {
	type alias OAuthCredential
	aux := struct {
		ExpiresAt json.RawMessage `json:"expires_at,omitempty"`
		*alias
	}{alias: (*alias)(c)}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	ms, err := parseExpiresAtMillis(aux.ExpiresAt)
	if err != nil {
		return fmt.Errorf("expires_at: %w", err)
	}
	c.ExpiresAt = ms
	return nil
}

// parseExpiresAtMillis converts a JSON expires_at value into Unix
// milliseconds. Accepted shapes:
//
//   - missing / null / empty → 0
//   - JSON number (integer)  → used verbatim as Unix milliseconds
//   - JSON string containing an RFC3339 / RFC3339Nano timestamp
//   - JSON string containing a decimal Unix-millisecond integer
//
// All other shapes return an error.
func parseExpiresAtMillis(raw json.RawMessage) (int64, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return 0, nil
	}
	if trimmed[0] != '"' {
		var n int64
		if err := json.Unmarshal(trimmed, &n); err != nil {
			return 0, fmt.Errorf("expected integer milliseconds or RFC3339 string: %w", err)
		}
		return n, nil
	}
	var s string
	if err := json.Unmarshal(trimmed, &s); err != nil {
		return 0, err
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if ts, err := time.Parse(layout, s); err == nil {
			return ts.UnixMilli(), nil
		}
	}
	return 0, fmt.Errorf("unrecognised timestamp %q (want Unix milliseconds or RFC3339)", s)
}

// HeadersWithOAuth returns entry.Headers plus an Authorization bearer
// header when OAuth access-token material is present. Explicit stored
// Authorization headers take precedence so existing auth-header configs
// keep their behaviour.
func (e AuthEntry) HeadersWithOAuth() map[string]string {
	out := make(map[string]string, len(e.Headers)+1)
	maps.Copy(out, e.Headers)
	for key := range out {
		if strings.EqualFold(key, "Authorization") {
			return out
		}
	}
	if e.Tokens != nil && e.Tokens.AccessToken != "" {
		out["Authorization"] = "Bearer " + e.Tokens.AccessToken
		return out
	}
	if e.OAuth != nil && e.OAuth.AccessToken != "" {
		out["Authorization"] = "Bearer " + e.OAuth.AccessToken
	}
	return out
}

// RefreshOAuth refreshes the OAuth access token when it is close to expiry.
// It returns true when the entry changed and should be persisted.
func (e *AuthEntry) RefreshOAuth(client *http.Client, now time.Time) (bool, error) {
	if e == nil {
		return false, nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	if e.Tokens != nil {
		if e.Tokens.RefreshToken == "" || e.Tokens.TokenURL == "" {
			return false, nil
		}
		if e.Tokens.AccessToken != "" && e.Tokens.ExpiresAt > 0 && now.Add(2*time.Minute).UnixMilli() < e.Tokens.ExpiresAt {
			return false, nil
		}
		clientID, clientSecret := "", ""
		if e.ClientInfo != nil {
			clientID = e.ClientInfo.ClientID
			clientSecret = e.ClientInfo.ClientSecret
		}
		fresh, err := RefreshOAuthToken(client, OAuthRefreshRequest{
			TokenURL:     e.Tokens.TokenURL,
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RefreshToken: e.Tokens.RefreshToken,
		})
		if err != nil {
			return false, err
		}
		if fresh.AccessToken != "" {
			e.Tokens.AccessToken = fresh.AccessToken
		}
		if fresh.RefreshToken != "" {
			e.Tokens.RefreshToken = fresh.RefreshToken
		}
		if fresh.ExpiresIn > 0 {
			e.Tokens.ExpiresAt = now.Add(time.Duration(fresh.ExpiresIn) * time.Second).UnixMilli()
		} else {
			e.Tokens.ExpiresAt = 0
		}
		if fresh.Scope != "" {
			e.Tokens.Scope = fresh.Scope
		}
		return true, nil
	}
	if e.OAuth == nil || e.OAuth.RefreshToken == "" || e.OAuth.TokenURL == "" {
		return false, nil
	}
	if e.OAuth.AccessToken != "" && e.OAuth.ExpiresAt > 0 && now.Add(2*time.Minute).UnixMilli() < e.OAuth.ExpiresAt {
		return false, nil
	}
	fresh, err := RefreshOAuthToken(client, OAuthRefreshRequest{
		TokenURL:     e.OAuth.TokenURL,
		ClientID:     e.OAuth.ClientID,
		ClientSecret: e.OAuth.ClientSecret,
		RefreshToken: e.OAuth.RefreshToken,
	})
	if err != nil {
		return false, err
	}
	if fresh.AccessToken != "" {
		e.OAuth.AccessToken = fresh.AccessToken
	}
	if fresh.RefreshToken != "" {
		e.OAuth.RefreshToken = fresh.RefreshToken
	}
	if fresh.ExpiresIn > 0 {
		e.OAuth.ExpiresAt = now.Add(time.Duration(fresh.ExpiresIn) * time.Second).UnixMilli()
	} else {
		e.OAuth.ExpiresAt = 0
	}
	if fresh.Scope != "" {
		e.OAuth.Scope = fresh.Scope
	}
	return true, nil
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
