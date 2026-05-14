// Package skill loads named markdown procedures the model can invoke
// at runtime.  Skills live in plain-text `.md` files with a small
// YAML-like frontmatter block; the loader assembles a Registry by
// reading every file from a fixed four-layer search path.
//
// # The .agents convention
//
// Hygge follows the vendor-neutral `.agents` directory convention as the
// gravitational center for shared agent assets.  Skills are loaded from
// four layers (lowest priority first, later overrides earlier):
//
//  1. ~/.agents/skills/             — vendor-neutral, per-user
//  2. ~/.config/hygge/skills/       — hygge-native, per-user
//  3. <pwd>/.agents/skills/         — vendor-neutral, per-project (walk-up)
//  4. <pwd>/.hygge/skills/          — hygge-native, per-project (walk-up)
//
// The hygge-native paths override `.agents` paths so users can shadow a
// shared skill with a hygge-specific tweak.  Project paths override user
// paths so per-repo conventions win.
//
// # Walk-up
//
// For project-level layers we walk parent directories from Pwd until we
// either find the directory in question or hit a project-root marker.
// The walk halts at the first `.git` directory at or above the current
// level — that's the conventional "this is the project root" signal.
// The walk also halts when it reaches $HOME so the user-level
// `.agents/` and `.hygge/` directories are not double-counted as
// project layers when Pwd lives below $HOME.  Files above the .git (or
// $HOME) boundary are NOT loaded.  Only the FIRST match in each
// project layer is used; we don't merge skills from multiple
// `.hygge/skills` directories up the tree.
//
// # System prompt integration
//
// The Registry holds only the index (name + description + when-to-use)
// in memory; full skill bodies are loaded on demand via the `skill` tool.
// BuildSystemPromptAdditions renders the index as a markdown block
// appended to the base system prompt.
package skill

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Skill is a single named procedure the agent can invoke.
type Skill struct {
	// Name is the unique identifier.  Must match the filename stem and
	// the regular expression ^[a-z][a-z0-9-]{0,63}$.
	Name string

	// Description is the one-line summary shown in the system prompt.
	Description string

	// WhenToUse describes the situations in which the model should
	// invoke this skill.  Shown in the system prompt under the
	// description.
	WhenToUse string

	// Body is the markdown body of the skill, with leading and
	// trailing blank lines stripped.  Loaded eagerly along with the
	// frontmatter; the tool returns it verbatim when invoked.
	Body string

	// Extras carries unknown frontmatter keys verbatim.  Empty when no
	// extra keys were present.
	Extras map[string]string

	// Path is the absolute path the skill was loaded from.
	Path string

	// Source identifies which of the four layers this skill came from.
	Source Source

	// LoadedAt is when the parser finished reading the file.
	LoadedAt time.Time
}

// Source describes where a skill (or any .agents asset) was found.
type Source int

// Source values, ordered by precedence (lower-priority first).
const (
	// SourceUserAgents is ~/.agents/skills/.
	SourceUserAgents Source = iota
	// SourceUserHygge is ~/.config/hygge/skills/.
	SourceUserHygge
	// SourceProjectAgents is <pwd>/.agents/skills/ (walk-up).
	SourceProjectAgents
	// SourceProjectHygge is <pwd>/.hygge/skills/ (walk-up).
	SourceProjectHygge
)

// String returns a short diagnostic token for the source.
func (s Source) String() string {
	switch s {
	case SourceUserAgents:
		return "user/.agents"
	case SourceUserHygge:
		return "user/hygge"
	case SourceProjectAgents:
		return "project/.agents"
	case SourceProjectHygge:
		return "project/hygge"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// LoadOptions configures Load.  At least Pwd should be set; HomeDir
// falls back to os.UserHomeDir() if zero, and XDGConfigHome falls back
// to $HOME/.config.
type LoadOptions struct {
	// HomeDir overrides $HOME for tests.
	HomeDir string
	// XDGConfigHome overrides $XDG_CONFIG_HOME for tests.  Empty means
	// fall back to $HOME/.config.
	XDGConfigHome string
	// Pwd is the starting directory for the project walk-up.  When
	// empty no project layers are consulted.
	Pwd string
}

// Registry holds the loaded skills.  Construct via Load; the zero value
// is a valid empty registry.
type Registry struct {
	byName map[string]Skill
}

// Load reads skills from all four layers and returns a Registry.
// Skills with the same Name are deduped using the precedence order
// documented on the package: later layers override earlier ones.
// Returns an empty Registry if no skills exist anywhere.
//
// A skill file with malformed frontmatter, an invalid name, or a
// filename-stem / frontmatter-name mismatch is logged via slog.Warn and
// skipped; other valid skills still appear.  Files without frontmatter
// at all are silently skipped — they are not skills.
//
// Missing layer directories are NOT an error — the common case is that
// most layers are empty.
func Load(opts LoadOptions) (*Registry, error) {
	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("skill: home dir: %w", err)
		}
		homeDir = h
	}
	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}

	reg := &Registry{byName: make(map[string]Skill)}

	// Layer 1: ~/.agents/skills/
	loadFromDir(reg, filepath.Join(homeDir, ".agents", "skills"), SourceUserAgents)

	// Layer 2: ~/.config/hygge/skills/
	loadFromDir(reg, filepath.Join(xdgConfig, "hygge", "skills"), SourceUserHygge)

	// Layer 3 + 4: project walk-up.  We pass homeDir as a stop so the
	// walk does not climb into $HOME and re-treat the user-level
	// .agents/.hygge directories as project layers.
	if opts.Pwd != "" {
		if dir := findProjectDir(opts.Pwd, filepath.Join(".agents", "skills"), homeDir); dir != "" {
			loadFromDir(reg, dir, SourceProjectAgents)
		}
		if dir := findProjectDir(opts.Pwd, filepath.Join(".hygge", "skills"), homeDir); dir != "" {
			loadFromDir(reg, dir, SourceProjectHygge)
		}
	}

	return reg, nil
}

// loadFromDir reads every *.md file in dir, parses each, and inserts
// the resulting Skill into reg.  Files that fail to parse are logged
// and skipped.  A missing dir is silently ignored.
func loadFromDir(reg *Registry, dir string, source Source) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		slog.Warn("skill: read dir failed", "dir", dir, "err", err)
		return
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		full := filepath.Join(dir, name)
		sk, err := ParseFile(full)
		if err != nil {
			if errorIsNoFrontmatter(err) {
				slog.Warn("skill: skipped (no frontmatter)", "path", full)
				continue
			}
			slog.Warn("skill: skipped (parse error)", "path", full, "err", err)
			continue
		}
		sk.Source = source
		reg.byName[sk.Name] = sk
	}
}

func errorIsNoFrontmatter(err error) bool {
	return errors.Is(err, ErrNoFrontmatter)
}

// findProjectDir walks parents of start looking for a directory
// containing the relative path rel.  The walk halts at the first
// directory that contains a `.git` entry (the conventional project
// root) — at that level we still check for rel, but we do not climb
// past it.  It also halts when the current directory equals homeStop
// so the user-level .agents / .hygge directories under $HOME are not
// re-discovered as project layers.  Returns the absolute matching
// path, or "" if no match was found before the walk terminated.
func findProjectDir(start, rel, homeStop string) string {
	dir := filepath.Clean(start)
	homeStop = filepath.Clean(homeStop)
	for {
		// Stop when we reach $HOME — anything from here up is
		// user-level or system; the user-level layers are already
		// being loaded via their explicit paths and we must not
		// rediscover them as project layers.
		if homeStop != "" && dir == homeStop {
			return ""
		}
		candidate := filepath.Join(dir, rel)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		// Stop at .git boundary.
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

// All returns every loaded skill, sorted by Name ascending.  The
// returned slice is a fresh copy.
func (r *Registry) All() []Skill {
	if r == nil || len(r.byName) == 0 {
		return nil
	}
	out := make([]Skill, 0, len(r.byName))
	for _, sk := range r.byName {
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns a skill by name.
func (r *Registry) Get(name string) (*Skill, bool) {
	if r == nil {
		return nil, false
	}
	sk, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	cp := sk
	return &cp, true
}

// Len returns the number of unique skills in the registry.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.byName)
}
