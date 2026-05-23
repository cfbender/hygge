package mcp

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func hermeticMCPAuth(t *testing.T) (string, AuthLoadOptions) {
	t.Helper()
	home := t.TempDir()
	xdgState := filepath.Join(home, ".local", "state")
	return home, AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
}

// ---------------------------------------------------------------------------
// Path
// ---------------------------------------------------------------------------

func TestAuthPath_UsesXDGStateHome(t *testing.T) {
	home := t.TempDir()
	xdgState := filepath.Join(home, "custom-state")
	opts := AuthLoadOptions{HomeDir: home, XDGStateHome: xdgState}
	p, err := AuthPath(opts)
	if err != nil {
		t.Fatalf("AuthPath: %v", err)
	}
	want := filepath.Join(xdgState, "hygge", "mcp-auth.json")
	if p != want {
		t.Errorf("got %q, want %q", p, want)
	}
}

// ---------------------------------------------------------------------------
// Load — missing file is not an error
// ---------------------------------------------------------------------------

func TestLoadAuth_MissingFile(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if s == nil {
		t.Fatal("store is nil")
	}
	if len(s.Servers) != 0 {
		t.Errorf("want empty Servers, got %v", s.Servers)
	}
}

func TestLoadAuth_EmptyFileIsEmptyStore(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	p, err := AuthPath(opts)
	if err != nil {
		t.Fatalf("AuthPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	if len(s.Servers) != 0 {
		t.Fatalf("got %d servers, want empty store", len(s.Servers))
	}
}

// ---------------------------------------------------------------------------
// SetAuth + LoadAuth round-trip
// ---------------------------------------------------------------------------

func TestSetAuth_RoundTrip(t *testing.T) {
	_, opts := hermeticMCPAuth(t)

	entry := AuthEntry{
		Headers: map[string]string{
			"Authorization": "Bearer tok-secret",
			"X-Custom":      "abc",
		},
		AddedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := SetAuth("my-server", entry, opts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}

	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	got, ok := s.GetAuth("my-server")
	if !ok {
		t.Fatal("server not found after set")
	}
	if got.Headers["Authorization"] != "Bearer tok-secret" {
		t.Errorf("Authorization: got %q", got.Headers["Authorization"])
	}
	if got.Headers["X-Custom"] != "abc" {
		t.Errorf("X-Custom: got %q", got.Headers["X-Custom"])
	}
}

// ---------------------------------------------------------------------------
// SetAuth fills AddedAt when zero
// ---------------------------------------------------------------------------

func TestSetAuth_FillsAddedAt(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	before := time.Now()
	if err := SetAuth("srv", AuthEntry{Headers: map[string]string{"k": "v"}}, opts); err != nil {
		t.Fatalf("SetAuth: %v", err)
	}
	s, _ := LoadAuth(opts)
	got, _ := s.GetAuth("srv")
	if got.AddedAt.IsZero() {
		t.Error("AddedAt should be filled, got zero")
	}
	if got.AddedAt.Before(before) {
		t.Errorf("AddedAt %v is before test started %v", got.AddedAt, before)
	}
}

// ---------------------------------------------------------------------------
// Multiple servers coexist
// ---------------------------------------------------------------------------

func TestSetAuth_MultipleServers(t *testing.T) {
	_, opts := hermeticMCPAuth(t)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if err := SetAuth(name, AuthEntry{Headers: map[string]string{"key": name + "-tok"}}, opts); err != nil {
			t.Fatalf("SetAuth(%s): %v", name, err)
		}
	}

	s, err := LoadAuth(opts)
	if err != nil {
		t.Fatalf("LoadAuth: %v", err)
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e, ok := s.GetAuth(name)
		if !ok {
			t.Errorf("server %q not found", name)
			continue
		}
		if e.Headers["key"] != name+"-tok" {
			t.Errorf("server %q: got %q", name, e.Headers["key"])
		}
	}
}

// ---------------------------------------------------------------------------
// RemoveAuth
// ---------------------------------------------------------------------------

func TestRemoveAuth_Removes(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	if err := SetAuth("to-remove", AuthEntry{Headers: map[string]string{"k": "v"}}, opts); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := RemoveAuth("to-remove", opts); err != nil {
		t.Fatalf("remove: %v", err)
	}
	s, _ := LoadAuth(opts)
	if _, ok := s.GetAuth("to-remove"); ok {
		t.Error("server should be removed")
	}
}

func TestRemoveAuth_Idempotent(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	// remove a server that was never added
	if err := RemoveAuth("ghost", opts); err != nil {
		t.Errorf("RemoveAuth on absent server should be a no-op: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Empty server name → error
// ---------------------------------------------------------------------------

func TestSetAuth_EmptyName(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	if err := SetAuth("", AuthEntry{}, opts); err == nil {
		t.Error("expected error for empty server name")
	}
}

// ---------------------------------------------------------------------------
// File has mode 0600
// ---------------------------------------------------------------------------

func TestMCPAuthFile_Mode(t *testing.T) {
	_, opts := hermeticMCPAuth(t)
	if err := SetAuth("srv", AuthEntry{Headers: map[string]string{"k": "v"}}, opts); err != nil {
		t.Fatalf("set: %v", err)
	}
	p, _ := AuthPath(opts)
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode: got %o, want 0600", info.Mode().Perm())
	}
}

// ---------------------------------------------------------------------------
// GetAuth on nil store is safe
// ---------------------------------------------------------------------------

func TestGetAuth_NilSafe(t *testing.T) {
	var s *AuthStore
	if _, ok := s.GetAuth("any"); ok {
		t.Error("nil store should return not-found")
	}
}
