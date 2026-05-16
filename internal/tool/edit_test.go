package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupEditFile(t *testing.T, contents string) (dir, target string, tracker *readTracker) {
	t.Helper()
	dir = t.TempDir()
	target = filepath.Join(dir, "src.txt")
	if err := os.WriteFile(target, []byte(contents), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	tracker = newReadTracker()
	return
}

func TestEdit_HappyPath(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "alpha beta gamma\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"beta","newString":"BETA"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	got, _ := os.ReadFile(target) //nolint:gosec // test reads from t.TempDir
	if string(got) != "alpha BETA gamma\n" {
		t.Errorf("file: %q", got)
	}
	if res.Metadata["replacements"].(int) != 1 {
		t.Errorf("replacements: %v", res.Metadata["replacements"])
	}
	for _, want := range []string{"edited ", "--- ", "+++ ", "-beta", "+BETA"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("result diff missing %q in:\n%s", want, res.Content)
		}
	}
}

func TestEdit_RefuseWithoutRead(t *testing.T) {
	dir, _, tracker := setupEditFile(t, "alpha\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"alpha","newString":"a"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError without read")
	}
}

func TestEdit_NotFound(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "alpha\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"missing","newString":"x"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for missing oldString")
	}
	if !strings.Contains(res.Content, "not found") {
		t.Errorf("Content: %q", res.Content)
	}
}

func TestEdit_AmbiguousWithoutReplaceAll(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "a b a b a\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"a","newString":"A"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected ambiguity error")
	}
	if res.Metadata["matches"].(int) != 3 {
		t.Errorf("matches: %v", res.Metadata["matches"])
	}
}

func TestEdit_ReplaceAll(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "a b a b a\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"a","newString":"A","replaceAll":true}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	got, _ := os.ReadFile(target) //nolint:gosec // test reads from t.TempDir
	if string(got) != "A b A b A\n" {
		t.Errorf("file: %q", got)
	}
	if res.Metadata["replacements"].(int) != 3 {
		t.Errorf("replacements: %v", res.Metadata["replacements"])
	}
}

func TestEdit_EmptyOldString(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "x\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"","newString":"y"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for empty oldString")
	}
}

func TestEdit_IdenticalStrings(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "x\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"x","newString":"x"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for no-op")
	}
}

func TestEdit_PermissionDenied(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "old\n")
	e, b := builtinTestEngine(t, denyAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)

	res, err := et.Execute(context.Background(),
		json.RawMessage(`{"path":"src.txt","oldString":"old","newString":"new"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for deny")
	}
	got, _ := os.ReadFile(target) //nolint:gosec // test reads from t.TempDir
	if string(got) != "old\n" {
		t.Errorf("file should be untouched: %q", got)
	}
}

func TestEdit_InvalidArgs(t *testing.T) {
	dir, target, tracker := setupEditFile(t, "x\n")
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker.markRead(ec.SessionID, target)
	et := newEditTool(tracker)
	_ = target

	cases := []string{
		`{}`,
		`{"path":"src.txt"}`,
		`{"path":"src.txt","oldString":"x"}`,
		`{"path":"src.txt","oldString":"x","newString":"y","unknown":1}`,
		`not json`,
	}
	for _, args := range cases {
		_, err := et.Execute(context.Background(), json.RawMessage(args), ec)
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
