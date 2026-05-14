package auth

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

// opts returns a hermetic LoadOptions pointing entirely into dir.
func opts(dir string) LoadOptions {
	return LoadOptions{XDGStateHome: dir}
}

// authPath returns the expected auth.json path for a given XDG state dir.
func authPath(t *testing.T, dir string) string {
	t.Helper()
	p, err := Path(opts(dir))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	return p
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- 1: nonexistent file -----------------------------------------------------

func TestLoad_NonexistentFile(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(opts(dir))
	if err != nil {
		t.Fatalf("Load on empty dir returned error: %v", err)
	}
	if s == nil {
		t.Fatal("Load returned nil Store")
	}
	if s.Providers == nil {
		t.Error("Providers map nil; want initialised empty map")
	}
	if len(s.Providers) != 0 {
		t.Errorf("Providers: got %v, want empty", s.Providers)
	}
}

// --- 2: API key round-trip ---------------------------------------------------

func TestSetGet_APIKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	cred := Credential{Type: CredAPIKey, APIKey: "sk-abc1234567890"}
	if err := Set("anthropic", cred, o); err != nil {
		t.Fatalf("Set: %v", err)
	}

	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := loaded.Get("anthropic")
	if !ok {
		t.Fatal("Get returned ok=false after Set")
	}
	if got.Type != CredAPIKey {
		t.Errorf("Type: got %q, want %q", got.Type, CredAPIKey)
	}
	if got.APIKey != "sk-abc1234567890" {
		t.Errorf("APIKey: got %q, want %q", got.APIKey, "sk-abc1234567890")
	}
	if got.AddedAt.IsZero() {
		t.Error("AddedAt was not auto-populated by Set")
	}
}

// --- 3: OAuth round-trip incl. ExpiresAt -------------------------------------

func TestSetGet_OAuthRoundTrip(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	cred := Credential{
		Type:         CredOAuth,
		AccessToken:  "at-xyz",
		RefreshToken: "rt-xyz",
		ExpiresAt:    expires,
	}
	if err := Set("openai", cred, o); err != nil {
		t.Fatalf("Set: %v", err)
	}

	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := loaded.Get("openai")
	if !ok {
		t.Fatal("Get returned ok=false after Set")
	}
	if got.Type != CredOAuth {
		t.Errorf("Type: got %q, want %q", got.Type, CredOAuth)
	}
	if got.AccessToken != "at-xyz" || got.RefreshToken != "rt-xyz" {
		t.Errorf("tokens: got access=%q refresh=%q", got.AccessToken, got.RefreshToken)
	}
	if !got.ExpiresAt.Equal(expires) {
		t.Errorf("ExpiresAt: got %v, want %v", got.ExpiresAt, expires)
	}
}

// --- 4: Set overwrites -------------------------------------------------------

func TestSet_Overwrites(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	if err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "old"}, o); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	if err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "new"}, o); err != nil {
		t.Fatalf("Set second: %v", err)
	}
	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, _ := loaded.Get("anthropic")
	if got.APIKey != "new" {
		t.Errorf("APIKey after overwrite: got %q, want %q", got.APIKey, "new")
	}
	if len(loaded.Providers) != 1 {
		t.Errorf("Providers len: got %d, want 1", len(loaded.Providers))
	}
}

// --- 5: Remove idempotent + deletes -----------------------------------------

func TestRemove_IdempotentAndDeletes(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Removing from an absent file is not an error.
	if err := Remove("nope", o); err != nil {
		t.Errorf("Remove on absent provider (no file): %v", err)
	}

	if err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "k"}, o); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Remove("anthropic", o); err != nil {
		t.Fatalf("Remove existing: %v", err)
	}
	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load after Remove: %v", err)
	}
	if _, ok := loaded.Get("anthropic"); ok {
		t.Error("Get returned ok=true after Remove")
	}

	// Removing again is still a no-op.
	if err := Remove("anthropic", o); err != nil {
		t.Errorf("Remove second time: %v", err)
	}
}

// --- 6: List sorted ----------------------------------------------------------

func TestList_Sorted(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	for _, name := range []string{"openai", "anthropic", "groq", "mistral"} {
		if err := Set(name, Credential{Type: CredAPIKey, APIKey: "k"}, o); err != nil {
			t.Fatalf("Set %s: %v", name, err)
		}
	}
	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := loaded.List()
	want := []string{"anthropic", "groq", "mistral", "openai"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("List: got %v, want %v", got, want)
	}
}

// --- 7: file mode 0o600 -----------------------------------------------------

func TestSet_FileMode0600(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	if err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "k"}, o); err != nil {
		t.Fatalf("Set: %v", err)
	}
	p := authPath(t, dir)
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %04o, want 0600", mode)
	}
}

// --- 8: parent dir mode 0o700 -----------------------------------------------

func TestSet_ParentDirMode0700(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	if err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "k"}, o); err != nil {
		t.Fatalf("Set: %v", err)
	}
	p := authPath(t, dir)
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := di.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir mode: got %04o, want 0700", mode)
	}
}

// --- 9: corrupt JSON ---------------------------------------------------------

func TestLoad_CorruptJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, authPath(t, dir), "not json")
	_, err := Load(opts(dir))
	if err == nil {
		t.Fatal("expected error for corrupt file")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("expected ErrCorrupt, got: %v", err)
	}
}

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, authPath(t, dir), "")
	_, err := Load(opts(dir))
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("expected ErrCorrupt, got: %v", err)
	}
}

// --- 10: unknown top-level field --------------------------------------------

func TestLoad_UnknownField(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, authPath(t, dir), `{"future_thing": 1, "providers": {}}`)
	_, err := Load(opts(dir))
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("expected ErrCorrupt, got: %v", err)
	}
}

// --- 11: Path returns expected absolute path --------------------------------

func TestPath_AbsoluteUnderXDG(t *testing.T) {
	base := t.TempDir()
	p, err := Path(opts(base))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(base, "hygge", "auth.json")
	if p != want {
		t.Errorf("Path: got %q, want %q", p, want)
	}
}

// --- 12: concurrent Set on different providers ------------------------------

func TestSet_ConcurrentDifferentProviders(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	const n = 25
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := "p" + string(rune('A'+i))
			if err := Set(name, Credential{Type: CredAPIKey, APIKey: "k"}, o); err != nil {
				errCh <- err
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent Set error: %v", err)
	}

	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(loaded.Providers) != n {
		t.Errorf("Providers len: got %d, want %d", len(loaded.Providers), n)
	}
}

// --- bonus: empty Providers serialises as {} not null -----------------------

func TestSave_EmptyProvidersNotNull(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	// Save an empty store explicitly via Set then Remove.
	if err := Set("x", Credential{Type: CredAPIKey, APIKey: "k"}, o); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := Remove("x", o); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	data, err := os.ReadFile(authPath(t, dir))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)
	// JSON must contain providers as an object literal, never null.
	if !contains(got, `"providers": {}`) {
		t.Errorf("expected empty providers to serialise as {}; got:\n%s", got)
	}
}

// --- bonus: AddedAt explicitly set by caller is preserved -------------------

func TestSet_PreserveCallerAddedAt(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	when := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	cred := Credential{Type: CredAPIKey, APIKey: "k", AddedAt: when}
	if err := Set("anthropic", cred, o); err != nil {
		t.Fatalf("Set: %v", err)
	}
	loaded, _ := Load(o)
	got, _ := loaded.Get("anthropic")
	if !got.AddedAt.Equal(when) {
		t.Errorf("AddedAt: got %v, want %v", got.AddedAt, when)
	}
}

// --- bonus: Set with empty provider name errors -----------------------------

func TestSet_EmptyProviderName(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	if err := Set("", Credential{Type: CredAPIKey, APIKey: "k"}, o); err == nil {
		t.Error("expected error for empty provider name")
	}
}

// --- bonus: OAuth stubs return ErrOAuthUnsupported --------------------------

func TestOAuthStubs(t *testing.T) {
	if _, err := StartOAuth(t.Context(), "anthropic", LoadOptions{}); !errors.Is(err, ErrOAuthUnsupported) {
		t.Errorf("StartOAuth: got %v, want ErrOAuthUnsupported", err)
	}
	if err := CompleteOAuth(t.Context(), "anthropic", "code", LoadOptions{}); !errors.Is(err, ErrOAuthUnsupported) {
		t.Errorf("CompleteOAuth: got %v, want ErrOAuthUnsupported", err)
	}
}

// --- bonus: List/Get on nil receiver safe -----------------------------------

func TestStore_NilSafety(t *testing.T) {
	var s *Store
	if names := s.List(); names != nil {
		t.Errorf("nil List: got %v, want nil", names)
	}
	if _, ok := s.Get("x"); ok {
		t.Error("nil Get: ok=true, want false")
	}
}

// --- bonus: resolveStateDir XDG env fallback --------------------------------

func TestResolveStateDir_XDGEnvFallback(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	o := LoadOptions{HomeDir: t.TempDir()} // HomeDir would be the final fallback
	p, err := Path(o)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(base, "hygge", "auth.json")
	if p != want {
		t.Errorf("Path: got %q, want %q", p, want)
	}
}

func TestResolveStateDir_HomeFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	home := t.TempDir()
	o := LoadOptions{HomeDir: home}
	dir, err := resolveStateDir(o)
	if err != nil {
		t.Fatalf("resolveStateDir: %v", err)
	}
	want := filepath.Join(home, ".local", "state")
	if dir != want {
		t.Errorf("dir: got %q, want %q", dir, want)
	}
}

// --- error paths ------------------------------------------------------------

// TestLoad_UnreadableFile exercises the read-error branch in Load:
// the file exists but cannot be opened.
func TestLoad_UnreadableFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	dir := t.TempDir()
	p := authPath(t, dir)
	writeFile(t, p, `{"providers":{}}`)
	if err := os.Chmod(p, 0o000); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p, 0o600) })

	_, err := Load(opts(dir))
	if err == nil {
		t.Fatal("expected error reading unreadable file")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("expected ErrCorrupt, got: %v", err)
	}
}

// TestSet_LoadFails: Set propagates a Load corruption error wrapped.
func TestSet_LoadFails(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	writeFile(t, authPath(t, dir), "garbage")
	err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "k"}, o)
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("Set: expected wrapped ErrCorrupt, got: %v", err)
	}
}

// TestRemove_LoadFails: Remove propagates a Load corruption error wrapped.
func TestRemove_LoadFails(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	writeFile(t, authPath(t, dir), "garbage")
	err := Remove("anthropic", o)
	if !errors.Is(err, ErrCorrupt) {
		t.Errorf("Remove: expected wrapped ErrCorrupt, got: %v", err)
	}
}

// TestSave_UnwritableParent exercises the MkdirAll branch of save.
func TestSave_UnwritableParent(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	base := t.TempDir()
	readOnly := filepath.Join(base, "ro")
	if err := os.MkdirAll(readOnly, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) }) //nolint:gosec // restoring directory permissions in test cleanup

	o := LoadOptions{XDGStateHome: filepath.Join(readOnly, "state")}
	err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "k"}, o)
	if err == nil {
		t.Fatal("expected error saving into unwritable directory")
	}
}

// TestSave_ReadOnlyDir exercises the OpenFile-tmp branch of save.
func TestSave_ReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	base := t.TempDir()
	o := opts(base)

	p, err := Path(o)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	stateDir := filepath.Dir(p)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(stateDir, 0o500); err != nil { //nolint:gosec // test: make dir read-only
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(stateDir, 0o700) }) //nolint:gosec // restoring directory permissions in test cleanup

	if err := Set("anthropic", Credential{Type: CredAPIKey, APIKey: "k"}, o); err == nil {
		t.Fatal("expected error saving into read-only directory")
	}
}

// TestLoad_MissingProvidersFieldInitialised: a JSON file with no
// "providers" key still yields an initialised map.  Exercises the
// nil-guard branch in Load.
func TestLoad_MissingProvidersFieldInitialised(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, authPath(t, dir), `{}`)
	loaded, err := Load(opts(dir))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Providers == nil {
		t.Error("Providers nil; want initialised empty map")
	}
	// And Get/List still work.
	if _, ok := loaded.Get("x"); ok {
		t.Error("Get on empty store returned ok=true")
	}
	if names := loaded.List(); names != nil {
		t.Errorf("List on empty store: got %v, want nil", names)
	}
}

// TestPath_ResolveDirError: with no XDG override, no env, and no
// HomeDir, Path falls back to os.UserHomeDir.  We can't easily make
// that fail on a real machine — instead, prove the no-opt path
// returns a non-empty value.  This exercises the final resolveStateDir
// branch.
func TestPath_NoOptsFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	dir, err := resolveStateDir(LoadOptions{})
	if err != nil {
		t.Fatalf("resolveStateDir: %v", err)
	}
	if dir == "" {
		t.Error("expected non-empty fallback dir")
	}
}

// contains is a small helper to keep test imports minimal.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
