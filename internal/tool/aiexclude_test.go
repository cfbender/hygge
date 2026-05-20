package tool

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAIExcludeDeniesRead(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".aiexclude"), []byte("private.txt\n"), 0o600); err != nil {
		t.Fatalf("write .aiexclude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "private.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatalf("write private.txt: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	res, err := newReadTool(newReadTracker()).Execute(context.Background(), json.RawMessage(`{"path":"private.txt"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "permission denied") || !strings.Contains(res.Content, "aiexclude") {
		t.Fatalf("expected aiexclude permission denial, got %+v", res)
	}
}

func TestAIExcludeDeniesWrite(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".aiexclude"), []byte("private.txt\n"), 0o600); err != nil {
		t.Fatalf("write .aiexclude: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	res, err := newWriteTool(newReadTracker()).Execute(context.Background(), json.RawMessage(`{"path":"private.txt","content":"secret"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "aiexclude") {
		t.Fatalf("expected aiexclude permission denial, got %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dir, "private.txt")); !os.IsNotExist(err) {
		t.Fatalf("excluded file should not be created, stat err=%v", err)
	}
}

func TestAIExcludeDeniesEdit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(filepath.Join(dir, ".aiexclude"), []byte("private.txt\n"), 0o600); err != nil {
		t.Fatalf("write .aiexclude: %v", err)
	}
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("write private.txt: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker := newReadTracker()
	tracker.markRead(ec.SessionID, target)
	res, err := newEditTool(tracker).Execute(context.Background(), json.RawMessage(`{"path":"private.txt","oldString":"old","newString":"new"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || !strings.Contains(res.Content, "aiexclude") {
		t.Fatalf("expected aiexclude permission denial, got %+v", res)
	}
	got, err := os.ReadFile(target) //nolint:gosec // test reads from t.TempDir
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != "old" {
		t.Fatalf("excluded file should not be edited, got %q", got)
	}
}
