package procenv

import (
	"slices"
	"strings"
	"testing"
)

func envMap(t *testing.T, pairs []string) map[string]string {
	t.Helper()
	out := make(map[string]string, len(pairs))
	for _, kv := range pairs {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("malformed pair %q", kv)
		}
		out[k] = v
	}
	return out
}

func TestFiltered_OnlyAllowlistedVars(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/test")
	t.Setenv("HYGGE_PROCENV_TEST_LEAK", "leaky")

	got := envMap(t, Filtered())
	if _, leaked := got["HYGGE_PROCENV_TEST_LEAK"]; leaked {
		t.Fatalf("non-allowlisted var leaked: %v", got)
	}
	if got["PATH"] != "/usr/bin:/bin" {
		t.Fatalf("PATH not forwarded: %q", got["PATH"])
	}
	if got["HOME"] != "/home/test" {
		t.Fatalf("HOME not forwarded: %q", got["HOME"])
	}
	for k := range got {
		if !slices.Contains(Allowlist[:], k) {
			t.Fatalf("unexpected key %q in filtered env", k)
		}
	}
}

func TestMerged_ExtraOverridesAndAdds(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HYGGE_PROCENV_TEST_LEAK", "leaky")

	got := envMap(t, Merged(map[string]string{
		"GITHUB_TOKEN": "abc",
		"PATH":         "/override",
	}))
	if _, leaked := got["HYGGE_PROCENV_TEST_LEAK"]; leaked {
		t.Fatalf("non-allowlisted var leaked: %v", got)
	}
	if got["GITHUB_TOKEN"] != "abc" {
		t.Fatalf("extra var missing or wrong: %q", got["GITHUB_TOKEN"])
	}
	if got["PATH"] != "/override" {
		t.Fatalf("extra did not override allowlisted var: %q", got["PATH"])
	}
}

func TestMerged_Sorted(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("HOME", "/home/test")
	pairs := Merged(map[string]string{"AAA": "1", "ZZZ": "2"})
	if !slices.IsSorted(pairs) {
		t.Fatalf("merged env not sorted: %v", pairs)
	}
}

func TestLimitedBuffer_UnderLimit(t *testing.T) {
	t.Parallel()
	b := &LimitedBuffer{Max: 16}
	n, err := b.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = (%d, %v), want (5, nil)", n, err)
	}
	if b.Truncated() {
		t.Fatal("under-limit write marked truncated")
	}
	if b.String() != "hello" {
		t.Fatalf("String = %q", b.String())
	}
	if b.Len() != 5 {
		t.Fatalf("Len = %d", b.Len())
	}
}

func TestLimitedBuffer_ExactlyAtLimit(t *testing.T) {
	t.Parallel()
	b := &LimitedBuffer{Max: 5}
	if _, err := b.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if b.Truncated() {
		t.Fatal("at-limit write marked truncated")
	}
	if b.String() != "hello" {
		t.Fatalf("String = %q", b.String())
	}
}

func TestLimitedBuffer_OverLimit(t *testing.T) {
	t.Parallel()
	b := &LimitedBuffer{Max: 4}
	n, err := b.Write([]byte("hello"))
	if err != nil || n != 5 {
		t.Fatalf("Write = (%d, %v), want full len reported", n, err)
	}
	if !b.Truncated() {
		t.Fatal("over-limit write not marked truncated")
	}
	want := "hell\n" + TruncationMarker
	if b.String() != want {
		t.Fatalf("String = %q, want %q", b.String(), want)
	}
	if b.Len() != 4 {
		t.Fatalf("Len = %d, want 4", b.Len())
	}
}

func TestLimitedBuffer_MultiWriteSpanningBoundary(t *testing.T) {
	t.Parallel()
	b := &LimitedBuffer{Max: 8}
	for _, chunk := range []string{"abc", "def", "ghi"} {
		n, err := b.Write([]byte(chunk))
		if err != nil || n != len(chunk) {
			t.Fatalf("Write(%q) = (%d, %v)", chunk, n, err)
		}
	}
	if !b.Truncated() {
		t.Fatal("boundary-spanning writes not marked truncated")
	}
	want := "abcdefgh\n" + TruncationMarker
	if b.String() != want {
		t.Fatalf("String = %q, want %q", b.String(), want)
	}

	// Writes after the cap are discarded but still report success.
	n, err := b.Write([]byte("more"))
	if err != nil || n != 4 {
		t.Fatalf("post-cap Write = (%d, %v)", n, err)
	}
	if b.Len() != 8 {
		t.Fatalf("Len = %d, want 8", b.Len())
	}
}

func TestLimitedBuffer_ZeroMaxDefaults(t *testing.T) {
	t.Parallel()
	var b LimitedBuffer
	payload := make([]byte, MaxOutputBytes+1)
	if _, err := b.Write(payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !b.Truncated() {
		t.Fatal("write past default cap not marked truncated")
	}
	if b.Len() != MaxOutputBytes {
		t.Fatalf("Len = %d, want %d", b.Len(), MaxOutputBytes)
	}
}
