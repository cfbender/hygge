package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/permission"
)

const (
	bashDefaultTimeoutMs = 30_000
	bashMaxTimeoutMs     = 600_000
	bashMaxOutputBytes   = 100 * 1024 // 100 KB cap on combined output
)

// envPassthrough is the minimal allowlist of environment variables forwarded
// to bash commands.  Anything else is stripped to keep the blast radius
// small.  Tools that genuinely need more should propose a schema field in
// a future version rather than expanding this list.
var envPassthrough = []string{"PATH", "HOME", "LANG", "USER", "TERM", "LC_ALL", "LC_CTYPE"}

type bashArgs struct {
	Command     string `json:"command"`
	TimeoutMs   int    `json:"timeout_ms,omitempty"`
	Description string `json:"description,omitempty"`
}

// bashTool implements the "bash" built-in.
type bashTool struct{}

func newBashTool() *bashTool { return &bashTool{} }

func (t *bashTool) Name() string { return "bash" }

// Parallelizable returns false: bash executes arbitrary shell commands with
// arbitrary side effects and must not run concurrently with other tools.
func (t *bashTool) Parallelizable() bool { return false }

func (t *bashTool) Description() string {
	return "Run a shell command via `sh -c` with a timeout. Inherits only PATH/HOME/LANG/" +
		"USER/TERM/LC_*. Streams stdout and stderr line-by-line as bus progress events. " +
		"Combined output is capped at 100 KB. Requires shell permission."
}

func (t *bashTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"command"},
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "Shell command. Run via `sh -c`.",
			},
			"timeout_ms": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     bashMaxTimeoutMs,
				"description": fmt.Sprintf("Timeout in milliseconds (default %d, max %d).", bashDefaultTimeoutMs, bashMaxTimeoutMs),
			},
			"description": map[string]any{
				"type":        "string",
				"description": "Short human description of what this command does. Surfaced to the permission prompt.",
			},
		},
	}
}

func (t *bashTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	var a bashArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(a.Command) == "" {
		return Result{}, newInvalidArgs("command is required", nil)
	}
	if a.TimeoutMs < 0 {
		return Result{}, newInvalidArgs("timeout_ms must be > 0", nil)
	}
	if a.TimeoutMs == 0 {
		a.TimeoutMs = bashDefaultTimeoutMs
	}
	if a.TimeoutMs > bashMaxTimeoutMs {
		return Result{}, newInvalidArgs(fmt.Sprintf("timeout_ms exceeds maximum of %d", bashMaxTimeoutMs), nil)
	}

	_, denied, perr := askPermission(ctx, ec, permission.Request{
		Category: permission.CategoryShell,
		Target:   a.Command,
		Command:  a.Command,
		Reason:   a.Description,
		ToolName: t.Name(),
	})
	if perr != nil {
		return Result{}, perr
	}
	if denied != nil {
		return *denied, nil
	}

	timeout := time.Duration(a.TimeoutMs) * time.Millisecond
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Note: portable shell-out on Windows is a known v0.2 follow-up.
	// For now we assume a POSIX `sh` is on PATH.
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", a.Command) //nolint:gosec // command is permission-gated above
	if ec.Pwd != "" {
		cmd.Dir = ec.Pwd
	}
	cmd.Env = filteredEnv(envPassthrough)

	stdoutR, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, newExecutionFailed("attach stdout pipe", err)
	}
	stderrR, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, newExecutionFailed("attach stderr pipe", err)
	}

	nowFn := ec.nowFn()
	start := nowFn()

	if err := cmd.Start(); err != nil {
		return Result{}, newExecutionFailed("start command", err)
	}

	var (
		mu           sync.Mutex
		combined     strings.Builder
		stdoutBytes  int
		stderrBytes  int
		outTruncated bool
	)

	appendOutput := func(line, stream string) {
		mu.Lock()
		defer mu.Unlock()
		if outTruncated {
			return
		}
		// +1 for newline.
		remaining := bashMaxOutputBytes - combined.Len()
		if remaining <= 0 {
			outTruncated = true
			return
		}
		toWrite := line + "\n"
		if len(toWrite) > remaining {
			combined.WriteString(toWrite[:remaining])
			combined.WriteString("… (output truncated)\n")
			outTruncated = true
		} else {
			combined.WriteString(toWrite)
		}
		switch stream {
		case "stdout":
			stdoutBytes += len(line) + 1
		case "stderr":
			stderrBytes += len(line) + 1
		}
	}

	publishLine := func(line, stream string) {
		if ec.Bus != nil {
			bus.Publish(ec.Bus, bus.ToolCallProgress{
				SessionID: ec.SessionID,
				MessageID: ec.MessageID,
				ToolUseID: ec.ToolUseID,
				ToolName:  t.Name(),
				Stream:    stream,
				Line:      line,
				At:        nowFn(),
			})
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go scanPipe(&wg, stdoutR, "stdout", appendOutput, publishLine)
	go scanPipe(&wg, stderrR, "stderr", appendOutput, publishLine)

	// Wait for the process and for both readers to drain.  Order matters:
	// cmd.Wait blocks until the process exits AND its IO is fully read,
	// so wg.Wait() before cmd.Wait() would deadlock on the readers (the
	// pipes only close when the process exits).  cmd.Wait closes the
	// pipes for us; then wg.Wait is non-blocking.
	waitErr := cmd.Wait()
	wg.Wait()

	duration := nowFn().Sub(start)

	// Detect timeout: cmdCtx.Err is set if the timeout fired before Wait
	// returned.  Distinct from a generic context cancellation by the
	// caller — but we treat both as "ran out of time" from the tool's
	// perspective.
	timedOut := errors.Is(cmdCtx.Err(), context.DeadlineExceeded)

	if timedOut {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("command timed out after %dms", a.TimeoutMs),
			Metadata: map[string]any{
				"timed_out":    true,
				"timeout_ms":   a.TimeoutMs,
				"duration_ms":  duration.Milliseconds(),
				"stdout_bytes": stdoutBytes,
				"stderr_bytes": stderrBytes,
			},
		}, nil
	}

	exitCode := 0
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			// Other errors (e.g. signal, exec problem) — surface as
			// execution failure rather than an IsError result, since
			// the model has no actionable signal.
			return Result{}, newExecutionFailed("wait for command", waitErr)
		}
	}

	return Result{
		Content: combined.String(),
		IsError: exitCode != 0,
		Metadata: map[string]any{
			"exit_code":    exitCode,
			"duration_ms":  duration.Milliseconds(),
			"timed_out":    false,
			"stdout_bytes": stdoutBytes,
			"stderr_bytes": stderrBytes,
			"truncated":    outTruncated,
		},
	}, nil
}

// scanPipe reads lines from r and dispatches them to append + publish.
// Lines exceeding bufio.Scanner's default buffer are split — we grow the
// buffer once to 1 MB to keep most outputs intact.
func scanPipe(wg *sync.WaitGroup, r io.Reader, stream string,
	appendOutput func(line, stream string),
	publishLine func(line, stream string),
) {
	defer wg.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		publishLine(line, stream)
		appendOutput(line, stream)
	}
	// Scanner errors here are surfaced as truncation; we do not propagate
	// them to the caller because the process exit status is the
	// authoritative outcome signal.
}

// filteredEnv returns a slice of "KEY=value" pairs for the named
// environment variables that are actually set in the current process.
func filteredEnv(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}
