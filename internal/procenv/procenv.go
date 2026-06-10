// Package procenv is the single source of truth for the policy applied
// to subprocesses hygge spawns: which parent environment variables are
// forwarded to children, and how much of their output is retained.
// Every site that shells out (bash tool, MCP stdio servers, shell
// hooks, plugin exec) builds its child environment and output capture
// from this package so the blast radius of a spawned process stays
// small and consistent across the codebase.
package procenv

import (
	"maps"
	"os"
	"sort"
)

// Allowlist is the minimal set of environment variables forwarded to
// subprocesses.  Anything else is stripped to keep the blast radius
// small.  Callers that genuinely need more should merge explicit extras
// via Merged rather than expanding this list.
var Allowlist = [...]string{"PATH", "HOME", "LANG", "USER", "TERM", "LC_ALL", "LC_CTYPE"}

// MaxOutputBytes is the default cap on captured subprocess output.
const MaxOutputBytes = 100 * 1024 // 100 KB

// TruncationMarker is the text appended to capped subprocess output so
// the model (and the user) can tell the capture is incomplete.
const TruncationMarker = "… (output truncated)"

// Filtered returns "KEY=value" pairs for the allowlisted variables that
// are actually set in the current process.
func Filtered() []string {
	out := make([]string, 0, len(Allowlist))
	for _, name := range Allowlist {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// Merged builds a child environment from the parent's filtered
// allowlist plus the caller-supplied overrides.  Returns deterministic
// "KEY=value" pairs sorted for stable test output.
func Merged(extra map[string]string) []string {
	merged := make(map[string]string, len(Allowlist)+len(extra))
	for _, name := range Allowlist {
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

// LimitedBuffer is an io.Writer that stores up to Max bytes and
// silently discards the rest, recording whether anything was dropped.
// Write never returns an error, so a subprocess keeps running (and
// exits with its real status) even after its output is capped.
//
// A Max of zero or less falls back to MaxOutputBytes, making the zero
// value safe to use.  Not safe for concurrent writers; give each
// stream its own buffer.
type LimitedBuffer struct {
	// Max is the capture cap in bytes.
	Max int

	buf       []byte
	truncated bool
}

// Write implements io.Writer.  It reports the full len(p) so writers
// upstream never see a short-write error.
func (b *LimitedBuffer) Write(p []byte) (int, error) {
	max := b.Max
	if max <= 0 {
		max = MaxOutputBytes
	}
	remaining := max - len(b.buf)
	switch {
	case remaining <= 0:
		if len(p) > 0 {
			b.truncated = true
		}
	case len(p) > remaining:
		b.buf = append(b.buf, p[:remaining]...)
		b.truncated = true
	default:
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

// Truncated reports whether any written bytes were discarded.
func (b *LimitedBuffer) Truncated() bool { return b.truncated }

// Len returns the number of bytes retained.
func (b *LimitedBuffer) Len() int { return len(b.buf) }

// String returns the retained bytes.  When the capture was truncated
// the TruncationMarker is appended on its own line so consumers see
// the same signal the bash tool emits.
func (b *LimitedBuffer) String() string {
	if !b.truncated {
		return string(b.buf)
	}
	return string(b.buf) + "\n" + TruncationMarker
}
