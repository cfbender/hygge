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

func TestRead_HappyPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(target, []byte("alpha\nbeta\ngamma\n"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	tracker := newReadTracker()
	rd := newReadTool(tracker)

	res, err := rd.Execute(context.Background(), json.RawMessage(`{"path":"hello.txt"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	want := "1: alpha\n2: beta\n3: gamma\n"
	if res.Content != want {
		t.Errorf("Content: got %q want %q", res.Content, want)
	}
	if got := res.Metadata["lines_returned"]; got != 3 {
		t.Errorf("lines_returned: got %v", got)
	}
	if !tracker.hasRead(ec.SessionID, target) {
		t.Errorf("file should be marked as read")
	}
}

func TestRead_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "lines.txt")
	var sb strings.Builder
	for i := 1; i <= 50; i++ {
		sb.WriteString("line")
		sb.WriteString(itoa(i))
		sb.WriteByte('\n')
	}
	if err := os.WriteFile(target, []byte(sb.String()), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	rd := newReadTool(newReadTracker())

	res, err := rd.Execute(context.Background(),
		json.RawMessage(`{"path":"lines.txt","offset":10,"limit":3}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "10: line10") {
		t.Errorf("expected offset to start at line 10, got %q", res.Content)
	}
	if strings.Contains(res.Content, "13: line13") {
		t.Errorf("expected limit=3 to cap before line 13, got %q", res.Content)
	}
	if res.Metadata["lines_returned"].(int) != 3 {
		t.Errorf("lines_returned: %v", res.Metadata["lines_returned"])
	}
	if res.Metadata["total_lines"].(int) != 50 {
		t.Errorf("total_lines: %v", res.Metadata["total_lines"])
	}
}

func TestRead_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	_ = os.WriteFile(target, []byte("hidden"), 0o644)

	e, b := builtinTestEngine(t, denyAll)
	ec := newExecContext(b, e, dir)
	rd := newReadTool(newReadTracker())

	res, err := rd.Execute(context.Background(),
		json.RawMessage(`{"path":"`+target+`"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for deny")
	}
	if !strings.Contains(res.Content, "permission denied") {
		t.Errorf("Content: %q", res.Content)
	}
}

func TestRead_InvalidArgs(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	rd := newReadTool(newReadTracker())

	cases := []struct {
		name string
		args string
	}{
		{"missing path", `{}`},
		{"unknown field", `{"path":"x","foo":1}`},
		{"bad json", `{not json`},
		{"empty path", `{"path":""}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := rd.Execute(context.Background(), json.RawMessage(c.args), ec)
			if err == nil {
				t.Fatal("expected ToolError")
			}
			var te *ToolError
			if !errors.As(err, &te) {
				t.Fatalf("expected *ToolError, got %T: %v", err, err)
			}
			if te.Code != CodeInvalidArgs {
				t.Errorf("Code: got %q want %q", te.Code, CodeInvalidArgs)
			}
		})
	}
}

func TestRead_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	rd := newReadTool(newReadTracker())

	res, err := rd.Execute(context.Background(),
		json.RawMessage(`{"path":"nope.txt"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for missing file")
	}
	if !strings.Contains(res.Content, "file not found") {
		t.Errorf("Content: %q", res.Content)
	}
}

func TestRead_LongLineTruncated(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "wide.txt")
	long := strings.Repeat("x", 3000) + "\n"
	if err := os.WriteFile(target, []byte(long), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	rd := newReadTool(newReadTracker())

	res, err := rd.Execute(context.Background(),
		json.RawMessage(`{"path":"wide.txt"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "… (truncated)") {
		t.Errorf("expected truncation marker, got %q", res.Content[:120])
	}
	if res.Metadata["truncated_lines"].(int) != 1 {
		t.Errorf("truncated_lines: %v", res.Metadata["truncated_lines"])
	}
}

func TestRead_PermissionRequestShape(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "asset.txt")
	_ = os.WriteFile(target, []byte("x\n"), 0o644)

	rec := newRecordingResponder(bus.PermissionReplied{Decision: "allow", Scope: "once"})
	e, b := builtinTestEngine(t, rec.decide)
	// Force the "ask" path by reading outside the pwd default-allow zone.
	outside := t.TempDir()
	otherFile := filepath.Join(outside, "out.txt")
	_ = os.WriteFile(otherFile, []byte("y\n"), 0o644)

	ec := newExecContext(b, e, dir)
	rd := newReadTool(newReadTracker())

	if _, err := rd.Execute(context.Background(),
		json.RawMessage(`{"path":"`+otherFile+`"}`), ec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := rec.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 permission request, got %d", len(reqs))
	}
	r := reqs[0]
	if r.Category != "file.read" {
		t.Errorf("Category: got %q", r.Category)
	}
	if r.Target != filepath.Clean(otherFile) {
		t.Errorf("Target: got %q want %q", r.Target, otherFile)
	}
	if r.ToolName != "read" {
		t.Errorf("ToolName: got %q", r.ToolName)
	}
}

// itoa is a tiny strconv-free integer-to-string helper used in test data.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
