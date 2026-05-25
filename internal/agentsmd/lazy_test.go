package agentsmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mkFile is a tiny helper for table-driven setup.
func mkFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestLazyTracker_DisabledWhenRootEmpty verifies the tracker no-ops
// when no project root could be discovered.
func TestLazyTracker_DisabledWhenRootEmpty(t *testing.T) {
	tr := NewLazyTracker("", "", nil)
	got := tr.Touch("/tmp", []string{"foo.txt"})
	if got != nil {
		t.Fatalf("want nil for empty projectRoot, got %+v", got)
	}
}

// TestLazyTracker_WalksUpAndLoadsSubdirContext verifies the basic
// happy path: tool touches a path inside a subdirectory that has its
// own AGENTS.md, and the tracker returns it as a SourceProjectSubdir
// block.
func TestLazyTracker_WalksUpAndLoadsSubdirContext(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg", "sub")
	mkFile(t, filepath.Join(sub, "AGENTS.md"), "sub rules")
	mkFile(t, filepath.Join(sub, "code.go"), "package sub")

	tr := NewLazyTracker("", root, nil)
	blocks := tr.Touch(root, []string{filepath.Join(sub, "code.go")})
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d: %+v", len(blocks), blocks)
	}
	b := blocks[0]
	if b.Source != SourceProjectSubdir {
		t.Fatalf("want SourceProjectSubdir, got %v", b.Source)
	}
	if b.Content != "sub rules" {
		t.Fatalf("unexpected content: %q", b.Content)
	}
	if b.RelPath != filepath.Join("pkg", "sub", "AGENTS.md") {
		t.Fatalf("unexpected RelPath: %q", b.RelPath)
	}
}

// TestLazyTracker_AgentsTakesPrecedenceOverClaude verifies that when
// AGENTS.md and CLAUDE.md exist in the same subdirectory, only AGENTS.md
// is surfaced.
func TestLazyTracker_AgentsTakesPrecedenceOverClaude(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "svc")
	mkFile(t, filepath.Join(sub, "AGENTS.md"), "a")
	mkFile(t, filepath.Join(sub, "CLAUDE.md"), "c")
	mkFile(t, filepath.Join(sub, "CLAUDE.local.md"), "cl")

	tr := NewLazyTracker("", root, nil)
	blocks := tr.Touch(root, []string{sub})
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d: %+v", len(blocks), blocks)
	}
	if !strings.HasSuffix(blocks[0].Path, "AGENTS.md") {
		t.Fatalf("want AGENTS.md, got %q", blocks[0].Path)
	}
}

// TestLazyTracker_NoDoubleInjection verifies that touching the same
// directory twice only loads its context the first time.
func TestLazyTracker_NoDoubleInjection(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	mkFile(t, filepath.Join(sub, "AGENTS.md"), "pkg rules")

	tr := NewLazyTracker("", root, nil)
	first := tr.Touch(root, []string{sub})
	if len(first) != 1 {
		t.Fatalf("first Touch: want 1 block, got %d", len(first))
	}
	second := tr.Touch(root, []string{sub})
	if second != nil {
		t.Fatalf("second Touch: want nil, got %+v", second)
	}
}

// TestLazyTracker_BootstrapSeedSkipsKnownDirs verifies the seed of
// already-loaded directories prevents re-injection.
func TestLazyTracker_BootstrapSeedSkipsKnownDirs(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "AGENTS.md"), "root rules")
	sub := filepath.Join(root, "pkg")
	mkFile(t, filepath.Join(sub, "AGENTS.md"), "pkg rules")

	// Seed root as already-seen.  Touching inside sub should still
	// surface sub's AGENTS.md but not the root's.
	tr := NewLazyTracker("", root, []string{root})
	blocks := tr.Touch(root, []string{sub})
	if len(blocks) != 1 {
		t.Fatalf("want 1 block, got %d: %+v", len(blocks), blocks)
	}
	if !strings.Contains(blocks[0].Path, filepath.Join("pkg", "AGENTS.md")) {
		t.Fatalf("want pkg/AGENTS.md, got %q", blocks[0].Path)
	}
}

// TestLazyTracker_ExcludeDirsSkipped verifies LazyExcludeDirs entries
// are walked through transparently — their AGENTS.md is ignored but
// the walk continues upward.
func TestLazyTracker_ExcludeDirsSkipped(t *testing.T) {
	root := t.TempDir()
	mkFile(t, filepath.Join(root, "AGENTS.md"), "root rules")
	// node_modules is in LazyExcludeDirs.  An AGENTS.md inside it
	// must not be loaded; the walk should continue to root.
	excluded := filepath.Join(root, "node_modules", "lib")
	mkFile(t, filepath.Join(excluded, "AGENTS.md"), "vendor noise")

	tr := NewLazyTracker("", root, nil)
	blocks := tr.Touch(root, []string{excluded})
	for _, b := range blocks {
		if strings.Contains(b.Path, "node_modules") {
			t.Fatalf("excluded dir leaked: %+v", b)
		}
	}
	// And the root's AGENTS.md SHOULD be loaded.
	if len(blocks) != 1 {
		t.Fatalf("want 1 block (root only), got %d: %+v", len(blocks), blocks)
	}
}

// TestLazyTracker_FileCap verifies the tracker stops loading once
// MaxLazyContextFiles is reached.
func TestLazyTracker_FileCap(t *testing.T) {
	root := t.TempDir()
	// Build MaxLazyContextFiles directories each with an AGENTS.md,
	// plus one extra that should be skipped.
	var touches []string
	for i := 0; i <= MaxLazyContextFiles; i++ {
		d := filepath.Join(root, "d", "n", "x") // shared parents inflate seenDirs cheaply
		d = filepath.Join(d, "i", "z", "p", "q")
		// Append a unique leaf per i.
		leaf := filepath.Join(d, "leaf")
		// Use a unique tail directory.
		tail := filepath.Join(d, "leaf-"+itoa(i))
		mkFile(t, filepath.Join(tail, "AGENTS.md"), "x")
		touches = append(touches, tail)
		_ = leaf
	}

	tr := NewLazyTracker("", root, nil)
	got := tr.Touch(root, touches)
	if len(got) > MaxLazyContextFiles {
		t.Fatalf("want <= %d blocks, got %d", MaxLazyContextFiles, len(got))
	}
	// Subsequent Touch should return nil after the cap fires.
	more := tr.Touch(root, touches)
	if more != nil {
		t.Fatalf("want nil after cap, got %+v", more)
	}
}

// TestLazyTracker_ByteCap verifies the byte cap fires before the file
// cap when a single oversize file would push the byte total over the
// limit.
func TestLazyTracker_ByteCap(t *testing.T) {
	root := t.TempDir()
	huge := strings.Repeat("x", MaxLazyContextBytes+1)
	sub := filepath.Join(root, "big")
	mkFile(t, filepath.Join(sub, "AGENTS.md"), huge)

	tr := NewLazyTracker("", root, nil)
	got := tr.Touch(root, []string{sub})
	if len(got) != 0 {
		t.Fatalf("want zero blocks (oversize skipped), got %d", len(got))
	}
	more := tr.Touch(root, []string{sub})
	if more != nil {
		t.Fatalf("want nil after cap, got %+v", more)
	}
}

// TestLazyTracker_TouchedOutsideRoot verifies paths outside the
// project root contribute nothing.
func TestLazyTracker_TouchedOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	mkFile(t, filepath.Join(outside, "AGENTS.md"), "stranger")

	tr := NewLazyTracker("", root, nil)
	got := tr.Touch(root, []string{filepath.Join(outside, "file.txt")})
	if got != nil {
		t.Fatalf("want nil for outside-root touch, got %+v", got)
	}
}

// TestLazyTracker_RelativePathResolvedAgainstPwd verifies relative
// paths use pwd as the resolution anchor.
func TestLazyTracker_RelativePathResolvedAgainstPwd(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "rel")
	mkFile(t, filepath.Join(sub, "AGENTS.md"), "rel rules")
	mkFile(t, filepath.Join(sub, "code.go"), "package rel")

	tr := NewLazyTracker("", root, nil)
	got := tr.Touch(root, []string{"rel/code.go"})
	if len(got) != 1 {
		t.Fatalf("want 1 block, got %d: %+v", len(got), got)
	}
}

// TestLazyTracker_AgentsLocalNotLoadedInSubdir verifies AGENTS.local.md
// in a subdirectory is intentionally ignored.
func TestLazyTracker_AgentsLocalNotLoadedInSubdir(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "pkg")
	mkFile(t, filepath.Join(sub, "AGENTS.local.md"), "machine-specific")

	tr := NewLazyTracker("", root, nil)
	got := tr.Touch(root, []string{sub})
	if got != nil {
		t.Fatalf("want nil (AGENTS.local.md ignored in subdir), got %+v", got)
	}
}

// TestBuildLazyAddition_Empty verifies the formatter returns "" for
// no blocks.
func TestBuildLazyAddition_Empty(t *testing.T) {
	if got := BuildLazyAddition(nil); got != "" {
		t.Fatalf("want empty string, got %q", got)
	}
}

// TestBuildLazyAddition_HeaderAndContent verifies the lazy header is
// used and content is rendered.
func TestBuildLazyAddition_HeaderAndContent(t *testing.T) {
	blocks := []Block{
		{Path: "/x/y/AGENTS.md", RelPath: "y/AGENTS.md", Source: SourceProjectSubdir, Content: "rules"},
	}
	got := BuildLazyAddition(blocks)
	if !strings.Contains(got, "## Additional project context (loaded for this turn)") {
		t.Fatalf("missing lazy header: %q", got)
	}
	if !strings.Contains(got, "rules") {
		t.Fatalf("missing content: %q", got)
	}
	if !strings.Contains(got, "project/subdir") {
		t.Fatalf("missing source token: %q", got)
	}
}

// itoa is a local intToString to avoid importing strconv in tests
// where we only need decimal int-to-string for unique leaf names.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
