package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/bus"
)

func TestWrite_CreateNewFile(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker := newReadTracker()
	wt := newWriteTool(tracker)

	res, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"new.txt","content":"hello world"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "new.txt")) //nolint:gosec // test reads from t.TempDir
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("contents: got %q", got)
	}
	if !res.Metadata["created"].(bool) {
		t.Errorf("created: want true")
	}
	if !tracker.hasRead(ec.SessionID, filepath.Join(dir, "new.txt")) {
		t.Errorf("file should be marked read after write")
	}
}

func TestWrite_OverwriteRefusedWithoutRead(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(target, []byte("original"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	wt := newWriteTool(newReadTracker())

	res, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"exists.txt","content":"new"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for unread overwrite")
	}
	got, _ := os.ReadFile(target) //nolint:gosec // test reads from t.TempDir
	if string(got) != "original" {
		t.Errorf("file should not have been modified, got %q", got)
	}
}

func TestWrite_OverwriteOkAfterRead(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "exists.txt")
	if err := os.WriteFile(target, []byte("v1"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker := newReadTracker()
	tracker.markRead(ec.SessionID, target)
	wt := newWriteTool(tracker)

	res, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"exists.txt","content":"v2"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	got, _ := os.ReadFile(target) //nolint:gosec // test reads from t.TempDir
	if string(got) != "v2" {
		t.Errorf("file: got %q", got)
	}
	if res.Metadata["created"].(bool) {
		t.Errorf("created: want false on overwrite")
	}
}

func TestWrite_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, denyAll)
	ec := newExecContext(b, e, dir)
	wt := newWriteTool(newReadTracker())

	res, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"newfile.txt","content":"x"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for deny")
	}
	if _, err := os.Stat(filepath.Join(dir, "newfile.txt")); !os.IsNotExist(err) {
		t.Errorf("file should not exist after deny: %v", err)
	}
}

func TestWrite_InvalidArgs(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	wt := newWriteTool(newReadTracker())

	cases := []string{`{}`, `{"path":"x"}`, `{"content":"y"}`, `{"path":"","content":"y"}`, `{"path":"x","content":"y","extra":1}`}
	for _, args := range cases {
		_, err := wt.Execute(context.Background(), json.RawMessage(args), ec)
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

func TestWrite_RefuseDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	wt := newWriteTool(newReadTracker())

	res, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"subdir","content":"x"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when target is a directory")
	}
	if !strings.Contains(res.Content, "directory") {
		t.Errorf("content: %q", res.Content)
	}
}

func TestWrite_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	wt := newWriteTool(newReadTracker())

	res, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"a/b/c.txt","content":"deep"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	got, err := os.ReadFile(filepath.Join(dir, "a", "b", "c.txt")) //nolint:gosec // test reads from t.TempDir
	if err != nil {
		t.Fatalf("readback: %v", err)
	}
	if string(got) != "deep" {
		t.Errorf("contents: %q", got)
	}
}

func TestWrite_PermissionTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "asset.txt")
	rec := newRecordingResponder(allowAll(bus.PermissionAsked{}))
	e, b := builtinTestEngine(t, rec.decide)
	ec := newExecContext(b, e, dir)
	wt := newWriteTool(newReadTracker())

	if _, err := wt.Execute(context.Background(),
		json.RawMessage(`{"path":"asset.txt","content":"x"}`), ec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := rec.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("requests: %d", len(reqs))
	}
	if reqs[0].Category != "file.write" {
		t.Errorf("Category: got %q", reqs[0].Category)
	}
	if reqs[0].Target != filepath.Clean(target) {
		t.Errorf("Target: got %q want %q", reqs[0].Target, target)
	}
	if reqs[0].ToolName != "write" {
		t.Errorf("ToolName: got %q", reqs[0].ToolName)
	}
}
