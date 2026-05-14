package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
)

func TestBash_HappyPath(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"echo hello"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	if !strings.Contains(res.Content, "hello") {
		t.Errorf("Content: %q", res.Content)
	}
	if res.Metadata["exit_code"].(int) != 0 {
		t.Errorf("exit_code: %v", res.Metadata["exit_code"])
	}
}

func TestBash_NonZeroExitIsError(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"exit 7"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for non-zero exit")
	}
	if res.Metadata["exit_code"].(int) != 7 {
		t.Errorf("exit_code: %v", res.Metadata["exit_code"])
	}
}

func TestBash_PermissionDenied(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, denyAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"echo nope"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError for deny")
	}
	if strings.Contains(res.Content, "nope") {
		t.Errorf("command should not have run: %q", res.Content)
	}
}

func TestBash_InvalidArgs(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	cases := []string{
		`{}`,
		`{"command":""}`,
		`{"command":"echo","unknown":1}`,
		`not json`,
		`{"command":"echo","timeout_ms":700000}`,
	}
	for _, args := range cases {
		_, err := bt.Execute(context.Background(), json.RawMessage(args), ec)
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

func TestBash_Timeout(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	start := time.Now()
	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"sleep 60","timeout_ms":100}`), ec)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("timeout took too long: %v", elapsed)
	}
	if !res.IsError {
		t.Fatal("expected IsError for timeout")
	}
	if got := res.Metadata["timed_out"]; got != true {
		t.Errorf("timed_out: got %v", got)
	}
}

func TestBash_StreamingProgress(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	sub := bus.Subscribe[bus.ToolCallProgress](b, bus.SubscribeOptions{BufferSize: 32})
	defer sub.Unsubscribe()

	// Collect events in a goroutine; the bus delivery is async with respect to
	// command execution, but Execute does not return until both pipe readers
	// drain, so by the time Execute returns every progress event has been
	// published.  Add a tiny grace window for the bus to deliver them.
	var (
		mu     sync.Mutex
		events []bus.ToolCallProgress
		done   = make(chan struct{})
	)
	go func() {
		defer close(done)
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			select {
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				mu.Lock()
				events = append(events, ev)
				mu.Unlock()
			case <-timer.C:
				return
			}
		}
	}()

	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"printf 'a\\nb\\nc\\n'"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError: %+v", res)
	}
	// Give the bus a moment to flush.
	time.Sleep(100 * time.Millisecond)
	sub.Unsubscribe()
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("progress events: got %d, want 3 (%v)", len(events), events)
	}
	want := []string{"a", "b", "c"}
	for i, ev := range events {
		if ev.Line != want[i] {
			t.Errorf("event %d: Line=%q want %q", i, ev.Line, want[i])
		}
		if ev.Stream != "stdout" {
			t.Errorf("event %d: Stream=%q", i, ev.Stream)
		}
		if ev.ToolName != "bash" {
			t.Errorf("event %d: ToolName=%q", i, ev.ToolName)
		}
		if ev.ToolUseID != "tu-1" {
			t.Errorf("event %d: ToolUseID=%q", i, ev.ToolUseID)
		}
	}
}

func TestBash_EnvPassthrough(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	// TERM is in the allowlist.  Force it to a known value via t.Setenv.
	t.Setenv("TERM", "hygge-test-term")
	t.Setenv("HYGGE_BLOCKED_VAR", "should-be-stripped")

	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"env"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "TERM=hygge-test-term") {
		t.Errorf("expected TERM to pass through, got: %q", res.Content)
	}
	if strings.Contains(res.Content, "HYGGE_BLOCKED_VAR") {
		t.Errorf("HYGGE_BLOCKED_VAR should be stripped, got: %q", res.Content)
	}
}

func TestBash_StderrAndStdoutInterleaved(t *testing.T) {
	dir := t.TempDir()
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	res, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"echo out1; echo err1 1>&2; echo out2"}`), ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, s := range []string{"out1", "err1", "out2"} {
		if !strings.Contains(res.Content, s) {
			t.Errorf("missing %q in combined output: %q", s, res.Content)
		}
	}
	if res.Metadata["stderr_bytes"].(int) == 0 {
		t.Errorf("stderr_bytes should be > 0")
	}
}

func TestBash_PermissionTarget(t *testing.T) {
	dir := t.TempDir()
	rec := newRecordingResponder(bus.PermissionReplied{Decision: "allow", Scope: "once"})
	e, b := builtinTestEngine(t, rec.decide)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	if _, err := bt.Execute(context.Background(),
		json.RawMessage(`{"command":"echo hello","description":"say hi"}`), ec); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := rec.snapshot()
	if len(reqs) != 1 {
		t.Fatalf("requests: %d", len(reqs))
	}
	if reqs[0].Category != "shell" {
		t.Errorf("Category: got %q", reqs[0].Category)
	}
	if reqs[0].Target != "echo hello" {
		t.Errorf("Target: got %q", reqs[0].Target)
	}
	if reqs[0].ToolName != "bash" {
		t.Errorf("ToolName: got %q", reqs[0].ToolName)
	}
}
