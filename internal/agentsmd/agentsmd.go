// Package agentsmd loads project-context markdown files (AGENTS.md plus
// CLAUDE.md for compatibility) and composes them into a system-prompt
// addition.
//
// # Difference from skills
//
// Skills (internal/skill) are model-invoked: the model sees an index in
// the system prompt and pulls in a skill's body on demand via the
// `skill` tool.  Project-context files are the opposite: their contents
// are auto-appended to the system prompt unconditionally so the model
// always has the project's house rules in context.
//
// # Discovery (v0.2 surface)
//
// Load() returns AT MOST the following blocks, in precedence order
// (lowest first; all that exist are concatenated, none overrides any
// other):
//
//	User layers:
//	  1. ~/.agents/AGENTS.md                  (SourceUserAgents)
//	  2. ~/.config/hygge/AGENTS.md            (SourceUserHygge)
//	  3. ~/.claude/CLAUDE.md                  (SourceUserClaude)
//
//	Project layers, gated on a project root found via walk-up from Pwd:
//	  4. <project-root>/.agents/AGENTS.md     (SourceProjectAgents)
//	  5. <project-root>/AGENTS.md             (SourceProjectRoot)
//	  6. <project-root>/AGENTS.local.md       (SourceProjectRoot)
//	  7. <project-root>/CLAUDE.md             (SourceProjectRoot)
//	  8. <project-root>/CLAUDE.local.md       (SourceProjectRoot)
//
// Subdirectory AGENTS.md / CLAUDE.md files are NOT loaded at startup.
// They are loaded lazily, on-demand, by the per-tool-call loader
// described in STATUS.md (walks up from each tool-touched path to the
// project root, injecting any newly-seen context block as a transient
// system note in the next provider turn).
//
// The project-root walk stops at the first directory containing
// AGENTS.md, CLAUDE.md, .git, .agents/, or .hygge/.  The walk also
// halts when it reaches $HOME so user-level files are not
// re-discovered as project-level blocks.
package agentsmd

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// MaxLazyContextFiles bounds how many subdirectory context files the
// future lazy per-tool-call loader (see STATUS.md) may inject across a
// single session.  Beyond this cap the loader is expected to log a
// slog.Warn and stop loading new files.
//
// Reserved for the lazy loader; not consulted by Load() in v0.2.
const MaxLazyContextFiles = 50

// MaxLazyContextBytes bounds the total byte size of lazy-loaded
// subdirectory context blocks.  Files that would push the running
// total over this cap are expected to be skipped with a slog.Warn.
//
// Reserved for the lazy loader; not consulted by Load() in v0.2.
const MaxLazyContextBytes = 256 * 1024

// LazyExcludeDirs is the set of directory names the future lazy loader
// should skip when walking up from a tool-touched path looking for
// AGENTS.md / CLAUDE.md.  Their top-level files are already handled by
// dedicated layers, and dependency / build directories should never
// contribute project context.
//
// Reserved for the lazy loader; not consulted by Load() in v0.2.
var LazyExcludeDirs = map[string]struct{}{
	".git":         {},
	".agents":      {},
	".hygge":       {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
	"__pycache__":  {},
	"dist":         {},
	"target":       {},
	"bin":          {},
	"build":        {},
}

// Source identifies which discovery location a Block came from.
type Source int

// Source values, ordered by precedence (lower-priority first).
const (
	// SourceUserAgents is ~/.agents/AGENTS.md.
	SourceUserAgents Source = iota
	// SourceUserHygge is ~/.config/hygge/AGENTS.md.
	SourceUserHygge
	// SourceUserClaude is ~/.claude/CLAUDE.md (CLAUDE-format compat
	// at the user level).
	SourceUserClaude
	// SourceProjectAgents is <project-root>/.agents/AGENTS.md.
	SourceProjectAgents
	// SourceProjectRoot is the conventional file (AGENTS.md /
	// AGENTS.local.md / CLAUDE.md / CLAUDE.local.md) at the project
	// root itself.
	SourceProjectRoot
	// SourceProjectSubdir is an AGENTS.md or CLAUDE.md found in a
	// subdirectory of the project root.
	//
	// Not produced by Load() in v0.2; reserved for the lazy
	// per-tool-call loader described in STATUS.md, which surfaces
	// subdir context only when the agent actually touches the
	// directory via a tool call.
	SourceProjectSubdir
)

// String returns a short diagnostic token for the source.
func (s Source) String() string {
	switch s {
	case SourceUserAgents:
		return "user/.agents"
	case SourceUserHygge:
		return "user/hygge"
	case SourceUserClaude:
		return "user/.claude"
	case SourceProjectAgents:
		return "project/.agents"
	case SourceProjectRoot:
		return "project/root"
	case SourceProjectSubdir:
		return "project/subdir"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// Block is a single project-context file loaded from a specific
// location.
type Block struct {
	// Path is the absolute path the file was read from.
	Path string
	// RelPath is the project-relative path for project-layer blocks
	// (empty for user-layer blocks).  Used by `hygge context list`
	// and the system-prompt comment header so the model can locate
	// the source within the project.
	RelPath string
	// Source identifies which layer this block came from.
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

// Load returns every project-context file found, in precedence order
// (lowest first).  Missing files are silently skipped; an empty slice
// is returned when nothing exists anywhere.
//
// Load only considers the project-root files and the user layers — it
// does NOT walk subdirectories.  Subdirectory context is loaded
// lazily by the per-tool-call loader described in STATUS.md.
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
	if b, ok := readBlock(filepath.Join(homeDir, ".agents", "AGENTS.md"), "", SourceUserAgents); ok {
		blocks = append(blocks, b)
	}

	// Layer 2: ~/.config/hygge/AGENTS.md
	if b, ok := readBlock(filepath.Join(xdgConfig, "hygge", "AGENTS.md"), "", SourceUserHygge); ok {
		blocks = append(blocks, b)
	}

	// Layer 3: ~/.claude/CLAUDE.md  (claude-compat)
	if b, ok := readBlock(filepath.Join(homeDir, ".claude", "CLAUDE.md"), "", SourceUserClaude); ok {
		blocks = append(blocks, b)
	}

	if opts.Pwd != "" {
		root := findProjectRoot(opts.Pwd, homeDir)
		if root != "" {
			// Layer 4: <project-root>/.agents/AGENTS.md
			if b, ok := readBlock(filepath.Join(root, ".agents", "AGENTS.md"),
				relTo(root, filepath.Join(root, ".agents", "AGENTS.md")),
				SourceProjectAgents); ok {
				blocks = append(blocks, b)
			}
			// Layers 5-8: project-root files.  All share
			// SourceProjectRoot; AGENTS.local.md and CLAUDE.local.md
			// are the per-machine override conventions for AGENTS.md
			// and CLAUDE.md respectively.
			rootFiles := []string{
				"AGENTS.md",
				"AGENTS.local.md",
				"CLAUDE.md",
				"CLAUDE.local.md",
			}
			for _, name := range rootFiles {
				p := filepath.Join(root, name)
				if b, ok := readBlock(p, relTo(root, p), SourceProjectRoot); ok {
					blocks = append(blocks, b)
				}
			}
		}
	}

	return blocks, nil
}

// readBlock reads path and returns the Block + true.  Missing files
// return ok=false (without an error).  Other read errors are logged
// and the file is treated as absent — never fatal.
func readBlock(path, relPath string, source Source) (Block, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path is built from controlled discovery roots
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("agentsmd: read failed", "path", path, "err", err)
		}
		return Block{}, false
	}
	return Block{
		Path:    path,
		RelPath: relPath,
		Source:  source,
		Content: strings.TrimSpace(string(data)),
	}, true
}

// relTo returns path relative to root, or "" if Rel fails.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return rel
}

// findProjectRoot walks parents of start looking for the first
// directory that contains one of the project-root markers (AGENTS.md,
// CLAUDE.md, .git, .agents/, .hygge/).  Returns the absolute matching
// directory, or "" if no marker was found before hitting homeStop or
// the filesystem root.  homeStop bounds the walk so the user-level
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
// markers: AGENTS.md / CLAUDE.md (file), .git (file or dir),
// .agents/ (dir), .hygge/ (dir).
func hasMarker(dir string) bool {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md"} {
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil && !info.IsDir() {
			return true
		}
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

// BuildSystemPromptAdditions returns the project-context content as
// one concatenated block ready to APPEND to the base system prompt.
// Returns an empty string when blocks is empty.
//
// The output follows this stable shape:
//
//	## Project context
//
//	<!-- source: <source-token>: <path-or-relpath> -->
//	<content>
//
//	---
//
//	<!-- source: <source-token>: <path-or-relpath> -->
//	<content>
//
// For project-layer blocks the comment uses the relative path (e.g.
// `project/root: CLAUDE.local.md`) so the model can locate the file
// inside the working tree.
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
		label := blk.Path
		if blk.RelPath != "" {
			label = blk.RelPath
		}
		fmt.Fprintf(&b, "<!-- source: %s: %s -->\n", blk.Source.String(), label)
		if blk.Content != "" {
			b.WriteString(blk.Content)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
