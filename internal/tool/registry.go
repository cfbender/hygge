package tool

import (
	"fmt"
	"sort"
	"sync"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/skill"
)

// Registry holds a named set of tools and the cross-tool state they share
// (currently just the read-tracker used for anti-clobber on write/edit).
//
// Registries are safe for concurrent use; Register may be called from any
// goroutine and Get/All/AsProviderTools never block writers.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	reads *readTracker
	todos *todoStore
}

// NewRegistry returns an empty Registry with a fresh read-tracker.
func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
		reads: newReadTracker(),
		todos: newTodoStore(),
	}
}

// Register adds t to the registry under t.Name().  Returns an error when
// the name is empty or already in use; the registry is unchanged on error.
func (r *Registry) Register(t Tool) error {
	if t == nil {
		return fmt.Errorf("tool: Register: nil tool")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("tool: Register: tool name is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool: Register: duplicate name %q", name)
	}
	r.tools[name] = t
	return nil
}

// Get returns the tool registered under name and true, or (nil, false) if
// no such tool exists.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns every registered tool, sorted by name.  The returned slice
// is a fresh copy; mutating it does not affect the registry.
func (r *Registry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// AsProviderTools converts the registry into the slice the provider layer
// consumes when constructing a [provider.Request].  Output is sorted by
// name so the model sees a deterministic tool list.
func (r *Registry) AsProviderTools() []provider.Tool {
	all := r.All()
	out := make([]provider.Tool, 0, len(all))
	for _, t := range all {
		out = append(out, provider.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			InputSchema: t.InputSchema(),
		})
	}
	return out
}

// ReadTracker returns the registry's anti-clobber read tracker.  Exposed
// for tests and for callers that build their own tool set sharing the
// same tracker as the built-ins.
func (r *Registry) ReadTracker() *readTracker { //nolint:revive // exported intentionally; type is package-internal by design
	return r.reads
}

// Default returns a Registry preloaded with the built-in tools. The returned
// registry owns its own read-tracker and todo store; the built-ins are wired
// to use them.
//
// Equivalent to DefaultWith(DefaultOptions{}).
func Default() *Registry {
	return DefaultWith(DefaultOptions{})
}

// DefaultOptions configures DefaultWith.  Add fields here when a new
// built-in needs caller-supplied dependencies.
type DefaultOptions struct {
	// SkillRegistry, when non-nil, causes the returned tool registry to
	// include the "skill" tool wired to it.  When nil the skill tool is
	// omitted; the model never sees it in the tool list.
	SkillRegistry *skill.Registry
}

// DefaultWith returns a Registry preloaded with the built-in tools, plus any
// optional tools enabled by opts. Callers that need the skill tool pass
// DefaultOptions{SkillRegistry: reg}.
func DefaultWith(opts DefaultOptions) *Registry {
	r := NewRegistry()
	mustRegister(r, newReadTool(r.reads))
	mustRegister(r, newWriteTool(r.reads))
	mustRegister(r, newEditTool(r.reads))
	mustRegister(r, newBashTool())
	mustRegister(r, newGrepTool())
	mustRegister(r, newGlobTool())
	mustRegister(r, newTodoTool(r.todos))
	if opts.SkillRegistry != nil {
		mustRegister(r, NewSkillTool(opts.SkillRegistry))
	}
	return r
}

// mustRegister panics if Register fails; only used for the built-ins
// where a duplicate name would be a programmer error.
func mustRegister(r *Registry, t Tool) {
	if err := r.Register(t); err != nil {
		panic(fmt.Sprintf("tool: Default: %v", err))
	}
}

// readTracker is the per-session set of "this file was read this session"
// markers used by write and edit to prevent unconditional overwrites.
//
// The zero value is not usable; construct with newReadTracker.
type readTracker struct {
	mu   sync.RWMutex
	seen map[string]map[string]struct{} // sessionID -> set of abs paths
}

// newReadTracker constructs an empty tracker.
func newReadTracker() *readTracker {
	return &readTracker{seen: make(map[string]map[string]struct{})}
}

// markRead records that sessionID has read absPath this session.
func (t *readTracker) markRead(sessionID, absPath string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	m, ok := t.seen[sessionID]
	if !ok {
		m = make(map[string]struct{})
		t.seen[sessionID] = m
	}
	m[absPath] = struct{}{}
}

// hasRead reports whether sessionID has read absPath this session.
func (t *readTracker) hasRead(sessionID, absPath string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	m, ok := t.seen[sessionID]
	if !ok {
		return false
	}
	_, ok = m[absPath]
	return ok
}

// forget clears all tracking state for sessionID.  Not currently used by
// the framework; provided for future session-lifecycle wiring.
func (t *readTracker) forget(sessionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.seen, sessionID)
}
