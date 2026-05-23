package mcp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStdioTransport_RoundtripWithShCat(t *testing.T) {
	t.Parallel()
	// `sh -c cat` echoes its stdin to its stdout: framed input becomes
	// framed output.  This exercises the full subprocess-spawn +
	// pipe-wire-up path without depending on any MCP binary.
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	tr := NewStdio(StdioOptions{
		Command: "sh",
		Args:    []string{"-c", "cat"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"hello"}`)
	if err := tr.Send(context.Background(), body); err != nil {
		t.Fatalf("Send: %v", err)
	}
	got, err := tr.Recv(context.Background())
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("body mismatch: got %q want %q", got, body)
	}
}

func TestStdioTransport_EnvAllowlist(t *testing.T) {
	if _, err := exec.LookPath("env"); err != nil {
		t.Skip("env not on PATH")
	}
	// Set an unusual var that should NOT propagate.
	t.Setenv("HYGGE_MCP_TEST_LEAK", "leaky")
	t.Setenv("PATH", "/usr/bin:/bin")

	// Build the child env via mergeEnv and inspect it directly — this
	// is the deterministic, fast path that doesn't depend on shelling
	// out.
	env := mergeEnv(stdioEnvAllowlist, map[string]string{"GITHUB_TOKEN": "abc"})
	gotKeys := make(map[string]string, len(env))
	for _, kv := range env {
		before, after, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		gotKeys[before] = after
	}
	if _, leaked := gotKeys["HYGGE_MCP_TEST_LEAK"]; leaked {
		t.Fatalf("non-allowlisted env var leaked: %v", gotKeys)
	}
	if v := gotKeys["GITHUB_TOKEN"]; v != "abc" {
		t.Fatalf("override GITHUB_TOKEN missing or wrong: %q", v)
	}
	if v := gotKeys["PATH"]; v != "/usr/bin:/bin" {
		t.Fatalf("PATH not forwarded: %q", v)
	}
}

func TestStdioTransport_StderrRingCapturedInCloseError(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	// Spawn a child that writes to stderr and exits non-zero.
	tr := NewStdio(StdioOptions{
		Command: "sh",
		Args:    []string{"-c", "printf 'boom: bad config\\n' >&2; exit 7"},
	})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Give the child a moment to write + exit; Close will join.
	err := tr.Close()
	if err == nil {
		t.Fatalf("expected non-nil error from Close after non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom: bad config") {
		t.Fatalf("expected stderr tail in error, got %q", err.Error())
	}
}

func TestStdioTransport_SendBeforeStart(t *testing.T) {
	t.Parallel()
	tr := NewStdio(StdioOptions{Command: "sh", Args: []string{"-c", "cat"}})
	err := tr.Send(context.Background(), []byte("{}"))
	if !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed before Start, got %v", err)
	}
}

func TestStdioTransport_CloseIdempotent(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	tr := NewStdio(StdioOptions{Command: "sh", Args: []string{"-c", "sleep 0.05"}})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Two Closes back-to-back should not panic or hang.
	_ = tr.Close()
	_ = tr.Close()
}

func TestStdioTransport_ServerLabel(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts StdioOptions
		want string
	}{
		{"empty", StdioOptions{}, "stdio:(unset)"},
		{"command-only", StdioOptions{Command: "mcp-server-github"}, "mcp-server-github"},
		{"with-args", StdioOptions{Command: "mcp-server-github", Args: []string{"--token", "xyz"}}, "mcp-server-github --token"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := NewStdio(tc.opts)
			if got := tr.ServerLabel(); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestStdioTransport_StartTwice(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	tr := NewStdio(StdioOptions{Command: "sh", Args: []string{"-c", "sleep 0.05"}})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()
	if err := tr.Start(context.Background()); err == nil {
		t.Fatalf("expected error on second Start")
	}
}

func TestStdioTransport_RecvAtEOF(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not on PATH")
	}
	tr := NewStdio(StdioOptions{Command: "sh", Args: []string{"-c", "exit 0"}})
	if err := tr.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = tr.Close() }()

	// Allow process to exit fully.
	time.Sleep(50 * time.Millisecond)

	_, err := tr.Recv(context.Background())
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestRingBuffer_DropsOldData(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(8)
	mustWrite(t, rb, []byte("aaaaaaaa"))     // exactly cap
	mustWrite(t, rb, []byte("bbbbbbbbbbbb")) // exceeds cap
	if got := rb.String(); got != "bbbbbbbb" {
		t.Fatalf("got %q want %q", got, "bbbbbbbb")
	}
}

func TestRingBuffer_ConcurrentWrites(t *testing.T) {
	t.Parallel()
	rb := newRingBuffer(1024)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			mustWrite(t, rb, bytes.Repeat([]byte("x"), 50))
		})
	}
	wg.Wait()
	// Size <= cap, length is a multiple of 50.
	if l := len(rb.String()); l > 1024 {
		t.Fatalf("buffer overran cap: %d", l)
	}
}

func mustWrite(t *testing.T, w io.Writer, p []byte) {
	t.Helper()
	if _, err := w.Write(p); err != nil {
		t.Fatalf("Write: %v", err)
	}
}
