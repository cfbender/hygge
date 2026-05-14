package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
)

func setupGlobTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := []struct {
		path string
		when time.Time
	}{
		{"a.go", time.Now().Add(-3 * time.Hour)},
		{"b.go", time.Now().Add(-1 * time.Hour)},
		{"sub/c.go", time.Now().Add(-2 * time.Hour)},
		{"readme.md", time.Now().Add(-4 * time.Hour)},
		{"node_modules/skip.go", time.Now()},
		{".git/HEAD", time.Now()},
	}
	for _, f := range files {
		full := filepath.Join(dir, f.path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte("x\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		if err := os.Chtimes(full, f.when, f.when); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	return dir
}

func TestGlob_HappyPath(t *testing.T) {
	dir := setupGlobTree(t)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGlobTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"**/*.go"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	// Should match a.go, b.go, sub/c.go but not node_modules/skip.go.
	for _, want := range []string{"a.go", "b.go", "c.go"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("missing %s in %q", want, res.Content)
		}
	}
	if strings.Contains(res.Content, "node_modules") {
		t.Errorf("node_modules should be excluded: %q", res.Content)
	}
	if strings.Contains(res.Content, ".git") {
		t.Errorf(".git should be excluded: %q", res.Content)
	}
	// Newest first: b.go (-1h), c.go (-2h), a.go (-3h).
	lines := strings.Split(strings.TrimSpace(res.Content), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines: got %d, want 3 (%v)", len(lines), lines)
	}
	if !strings.HasSuffix(lines[0], "b.go") {
		t.Errorf("expected b.go first, got %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], "c.go") {
		t.Errorf("expected c.go second, got %q", lines[1])
	}
	if !strings.HasSuffix(lines[2], "a.go") {
		t.Errorf("expected a.go third, got %q", lines[2])
	}
}

func TestGlob_NoMatches(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGlobTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"**/*.zz"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError on empty: %+v", res)
	}
	if !strings.Contains(res.Content, "no matches") {
		t.Errorf("Content: %q", res.Content)
	}
}

func TestGlob_InvalidPattern(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGlobTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"[unclosed"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for invalid pattern")
	}
}

func TestGlob_InvalidArgs(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGlobTool()

	cases := []string{`{}`, `{"pattern":""}`, `{"pattern":"x","unknown":1}`, `not json`}
	for _, args := range cases {
		_, err := gt.Execute(context.Background(), json.RawMessage(args), ec)
		if err == nil {
			t.Errorf("expected error for %s", args)
			continue
		}
		var te *ToolError
		if !errors.As(err, &te) || te.Code != CodeInvalidArgs {
			t.Errorf("want invalid_args for %s, got %v", args, err)
		}
	}
}

func TestGlob_PermissionDenied(t *testing.T) {
	dir := setupGlobTree(t)
	outside := t.TempDir()
	e, b := builtinTestEngine(t, denyAll)
	ec := newExecContext(b, e, dir)
	gt := newGlobTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"**/*","path":"`+outside+`"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for deny")
	}
}

func TestGlob_PermissionTarget(t *testing.T) {
	dir := setupGlobTree(t)
	rec := newRecordingResponder(bus.PermissionReplied{Decision: "allow", Scope: "once"})
	e, b := builtinTestEngine(t, rec.decide)
	ec := newExecContext(b, e, dir)
	outside := t.TempDir()
	gt := newGlobTool()

	if _, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"*.go","path":"`+outside+`"}`), ec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := rec.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("requests: %d", len(reqs))
	}
	if reqs[0].Category != "file.read" {
		t.Errorf("Category: %q", reqs[0].Category)
	}
	if reqs[0].Target != filepath.Clean(outside) {
		t.Errorf("Target: got %q want %q", reqs[0].Target, outside)
	}
	if reqs[0].ToolName != "glob" {
		t.Errorf("ToolName: %q", reqs[0].ToolName)
	}
}

func TestGlob_TruncateMaxResults(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		full := filepath.Join(dir, "f"+itoa(i)+".txt")
		_ = os.WriteFile(full, []byte("x"), 0o644)
	}
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	gt := newGlobTool()

	res, err := gt.Execute(context.Background(),
		json.RawMessage(`{"pattern":"*.txt","max_results":3}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Metadata["matches"].(int) != 3 {
		t.Errorf("matches: %v", res.Metadata["matches"])
	}
	if !res.Metadata["truncated"].(bool) {
		t.Errorf("truncated: want true")
	}
}
