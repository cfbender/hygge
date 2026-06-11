// Package plugin implements a Lua-based plugin host for hygge.
//
// # Overview
//
// Plugins are portable .lua scripts (or directories containing one) that
// register into hygge's existing tool / hook / command / subagent registries
// through a neovim-style hygge.* global module.  Sources are declared in
// config.toml as github: or local: URIs and managed via
// `hygge plugins install/update/remove/list/show`.
//
// # Architecture
//
// The core abstractions are:
//
//   - [Plugin]: one loaded plugin. Implementations: luaPlugin today,
//     subprocessPlugin reserved for later. The Registry owns plugins and
//     dispatches lifecycle calls into them.
//   - [Host]: the bridge from a plugin into hygge's running state.
//   - [Loader]: the extension point that decides which runtime to use for
//     a given plugin manifest.
//   - [Registry]: manages the set of installed plugins and their lifecycle.
//
// # Lua plugin lifecycle
//
// When a Lua plugin is loaded the script is executed top-to-bottom inside a
// sandboxed gopher-lua LState.  During that initial execution the script
// calls hygge.register_tool / hygge.register_hook / etc. to declare its
// contributions.  After Load returns, the registered adapters are live in
// the host registries.  The LState is kept alive for the process lifetime;
// each adapter invocation acquires a per-plugin mutex before touching the
// LState.
//
// # Concurrency
//
// gopher-lua's LState is NOT safe for concurrent use.  Each luaPlugin holds
// a single LState and a sync.Mutex that serialises all calls into it.
// Concurrent tool/hook/command invocations are queued per plugin.  This is
// by design: plugin code is rarely the bottleneck, and the alternative
// (one LState per goroutine with shared state through channels) is vastly
// more complex.
//
// # Error handling
//
// Any Lua runtime error during a handler causes the handler to fail-open
// (return Allow / pass-through) with a slog.Warn — mirroring the shell-hook
// contract established in the hook package (T1.4).  Errors during Load
// surface as proper Go errors so the Registry can skip the offending plugin
// without crashing the others.
//
// # Forward compatibility: subprocess plugins
//
// The Plugin and Host interfaces are implementation-agnostic.  A future
// subprocess JSON-RPC loader can implement Plugin / Host and slot in without
// touching the Registry or any caller.  The Loader interface is the declared
// extension point.  A stub SubprocessLoader is included in this package to
// keep the abstraction honest; it always errors.
package plugin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/hook"
)

// Plugin is one loaded plugin.
//
// Implementations: luaPlugin today, subprocessPlugin tomorrow.  The Registry
// owns plugins and dispatches lifecycle calls into them.
//
// Implementations need not be safe for concurrent calls to non-event methods
// (Load, Close).  Execute/handler dispatch methods MUST be safe for
// concurrent use since multiple sessions may invoke a plugin simultaneously —
// the luaPlugin implementation satisfies this through a per-plugin mutex.
type Plugin interface {
	// Name returns the plugin's unique identifier (derived from the manifest
	// or the source URI basename).
	Name() string

	// Source returns the URI the plugin was installed from, e.g.
	// "github:cfbender/hygge-policy-guard" or
	// "local:/Users/cfb/code/my-plugin".
	Source() string

	// Manifest returns the parsed manifest for this plugin.
	Manifest() Manifest

	// Load runs the plugin's initialisation.  For Lua this executes the
	// script top-to-bottom in its sandbox.  A future subprocess plugin
	// would spawn the process and exchange the initialize handshake.
	//
	// Load is called exactly once, before any dispatch.  Calling Load twice
	// is a programmer error.
	Load(ctx context.Context, h Host) error

	// Close releases resources.  For Lua, closes the LState; for a
	// subprocess, sends shutdown and force-kills on timeout.  Idempotent.
	Close(ctx context.Context) error
}

// Host is the bridge from a plugin into hygge's running state.
//
// Every Plugin implementation receives a Host at Load time.  The Lua loader
// implements gopher-lua bindings that route to Host; a future subprocess
// loader would implement a JSON-RPC dispatcher that does the same.
//
// Host MUST be safe for concurrent use.
type Host interface {
	// PluginName returns the calling plugin's name, used for logging and
	// rate-limit accounting.  Each plugin's bindings carry their own Host
	// wrapper that fills this in.
	PluginName() string

	RegisterTool(PluginTool) error
	RegisterHook(HookRegistration) error
	RegisterCommand(CommandRegistration) error
	RegisterSubagent(SubagentRegistration) error

	// SendMessage injects a message into sessionID with the given role and
	// content.  Role must be "user" or "assistant".  Plugin calls are rate-
	// limited to 10 per turn to prevent runaway loops.
	SendMessage(ctx context.Context, sessionID, role, content string) error

	// Notify emits a user-visible notification.  level is "info", "warn",
	// or "error".
	Notify(level, message string)

	// Log writes a structured log entry under the plugin's name.
	Log(level, message string, fields map[string]any)

	// Exec runs a subprocess.  The call goes through the permission engine
	// under CategoryShell so a plugin cannot bypass user policy.
	Exec(ctx context.Context, command string, args []string, opts ExecOptions) (ExecResult, error)

	// Config returns the [plugins.<plugin-name>] TOML table as a generic
	// map.  Empty when no overrides are set.
	Config() map[string]any

	// ProfileDir returns the resolved active profile directory, or "" when
	// no named profile is active.  Plugins can use this to load files that
	// live adjacent to the active profile config.
	ProfileDir() string
}

// PluginTool describes a tool being registered by a plugin.
//
//nolint:revive // PluginTool is intentionally named to distinguish from tool.Tool (the interface in internal/tool)
type PluginTool struct {
	// Name is the stable identifier the model uses to invoke this tool.
	// Must match [a-z][a-z0-9_]*.
	Name string

	// Description is the human-language summary surfaced to the model.
	Description string

	// InputSchema is a JSON Schema object for the model.  When nil, an
	// empty object schema is synthesised.
	InputSchema json.RawMessage

	// Parallelizable controls whether the tool may be invoked concurrently
	// with other parallelizable tools in the same turn.  Defaults to false
	// for safety — plugin tools should only set this to true when their
	// Execute function has no observable side effects beyond reading state
	// (e.g. searching a local index, calling a read-only external API).
	//
	// Plugin tools registered with Parallelizable = true are subject to the
	// same concurrency semantics as built-in parallel tools: their bus events
	// arrive in undefined order relative to sibling parallel calls, and the
	// gopher-lua LState mutex serialises execution within a single Lua plugin
	// even when Parallelizable is true.
	Parallelizable bool

	// Execute runs the tool.
	Execute func(ctx context.Context, input json.RawMessage) (PluginToolResult, error)
}

// PluginToolResult is the outcome of a plugin tool call.
//
//nolint:revive // PluginToolResult parallels PluginTool naming convention
type PluginToolResult struct {
	Content string
	IsError bool
}

// HookRegistration describes a hook being registered by a plugin.
type HookRegistration struct {
	// Name is the unique hook name.
	Name string

	// Event is the hook.Event this hook fires for.
	Event hook.Event

	// Mode is the hook execution mode.  Defaults to hook.ModeSync.
	Mode hook.Mode

	// Timeout is the per-invocation timeout.  Defaults to 5 s.
	Timeout time.Duration

	// Handler is the Go function to invoke.
	Handler func(ctx context.Context, in hook.Input) (hook.Action, error)
}

// CommandRegistration describes a slash command being registered by a plugin.
type CommandRegistration struct {
	// Name is the command name without the leading slash.
	Name string

	// Description is the one-line summary.
	Description string

	// Args declares the named arguments.
	Args []command.ArgSpec

	// Execute runs the command.  Returns the outcome text (surfaced as
	// Outcome.Message).
	Execute func(ctx context.Context, input string) (string, error)
}

// SubagentRegistration describes a sub-agent type being registered by a plugin.
type SubagentRegistration struct {
	// Name is the stable identifier.
	Name string

	// Description is the one-line summary.
	Description string

	// SystemPrompt is the full system prompt.
	SystemPrompt string

	// Tools is the tool-name allowlist.  Empty means "default sub-agent
	// tools".
	Tools []string

	// Model, when non-empty, overrides the parent's provider/model.
	// Shape: "<provider>/<model-id>".
	Model string
}

// ExecOptions configures a plugin Exec call.
type ExecOptions struct {
	// Env is extra environment variables to pass.  Merged on top of
	// the parent's environment filtered through procenv.Allowlist.
	Env map[string]string

	// Dir is the working directory; empty means the session's pwd.
	Dir string

	// Timeout caps the subprocess runtime.  Zero means 30 s.
	Timeout time.Duration
}

// ExecResult holds the result of a plugin Exec call.
type ExecResult struct {
	Stdout string
	Stderr string
	Code   int
}
