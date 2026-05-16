package hook

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// maxAsyncInflight caps the number of concurrently running async hooks
// across the registry.  Beyond this limit new async dispatches are
// dropped with a warning.
const maxAsyncInflight = 32

// defaultTimeout is the per-hook timeout when none is specified.
const defaultTimeout = 5 * time.Second

// Registry holds the loaded hooks, indexed by event type.  Construct
// via [New] or [Load]; the zero value is a valid empty registry but
// Close must still be called.
type Registry struct {
	// hooks maps each event to its ordered list of hooks.
	hooks map[Event][]Hook

	// sem limits async goroutine count.
	sem chan struct{}

	// wg tracks in-flight async goroutines for Close.
	wg sync.WaitGroup
}

// New returns an empty Registry.
func New() *Registry {
	return &Registry{
		hooks: make(map[Event][]Hook),
		sem:   make(chan struct{}, maxAsyncInflight),
	}
}

// Register adds h to the registry for each of h's declared events.
// Duplicate names within an event are allowed (later hooks run later in
// the chain).
func (r *Registry) Register(h Hook) error {
	if h == nil {
		return fmt.Errorf("hook: Register: nil hook")
	}
	if h.Name() == "" {
		return fmt.Errorf("hook: Register: hook name must not be empty")
	}
	for _, ev := range h.Events() {
		r.hooks[ev] = append(r.hooks[ev], h)
	}
	return nil
}

// Unregister removes hooks with the given name from every event list. It is a
// no-op when the name is not present.
func (r *Registry) Unregister(name string) {
	if r == nil {
		return
	}
	for ev, hooks := range r.hooks {
		out := hooks[:0]
		for _, h := range hooks {
			if h.Name() != name {
				out = append(out, h)
			}
		}
		if len(out) == 0 {
			delete(r.hooks, ev)
			continue
		}
		r.hooks[ev] = out
	}
}

// For returns the hooks registered for event, in registration order.
func (r *Registry) For(event Event) []Hook {
	if r == nil {
		return nil
	}
	return append([]Hook(nil), r.hooks[event]...)
}

// All returns every hook registered, deduplicated by name (first seen
// wins), sorted by name for deterministic output.
func (r *Registry) All() []Hook {
	if r == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []Hook
	for _, ev := range []Event{EventPreTool, EventPostTool, EventPreMessage, EventPostMessage} {
		for _, h := range r.hooks[ev] {
			if _, ok := seen[h.Name()]; ok {
				continue
			}
			seen[h.Name()] = struct{}{}
			out = append(out, h)
		}
	}
	return out
}

// RunPre executes all sync hooks registered for a pre_* event in
// registration order, stopping on the first Deny.  Modify decisions
// accumulate: later hooks see the updated payload.
//
// Returns:
//   - out: the (possibly modified) Input.
//   - dec: Allow or Deny.
//   - denier: name of the hook that denied (empty when dec=Allow).
//   - reason: the deny reason (empty when dec=Allow).
//   - warns: non-fatal errors from hooks that fell open.
func (r *Registry) RunPre(
	ctx context.Context, event Event, in Input,
) (out Input, dec Decision, denier, reason string, warns []Warning) {
	if r == nil {
		return in, DecisionAllow, "", "", nil
	}
	out = in
	for _, h := range r.hooks[event] {
		if h.Mode() == ModeAsync {
			// Should not happen: load-time validation rejects async/pre
			// combinations.  Defensive skip.
			continue
		}
		act, err := h.Run(ctx, out)
		if err != nil {
			slog.Warn("hook: sync hook execution error; failing open",
				"hook", h.Name(), "err", err)
			warns = append(warns, Warning{HookName: h.Name(), Err: err.Error()})
			continue
		}
		switch act.Decision {
		case DecisionDeny:
			return out, DecisionDeny, h.Name(), act.Reason, warns
		case DecisionModify:
			applyPreModify(event, &out, act)
		}
		// Allow or empty decision: continue chain.
	}
	return out, DecisionAllow, "", "", warns
}

// RunPost executes hooks for a post_* event.  Sync hooks run in order
// (modify decisions accumulate).  Async hooks are dispatched in
// goroutines after sync hooks finish; they receive the post-sync-modified
// Input.
//
// If all post_message hooks are coerced to async at load time, this
// function is effectively async-only for that event.
func (r *Registry) RunPost(
	ctx context.Context, event Event, in Input,
) (out Input, warns []Warning) {
	if r == nil {
		return in, nil
	}
	out = in

	// Run sync hooks first.
	for _, h := range r.hooks[event] {
		if h.Mode() == ModeAsync {
			continue
		}
		act, err := h.Run(ctx, out)
		if err != nil {
			slog.Warn("hook: post sync hook execution error; failing open",
				"hook", h.Name(), "err", err)
			warns = append(warns, Warning{HookName: h.Name(), Err: err.Error()})
			continue
		}
		if act.Decision == DecisionModify {
			applyPostModify(event, &out, act)
		}
	}

	// Dispatch async hooks.  They receive the post-sync-modified payload.
	asyncIn := out
	for _, h := range r.hooks[event] {
		if h.Mode() != ModeAsync {
			continue
		}
		select {
		case r.sem <- struct{}{}:
		default:
			slog.Warn("hook: async hook cap reached; dropping",
				"hook", h.Name(), "cap", maxAsyncInflight)
			continue
		}
		r.wg.Add(1)
		go func(h Hook, payload Input) { //nolint:gosec // G118: async hooks intentionally use context.Background — they outlive the triggering request
			defer r.wg.Done()
			defer func() { <-r.sem }()

			runCtx := context.Background() //nolint:gosec // G118: intentional; async hooks are fire-and-forget
			if h.Timeout() > 0 {
				var cancel context.CancelFunc
				runCtx, cancel = context.WithTimeout(runCtx, h.Timeout())
				defer cancel()
			}
			act, err := h.Run(runCtx, payload)
			if err != nil {
				slog.Warn("hook: async hook execution error",
					"hook", h.Name(), "err", err)
				return
			}
			if act.Decision == DecisionModify {
				slog.Warn("hook: modify decision on async hook ignored",
					"hook", h.Name())
			}
		}(h, asyncIn)
	}

	return out, warns
}

// Close waits up to 2 s for in-flight async hooks to finish, then
// returns.  Idempotent.
func (r *Registry) Close() {
	if r == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		slog.Warn("hook: Close: timed out waiting for async hooks; some may still be running")
	}
}

// applyPreModify mutates out based on act for pre_* events.
func applyPreModify(event Event, out *Input, act Action) {
	switch event {
	case EventPreTool:
		if len(act.ModifiedToolInput) > 0 {
			out.ToolInput = act.ModifiedToolInput
		}
	case EventPreMessage:
		if act.ModifiedMessage != "" {
			out.Message = act.ModifiedMessage
		}
	}
}

// applyPostModify mutates out based on act for post_* events.
func applyPostModify(event Event, out *Input, act Action) {
	switch event {
	case EventPostTool:
		if act.ModifiedToolResult != nil {
			out.ToolResult = act.ModifiedToolResult
		}
	case EventPostMessage:
		if act.ModifiedMessage != "" {
			out.Message = act.ModifiedMessage
		}
	}
}

// ---------------------------------------------------------------------------
// TOML loading
// ---------------------------------------------------------------------------

// LoadOptions configures [Load].
type LoadOptions struct {
	// HomeDir overrides $HOME for tests.
	HomeDir string
	// XDGConfigHome overrides $XDG_CONFIG_HOME for tests.
	XDGConfigHome string
	// Pwd is the starting directory for the project walk-up.  When
	// empty no project layers are consulted.
	Pwd string
}

// Load assembles a Registry from hooks.toml at the standard four
// discovery paths, plus the built-in zero hooks.  Missing files are
// silently ignored.  Malformed entries log slog.Warn and are skipped.
func Load(opts LoadOptions) (*Registry, error) {
	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("hook: home dir: %w", err)
		}
		homeDir = h
	}
	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}

	reg := New()

	// Layer 1: ~/.agents/hooks.toml
	loadTOML(reg, filepath.Join(homeDir, ".agents", "hooks.toml"), "user")

	// Layer 2: ~/.config/hygge/hooks.toml
	loadTOML(reg, filepath.Join(xdgConfig, "hygge", "hooks.toml"), "user")

	// Layers 3+4: project walk-up.
	if opts.Pwd != "" {
		if p := findProjectFile(opts.Pwd, filepath.Join(".agents", "hooks.toml"), homeDir); p != "" {
			loadTOML(reg, p, "project")
		}
		if p := findProjectFile(opts.Pwd, filepath.Join(".hygge", "hooks.toml"), homeDir); p != "" {
			loadTOML(reg, p, "project")
		}
	}

	return reg, nil
}

// ---------------------------------------------------------------------------
// TOML schema
// ---------------------------------------------------------------------------

// tomlFile is the top-level shape of hooks.toml.
type tomlFile struct {
	Hooks map[string]tomlEntry `toml:"hooks"`
}

// tomlEntry is one [hooks.<name>] block.
type tomlEntry struct {
	Description string            `toml:"description"`
	Events      []string          `toml:"events"`
	Command     string            `toml:"command"`
	Args        []string          `toml:"args"`
	Timeout     string            `toml:"timeout"`
	Mode        string            `toml:"mode"`
	FailClosed  bool              `toml:"fail_closed"` // reserved; default false
	Env         map[string]string `toml:"env"`
}

// loadTOML reads path, parses it, and registers the resulting hooks into
// reg under source.  A missing file is silently ignored; any other error
// is logged.
func loadTOML(reg *Registry, path, source string) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from controlled discovery roots
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("hook: read failed", "path", path, "err", err)
		}
		return
	}

	var schema tomlFile
	if err := toml.Unmarshal(data, &schema); err != nil {
		slog.Warn("hook: parse failed", "path", path, "err", err)
		return
	}

	for name, entry := range schema.Hooks {
		h, err := buildShellHook(name, entry, source)
		if err != nil {
			slog.Warn("hook: skipping entry", "path", path, "name", name, "err", err)
			continue
		}
		if err := reg.Register(h); err != nil {
			slog.Warn("hook: register failed", "path", path, "name", name, "err", err)
		}
	}
}

// validEvents is the set of known event strings.
var validEvents = map[Event]bool{
	EventPreTool:     true,
	EventPostTool:    true,
	EventPreMessage:  true,
	EventPostMessage: true,
}

// preEvents is the set of events where async mode is rejected.
var preEvents = map[Event]bool{
	EventPreTool:    true,
	EventPreMessage: true,
}

// buildShellHook validates the TOML entry and returns a *shellHook.
func buildShellHook(name string, e tomlEntry, source string) (*shellHook, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("hook name must not be empty")
	}
	if e.Command == "" {
		return nil, fmt.Errorf("command is required")
	}
	if len(e.Events) == 0 {
		return nil, fmt.Errorf("events must be non-empty")
	}

	// Parse and validate events.
	var events []Event
	for _, raw := range e.Events {
		ev := Event(strings.TrimSpace(raw))
		if !validEvents[ev] {
			slog.Warn("hook: unknown event; skipping for this hook",
				"hook", name, "event", raw)
			continue
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		return nil, fmt.Errorf("no valid events remain after filtering")
	}

	// Parse mode.
	mode := ModeSync
	if raw := strings.TrimSpace(strings.ToLower(e.Mode)); raw == "async" {
		mode = ModeAsync
	}

	// Reject async on pre_* events.
	if mode == ModeAsync {
		for _, ev := range events {
			if preEvents[ev] {
				slog.Warn("hook: async mode is not valid for pre_* events; skipping hook",
					"hook", name, "event", ev)
				return nil, fmt.Errorf("async mode not valid for pre_* event %q", ev)
			}
		}
	}

	// Coerce post_message sync → async (post_message is always async).
	for _, ev := range events {
		if ev == EventPostMessage && mode == ModeSync {
			slog.Warn("hook: post_message hooks are always async; coercing mode",
				"hook", name)
			mode = ModeAsync
			break
		}
	}

	// Parse timeout.
	timeout := defaultTimeout
	if t := strings.TrimSpace(e.Timeout); t != "" {
		d, err := time.ParseDuration(t)
		if err != nil {
			slog.Warn("hook: invalid timeout; using default",
				"hook", name, "timeout", t, "err", err)
		} else {
			timeout = d
		}
	}

	return &shellHook{
		name:        name,
		description: strings.TrimSpace(e.Description),
		source:      source,
		events:      events,
		mode:        mode,
		timeout:     timeout,
		command:     e.Command,
		args:        append([]string(nil), e.Args...),
		env:         e.Env,
	}, nil
}

// findProjectFile walks parents of start looking for a file at the
// relative path rel.  The walk halts at the first directory containing
// `.git`, the first match, $HOME, or the filesystem root.  Mirrors the
// convention used by internal/mcp, internal/skill, and internal/subagent.
func findProjectFile(start, rel, homeStop string) string {
	dir := filepath.Clean(start)
	homeStop = filepath.Clean(homeStop)
	for {
		if homeStop != "" && dir == homeStop {
			return ""
		}
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
