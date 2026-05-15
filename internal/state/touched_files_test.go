package state

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func TestTouchedFiles_AddAndList(t *testing.T) {
	tf := NewTouchedFiles()
	if got := tf.List(); len(got) != 0 {
		t.Fatalf("new tracker should be empty; got %v", got)
	}

	tf.Add("/abs/path/a.go", "")
	tf.Add("/abs/path/b.go", "")
	tf.Add("/abs/path/a.go", "") // duplicate

	got := tf.List()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if got[0] != "/abs/path/a.go" || got[1] != "/abs/path/b.go" {
		t.Errorf("unexpected order or values: %v", got)
	}
}

func TestTouchedFiles_RelativePathResolved(t *testing.T) {
	tf := NewTouchedFiles()
	tf.Add("relative/file.go", "/home/user/proj")

	got := tf.List()
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	want := "/home/user/proj/relative/file.go"
	if got[0] != want {
		t.Errorf("got %q, want %q", got[0], want)
	}
}

func TestTouchedFiles_EmptyPathIgnored(t *testing.T) {
	tf := NewTouchedFiles()
	tf.Add("", "/home/user/proj")
	tf.Add(".", "/home/user/proj")
	if got := tf.List(); len(got) != 0 {
		t.Fatalf("empty/dot paths should be ignored; got %v", got)
	}
}

func TestTouchedFiles_RelativeNoProjectDir(t *testing.T) {
	tf := NewTouchedFiles()
	tf.Add("relative/file.go", "") // no projectDir → ignored
	if got := tf.List(); len(got) != 0 {
		t.Fatalf("relative path without projectDir should be ignored; got %v", got)
	}
}

func TestTouchedFiles_Len(t *testing.T) {
	tf := NewTouchedFiles()
	if tf.Len() != 0 {
		t.Fatal("new tracker should have Len==0")
	}
	tf.Add("/a.go", "")
	tf.Add("/b.go", "")
	if tf.Len() != 2 {
		t.Fatalf("expected Len==2, got %d", tf.Len())
	}
}

func TestTouchedFiles_ListIsSorted(t *testing.T) {
	tf := NewTouchedFiles()
	paths := []string{"/z.go", "/a.go", "/m.go"}
	for _, p := range paths {
		tf.Add(p, "")
	}
	got := tf.List()
	want := make([]string, len(paths))
	copy(want, paths)
	sort.Strings(want)
	for i, g := range got {
		if g != want[i] {
			t.Errorf("index %d: got %q, want %q", i, g, want[i])
		}
	}
}

// TestParseNumstat tests the internal numstat parser directly.
func TestParseNumstat(t *testing.T) {
	projectDir := "/home/user/proj"
	input := []byte("12\t3\tinternal/foo.go\n0\t7\tREADME.md\n-\t-\tbinary.bin\n")

	got := parseNumstat(input, projectDir)
	if len(got) != 3 {
		t.Fatalf("expected 3 entries, got %d: %v", len(got), got)
	}

	fooKey := projectDir + "/internal/foo.go"
	readmeKey := projectDir + "/README.md"
	binKey := projectDir + "/binary.bin"

	checkEntry := func(key string, wantAdded, wantDeleted int) {
		t.Helper()
		ns, ok := got[key]
		if !ok {
			t.Errorf("key %q not found in result", key)
			return
		}
		if ns.Added != wantAdded {
			t.Errorf("%q: added=%d, want %d", key, ns.Added, wantAdded)
		}
		if ns.Deleted != wantDeleted {
			t.Errorf("%q: deleted=%d, want %d", key, ns.Deleted, wantDeleted)
		}
	}
	checkEntry(fooKey, 12, 3)
	checkEntry(readmeKey, 0, 7)
	checkEntry(binKey, 0, 0) // binary → 0/0
}

func TestParseNumstat_EmptyInput(t *testing.T) {
	got := parseNumstat([]byte(""), "/proj")
	if len(got) != 0 {
		t.Fatalf("expected empty map; got %v", got)
	}
}

func TestNumstatForFiles_RealGit(t *testing.T) {
	// This test uses a real `git init` to exercise the full code path
	// including the safety env vars.  If git is not available, skip.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available:", err)
	}
	_ = gitPath

	dir := t.TempDir()

	runCmd := func(label string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...) //nolint:gosec
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("%s: git %v failed: %v\n%s", label, args, err, out)
		}
	}

	runCmd("init", "init")
	runCmd("config-email", "config", "user.email", "test@test.invalid")
	runCmd("config-name", "config", "user.name", "Test")

	filePath := filepath.Join(dir, "hello.go")
	if err := os.WriteFile(filePath, []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	runCmd("add", "add", "hello.go")
	runCmd("commit", "commit", "-m", "initial")

	// Modify so there is a diff against HEAD.
	if err := os.WriteFile(filePath, []byte("package main\nimport \"fmt\"\nfunc main() { fmt.Println() }\n"), 0o644); err != nil {
		t.Fatalf("modify file: %v", err)
	}

	got := NumstatForFiles(t.Context(), dir, []string{filePath})
	ns, ok := got[filePath]
	if !ok {
		t.Fatalf("expected entry for %q; map=%v", filePath, got)
	}
	if ns.Added < 1 {
		t.Errorf("expected added>=1, got %d", ns.Added)
	}
}
