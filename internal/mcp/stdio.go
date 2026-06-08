package mcp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"sort"
	"sync"
	"syscall"
	"time"
)

// Transport is the wire-level interface a Client speaks to.  In v0.2
// the stdio implementation exists; v0.3 adds the SSE transport.
// Streamable HTTP (the 2026 spec) lands in the next slice.
//
// Transport implementations need not be safe for concurrent Send calls
// from multiple goroutines — the Client serialises sends through its
// own dispatcher.  Recv, however, runs on its own read goroutine and
// must safely coexist with Close called from any goroutine.
type Transport interface {
	// Start launches the underlying process / connection.  Calling
	// Start twice is an error; calling any other method before Start
	// returns an error.
	Start(ctx context.Context) error

	// Send writes one framed JSON-RPC body to the server.
	Send(ctx context.Context, body []byte) error

	// Recv blocks until the next framed message arrives.  Returns
	// io.EOF when the server closes cleanly.
	Recv(ctx context.Context) ([]byte, error)

	// Close shuts the transport down.  Idempotent and safe to call
	// from any goroutine.  Returns the underlying wait error (or nil
	// for a clean exit).
	Close() error

	// ServerLabel returns a short diagnostics-friendly label for the
	// transport — e.g. "mcp-server-github" or "sh -c cat".
	ServerLabel() string
}

// stdioEnvAllowlist is the set of environment variables forwarded from
// the parent process to every MCP subprocess.  Matches internal/tool
// bash's envPassthrough so the behaviour is consistent across the
// codebase.
var stdioEnvAllowlist = []string{"PATH", "HOME", "LANG", "USER", "TERM", "LC_ALL", "LC_CTYPE"}

// stderrRingSize bounds the stderr capture buffer for diagnostics.
// 64 KiB matches the spec; older lines are dropped.
const stderrRingSize = 64 * 1024

// StdioOptions configures NewStdio.
type StdioOptions struct {
	// Command is the binary to spawn.  Looked up via $PATH unless
	// absolute.
	Command string

	// Args are passed positionally to Command.
	Args []string

	// Env adds / overrides environment variables for the child.
	// Merged on top of the parent's environment filtered through
	// stdioEnvAllowlist.
	Env map[string]string

	// Dir sets the child's working directory.  Defaults to the
	// parent's cwd when empty.
	Dir string

	// Now is an injectable clock; unused by the transport itself but
	// reserved so callers can pass through to the Client constructor
	// without juggling two clocks.  Reserved.
	Now func() time.Time
}

// stdioTransport spawns an MCP server as a subprocess and exchanges
// JSON-RPC messages over its stdio pipes.
type stdioTransport struct {
	opts StdioOptions

	mu       sync.Mutex // guards started/closed/cmd/stdin/stdout
	started  bool
	closed   bool
	cmd      *exec.Cmd
	stdinW   io.WriteCloser
	stdoutR  io.ReadCloser
	stdoutBR *bufio.Reader
	stderrW  *ringBuffer

	// sendMu serialises Send calls to keep WriteNDJSON's single write
	// atomic on the pipe for concurrent callers.
	sendMu sync.Mutex

	// waitDone is closed when the wait goroutine finishes.
	waitDone chan struct{}
	waitErr  error
}

// NewStdio constructs a stdio Transport.  The subprocess is not
// started until Start is called.
func NewStdio(opts StdioOptions) Transport {
	return &stdioTransport{opts: opts}
}

// ServerLabel returns the command name and the first arg for log
// readability.  Sensitive args (api keys, tokens) should NOT appear in
// the first arg slot.
func (t *stdioTransport) ServerLabel() string {
	if t.opts.Command == "" {
		return "stdio:(unset)"
	}
	if len(t.opts.Args) == 0 {
		return t.opts.Command
	}
	return t.opts.Command + " " + t.opts.Args[0]
}

// Start spawns the subprocess and wires stdio.
func (t *stdioTransport) Start(_ context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.started {
		return fmt.Errorf("mcp: stdio: already started")
	}
	if t.closed {
		return ErrClosed
	}
	if t.opts.Command == "" {
		return fmt.Errorf("mcp: stdio: command is required")
	}

	// We do NOT pass ctx to exec.CommandContext: cancellation should
	// be handled by Close (SIGTERM → SIGKILL) so we can read any
	// final stderr.  The Client owns Recv-side cancellation by
	// closing stdin via Close.
	cmd := exec.Command(t.opts.Command, t.opts.Args...) //nolint:gosec // command is permission-gated and config-supplied
	cmd.Env = mergeEnv(stdioEnvAllowlist, t.opts.Env)
	if t.opts.Dir != "" {
		cmd.Dir = t.opts.Dir
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("mcp: stdio: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return fmt.Errorf("mcp: stdio: stdout pipe: %w", err)
	}
	// Wire stderr directly to the ring buffer so cmd.Wait drains it
	// synchronously with the child's exit.  Using StderrPipe + a
	// reader goroutine races: Wait closes the read end of the pipe
	// to "see the command exit", which can interrupt an in-flight
	// io.Copy before the final buffered bytes are read.
	stderrBuf := newRingBuffer(stderrRingSize)
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return fmt.Errorf("mcp: stdio: start %q: %w", t.opts.Command, err)
	}

	t.cmd = cmd
	t.stdinW = stdin
	t.stdoutR = stdout
	t.stdoutBR = bufio.NewReader(stdout)
	t.stderrW = stderrBuf
	t.waitDone = make(chan struct{})

	go func() {
		t.waitErr = cmd.Wait()
		close(t.waitDone)
	}()

	t.started = true
	return nil
}

// Send writes one newline-delimited JSON message to the server's stdin.
func (t *stdioTransport) Send(_ context.Context, body []byte) error {
	t.mu.Lock()
	if !t.started || t.closed {
		t.mu.Unlock()
		return ErrClosed
	}
	w := t.stdinW
	t.mu.Unlock()

	t.sendMu.Lock()
	defer t.sendMu.Unlock()
	_, err := WriteNDJSON(w, body)
	return err
}

// Recv reads one newline-delimited JSON message from the server's stdout.
// When the server's stdout has closed (clean exit, pipe broken) Recv
// returns io.EOF so the Client can shut down pending calls
// deterministically.
func (t *stdioTransport) Recv(_ context.Context) ([]byte, error) {
	t.mu.Lock()
	if !t.started {
		t.mu.Unlock()
		return nil, fmt.Errorf("mcp: stdio: not started")
	}
	r := t.stdoutBR
	t.mu.Unlock()
	body, err := ReadNDJSON(r)
	if err == nil {
		return body, nil
	}
	if errors.Is(err, io.EOF) {
		return nil, io.EOF
	}
	// Closed-pipe errors after the child exits or after Close are the
	// stdio-transport analogue of EOF — surface as EOF so the Client's
	// read loop terminates cleanly.
	if errors.Is(err, os.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
		return nil, io.EOF
	}
	return nil, err
}

// Close terminates the subprocess: closes stdin (so the server sees
// EOF), waits up to 3s for clean exit, then SIGTERM, then SIGKILL.
// Returns the wait error annotated with captured stderr for
// diagnostics.  Idempotent.
func (t *stdioTransport) Close() error {
	t.mu.Lock()
	if t.closed || !t.started {
		t.closed = true
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	cmd := t.cmd
	stdin := t.stdinW
	stdout := t.stdoutR
	waitDone := t.waitDone
	stderrBuf := t.stderrW
	t.mu.Unlock()

	// 1) Close stdin so the server sees EOF and can exit cleanly.
	if stdin != nil {
		_ = stdin.Close()
	}

	// 2) Wait up to 3s for the process to exit on its own.
	select {
	case <-waitDone:
		// clean exit (possibly non-zero)
	case <-time.After(3 * time.Second):
		// 3) SIGTERM, then up to 3s more.
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
		}
		select {
		case <-waitDone:
		case <-time.After(3 * time.Second):
			// 4) Final SIGKILL.
			if cmd != nil && cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGKILL)
			}
			<-waitDone
		}
	}

	if stdout != nil {
		_ = stdout.Close()
	}

	werr := t.waitErr
	if werr == nil {
		return nil
	}
	// Clean exits surface as nil; a non-zero exit becomes an
	// *exec.ExitError.  Annotate with the tail of captured stderr so
	// misconfigured servers are easy to diagnose.
	stderrTail := ""
	if stderrBuf != nil {
		stderrTail = stderrBuf.String()
	}
	if stderrTail != "" {
		return fmt.Errorf("%w; stderr: %s", werr, stderrTail)
	}
	return werr
}

// mergeEnv builds the child's environment from the parent's filtered
// allowlist plus the caller-supplied overrides.  Returns deterministic
// "KEY=value" pairs sorted for stable test output.
func mergeEnv(allow []string, extra map[string]string) []string {
	merged := make(map[string]string, len(allow)+len(extra))
	for _, name := range allow {
		if v, ok := os.LookupEnv(name); ok {
			merged[name] = v
		}
	}
	maps.Copy(merged, extra)
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// ringBuffer is a bounded byte buffer that drops older data when full.
// Used to capture stderr tails from MCP subprocesses without unbounded
// memory growth.
type ringBuffer struct {
	mu  sync.Mutex
	buf []byte
	cap int
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{cap: capacity}
}

// Write appends p; if the buffer would exceed capacity the oldest data
// is discarded.  Always returns len(p), nil.
func (b *ringBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		b.buf = b.buf[len(b.buf)-b.cap:]
	}
	return len(p), nil
}

// String returns a snapshot of the buffered bytes as a string.
func (b *ringBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
