// Package agentsmd loads project-context markdown files (AGENTS.md) and
// composes them into a system-prompt addition.
//
// # Difference from skills
//
// Skills (internal/skill) are model-invoked: the model sees an index in
// the system prompt and pulls in a skill's body on demand via the
// `skill` tool.  AGENTS.md is the opposite: its contents are
// auto-appended to the system prompt unconditionally so the model always
// has the project's house rules in context.
//
// # Discovery
//
// Each location loads at most ONE AGENTS.md.  Locations, in precedence
// order (lowest first; later wins on duplicates… though duplicates do
// not arise — every block is concatenated rather than overridden, in
// precedence order):
//
//  1. ~/.agents/AGENTS.md                     (user-level, vendor-neutral)
//  2. ~/.config/hygge/AGENTS.md               (user-level, hygge-native)
//  3. <pwd>/.agents/AGENTS.md  (walk-up)      (project-level, vendor-neutral)
//  4. <project-root>/AGENTS.md (walk-up)      (project-level, conventional)
//
// The walk for both project layers stops at the first directory
// containing AGENTS.md, .git, .agents/, or .hygge/.  Above that
// directory is "outside the project" and is not consulted.  The walk
// also halts when it reaches $HOME so the user-level files are not
// re-discovered as project-level blocks.
package agentsmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Source identifies which of the four discovery locations a Block came
// from.  We keep our own enum (rather than reusing skill.Source) because
// the project-root location has no analog in the skill loader — skills
// always come from a `skills/` subdirectory, while AGENTS.md sits at
// the project root itself.
type Source int

// Source values, ordered by precedence (lower-priority first).
const (
	// SourceUserAgents is ~/.agents/AGENTS.md.
	SourceUserAgents Source = iota
	// SourceUserHygge is ~/.config/hygge/AGENTS.md.
	SourceUserHygge
	// SourceProjectAgents is <pwd>/.agents/AGENTS.md (walk-up).
	SourceProjectAgents
	// SourceProjectRoot is <project-root>/AGENTS.md (walk-up).
	SourceProjectRoot
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
	case SourceProjectRoot:
		return "project/root"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Block is a single AGENTS.md loaded from a specific location.
type Block struct {
	// Path is the absolute path the file was read from.
	Path string
	// Source identifies which of the four layers this block came from.
	Source Source
	// Content is the file's contents, trimmed of leading and trailing
	// whitespace.  Empty when the file was empty — the block is still
	// returned so callers know the user created the file deliberately.
	Content string
}

// LoadOptions mirrors skill.LoadOptions: HomeDir / XDGConfigHome let
// tests redirect the user layers into a tempdir; Pwd seeds the project
// walk-up.
type LoadOptions struct {
	HomeDir       string
	XDGConfigHome string
	Pwd           string
}

// Load returns every AGENTS.md found, in precedence order (lowest
// first).  Missing files are silently skipped; an empty slice is
// returned when nothing exists anywhere.
func Load(opts LoadOptions) ([]Block, error) {
	homeDir := opts.HomeDir
	if homeDir == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("agentsmd: home dir: %w", err)
		}
		homeDir = h
	}
	xdgConfig := opts.XDGConfigHome
	if xdgConfig == "" {
		xdgConfig = filepath.Join(homeDir, ".config")
	}

	var blocks []Block

	// Layer 1: ~/.agents/AGENTS.md
	if b, ok := readBlock(filepath.Join(homeDir, ".agents", "AGENTS.md"), SourceUserAgents); ok {
		blocks = append(blocks, b)
	}

	// Layer 2: ~/.config/hygge/AGENTS.md
	if b, ok := readBlock(filepath.Join(xdgConfig, "hygge", "AGENTS.md"), SourceUserHygge); ok {
		blocks = append(blocks, b)
	}

	// Layers 3 + 4: project walk-up.
	if opts.Pwd != "" {
		root := findProjectRoot(opts.Pwd, homeDir)
		if root != "" {
			// 3: <project-root>/.agents/AGENTS.md
			if b, ok := readBlock(filepath.Join(root, ".agents", "AGENTS.md"), SourceProjectAgents); ok {
				blocks = append(blocks, b)
			}
			// 4: <project-root>/AGENTS.md
			if b, ok := readBlock(filepath.Join(root, "AGENTS.md"), SourceProjectRoot); ok {
				blocks = append(blocks, b)
			}
		}
	}

	return blocks, nil
}

// readBlock reads path and returns the Block + true.  Missing files
// return ok=false (without an error).  Other read errors are logged
// and the file is treated as absent — never fatal.
func readBlock(path string, source Source) (Block, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from controlled discovery roots
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("agentsmd: read failed", "path", path, "err", err)
		}
		return Block{}, false
	}
	return Block{
		Path:    path,
		Source:  source,
		Content: strings.TrimSpace(string(data)),
	}, true
}

// findProjectRoot walks parents of start looking for the first
// directory that contains one of the project-root markers (AGENTS.md,
// .git, .agents/, .hygge/).  Returns the absolute matching directory,
// or "" if no marker was found before hitting homeStop or the
// filesystem root.  homeStop bounds the walk so the user-level
// directories under $HOME are not interpreted as project roots.
func findProjectRoot(start, homeStop string) string {
	dir := filepath.Clean(start)
	homeStop = filepath.Clean(homeStop)
	for {
		if homeStop != "" && dir == homeStop {
			return ""
		}
		if hasMarker(dir) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// hasMarker reports whether dir contains any of the project-root
// markers: AGENTS.md (file), .git (file or dir), .agents/ (dir),
// .hygge/ (dir).
func hasMarker(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil && !info.IsDir() {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if info, err := os.Stat(filepath.Join(dir, ".agents")); err == nil && info.IsDir() {
		return true
	}
	if info, err := os.Stat(filepath.Join(dir, ".hygge")); err == nil && info.IsDir() {
		return true
	}
	return false
}

// BuildSystemPromptAdditions returns the AGENTS.md content as one
// concatenated block ready to APPEND to the base system prompt.
// Returns an empty string when blocks is empty.
//
// The output follows this stable shape:
//
//	## Project context
//
//	<!-- source: <source-token>: <path> -->
//	<content>
//
//	---
//
//	<!-- source: <source-token>: <path> -->
//	<content>
//
// Blocks with empty Content are still emitted (with a comment but no
// body) so prompt size reflects the user's explicit intent.
func BuildSystemPromptAdditions(blocks []Block) string {
	if len(blocks) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Project context\n\n")
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("\n---\n\n")
		}
		fmt.Fprintf(&b, "<!-- source: %s: %s -->\n", blk.Source.String(), blk.Path)
		if blk.Content != "" {
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
