package skill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile_ValidMinimal(t *testing.T) {
	sk, err := ParseFile(filepath.Join("testdata", "valid-minimal.md"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if sk.Name != "valid-minimal" {
		t.Errorf("Name = %q, want valid-minimal", sk.Name)
	}
	if sk.Description == "" {
		t.Error("Description empty")
	}
	if sk.WhenToUse == "" {
		t.Error("WhenToUse empty")
	}
	if sk.Body == "" {
		t.Error("Body empty; want trimmed markdown")
	}
	if len(sk.Extras) != 0 {
		t.Errorf("Extras non-empty: %v", sk.Extras)
	}
	if sk.LoadedAt.IsZero() {
		t.Error("LoadedAt not set")
	}
}

func TestParseFile_ValidFull(t *testing.T) {
	sk, err := ParseFile(filepath.Join("testdata", "valid-full.md"))
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if sk.Name != "valid-full" {
		t.Errorf("Name = %q", sk.Name)
	}
	if sk.Extras["owner"] != "platform-team" {
		t.Errorf("Extras[owner] = %q, want platform-team", sk.Extras["owner"])
	}
	if sk.Extras["version"] != "2" {
		t.Errorf("Extras[version] = %q, want 2 (quotes stripped)", sk.Extras["version"])
	}
}

func TestParseFile_BadFrontmatter_MissingClose(t *testing.T) {
	_, err := ParseFile(filepath.Join("testdata", "bad-frontmatter.md"))
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var pe *ParseError
	if !asParseError(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if pe.Reason == "" {
		t.Error("ParseError.Reason empty")
	}
}

func TestParseFile_NoFrontmatter(t *testing.T) {
	_, err := ParseFile(filepath.Join("testdata", "no-frontmatter.md"))
	if !errors.Is(err, ErrNoFrontmatter) {
		t.Fatalf("err = %v, want ErrNoFrontmatter", err)
	}
}

func TestParseFile_StemMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "renamed.md")
	body := "---\nname: original\ndescription: x\nwhen_to_use: y\n---\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := ParseFile(path)
	var pe *ParseError
	if !asParseError(err, &pe) {
		t.Fatalf("expected *ParseError, got %T: %v", err, err)
	}
	if !contains(pe.Reason, "filename stem") {
		t.Errorf("Reason = %q; want mention of filename stem", pe.Reason)
	}
}

func TestParseFile_InvalidName(t *testing.T) {
	cases := []string{
		"Refactor",        // uppercase
		"with spaces",     // space
		"with/slash",      // slash
		"-leading-dash",   // does not start with [a-z]
		"name_underscore", // underscore not in regex
		"",                // empty (handled separately by required check)
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// Use a known-valid stem so the test isolates the regex check.
			path := filepath.Join(dir, "fixture.md")
			body := "---\nname: " + name + "\ndescription: x\nwhen_to_use: y\n---\nbody\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ParseFile(path)
			if err == nil {
				t.Fatalf("expected error for name %q", name)
			}
		})
	}
}

func TestParseFile_MissingRequiredKeys(t *testing.T) {
	cases := map[string]string{
		"missing description": "---\nname: ok\nwhen_to_use: y\n---\nbody\n",
		"missing when_to_use": "---\nname: ok\ndescription: x\n---\nbody\n",
		"missing name":        "---\ndescription: x\nwhen_to_use: y\n---\nbody\n",
	}
	for label, body := range cases {
		t.Run(label, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "ok.md")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ParseFile(path); err == nil {
				t.Fatalf("expected error for %s", label)
			}
		})
	}
}

func TestParseFile_CommentLinesIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.md")
	body := "---\n# this is a comment\nname: ok\ndescription: x\nwhen_to_use: y\n---\nbody\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sk, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Name != "ok" {
		t.Errorf("Name = %q", sk.Name)
	}
}

func TestParseFile_QuotedValuesStripped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.md")
	body := "---\nname: ok\ndescription: \"quoted desc\"\nwhen_to_use: 'quoted when'\n---\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	sk, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sk.Description != "quoted desc" {
		t.Errorf("Description = %q", sk.Description)
	}
	if sk.WhenToUse != "quoted when" {
		t.Errorf("WhenToUse = %q", sk.WhenToUse)
	}
}

// --- helpers ---

func asParseError(err error, out **ParseError) bool {
	if err == nil {
		return false
	}
	var pe *ParseError
	if !errors.As(err, &pe) {
		return false
	}
	*out = pe
	return true
}

func contains(haystack, needle string) bool {
	return needle == "" || (len(haystack) >= len(needle) && stringIndex(haystack, needle) >= 0)
}

func stringIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
