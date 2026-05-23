package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// --- ProjectPermissionsPath --------------------------------------------------

func TestProjectPermissionsPath_ReturnsHyggePath(t *testing.T) {
	dir := t.TempDir()
	got, err := ProjectPermissionsPath(dir)
	if err != nil {
		t.Fatalf("ProjectPermissionsPath: %v", err)
	}
	want := filepath.Join(dir, ".hygge", "permissions.json")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestProjectPermissionsPath_EmptyDirErrors(t *testing.T) {
	_, err := ProjectPermissionsPath("")
	if err == nil {
		t.Fatal("expected error for empty projectDir, got nil")
	}
}

// --- LoadProjectAllowRules ---------------------------------------------------

func TestLoadProjectAllowRules_MissingFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	rules, err := LoadProjectAllowRules(dir)
	if err != nil {
		t.Fatalf("LoadProjectAllowRules on missing file: %v", err)
	}
	if len(rules) != 0 {
		t.Errorf("got %d rules, want 0", len(rules))
	}
}

func TestLoadProjectAllowRules_EmptyFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".hygge", "permissions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProjectAllowRules(dir)
	if !errors.Is(err, ErrCorruptState) {
		t.Errorf("got %v, want ErrCorruptState", err)
	}
}

func TestLoadProjectAllowRules_CorruptFileErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".hygge", "permissions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadProjectAllowRules(dir)
	if !errors.Is(err, ErrCorruptState) {
		t.Errorf("got %v, want ErrCorruptState", err)
	}
}

func TestLoadProjectAllowRules_EmptyProjectDir(t *testing.T) {
	_, err := LoadProjectAllowRules("")
	if err == nil {
		t.Fatal("expected error for empty projectDir, got nil")
	}
}

// --- AddProjectAllowRule -----------------------------------------------------

func TestAddProjectAllowRule_WritesToHyggeDir(t *testing.T) {
	dir := t.TempDir()

	rule := AllowRule{Category: "file.write", Pattern: "/repo/src/**"}
	if err := AddProjectAllowRule(rule, dir); err != nil {
		t.Fatalf("AddProjectAllowRule: %v", err)
	}

	// File must exist at <dir>/.hygge/permissions.json.
	path, _ := ProjectPermissionsPath(dir)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("permissions.json not created: %v", err)
	}

	rules, err := LoadProjectAllowRules(dir)
	if err != nil {
		t.Fatalf("LoadProjectAllowRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	got := rules[0]
	if got.Category != rule.Category || got.Pattern != rule.Pattern {
		t.Errorf("rule mismatch: got %+v, want %+v", got, rule)
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt: got 0, want non-zero timestamp")
	}
}

func TestAddProjectAllowRule_Idempotent(t *testing.T) {
	dir := t.TempDir()
	rule := AllowRule{Category: "shell", Pattern: "git log"}

	for range 3 {
		if err := AddProjectAllowRule(rule, dir); err != nil {
			t.Fatalf("AddProjectAllowRule: %v", err)
		}
	}

	rules, err := LoadProjectAllowRules(dir)
	if err != nil {
		t.Fatalf("LoadProjectAllowRules: %v", err)
	}
	if len(rules) != 1 {
		t.Errorf("got %d rules after 3 identical adds, want 1", len(rules))
	}
}

func TestAddProjectAllowRule_MultipleCategoriesAllStored(t *testing.T) {
	dir := t.TempDir()

	rules := []AllowRule{
		{Category: "file.write", Pattern: "/repo/**"},
		{Category: "file.read", Pattern: "/repo/**"},
		{Category: "shell", Pattern: "go test ./..."},
	}
	for _, r := range rules {
		if err := AddProjectAllowRule(r, dir); err != nil {
			t.Fatalf("AddProjectAllowRule(%+v): %v", r, err)
		}
	}

	got, err := LoadProjectAllowRules(dir)
	if err != nil {
		t.Fatalf("LoadProjectAllowRules: %v", err)
	}
	if len(got) != len(rules) {
		t.Fatalf("got %d rules, want %d", len(got), len(rules))
	}
}

func TestAddProjectAllowRule_PreservesExistingTimestamp(t *testing.T) {
	dir := t.TempDir()
	original := AllowRule{Category: "shell", Pattern: "git status", CreatedAt: 1234567890}
	if err := AddProjectAllowRule(original, dir); err != nil {
		t.Fatalf("AddProjectAllowRule: %v", err)
	}

	// Re-adding without CreatedAt must not overwrite.
	if err := AddProjectAllowRule(AllowRule{Category: "shell", Pattern: "git status"}, dir); err != nil {
		t.Fatalf("AddProjectAllowRule re-add: %v", err)
	}

	rules, err := LoadProjectAllowRules(dir)
	if err != nil {
		t.Fatalf("LoadProjectAllowRules: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("got %d rules, want 1", len(rules))
	}
	if rules[0].CreatedAt != 1234567890 {
		t.Errorf("CreatedAt mutated by re-add: got %d, want 1234567890", rules[0].CreatedAt)
	}
}

func TestAddProjectAllowRule_DoesNotTouchGlobalState(t *testing.T) {
	projectDir := t.TempDir()
	homeDir := t.TempDir() // separate home dir to represent global state location

	rule := AllowRule{Category: "file.write", Pattern: "/repo/**"}
	if err := AddProjectAllowRule(rule, projectDir); err != nil {
		t.Fatalf("AddProjectAllowRule: %v", err)
	}

	// The user-global state.json must not have been touched.
	globalStateOpts := LoadOptions{HomeDir: homeDir}
	globalStatePath, _ := Path(globalStateOpts)
	if _, err := os.Stat(globalStatePath); !os.IsNotExist(err) {
		t.Errorf("global state.json was touched; err = %v", err)
	}
}
