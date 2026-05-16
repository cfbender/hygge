package state

import (
	"context"
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

func TestNumstatForFiles_UsesInjectedGitRunner(t *testing.T) {
	runner := &fakeGitRunner{out: []byte("2\t1\thello.go\n")}
	got := NumstatForFilesWithGitRunner(t.Context(), "/repo", []string{"/repo/hello.go"}, runner)
	ns, ok := got["/repo/hello.go"]
	if !ok {
		t.Fatalf("expected entry for hello.go; map=%v", got)
	}
	if ns.Added != 2 || ns.Deleted != 1 {
		t.Fatalf("numstat = %+v", ns)
	}
	if runner.dir != "/repo" {
		t.Fatalf("runner dir = %q", runner.dir)
	}
	wantArgs := []string{"diff", "--numstat", "HEAD", "--", "/repo/hello.go"}
	if len(runner.args) != len(wantArgs) {
		t.Fatalf("args len = %d, want %d: %v", len(runner.args), len(wantArgs), runner.args)
	}
	for i := range wantArgs {
		if runner.args[i] != wantArgs[i] {
			t.Fatalf("args[%d] = %q, want %q; args=%v", i, runner.args[i], wantArgs[i], runner.args)
		}
	}
}

type fakeGitRunner struct {
	dir  string
	args []string
	out  []byte
	err  error
}

func (f *fakeGitRunner) Run(_ context.Context, dir string, args ...string) ([]byte, error) {
	f.dir = dir
	f.args = append([]string(nil), args...)
	return append([]byte(nil), f.out...), f.err
}
