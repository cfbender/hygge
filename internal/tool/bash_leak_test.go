//go:build !windows

package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBash_CancelKillsGrandchildren is a regression test for orphaned
// background processes. The bash tool runs commands via `sh -c`; before the
// process-group fix, cancelling (interrupt) or timing out a command SIGKILLed
// only the direct `sh` child, leaving any grandchildren it spawned running and
// reparented to PID 1 — accumulating CPU over a session.
//
// The command launches a background subshell that sleeps, then touches a marker
// file. We cancel before the sleep elapses. If the whole process group is
// killed, the grandchild dies and the marker is never created. If only `sh`
// dies, the orphaned subshell survives and creates the marker.
func TestBash_CancelKillsGrandchildren(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "grandchild-ran")

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	// Background grandchild: sleeps 2s, then touches the marker. The parent
	// `sh` prints "started" and waits, so cmd.Start succeeds and the tool
	// begins streaming before we cancel.
	cmd := fmt.Sprintf(`( sleep 2; touch %q ) & echo started; wait`, marker)
	raw, err := json.Marshal(bashArgs{Command: cmd, TimeoutMs: 10_000})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = bt.Execute(ctx, raw, ec)
	}()

	// Give the grandchild time to spawn, then interrupt.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("Execute did not return after cancel (possible hang)")
	}

	// Wait past the grandchild's sleep window. If it survived the cancel it
	// will have created the marker by now.
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("grandchild survived cancellation and created marker file; process group was not killed")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}

// TestBash_TimeoutKillsGrandchildren is the timeout-path analogue: a short
// timeout must tear down the whole process tree, not just `sh`.
func TestBash_TimeoutKillsGrandchildren(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "grandchild-ran")

	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, dir)
	bt := newBashTool()

	cmd := fmt.Sprintf(`( sleep 2; touch %q ) & echo started; wait`, marker)
	raw, err := json.Marshal(bashArgs{Command: cmd, TimeoutMs: 300})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	res, err := bt.Execute(context.Background(), raw, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("expected timeout IsError result, got %+v", res)
	}

	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("grandchild survived timeout and created marker file; process group was not killed")
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat marker: %v", err)
	}
}
