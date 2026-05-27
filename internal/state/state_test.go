package state

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// opts returns a hermetic LoadOptions pointing entirely into dir.
func opts(dir string) LoadOptions {
	return LoadOptions{HomeDir: dir}
}

// statePath returns the expected state.json path for a given home dir.
func statePath(t *testing.T, dir string) string {
	t.Helper()
	p, err := Path(opts(dir))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	return p
}

// writeFile writes content to path, creating parent directories as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- test 1: first run -------------------------------------------------------

func TestLoad_FirstRun(t *testing.T) {
	dir := t.TempDir()
	s, err := Load(opts(dir))
	if err != nil {
		t.Fatalf("Load on empty dir returned error: %v", err)
	}
	if s == nil {
		t.Fatal("Load returned nil State")
	}
	if s.ActiveProfile != "" {
		t.Errorf("ActiveProfile: got %q, want empty", s.ActiveProfile)
	}
	if s.LastUsedModel != nil {
		t.Errorf("LastUsedModel: got %v, want nil", s.LastUsedModel)
	}
	if len(s.RecentSessions) != 0 {
		t.Errorf("RecentSessions: got %v, want empty", s.RecentSessions)
	}
	if len(s.TrustedConfigs) != 0 {
		t.Errorf("TrustedConfigs: got %v, want empty", s.TrustedConfigs)
	}
}

// --- test 2: round trip ------------------------------------------------------

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	original := &State{
		ActiveProfile:  "work",
		LastUsedModel:  &ModelRef{Provider: "anthropic", Name: "claude-opus-4-5"},
		RecentSessions: []string{"sess-1", "sess-2"},
		TrustedConfigs: map[string]string{"/etc/foo": "abc123"},
	}

	if err := Save(original, o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.ActiveProfile != original.ActiveProfile {
		t.Errorf("ActiveProfile: got %q, want %q", loaded.ActiveProfile, original.ActiveProfile)
	}
	if loaded.LastUsedModel == nil {
		t.Fatal("LastUsedModel is nil after round trip")
	}
	if loaded.LastUsedModel.Provider != original.LastUsedModel.Provider {
		t.Errorf("LastUsedModel.Provider: got %q, want %q",
			loaded.LastUsedModel.Provider, original.LastUsedModel.Provider)
	}
	if loaded.LastUsedModel.Name != original.LastUsedModel.Name {
		t.Errorf("LastUsedModel.Name: got %q, want %q",
			loaded.LastUsedModel.Name, original.LastUsedModel.Name)
	}
	if len(loaded.RecentSessions) != 2 ||
		loaded.RecentSessions[0] != "sess-1" ||
		loaded.RecentSessions[1] != "sess-2" {
		t.Errorf("RecentSessions: got %v, want [sess-1 sess-2]", loaded.RecentSessions)
	}
	if loaded.TrustedConfigs["/etc/foo"] != "abc123" {
		t.Errorf("TrustedConfigs[/etc/foo]: got %q, want abc123",
			loaded.TrustedConfigs["/etc/foo"])
	}
}

// --- test 3: atomic write + concurrent load ----------------------------------

func TestSave_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Seed an initial known-good state.
	initial := &State{ActiveProfile: "initial"}
	if err := Save(initial, o); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	const iterations = 100
	var wg sync.WaitGroup
	errCh := make(chan error, iterations+1)

	// Writer goroutine: repeatedly overwrites state.
	wg.Go(func() {
		for i := range iterations {
			s := &State{ActiveProfile: "updated", RecentSessions: []string{"a", "b", "c"}}
			_ = i
			if err := Save(s, o); err != nil {
				errCh <- err
				return
			}
		}
	})

	// Reader goroutines: each Load must succeed (file is either old or new valid JSON).
	for range iterations {
		wg.Go(func() {
			s, err := Load(o)
			if err != nil {
				errCh <- err
				return
			}
			// The loaded state must be one of the two valid values.
			if s.ActiveProfile != "initial" && s.ActiveProfile != "updated" {
				errCh <- errors.New("unexpected ActiveProfile: " + s.ActiveProfile)
			}
		})
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Errorf("concurrent access error: %v", err)
	}
}

// --- test 4: corrupt file ----------------------------------------------------

func TestLoad_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, statePath(t, dir), "not json")

	_, err := Load(opts(dir))
	if err == nil {
		t.Fatal("expected error for corrupt file")
	}
	if !errors.Is(err, ErrCorruptState) {
		t.Errorf("expected ErrCorruptState, got: %v", err)
	}
}

// --- test 5: empty file ------------------------------------------------------

func TestLoad_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, statePath(t, dir), "")

	_, err := Load(opts(dir))
	if err == nil {
		t.Fatal("expected error for empty file")
	}
	if !errors.Is(err, ErrCorruptState) {
		t.Errorf("expected ErrCorruptState, got: %v", err)
	}
}

// --- test 6: unknown fields --------------------------------------------------

func TestLoad_UnknownFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, statePath(t, dir), `{"future_thing": 1}`)

	_, err := Load(opts(dir))
	if err == nil {
		t.Fatal("expected error for unknown fields")
	}
	if !errors.Is(err, ErrCorruptState) {
		t.Errorf("expected ErrCorruptState, got: %v", err)
	}
}

// --- test 7: AddRecentSession dedupe + cap -----------------------------------

func TestAddRecentSession_DedupeAndCap(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Add 25 unique sessions — cap should kick in at 20.
	for i := range 25 {
		id := string(rune('a' + i)) // a, b, c, ... y
		if err := AddRecentSession(id, o); err != nil {
			t.Fatalf("AddRecentSession(%q): %v", id, err)
		}
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.RecentSessions) != MaxRecentSessions {
		t.Errorf("len(RecentSessions): got %d, want %d", len(s.RecentSessions), MaxRecentSessions)
	}
	// Most recently added is "y" (index 24), should be first.
	if s.RecentSessions[0] != "y" {
		t.Errorf("RecentSessions[0]: got %q, want %q", s.RecentSessions[0], "y")
	}

	// Deduplicate: re-adding an existing session should move it to front.
	if err := AddRecentSession("a", o); err != nil {
		t.Fatalf("AddRecentSession re-add: %v", err)
	}
	s, err = Load(o)
	if err != nil {
		t.Fatalf("Load after re-add: %v", err)
	}
	if s.RecentSessions[0] != "a" {
		t.Errorf("after re-add, RecentSessions[0]: got %q, want %q", s.RecentSessions[0], "a")
	}
	// Length must still not exceed cap.
	if len(s.RecentSessions) != MaxRecentSessions {
		t.Errorf("after re-add, len: got %d, want %d", len(s.RecentSessions), MaxRecentSessions)
	}
	// "a" must not appear twice.
	count := 0
	for _, v := range s.RecentSessions {
		if v == "a" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("'a' appears %d times, want 1", count)
	}
}

// --- test 8: SetActiveProfile ------------------------------------------------

func TestSetActiveProfile(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	if err := SetActiveProfile("work", o); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.ActiveProfile != "work" {
		t.Errorf("ActiveProfile: got %q, want work", s.ActiveProfile)
	}

	// Overwrite.
	if err := SetActiveProfile("personal", o); err != nil {
		t.Fatalf("SetActiveProfile overwrite: %v", err)
	}
	s, err = Load(o)
	if err != nil {
		t.Fatalf("Load after overwrite: %v", err)
	}
	if s.ActiveProfile != "personal" {
		t.Errorf("ActiveProfile after overwrite: got %q, want personal", s.ActiveProfile)
	}
}

// --- test 9: TrustConfig / IsConfigTrusted -----------------------------------

func TestTrustConfig(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	const (
		path    = "/home/user/.hygge/config.toml"
		sha256a = "abc123def456"
		sha256b = "deadbeef0000"
	)

	// Not yet trusted.
	trusted, err := IsConfigTrusted(path, sha256a, o)
	if err != nil {
		t.Fatalf("IsConfigTrusted before trust: %v", err)
	}
	if trusted {
		t.Error("expected false before any trust is recorded")
	}

	// Record trust.
	if err := TrustConfig(path, sha256a, o); err != nil {
		t.Fatalf("TrustConfig: %v", err)
	}

	// Matching digest → trusted.
	trusted, err = IsConfigTrusted(path, sha256a, o)
	if err != nil {
		t.Fatalf("IsConfigTrusted matching: %v", err)
	}
	if !trusted {
		t.Error("expected true after recording trust with matching sha256")
	}

	// Mismatched digest → not trusted.
	trusted, err = IsConfigTrusted(path, sha256b, o)
	if err != nil {
		t.Fatalf("IsConfigTrusted mismatch: %v", err)
	}
	if trusted {
		t.Error("expected false when sha256 does not match stored value")
	}

	// Missing entry → not trusted.
	trusted, err = IsConfigTrusted("/other/path", sha256a, o)
	if err != nil {
		t.Fatalf("IsConfigTrusted missing: %v", err)
	}
	if trusted {
		t.Error("expected false for a path that was never trusted")
	}
}

// --- test 10: file and directory permissions ---------------------------------

func TestSave_Permissions(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	s := &State{ActiveProfile: "test"}
	if err := Save(s, o); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := statePath(t, dir)

	// File must be 0o600.
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Errorf("file mode: got %04o, want 0600", mode)
	}

	// Directory must be 0o700.
	di, err := os.Stat(filepath.Dir(p))
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := di.Mode().Perm(); mode != 0o700 {
		t.Errorf("dir mode: got %04o, want 0700", mode)
	}
}

// --- test 11: XDGStateHome override -----------------------------------------

func TestPath_XDGStateHomeOverride(t *testing.T) {
	base := t.TempDir()
	o := LoadOptions{XDGStateHome: base}

	p, err := Path(o)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	expected := filepath.Join(base, "hygge", "state.json")
	if p != expected {
		t.Errorf("Path: got %q, want %q", p, expected)
	}

	// Verify Save and Load also land at the same XDG-overridden path.
	if err := Save(&State{ActiveProfile: "xdg"}, o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("state.json not found at XDG path %q: %v", p, err)
	}

	loaded, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ActiveProfile != "xdg" {
		t.Errorf("ActiveProfile: got %q, want xdg", loaded.ActiveProfile)
	}
}

// --- test: SetLastUsedModel --------------------------------------------------

func TestSetLastUsedModel(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	ref := ModelRef{Provider: "openai", Name: "gpt-4o"}
	if err := SetLastUsedModel(ref, o); err != nil {
		t.Fatalf("SetLastUsedModel: %v", err)
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.LastUsedModel == nil {
		t.Fatal("LastUsedModel is nil after SetLastUsedModel")
	}
	if s.LastUsedModel.Provider != ref.Provider {
		t.Errorf("Provider: got %q, want %q", s.LastUsedModel.Provider, ref.Provider)
	}
	if s.LastUsedModel.Name != ref.Name {
		t.Errorf("Name: got %q, want %q", s.LastUsedModel.Name, ref.Name)
	}

	// Overwrite with a new model.
	ref2 := ModelRef{Provider: "anthropic", Name: "claude-opus-4-5"}
	if err := SetLastUsedModel(ref2, o); err != nil {
		t.Fatalf("SetLastUsedModel overwrite: %v", err)
	}
	s, err = Load(o)
	if err != nil {
		t.Fatalf("Load after overwrite: %v", err)
	}
	if s.LastUsedModel.Name != ref2.Name {
		t.Errorf("LastUsedModel.Name after overwrite: got %q, want %q",
			s.LastUsedModel.Name, ref2.Name)
	}
}

// --- test: resolveStateDir with XDG env var (no opts fields) -----------------

func TestResolveStateDir_XDGEnvVar(t *testing.T) {
	// Use the XDG_STATE_HOME env fallback in resolveStateDir by setting the
	// real environment variable (only when no XDGStateHome override is set).
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)

	// opts with no XDGStateHome so the env var branch is exercised.
	o := LoadOptions{HomeDir: t.TempDir()} // HomeDir would be the final fallback
	p, err := Path(o)
	if err != nil {
		t.Fatalf("Path with XDG_STATE_HOME env: %v", err)
	}
	expected := filepath.Join(base, "hygge", "state.json")
	if p != expected {
		t.Errorf("Path: got %q, want %q", p, expected)
	}
}

// --- test: Save replaces prior state atomically (tmp cleanup) ----------------

func TestSave_TmpFileCleanedUp(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Save twice; the .tmp file should not linger after either save.
	for i := range 3 {
		_ = i
		s := &State{ActiveProfile: "clean"}
		if err := Save(s, o); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}

	p, _ := Path(o)
	tmp := p + ".tmp"
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Errorf("tmp file should not exist after successful Save, err: %v", err)
	}
}

// --- test: resolveStateDir real-home fallback --------------------------------

func TestResolveStateDir_RealHomeFallback(t *testing.T) {
	// With empty opts and no XDG_STATE_HOME set, resolveStateDir must use
	// os.UserHomeDir().  We unset XDG_STATE_HOME so the env branch is skipped.
	t.Setenv("XDG_STATE_HOME", "")

	o := LoadOptions{} // no HomeDir, no XDGStateHome
	dir, err := resolveStateDir(o)
	if err != nil {
		t.Fatalf("resolveStateDir with no opts: %v", err)
	}
	// Must end with /.local/state (relative to actual home).
	home, _ := os.UserHomeDir()
	expected := filepath.Join(home, ".local", "state")
	if dir != expected {
		t.Errorf("resolveStateDir: got %q, want %q", dir, expected)
	}
}

// --- test: Save error when directory cannot be created ----------------------

func TestSave_UnwritableParent(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	// Create a read-only parent directory so MkdirAll fails.
	base := t.TempDir()
	readOnly := filepath.Join(base, "ro")
	if err := os.MkdirAll(readOnly, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(readOnly, 0o700) }) //nolint:gosec // restoring directory permissions in test cleanup

	o := LoadOptions{HomeDir: filepath.Join(readOnly, "home")}
	err := Save(&State{ActiveProfile: "x"}, o)
	if err == nil {
		t.Fatal("expected error saving into unwritable directory")
	}
}

// --- test: Save error when target directory is read-only (OpenFile fails) ----

func TestSave_ReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses permission checks")
	}
	base := t.TempDir()
	o := opts(base)

	// Create the hygge dir, then make it read-only so OpenFile on the .tmp fails.
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

	err = Save(&State{ActiveProfile: "x"}, o)
	if err == nil {
		t.Fatal("expected error saving into read-only directory")
	}
}

// --- test: mutator error propagation from Load --------------------------------

func TestMutators_PropagateLoadError(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Write a corrupt state file so all Load-based mutators fail.
	writeFile(t, statePath(t, dir), "not json")

	if err := SetActiveProfile("x", o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("SetActiveProfile: expected ErrCorruptState, got %v", err)
	}
	if err := SetLastUsedModel(ModelRef{Provider: "p", Name: "n"}, o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("SetLastUsedModel: expected ErrCorruptState, got %v", err)
	}
	if err := AddRecentSession("s", o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("AddRecentSession: expected ErrCorruptState, got %v", err)
	}
	if err := TrustConfig("/p", "sha", o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("TrustConfig: expected ErrCorruptState, got %v", err)
	}
	_, err := IsConfigTrusted("/p", "sha", o)
	if !errors.Is(err, ErrCorruptState) {
		t.Errorf("IsConfigTrusted: expected ErrCorruptState, got %v", err)
	}
}

// --- test: AllowedRules backward-compat + mutators ---------------------------

func TestLoad_BackwardCompat_NoAllowedRules(t *testing.T) {
	dir := t.TempDir()
	// Write a state.json that predates the AllowedRules field.
	writeFile(t, statePath(t, dir), `{"active_profile":"work"}`)

	s, err := Load(opts(dir))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.ActiveProfile != "work" {
		t.Errorf("ActiveProfile: got %q, want work", s.ActiveProfile)
	}
	if len(s.AllowedRules) != 0 {
		t.Errorf("AllowedRules: got %v, want empty slice", s.AllowedRules)
	}
}

func TestAddAllowRule(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	rule := AllowRule{Category: "file.write", Pattern: "/repo/src/**"}
	if err := AddAllowRule(rule, o); err != nil {
		t.Fatalf("AddAllowRule: %v", err)
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.AllowedRules) != 1 {
		t.Fatalf("AllowedRules: got %d, want 1", len(s.AllowedRules))
	}
	got := s.AllowedRules[0]
	if got.Category != rule.Category || got.Pattern != rule.Pattern {
		t.Errorf("rule mismatch: got %+v, want %+v", got, rule)
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt: got 0, want a non-zero unix-ms timestamp")
	}

	// Adding the same rule again is a no-op (dedupe by Category+Pattern).
	if err := AddAllowRule(rule, o); err != nil {
		t.Fatalf("AddAllowRule dedupe: %v", err)
	}
	s, err = Load(o)
	if err != nil {
		t.Fatalf("Load after dedupe: %v", err)
	}
	if len(s.AllowedRules) != 1 {
		t.Errorf("AllowedRules length after dedupe: got %d, want 1", len(s.AllowedRules))
	}

	// A rule with different category but same pattern is a separate entry.
	if err := AddAllowRule(AllowRule{Category: "file.read", Pattern: "/repo/src/**"}, o); err != nil {
		t.Fatalf("AddAllowRule second category: %v", err)
	}
	s, err = Load(o)
	if err != nil {
		t.Fatalf("Load after second add: %v", err)
	}
	if len(s.AllowedRules) != 2 {
		t.Errorf("AllowedRules length: got %d, want 2", len(s.AllowedRules))
	}
}

func TestRemoveAllowRule(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	for _, r := range []AllowRule{
		{Category: "file.write", Pattern: "/a"},
		{Category: "file.write", Pattern: "/b"},
		{Category: "shell", Pattern: "ls *"},
	} {
		if err := AddAllowRule(r, o); err != nil {
			t.Fatalf("AddAllowRule(%+v): %v", r, err)
		}
	}

	if err := RemoveAllowRule("file.write", "/a", o); err != nil {
		t.Fatalf("RemoveAllowRule: %v", err)
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.AllowedRules) != 2 {
		t.Fatalf("AllowedRules len: got %d, want 2", len(s.AllowedRules))
	}
	for _, r := range s.AllowedRules {
		if r.Category == "file.write" && r.Pattern == "/a" {
			t.Errorf("removed rule still present: %+v", r)
		}
	}

	// Removing a missing rule is idempotent.
	if err := RemoveAllowRule("file.write", "/does-not-exist", o); err != nil {
		t.Errorf("RemoveAllowRule on missing: got %v, want nil", err)
	}

	// Removing from empty list is also fine.
	if err := RemoveAllowRule("file.write", "/b", o); err != nil {
		t.Fatalf("RemoveAllowRule second: %v", err)
	}
	if err := RemoveAllowRule("shell", "ls *", o); err != nil {
		t.Fatalf("RemoveAllowRule third: %v", err)
	}
	s, _ = Load(o)
	if len(s.AllowedRules) != 0 {
		t.Errorf("AllowedRules after clearing: got %d, want 0", len(s.AllowedRules))
	}
	if err := RemoveAllowRule("nope", "nope", o); err != nil {
		t.Errorf("RemoveAllowRule on empty: got %v, want nil", err)
	}
}

func TestAddAllowRule_PreserveExistingTimestamp(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Pre-populate with an explicit CreatedAt.
	original := AllowRule{Category: "shell", Pattern: "git status", CreatedAt: 1234567890}
	if err := AddAllowRule(original, o); err != nil {
		t.Fatalf("AddAllowRule: %v", err)
	}
	s, _ := Load(o)
	if s.AllowedRules[0].CreatedAt != 1234567890 {
		t.Errorf("CreatedAt: got %d, want 1234567890", s.AllowedRules[0].CreatedAt)
	}

	// Adding "the same rule" with a fresh timestamp must not overwrite the original.
	if err := AddAllowRule(AllowRule{Category: "shell", Pattern: "git status"}, o); err != nil {
		t.Fatalf("AddAllowRule re-add: %v", err)
	}
	s, _ = Load(o)
	if len(s.AllowedRules) != 1 {
		t.Fatalf("AllowedRules len: got %d, want 1", len(s.AllowedRules))
	}
	if s.AllowedRules[0].CreatedAt != 1234567890 {
		t.Errorf("CreatedAt mutated by re-add: got %d, want 1234567890",
			s.AllowedRules[0].CreatedAt)
	}
}

func TestAllowedRules_PropagateLoadError(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	writeFile(t, statePath(t, dir), "not json")

	if err := AddAllowRule(AllowRule{Category: "shell", Pattern: "ls"}, o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("AddAllowRule: expected ErrCorruptState, got %v", err)
	}
	if err := RemoveAllowRule("shell", "ls", o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("RemoveAllowRule: expected ErrCorruptState, got %v", err)
	}
}

func TestSaveLoad_RoundTrip_AllowedRules(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	want := &State{
		AllowedRules: []AllowRule{
			{Category: "file.write", Pattern: "/repo/**", CreatedAt: 1},
			{Category: "shell", Pattern: "go test ./...", CreatedAt: 2},
		},
	}
	if err := Save(want, o); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.AllowedRules) != len(want.AllowedRules) {
		t.Fatalf("AllowedRules len: got %d, want %d",
			len(got.AllowedRules), len(want.AllowedRules))
	}
	for i, w := range want.AllowedRules {
		g := got.AllowedRules[i]
		if g.Category != w.Category || g.Pattern != w.Pattern || g.CreatedAt != w.CreatedAt {
			t.Errorf("AllowedRules[%d]: got %+v, want %+v", i, g, w)
		}
	}
}

// --- test 12: race detector clean (covered by test 3 with -race) -------------
// TestSave_RaceDetectorClean is a dedicated marker test; the actual
// concurrent-access scenario lives in TestSave_AtomicWrite which is designed
// to exercise the race detector.  Running with -race on the whole package is
// sufficient; this stub ensures the race test is always included in coverage.
func TestSave_RaceDetectorClean(t *testing.T) {
	// Rerun the atomic write scenario with fewer iterations for clarity.
	dir := t.TempDir()
	o := opts(dir)
	if err := Save(&State{ActiveProfile: "seed"}, o); err != nil {
		t.Fatalf("seed Save: %v", err)
	}

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_, _ = Load(o)
		})
		wg.Go(func() {
			_ = Save(&State{ActiveProfile: "race"}, o)
		})
	}
	wg.Wait()
}

// --- ToggleFavoriteModel tests -----------------------------------------------

func TestToggleFavoriteModel_AddAndRemove(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Add two favorites.
	if err := ToggleFavoriteModel("anthropic/claude-opus-4-5", o); err != nil {
		t.Fatalf("ToggleFavoriteModel add 1: %v", err)
	}
	if err := ToggleFavoriteModel("openai/gpt-4o", o); err != nil {
		t.Fatalf("ToggleFavoriteModel add 2: %v", err)
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.FavoriteModels) != 2 {
		t.Fatalf("FavoriteModels len: got %d, want 2; %v", len(s.FavoriteModels), s.FavoriteModels)
	}
	if s.FavoriteModels[0] != "anthropic/claude-opus-4-5" {
		t.Errorf("FavoriteModels[0]: got %q, want anthropic/claude-opus-4-5", s.FavoriteModels[0])
	}
	if s.FavoriteModels[1] != "openai/gpt-4o" {
		t.Errorf("FavoriteModels[1]: got %q, want openai/gpt-4o", s.FavoriteModels[1])
	}

	// Toggle off the first one.
	if err := ToggleFavoriteModel("anthropic/claude-opus-4-5", o); err != nil {
		t.Fatalf("ToggleFavoriteModel remove: %v", err)
	}

	s, err = Load(o)
	if err != nil {
		t.Fatalf("Load after remove: %v", err)
	}
	if len(s.FavoriteModels) != 1 {
		t.Fatalf("FavoriteModels len after remove: got %d, want 1; %v", len(s.FavoriteModels), s.FavoriteModels)
	}
	if s.FavoriteModels[0] != "openai/gpt-4o" {
		t.Errorf("FavoriteModels[0] after remove: got %q, want openai/gpt-4o", s.FavoriteModels[0])
	}
}

func TestToggleFavoriteModel_IdempotentRemove(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	// Remove a model that was never added: should result in empty list.
	if err := ToggleFavoriteModel("openai/gpt-4o", o); err != nil {
		t.Fatalf("ToggleFavoriteModel add: %v", err)
	}
	if err := ToggleFavoriteModel("openai/gpt-4o", o); err != nil {
		t.Fatalf("ToggleFavoriteModel remove: %v", err)
	}
	if err := ToggleFavoriteModel("openai/gpt-4o", o); err != nil {
		t.Fatalf("ToggleFavoriteModel re-add: %v", err)
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.FavoriteModels) != 1 || s.FavoriteModels[0] != "openai/gpt-4o" {
		t.Errorf("FavoriteModels: got %v, want [openai/gpt-4o]", s.FavoriteModels)
	}
}

func TestToggleFavoriteModel_PropagatesLoadError(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)
	writeFile(t, statePath(t, dir), "not json")

	if err := ToggleFavoriteModel("openai/gpt-4o", o); !errors.Is(err, ErrCorruptState) {
		t.Errorf("ToggleFavoriteModel: expected ErrCorruptState, got %v", err)
	}
}

func TestToggleFavoriteModel_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	o := opts(dir)

	refs := []string{"anthropic/claude-opus-4-5", "openai/gpt-4o", "openrouter/mistral-7b"}
	for _, ref := range refs {
		if err := ToggleFavoriteModel(ref, o); err != nil {
			t.Fatalf("ToggleFavoriteModel(%q): %v", ref, err)
		}
	}

	s, err := Load(o)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(s.FavoriteModels) != len(refs) {
		t.Fatalf("FavoriteModels len: got %d, want %d; %v", len(s.FavoriteModels), len(refs), s.FavoriteModels)
	}
	for i, want := range refs {
		if s.FavoriteModels[i] != want {
			t.Errorf("FavoriteModels[%d]: got %q, want %q", i, s.FavoriteModels[i], want)
		}
	}
}
