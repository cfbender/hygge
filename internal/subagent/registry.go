package subagent

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

// builtinGeneral is the default sub-agent type.  It is always present in
// every Registry and cannot be removed; users CAN override its system
// prompt and tool allowlist by declaring a [subagents.general] block in
// a discovered TOML file.
var builtinGeneral = Type{
	Name: "general",
	Description: "General-purpose sub-agent with access to all built-in tools (except task). " +
		"Use for self-contained missions that should not pollute the main context.",
	SystemPrompt: `You are a general-purpose sub-agent of hygge.  You are operating in isolation:
your conversation is invisible to the user and to the parent agent.  Complete the
mission described in the user's first message and return ONE final assistant
message summarising the results.  Be concise.  Cite file paths and line numbers
when applicable.  Do not ask follow-up questions -- work with the information
you have.`,
	Tools:  nil, // nil = "all default sub-agent tools"
	Source: "builtin",
}

// nameRe is the validation pattern every sub-agent type name must match.
// Mirrors the tool-name convention.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Registry is the resolved set of [Type]s available to the `task` tool.
// Construct via [Load]; the zero value is not usable.
type Registry struct {
	types  []Type
	byName map[string]Type

	// defaultTools is the tool-name allowlist applied when a Type's
	// Tools is empty.  Resolved at Load time from the parent's tool
	// registry (minus `task`).
	defaultTools []string
}

// LoadOptions configures [Load].
type LoadOptions struct {
	// HomeDir overrides $HOME for tests.  Empty falls back to
	// os.UserHomeDir.
	HomeDir string

	// XDGConfigHome overrides $XDG_CONFIG_HOME for tests.  Empty
	// falls back to $HOME/.config.
	XDGConfigHome string

	// Pwd is the starting directory for the project walk-up.  When
	// empty no project layers are consulted.
	Pwd string

	// DefaultTools is the tool-name allowlist applied when a Type's
	// Tools is empty.  Callers pass the parent's built-in tool list
	// MINUS "task" so sub-agents inherit the orchestrator's full
	// toolbox by default.
	DefaultTools []string
}

// Load discovers sub-agent types in precedence order:
//
//  1. Built-in: "general" (always present).
//  2. ~/.agents/subagents.toml             (user, vendor-neutral)
//  3. ~/.config/hygge/subagents.toml       (user, hygge-native)
//  4. <project-root>/.agents/subagents.toml   (project, vendor-neutral)
//  5. <project-root>/.hygge/subagents.toml    (project, hygge-native)
//
// Later layers override earlier types of the same Name.  Missing files
// are silently ignored.  Malformed files emit slog.Warn and are
// skipped; the remaining valid types still load.
func Load(opts LoadOptions) (*Registry, error) {
	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("subagent: home dir: %w", err)
		}
		homeDir = h
	}
	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}

	byName := map[string]Type{
		builtinGeneral.Name: builtinGeneral,
	}

	loadOneFile(byName, filepath.Join(homeDir, ".agents", "subagents.toml"), "user")
	loadOneFile(byName, filepath.Join(xdgConfig, "hygge", "subagents.toml"), "user")

	if opts.Pwd != "" {
		if p := findProjectFile(opts.Pwd, filepath.Join(".agents", "subagents.toml"), homeDir); p != "" {
			loadOneFile(byName, p, "project")
		}
		if p := findProjectFile(opts.Pwd, filepath.Join(".hygge", "subagents.toml"), homeDir); p != "" {
			loadOneFile(byName, p, "project")
		}
	}

	// Resolve a sorted slice for deterministic iteration.
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	types := make([]Type, 0, len(names))
	for _, n := range names {
		types = append(types, byName[n])
	}

	return &Registry{
		types:        types,
		byName:       byName,
		defaultTools: append([]string(nil), opts.DefaultTools...),
	}, nil
}

// tomlFile is the surface shape of subagents.toml.  We only consume the
// [subagents] table; other top-level keys are tolerated but logged so
// the user knows the loader saw something unexpected.
type tomlFile struct {
	Subagents map[string]tomlEntry `toml:"subagents"`
}

type tomlEntry struct {
	Description string   `toml:"description"`
	Prompt      string   `toml:"prompt"`
	Tools       []string `toml:"tools"`
	Model       string   `toml:"model"`
}

// loadOneFile reads path (when present) and merges its entries into
// byName.  Errors and unknown structure produce slog.Warn but never
// abort the load -- one broken file should not deny the user every
// other valid type.
func loadOneFile(byName map[string]Type, path, source string) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from controlled discovery roots
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("subagent: read failed", "path", path, "err", err)
		}
		return
	}

	// Probe for unknown top-level keys so we can warn the user about
	// likely typos without aborting the parse.
	var rawTop map[string]any
	if err := toml.Unmarshal(data, &rawTop); err == nil {
		for k := range rawTop {
			if k != "subagents" {
				slog.Warn("subagent: unknown top-level key in subagents.toml; ignored",
					"path", path, "key", k)
			}
		}
	}

	var schema tomlFile
	if err := toml.Unmarshal(data, &schema); err != nil {
		slog.Warn("subagent: parse failed", "path", path, "err", err)
		return
	}

	for name, entry := range schema.Subagents {
		t, err := normalizeEntry(name, entry, source)
		if err != nil {
			slog.Warn("subagent: skipping entry", "path", path, "name", name, "err", err)
			continue
		}
		// Allow override of the builtin general type (intentional --
		// users may want to widen / narrow its tool list).  We still
		// pin Source = source so list output reflects the override
		// origin.
		byName[t.Name] = t
	}
}

// normalizeEntry validates one TOML entry and returns a Type.
func normalizeEntry(name string, e tomlEntry, source string) (Type, error) {
	name = strings.TrimSpace(name)
	if !nameRe.MatchString(name) {
		return Type{}, fmt.Errorf("invalid name %q (must match [a-z][a-z0-9_]*)", name)
	}
	desc := strings.TrimSpace(e.Description)
	if desc == "" {
		return Type{}, fmt.Errorf("description is required")
	}
	prompt := strings.TrimSpace(e.Prompt)
	if prompt == "" {
		return Type{}, fmt.Errorf("prompt is required")
	}
	// Filter `task` out of the tools list eagerly -- defence in depth
	// alongside the runtime's own guard.  Warn so the user knows their
	// TOML had no effect.
	var tools []string
	for _, t := range e.Tools {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == "task" {
			slog.Warn("subagent: task tool ignored in tools list (recursion guard)",
				"type", name)
			continue
		}
		tools = append(tools, t)
	}
	model := strings.TrimSpace(e.Model)
	if model != "" && !IsValidModelRef(model) {
		slog.Warn("subagent: malformed model override; falling back to parent's model",
			"type", name, "requested_model", model,
			"want_shape", "<provider>/<model-id>")
		model = ""
	}
	return Type{
		Name:         name,
		Description:  desc,
		SystemPrompt: prompt,
		Tools:        tools,
		Source:       source,
		Model:        model,
	}, nil
}

// Get returns the registered type with the given name.
func (r *Registry) Get(name string) (*Type, bool) {
	if r == nil {
		return nil, false
	}
	t, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	// Return a copy so callers cannot mutate the registry's storage.
	cp := t
	return &cp, true
}

// List returns every registered type, sorted by Name.  The returned
// slice is a fresh copy; mutating it does not affect the registry.
func (r *Registry) List() []Type {
	if r == nil {
		return nil
	}
	out := make([]Type, len(r.types))
	copy(out, r.types)
	return out
}

// Len returns the number of registered types.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.types)
}

// DefaultTools returns a copy of the tool-name allowlist applied when a
// Type's Tools slice is empty.  Exposed so the runtime can resolve the
// per-call tool registry against the same list the registry was loaded
// with.
func (r *Registry) DefaultTools() []string {
	if r == nil {
		return nil
	}
	out := make([]string, len(r.defaultTools))
	copy(out, r.defaultTools)
	return out
}

// findProjectFile walks parents of start looking for a file at the
// relative path rel.  The walk halts at the first directory containing
// `.git`, the first match, $HOME, or the filesystem root -- whichever
// comes first.  Mirrors the convention used by internal/mcp and
// internal/skill.
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
