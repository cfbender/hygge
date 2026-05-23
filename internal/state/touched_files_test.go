package state

import (
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
