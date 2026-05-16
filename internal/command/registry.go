package command

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

// nameRe is the validation regex every command name must match.
// Matches the spec: lowercase letter, then lowercase letters / digits
// / underscore / hyphen.  Case-sensitive on lookup.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// argNameRe is the validation regex template arg names must match.
// Stricter than nameRe (no hyphens) because template substitution
// uses `{{name}}` and a hyphen would be ambiguous with subtraction
// for users who don't read the docs.
var argNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// reservedTailArg is the well-known arg name that captures all
// remaining tail text after the named arguments.  Reserved — users
// cannot declare an [ArgSpec] with this name.
const reservedTailArg = "tail"

// Registry is the resolved set of registered slash commands.
// Construct via [New] or [Load]; the zero value is not usable.
type Registry struct {
	byName map[string]Command
	// order tracks insertion / final ordering.  After [Load] this is
	// "built-ins first, then TOML layers in their load order";
	// [Register] appends to the tail.  [List] returns a copy sorted
	// by name regardless — order here is for the layered loader's
	// own bookkeeping.
	order []string
}

// New constructs an empty registry.  Most callers want [Load]
// instead; [New] is for tests that want to register a hand-picked
// command set.
func New() *Registry {
	return &Registry{byName: map[string]Command{}}
}

// Register adds cmd to the registry.  Returns an error when the name
// is invalid or already taken — the layered loader uses
// [registerOrReplace] to express override semantics rather than
// going through [Register].
func (r *Registry) Register(cmd Command) error {
	if r == nil {
		return fmt.Errorf("command: Register on nil Registry")
	}
	if cmd == nil {
		return fmt.Errorf("command: Register: nil Command")
	}
	name := cmd.Name()
	if !nameRe.MatchString(name) {
		return fmt.Errorf("command: invalid name %q (must match %s)", name, nameRe)
	}
	if _, ok := r.byName[name]; ok {
		return fmt.Errorf("command: duplicate name %q", name)
	}
	r.byName[name] = cmd
	r.order = append(r.order, name)
	return nil
}

// Unregister removes the command registered under name. It is a no-op when the
// name is not present.
func (r *Registry) Unregister(name string) {
	if r == nil {
		return
	}
	if _, ok := r.byName[name]; !ok {
		return
	}
	delete(r.byName, name)
	out := r.order[:0]
	for _, existing := range r.order {
		if existing != name {
			out = append(out, existing)
		}
	}
	r.order = out
}

// registerOrReplace inserts cmd, overriding any same-named entry.
// Used by the TOML loader so a later layer wins over an earlier one
// (or over a built-in).  The replaced entry's slot in [order] is
// retained so the new command takes the original's position rather
// than moving to the tail — keeps `hygge commands list` ordering
// stable across reloads.
func (r *Registry) registerOrReplace(cmd Command) error {
	if r == nil {
		return fmt.Errorf("command: registerOrReplace on nil Registry")
	}
	if cmd == nil {
		return fmt.Errorf("command: registerOrReplace: nil Command")
	}
	name := cmd.Name()
	if !nameRe.MatchString(name) {
		return fmt.Errorf("command: invalid name %q", name)
	}
	if _, ok := r.byName[name]; !ok {
		r.order = append(r.order, name)
	}
	r.byName[name] = cmd
	return nil
}

// Get returns the registered command with the given name.  The
// boolean reports whether it was found.  Case-sensitive.
func (r *Registry) Get(name string) (Command, bool) {
	if r == nil {
		return nil, false
	}
	cmd, ok := r.byName[name]
	return cmd, ok
}

// List returns every registered command, sorted by Name.  The
// returned slice is a fresh copy.
func (r *Registry) List() []Command {
	if r == nil {
		return nil
	}
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Command, 0, len(names))
	for _, n := range names {
		out = append(out, r.byName[n])
	}
	return out
}

// Len returns the number of registered commands.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.byName)
}

// LookupPrefix returns every command whose Name fuzzily matches prefix,
// sorted by match quality then Name.  Prefix matches rank first, followed
// by deterministic subsequence matches.  Used by the command palette for
// autocomplete. An empty prefix returns the same as [List]. Case-sensitive —
// the palette feeds the user's typed buffer in verbatim.
func (r *Registry) LookupPrefix(prefix string) []Command {
	if r == nil {
		return nil
	}
	all := r.List()
	if prefix == "" {
		return all
	}
	type scored struct {
		cmd   Command
		score int
	}
	matches := make([]scored, 0, len(all))
	for _, c := range all {
		if score, ok := fuzzyCommandScore(c.Name(), prefix); ok {
			matches = append(matches, scored{cmd: c, score: score})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score < matches[j].score
		}
		return matches[i].cmd.Name() < matches[j].cmd.Name()
	})
	out := make([]Command, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.cmd)
	}
	return out
}

func fuzzyCommandScore(name, query string) (int, bool) {
	if strings.HasPrefix(name, query) {
		return 0, true
	}
	pos := 0
	gaps := 0
	for _, qr := range query {
		found := false
		for pos < len(name) {
			if rune(name[pos]) == qr {
				found = true
				pos++
				break
			}
			pos++
			gaps++
		}
		if !found {
			return 0, false
		}
	}
	return 10 + gaps, true
}

// Load discovers and registers commands in precedence order:
//
//  1. Built-ins ([RegisterBuiltins]).
//  2. ~/.agents/commands.toml             (user, vendor-neutral)
//  3. ~/.config/hygge/commands.toml       (user, hygge-native)
//  4. <project-root>/.agents/commands.toml   (project, vendor-neutral)
//  5. <project-root>/.hygge/commands.toml    (project, hygge-native)
//
// Later layers override earlier same-named entries.  Built-ins CAN
// be overridden by user TOML — that's the documented extension
// point.  Missing files are silently ignored; malformed files emit
// slog.Warn and are skipped (one bad file does not deny the user
// the rest of the registry).
func Load(opts LoadOptions) (*Registry, error) {
	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("command: home dir: %w", err)
		}
		homeDir = h
	}
	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}

	reg := New()
	RegisterBuiltins(reg)

	loadOneFile(reg, filepath.Join(homeDir, ".agents", "commands.toml"), "user")
	loadOneFile(reg, filepath.Join(xdgConfig, "hygge", "commands.toml"), "user")

	if opts.Pwd != "" {
		if p := findProjectFile(opts.Pwd, filepath.Join(".agents", "commands.toml"), homeDir); p != "" {
			loadOneFile(reg, p, "project")
		}
		if p := findProjectFile(opts.Pwd, filepath.Join(".hygge", "commands.toml"), homeDir); p != "" {
			loadOneFile(reg, p, "project")
		}
	}

	AttachHelpRegistry(reg)
	return reg, nil
}

// tomlFile is the surface shape of commands.toml.  Only the
// [commands] table is consumed; other top-level keys produce a warn
// so the user notices their typo without losing the valid entries.
type tomlFile struct {
	Commands map[string]tomlEntry `toml:"commands"`
}

type tomlEntry struct {
	Description string         `toml:"description"`
	Prompt      string         `toml:"prompt"`
	Args        []tomlArgEntry `toml:"args"`
}

type tomlArgEntry struct {
	Name        string `toml:"name"`
	Description string `toml:"description"`
	Required    bool   `toml:"required"`
}

// loadOneFile reads path (when present) and merges its entries into
// reg.  Errors and unknown structure produce slog.Warn but never
// abort the load.
func loadOneFile(reg *Registry, path, source string) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from controlled discovery roots
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("command: read failed", "path", path, "err", err)
		}
		return
	}

	// Probe for unknown top-level keys so users see typos surfaced
	// without aborting the parse.
	var rawTop map[string]any
	if err := toml.Unmarshal(data, &rawTop); err == nil {
		for k := range rawTop {
			if k != "commands" {
				slog.Warn("command: unknown top-level key in commands.toml; ignored",
					"path", path, "key", k)
			}
		}
	}

	var schema tomlFile
	if err := toml.Unmarshal(data, &schema); err != nil {
		slog.Warn("command: parse failed", "path", path, "err", err)
		return
	}

	for name, entry := range schema.Commands {
		cmd, err := buildTemplateCommand(name, entry, source, path)
		if err != nil {
			slog.Warn("command: skipping entry", "path", path, "name", name, "err", err)
			continue
		}
		if err := reg.registerOrReplace(cmd); err != nil {
			slog.Warn("command: registration failed", "path", path, "name", name, "err", err)
		}
	}
}

// buildTemplateCommand validates one TOML entry and produces a
// [templateCommand].  Returns an error when the name is malformed,
// description is missing, or an arg is malformed.
func buildTemplateCommand(name string, e tomlEntry, source, path string) (Command, error) {
	name = strings.TrimSpace(name)
	if !nameRe.MatchString(name) {
		return nil, fmt.Errorf("invalid name %q (must match %s)", name, nameRe)
	}
	desc := strings.TrimSpace(e.Description)
	if desc == "" {
		return nil, fmt.Errorf("description is required")
	}
	prompt := e.Prompt
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("prompt is required")
	}

	args := make([]ArgSpec, 0, len(e.Args))
	seen := map[string]struct{}{}
	for i, a := range e.Args {
		an := strings.TrimSpace(a.Name)
		if !argNameRe.MatchString(an) {
			return nil, fmt.Errorf("args[%d]: invalid name %q (must match %s)", i, an, argNameRe)
		}
		if an == reservedTailArg {
			return nil, fmt.Errorf("args[%d]: %q is reserved", i, reservedTailArg)
		}
		if _, dup := seen[an]; dup {
			return nil, fmt.Errorf("args[%d]: duplicate name %q", i, an)
		}
		seen[an] = struct{}{}
		args = append(args, ArgSpec{
			Name:        an,
			Description: strings.TrimSpace(a.Description),
			Required:    a.Required,
		})
	}

	// Warn (but do not fail) when the template references unknown
	// placeholders.  Renders as a literal `{{name}}` at runtime.
	for _, ph := range placeholdersIn(prompt) {
		if ph == reservedTailArg {
			continue
		}
		known := false
		for _, a := range args {
			if a.Name == ph {
				known = true
				break
			}
		}
		if !known {
			slog.Warn("command: template references unknown placeholder",
				"path", path, "name", name, "placeholder", ph)
		}
	}

	return &templateCommand{
		name:        name,
		description: desc,
		source:      source,
		args:        args,
		prompt:      prompt,
	}, nil
}

// findProjectFile walks parents of start looking for a file at the
// relative path rel.  Halts at the first directory containing `.git`,
// the first match, $HOME, or the filesystem root.  Mirrors the
// convention used by internal/subagent, internal/skill, and
// internal/mcp.
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
