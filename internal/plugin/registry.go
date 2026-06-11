package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/command"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/subagent"
	"github.com/cfbender/hygge/internal/tool"
)

// RegistryOptions configures a plugin Registry.
type RegistryOptions struct {
	// CacheDir is the root directory for plugin caches.
	// Defaults to $XDG_STATE_HOME/hygge/plugins.
	CacheDir string

	// ToolRegistry receives plugin-registered tools.  Required.
	Tools *tool.Registry

	// HookRegistry receives plugin-registered hooks.  Required.
	Hooks *hook.Registry

	// CommandRegistry receives plugin-registered commands.  Required.
	Commands *command.Registry

	// SubagentRegistry receives plugin-registered subagent types.  Required.
	Subagents *subagent.Registry

	// Permission is the engine used by Exec calls from plugins.  Required.
	Permission *permission.Engine

	// InjectMessage, when non-nil, is called by plugins that invoke
	// hygge.send_message.  Typically wired to agent.Agent.InjectMessage.
	InjectMessage func(ctx context.Context, sessionID, role, content string) error

	// PluginConfigs maps plugin names to their [plugins.<name>] TOML tables.
	PluginConfigs map[string]map[string]any

	// ProfileDir is the resolved active profile directory.  Plugins can use
	// this to load files that live next to the profile config.  May be empty
	// when no profile is active or the profile is the unnamed default.
	ProfileDir string

	// Pwd is the working directory for Exec calls.
	Pwd string

	// Loaders is the ordered set of loaders to try.  When nil,
	// defaultLoaders() is used.
	Loaders []Loader
}

// OwnedRegistration tracks which plugin registered a tool/hook/command/subagent
// so they can be cleaned up in a future reload operation.
type OwnedRegistration struct {
	Owner string // plugin name; "" for builtins
	Name  string
	Kind  string // "tool", "hook", "command", "subagent"
}

// Registry manages the set of installed plugins and dispatches lifecycle calls.
type Registry struct {
	opts RegistryOptions
	pm   *PackageManager

	mu      sync.RWMutex
	plugins map[string]Plugin   // name → Plugin
	sources map[string]Source   // name → Source
	owned   []OwnedRegistration // for future unregister support
	loaders []Loader
}

// NewRegistry constructs an empty Registry.
func NewRegistry(opts RegistryOptions) (*Registry, error) {
	if opts.Tools == nil {
		return nil, fmt.Errorf("plugin: NewRegistry: Tools is required")
	}
	if opts.Hooks == nil {
		return nil, fmt.Errorf("plugin: NewRegistry: Hooks is required")
	}
	if opts.Commands == nil {
		return nil, fmt.Errorf("plugin: NewRegistry: Commands is required")
	}
	if opts.Subagents == nil {
		return nil, fmt.Errorf("plugin: NewRegistry: Subagents is required")
	}
	// Permission is required in production (Exec calls need it) but tests
	// can pass nil to skip permission checks on Exec and CategoryPlugin.
	// A nil Permission means "allow all" — only appropriate in test contexts.

	cacheDir := opts.CacheDir
	if cacheDir == "" {
		cacheDir = resolveCacheDir()
	}

	loaders := opts.Loaders
	if loaders == nil {
		loaders = defaultLoaders()
	}

	return &Registry{
		opts:    opts,
		pm:      NewPackageManager(cacheDir),
		plugins: make(map[string]Plugin),
		sources: make(map[string]Source),
		loaders: loaders,
	}, nil
}

// LoadAll loads every plugin from the given list of source URIs.
// Failures in individual plugins are logged and skipped — one bad plugin
// should not block the rest from loading.
func (r *Registry) LoadAll(ctx context.Context, sourceURIs []string) {
	for _, uri := range sourceURIs {
		if err := r.loadOne(ctx, uri); err != nil {
			slog.Warn("plugin: failed to load plugin; skipping",
				"source", uri, "err", err)
		}
	}
}

// Install resolves, validates, and loads a plugin from the given source URI.
// Unlike LoadAll (which silently skips failures), Install returns an error
// so the CLI can surface it to the user.
func (r *Registry) Install(ctx context.Context, uri string) error {
	return r.loadOne(ctx, uri)
}

// loadOne resolves, validates, and loads a single plugin from a source URI.
func (r *Registry) loadOne(ctx context.Context, uri string) error {
	src, err := ParseSource(uri)
	if err != nil {
		return err
	}

	dir, err := r.pm.Resolve(ctx, src)
	if err != nil {
		return fmt.Errorf("plugin: resolve %q: %w", uri, err)
	}

	fallback := sourceBaseName(uri)
	manifest, err := findOrSynthesiseManifest(dir, fallback)
	if err != nil {
		return err
	}

	// Deduplicate by name.
	r.mu.Lock()
	if _, exists := r.plugins[manifest.Name]; exists {
		r.mu.Unlock()
		slog.Warn("plugin: duplicate plugin name; skipping",
			"name", manifest.Name, "source", uri)
		return nil
	}
	r.mu.Unlock()

	loader, err := resolveLoader(r.loaders, dir, manifest)
	if err != nil {
		return err
	}

	p, err := loader.Load(manifest.Name, uri, dir, manifest)
	if err != nil {
		return fmt.Errorf("plugin: loader.Load %q: %w", uri, err)
	}

	host := r.newPluginHost(manifest.Name)
	if err := p.Load(ctx, host); err != nil {
		r.UnregisterAll(manifest.Name)
		_ = p.Close(ctx)
		return fmt.Errorf("plugin: load %q: %w", manifest.Name, err)
	}

	r.mu.Lock()
	r.plugins[manifest.Name] = p
	r.sources[manifest.Name] = src
	r.mu.Unlock()

	slog.Info("plugin: loaded", "name", manifest.Name, "source", uri)
	return nil
}

// Get returns the plugin with the given name, or (nil, false).
func (r *Registry) Get(name string) (Plugin, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins[name]
	return p, ok
}

// List returns all loaded plugins, sorted by name.
func (r *Registry) List() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p)
	}
	return out
}

// Source returns the Source for a plugin name, or zero-value and false.
func (r *Registry) Source(name string) (Source, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sources[name]
	return s, ok
}

// Close closes all loaded plugins.
func (r *Registry) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var firstErr error
	for _, p := range r.plugins {
		if err := p.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Update re-fetches a plugin's source and reloads it.
func (r *Registry) Update(ctx context.Context, name string) error {
	r.mu.Lock()
	p, ok := r.plugins[name]
	src, srcOk := r.sources[name]
	r.mu.Unlock()

	if !ok || !srcOk {
		return fmt.Errorf("plugin: %q not found", name)
	}

	if err := r.pm.Update(ctx, src); err != nil {
		return fmt.Errorf("plugin: update %q: %w", name, err)
	}

	// Remove old host registrations before closing and reloading so updated
	// plugin declarations can claim the same tool/hook/command/subagent names.
	r.UnregisterAll(name)
	if err := p.Close(ctx); err != nil {
		slog.Warn("plugin: close error during update", "name", name, "err", err)
	}

	r.mu.Lock()
	delete(r.plugins, name)
	delete(r.sources, name)
	r.mu.Unlock()

	return r.loadOne(ctx, src.Raw)
}

// Remove closes a plugin and deletes its cache.
func (r *Registry) Remove(ctx context.Context, name string) error {
	r.mu.Lock()
	p, ok := r.plugins[name]
	src, srcOk := r.sources[name]
	if ok {
		delete(r.plugins, name)
	}
	if srcOk {
		delete(r.sources, name)
	}
	r.mu.Unlock()

	if ok {
		r.UnregisterAll(name)
		if err := p.Close(ctx); err != nil {
			slog.Warn("plugin: close error during remove", "name", name, "err", err)
		}
	}
	if srcOk {
		if err := r.pm.Remove(src); err != nil {
			return fmt.Errorf("plugin: remove cache: %w", err)
		}
	}
	return nil
}

// OwnedRegistrations returns a copy of the owned registration list.
// Useful for auditing which plugin contributed which tool/hook/command/subagent.
func (r *Registry) OwnedRegistrations() []OwnedRegistration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]OwnedRegistration, len(r.owned))
	copy(out, r.owned)
	return out
}

// UnregisterAll removes every host registration owned by pluginName from the
// tool, hook, slash-command, and subagent registries. It is safe to call for a
// plugin that has no tracked registrations.
func (r *Registry) UnregisterAll(pluginName string) {
	r.mu.Lock()
	owned := r.owned[:0]
	toRemove := make([]OwnedRegistration, 0)
	for _, reg := range r.owned {
		if reg.Owner == pluginName {
			toRemove = append(toRemove, reg)
			continue
		}
		owned = append(owned, reg)
	}
	r.owned = owned
	r.mu.Unlock()

	for _, reg := range toRemove {
		switch reg.Kind {
		case "tool":
			r.opts.Tools.Unregister(reg.Name)
		case "hook":
			r.opts.Hooks.Unregister(reg.Name)
		case "command":
			r.opts.Commands.Unregister(reg.Name)
		case "subagent":
			r.opts.Subagents.Unregister(reg.Name)
		}
	}
}

// PM returns the underlying PackageManager, for CLI commands that need to
// inspect cache directories.
func (r *Registry) PM() *PackageManager { return r.pm }

// SetInjectMessage wires the InjectMessage callback after construction.
// This allows the plugin registry to be created before the agent is
// available (avoiding a circular dependency), with the callback set
// once the agent is constructed.
func (r *Registry) SetInjectMessage(fn func(ctx context.Context, sessionID, role, content string) error) {
	r.opts.InjectMessage = fn
	// Update any existing plugin hosts — they hold a pointer to registryHost
	// which reads r.opts.InjectMessage at call time, so this is safe.
}

// newPluginHost creates a Host implementation for a specific plugin.
func (r *Registry) newPluginHost(pluginName string) Host {
	return &registryHost{
		reg:        r,
		pluginName: pluginName,
	}
}

// registryHost implements Host for a single plugin.  It wraps the Registry
// and routes calls into the actual host registries.
type registryHost struct {
	reg        *Registry
	pluginName string
}

func (h *registryHost) PluginName() string { return h.pluginName }

func (h *registryHost) RegisterTool(t PluginTool) error {
	if t.Name == "" {
		return fmt.Errorf("plugin: %s: RegisterTool: name is required", h.pluginName)
	}
	if t.Execute == nil {
		return fmt.Errorf("plugin: %s: RegisterTool: execute is required", h.pluginName)
	}
	if len(t.InputSchema) == 0 {
		t.InputSchema = json.RawMessage(`{"type":"object","properties":{}}`)
	}

	wrapped := &pluginToolAdapter{
		pluginName:     h.pluginName,
		name:           t.Name,
		description:    t.Description,
		inputSchema:    t.InputSchema,
		parallelizable: t.Parallelizable,
		execute:        t.Execute,
	}
	if err := h.reg.opts.Tools.Register(wrapped); err != nil {
		return fmt.Errorf("plugin: %s: register tool %q: %w", h.pluginName, t.Name, err)
	}

	h.reg.mu.Lock()
	h.reg.owned = append(h.reg.owned, OwnedRegistration{
		Owner: h.pluginName,
		Name:  t.Name,
		Kind:  "tool",
	})
	h.reg.mu.Unlock()

	slog.Debug("plugin: registered tool", "plugin", h.pluginName, "tool", t.Name)
	return nil
}

func (h *registryHost) RegisterHook(reg HookRegistration) error {
	if reg.Name == "" {
		return fmt.Errorf("plugin: %s: RegisterHook: name is required", h.pluginName)
	}
	if reg.Handler == nil {
		return fmt.Errorf("plugin: %s: RegisterHook: handler is required", h.pluginName)
	}
	if reg.Timeout == 0 {
		reg.Timeout = 5 * time.Second
	}

	wrapped := &pluginHookAdapter{
		name:    reg.Name,
		source:  "plugin:" + h.pluginName,
		events:  []hook.Event{reg.Event},
		mode:    reg.Mode,
		timeout: reg.Timeout,
		handler: reg.Handler,
	}
	if err := h.reg.opts.Hooks.Register(wrapped); err != nil {
		return fmt.Errorf("plugin: %s: register hook %q: %w", h.pluginName, reg.Name, err)
	}

	h.reg.mu.Lock()
	h.reg.owned = append(h.reg.owned, OwnedRegistration{
		Owner: h.pluginName,
		Name:  reg.Name,
		Kind:  "hook",
	})
	h.reg.mu.Unlock()

	slog.Debug("plugin: registered hook", "plugin", h.pluginName, "hook", reg.Name)
	return nil
}

func (h *registryHost) RegisterCommand(reg CommandRegistration) error {
	if reg.Name == "" {
		return fmt.Errorf("plugin: %s: RegisterCommand: name is required", h.pluginName)
	}
	if reg.Execute == nil {
		return fmt.Errorf("plugin: %s: RegisterCommand: execute is required", h.pluginName)
	}

	wrapped := &pluginCommandAdapter{
		name:        reg.Name,
		description: reg.Description,
		source:      "plugin:" + h.pluginName,
		args:        reg.Args,
		execute:     reg.Execute,
	}
	if err := h.reg.opts.Commands.Register(wrapped); err != nil {
		return fmt.Errorf("plugin: %s: register command %q: %w", h.pluginName, reg.Name, err)
	}

	h.reg.mu.Lock()
	h.reg.owned = append(h.reg.owned, OwnedRegistration{
		Owner: h.pluginName,
		Name:  reg.Name,
		Kind:  "command",
	})
	h.reg.mu.Unlock()

	slog.Debug("plugin: registered command", "plugin", h.pluginName, "command", reg.Name)
	return nil
}

func (h *registryHost) RegisterSubagent(reg SubagentRegistration) error {
	if reg.Name == "" {
		return fmt.Errorf("plugin: %s: RegisterSubagent: name is required", h.pluginName)
	}

	t := subagent.Type{
		Name:         reg.Name,
		Description:  reg.Description,
		SystemPrompt: reg.SystemPrompt,
		Tools:        reg.Tools,
		Model:        reg.Model,
		Source:       "plugin:" + h.pluginName,
	}
	if err := h.reg.opts.Subagents.Register(t); err != nil {
		return fmt.Errorf("plugin: %s: register subagent %q: %w", h.pluginName, reg.Name, err)
	}

	h.reg.mu.Lock()
	h.reg.owned = append(h.reg.owned, OwnedRegistration{
		Owner: h.pluginName,
		Name:  reg.Name,
		Kind:  "subagent",
	})
	h.reg.mu.Unlock()

	slog.Debug("plugin: registered subagent", "plugin", h.pluginName, "subagent", reg.Name)
	return nil
}

func (h *registryHost) SendMessage(ctx context.Context, sessionID, role, content string) error {
	if h.reg.opts.InjectMessage == nil {
		slog.Warn("plugin: send_message called but no InjectMessage handler is wired",
			"plugin", h.pluginName)
		return nil
	}
	return h.reg.opts.InjectMessage(ctx, sessionID, role, content)
}

func (h *registryHost) Notify(level, message string) {
	// For now, route to slog.  A future version can publish to the bus.
	switch level {
	case "error":
		slog.Error("plugin: notify", "plugin", h.pluginName, "message", message)
	case "warn":
		slog.Warn("plugin: notify", "plugin", h.pluginName, "message", message)
	default:
		slog.Info("plugin: notify", "plugin", h.pluginName, "message", message)
	}
}

func (h *registryHost) Log(level, message string, fields map[string]any) {
	args := make([]any, 0, len(fields)*2+2)
	args = append(args, "plugin", h.pluginName)
	for k, v := range fields {
		args = append(args, k, v)
	}
	switch level {
	case "error":
		slog.Error(message, args...)
	case "warn":
		slog.Warn(message, args...)
	case "debug":
		slog.Debug(message, args...)
	default:
		slog.Info(message, args...)
	}
}

func (h *registryHost) Exec(ctx context.Context, cmdStr string, args []string, opts ExecOptions) (ExecResult, error) {
	// Gate through the permission engine for CategoryShell.
	if h.reg.opts.Permission != nil {
		req := permission.Request{
			Category: permission.CategoryShell,
			Target:   cmdStr + " " + strings.Join(args, " "),
			Command:  cmdStr,
			ToolName: "plugin:" + h.pluginName,
		}
		if err := permission.Gate(ctx, h.reg.opts.Permission, req); err != nil {
			var denied *permission.DeniedError
			if !errors.As(err, &denied) {
				return ExecResult{}, fmt.Errorf("plugin: exec: permission ask: %w", err)
			}
			return ExecResult{
				Stderr: denied.Error(),
				Code:   1,
			}, nil
		}
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx2, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx2, cmdStr, args...) //nolint:gosec // gated by permission engine
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	} else if h.reg.opts.Pwd != "" {
		cmd.Dir = h.reg.opts.Pwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	code := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			code = exitErr.ExitCode()
			runErr = nil
		}
	}
	return ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
		Code:   code,
	}, runErr
}

func (h *registryHost) Config() map[string]any {
	if h.reg.opts.PluginConfigs == nil {
		return nil
	}
	return h.reg.opts.PluginConfigs[h.pluginName]
}

func (h *registryHost) ProfileDir() string {
	return h.reg.opts.ProfileDir
}

var _ Host = (*registryHost)(nil)

// --- Adapter types ---

// pluginToolAdapter wraps a plugin's PluginTool to satisfy tool.Tool.
type pluginToolAdapter struct {
	pluginName     string
	name           string
	description    string
	inputSchema    json.RawMessage
	parallelizable bool
	execute        func(ctx context.Context, input json.RawMessage) (PluginToolResult, error)
}

func (a *pluginToolAdapter) Name() string         { return a.name }
func (a *pluginToolAdapter) Description() string  { return a.description }
func (a *pluginToolAdapter) Parallelizable() bool { return a.parallelizable }
func (a *pluginToolAdapter) InputSchema() map[string]any {
	if len(a.inputSchema) == 0 {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	var m map[string]any
	if err := json.Unmarshal(a.inputSchema, &m); err != nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return m
}
func (a *pluginToolAdapter) Execute(ctx context.Context, args json.RawMessage, ec tool.ExecContext) (tool.Result, error) {
	// Gate with CategoryPlugin permission.
	if ec.Permission != nil {
		req := permission.Request{
			SessionID: ec.SessionID,
			Category:  permission.CategoryPlugin,
			Target:    a.name,
			ToolName:  a.name,
			Pwd:       ec.Pwd,
		}
		if err := permission.Gate(ctx, ec.Permission, req); err != nil {
			var denied *permission.DeniedError
			if !errors.As(err, &denied) {
				return tool.Result{}, &tool.ToolError{Code: tool.CodePermissionDenied, Message: err.Error(), Wrapped: err}
			}
			return tool.Result{IsError: true, Content: "permission denied: " + denied.Reason}, nil
		}
	}

	res, err := a.execute(ctx, args)
	if err != nil {
		return tool.Result{IsError: true, Content: err.Error()}, nil
	}
	return tool.Result{
		Content: res.Content,
		IsError: res.IsError,
	}, nil
}

// pluginHookAdapter wraps a plugin's HookRegistration to satisfy hook.Hook.
type pluginHookAdapter struct {
	name    string
	source  string
	events  []hook.Event
	mode    hook.Mode
	timeout time.Duration
	handler func(ctx context.Context, in hook.Input) (hook.Action, error)
}

func (a *pluginHookAdapter) Name() string           { return a.name }
func (a *pluginHookAdapter) Description() string    { return "plugin hook" }
func (a *pluginHookAdapter) Source() string         { return a.source }
func (a *pluginHookAdapter) Events() []hook.Event   { return a.events }
func (a *pluginHookAdapter) Mode() hook.Mode        { return a.mode }
func (a *pluginHookAdapter) Timeout() time.Duration { return a.timeout }
func (a *pluginHookAdapter) Run(ctx context.Context, in hook.Input) (hook.Action, error) {
	return a.handler(ctx, in)
}

// pluginCommandAdapter wraps a plugin's CommandRegistration to satisfy command.Command.
type pluginCommandAdapter struct {
	name        string
	description string
	source      string
	args        []command.ArgSpec
	execute     func(ctx context.Context, input string) (string, error)
}

func (a *pluginCommandAdapter) Name() string            { return a.name }
func (a *pluginCommandAdapter) Description() string     { return a.description }
func (a *pluginCommandAdapter) Source() string          { return a.source }
func (a *pluginCommandAdapter) Args() []command.ArgSpec { return a.args }
func (a *pluginCommandAdapter) Execute(ctx context.Context, _ command.App, input string) (command.Outcome, error) {
	msg, err := a.execute(ctx, input)
	if err != nil {
		return command.Outcome{Notice: fmt.Sprintf("plugin error: %s", err)}, nil
	}
	return command.Outcome{Message: msg}, nil
}
