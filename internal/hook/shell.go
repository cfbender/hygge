package hook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// envAllowlist mirrors the MCP stdio allowlist so hook subprocesses
// receive the same baseline environment as MCP servers.
var envAllowlist = []string{"PATH", "HOME", "LANG", "USER", "TERM", "LC_ALL", "LC_CTYPE"}

// maxStderrBytes is the cap applied when using stderr as a deny reason.
const maxStderrBytes = 1024

// shellHook invokes an external command.  stdin receives the Input JSON;
// stdout is parsed as the Action JSON (empty stdout → allow); a non-zero
// exit code is treated as Deny with the reason drawn from stderr
// (truncated to maxStderrBytes).
type shellHook struct {
	name        string
	description string
	source      string
	events      []Event
	mode        Mode
	timeout     time.Duration
	command     string
	args        []string
	env         map[string]string
}

// Name implements Hook.
func (h *shellHook) Name() string { return h.name }

// Description implements Hook.
func (h *shellHook) Description() string { return h.description }

// Source implements Hook.
func (h *shellHook) Source() string { return h.source }

// Events implements Hook.
func (h *shellHook) Events() []Event { return append([]Event(nil), h.events...) }

// Mode implements Hook.
func (h *shellHook) Mode() Mode { return h.mode }

// Timeout implements Hook.
func (h *shellHook) Timeout() time.Duration { return h.timeout }

// Run executes the hook subprocess.  Protocol:
//
//  1. Marshal in → stdin.
//  2. Run with timeout.
//  3. Zero exit + empty stdout → Allow.
//  4. Zero exit + stdout → parse as Action.
//  5. Non-zero exit → Deny (stderr is the reason).
//  6. Timeout → Deny ("hook timed out after Xs").
//  7. Malformed stdout → fail-open with slog.Warn.
func (h *shellHook) Run(ctx context.Context, in Input) (Action, error) {
	if h.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.timeout)
		defer cancel()
	}

	inBytes, err := json.Marshal(in) //nolint:gosec // G117: Pwd field is a working directory path, not a secret
	if err != nil {
		return Action{}, fmt.Errorf("hook %s: marshal input: %w", h.name, err)
	}

	cmd := exec.CommandContext(ctx, h.command, h.args...) //nolint:gosec // command is config-supplied and allowlisted
	cmd.Env = mergeEnv(envAllowlist, h.env)
	cmd.Stdin = bytes.NewReader(inBytes)
	// WaitDelay ensures the process is killed and pipes drained promptly
	// after the context deadline fires, preventing a misbehaving hook
	// from hanging the agent indefinitely.
	cmd.WaitDelay = 5 * time.Second

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Run()

	// Check context deadline/cancel first — this is the timeout path.
	if ctx.Err() != nil {
		return Action{
			Decision: DecisionDeny,
			Reason:   fmt.Sprintf("hook %s timed out after %s", h.name, h.timeout),
		}, nil
	}

	if runErr != nil {
		// Non-zero exit: treat as deny.
		reason := strings.TrimSpace(stderrBuf.String())
		if len(reason) > maxStderrBytes {
			reason = reason[:maxStderrBytes]
		}
		if reason == "" {
			reason = fmt.Sprintf("hook %s exited with error: %v", h.name, runErr)
		}
		return Action{Decision: DecisionDeny, Reason: reason}, nil
	}

	// Zero exit + empty stdout → allow.
	if stdoutBuf.Len() == 0 {
		return Action{Decision: DecisionAllow}, nil
	}

	// Zero exit + stdout → parse as Action.
	var act Action
	if err := json.Unmarshal(stdoutBuf.Bytes(), &act); err != nil {
		slog.Warn("hook: malformed stdout JSON; failing open",
			"hook", h.name,
			"err", err,
			"stdout", truncate(stdoutBuf.String(), 256),
		)
		return Action{Decision: DecisionAllow}, nil
	}
	return act, nil
}

// truncate clips s to at most n bytes (by byte count, not rune count).
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
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
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
