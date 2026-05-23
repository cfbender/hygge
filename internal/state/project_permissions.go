package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// projectPermissions is the schema for the project-scoped permissions file.
// It is stored at <projectDir>/.hygge/permissions.json and holds only the
// "always-allow" rules that the user approved while working in that project.
// This type is intentionally separate from State so the two files remain
// independent and project permissions are self-contained.
//
// Like State, this file is strict JSON: empty, malformed, or newer-schema
// files fail to load so unsafe allow-rules are not silently misread. Writes are
// atomic but not locked; concurrent read/modify/write callers may overwrite
// each other's additions in overlapping windows.
type projectPermissions struct {
	AllowedRules []AllowRule `json:"allowed_rules,omitempty"`
}

// ProjectPermissionsPath returns the path to the project-scoped permissions
// file for the given project directory.  The file need not exist.  Returns
// an error only when projectDir is empty.
func ProjectPermissionsPath(projectDir string) (string, error) {
	if projectDir == "" {
		return "", errors.New("state: ProjectPermissionsPath: projectDir is empty")
	}
	return filepath.Join(projectDir, ".hygge", "permissions.json"), nil
}

// LoadProjectAllowRules reads the project-scoped allow rules from
// <projectDir>/.hygge/permissions.json.  If the file does not exist, an
// empty slice is returned with a nil error.  Corrupt files return
// [ErrCorruptState].
func LoadProjectAllowRules(projectDir string) ([]AllowRule, error) {
	path, err := ProjectPermissionsPath(projectDir)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path) //nolint:gosec // path derived from project dir
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("state: read project permissions %s: %w", path, err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty file at %s", ErrCorruptState, path)
	}

	var pp projectPermissions
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if decErr := dec.Decode(&pp); decErr != nil {
		return nil, fmt.Errorf("%w: %s: %s", ErrCorruptState, path, decErr.Error())
	}

	return pp.AllowedRules, nil
}

// AddProjectAllowRule appends rule to the project-scoped allow rules at
// <projectDir>/.hygge/permissions.json, persisting atomically.  If a rule
// with the same Category and Pattern already exists, it is left in place
// and the call is a no-op.  The CreatedAt field is set from time.Now if
// zero.
func AddProjectAllowRule(rule AllowRule, projectDir string) error {
	path, err := ProjectPermissionsPath(projectDir)
	if err != nil {
		return err
	}

	rules, err := LoadProjectAllowRules(projectDir)
	if err != nil {
		return fmt.Errorf("state: AddProjectAllowRule: %w", err)
	}

	for _, existing := range rules {
		if existing.Category == rule.Category && existing.Pattern == rule.Pattern {
			return nil // already present; no-op
		}
	}

	if rule.CreatedAt == 0 {
		rule.CreatedAt = time.Now().UnixMilli()
	}
	rules = append(rules, rule)

	return saveProjectPermissions(path, projectPermissions{AllowedRules: rules})
}

// saveProjectPermissions writes pp to path atomically, creating the parent
// directory with mode 0o700 as needed.  The file is written with mode 0o600.
func saveProjectPermissions(path string, pp projectPermissions) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("state: create project permissions dir %s: %w", dir, err)
	}

	data, err := json.MarshalIndent(pp, "", "  ")
	if err != nil {
		return fmt.Errorf("state: marshal project permissions: %w", err)
	}
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
		return fmt.Errorf("state: write project permissions tmp: %w", writeErr)
	}
	if syncErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: sync project permissions tmp: %w", syncErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: close project permissions tmp: %w", closeErr)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("state: rename %s -> %s: %w", tmp, path, err)
	}

	return nil
}
