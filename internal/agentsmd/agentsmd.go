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
// # Discovery
//
// User layers (loaded if present):
//
//  1. ~/.agents/AGENTS.md                 (user-level, vendor-neutral)
//  2. ~/.config/hygge/AGENTS.md           (user-level, hygge-native)
//  3. ~/.claude/CLAUDE.md                 (user-level, claude-compat)
//
// Project layers, gated on a project root found via walk-up from Pwd:
//
//  4. <project-root>/.agents/AGENTS.md    (project, vendor-neutral)
//  5. <project-root>/AGENTS.md            (project, conventional)
//  6. <project-root>/CLAUDE.md            (project, claude-compat)
//  7. <project-root>/CLAUDE.local.md      (project, claude-compat local override)
//  8. <project-root>/**/{AGENTS.md,CLAUDE.md}  (recursive descent)
//
// The recursive descent (layer 8) excludes common dependency / build
// directories (.git, node_modules, vendor, .venv, __pycache__, dist,
// target, bin, build) and is bounded by MaxRecursiveFiles and
// MaxRecursiveBytes so a misconfigured workspace cannot blow up the
// system prompt.  Files duplicating layer-5/6/7 paths are skipped.
//
// The project-root walk stops at the first directory containing
// AGENTS.md, CLAUDE.md, .git, .agents/, or .hygge/.  The walk also
// halts when it reaches $HOME so user-level files are not
// re-discovered as project-level blocks.
package agentsmd

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// MaxRecursiveFiles bounds how many project-subdirectory context files
// the recursive descent will load.  Beyond this cap the loader logs a
// slog.Warn and stops descending.
const MaxRecursiveFiles = 50

// MaxRecursiveBytes bounds the total byte size of recursive-descent
// blocks.  Files that would push the running total over this cap are
// skipped with a slog.Warn.  The cap does not apply to the dedicated
// layers (1-7) — those are user-explicit choices.
const MaxRecursiveBytes = 256 * 1024

// excludeDirs is the set of directory names skipped during recursive
// descent.  Matches the convention used by the grep/glob builtins,
// plus .agents and .hygge whose top-level files are already handled
// by dedicated layers (preventing double-loading).
var excludeDirs = map[string]struct{}{
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
	// CLAUDE.md / CLAUDE.local.md) at the project root itself.
	SourceProjectRoot
	// SourceProjectSubdir is an AGENTS.md or CLAUDE.md found by
	// recursive descent from the project root.
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
			// Layers 5-7: project-root files.  Skipped at the
			// recursive layer below so we don't double-count.
			rootFiles := []string{"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md"}
			for _, name := range rootFiles {
				p := filepath.Join(root, name)
				if b, ok := readBlock(p, relTo(root, p), SourceProjectRoot); ok {
					blocks = append(blocks, b)
				}
			}
			// Layer 8: recursive descent.
			recursive := loadRecursive(root)
			blocks = append(blocks, recursive...)
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

// loadRecursive walks the project tree under root looking for
// AGENTS.md / CLAUDE.md / CLAUDE.local.md files in subdirectories.
// Root-level files are skipped (those are loaded as SourceProjectRoot
// separately).  excludeDirs are pruned wholesale.
//
// Capped by MaxRecursiveFiles and MaxRecursiveBytes; over-cap files
// are reported via slog.Warn and dropped.
func loadRecursive(root string) []Block {
	var blocks []Block
	var totalBytes int
	stopped := false

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Permission-denied or similar — log and skip the
			// offending entry, but continue the walk.
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("agentsmd: walk error", "path", path, "err", err)
			}
			return nil
		}
		if stopped {
			return filepath.SkipAll
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if _, skip := excludeDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		// File.  Skip anything that isn't one of our context names.
		name := d.Name()
		if name != "AGENTS.md" && name != "CLAUDE.md" && name != "CLAUDE.local.md" {
			return nil
		}
		// Skip root-level files — already loaded as SourceProjectRoot.
		if filepath.Dir(path) == root {
			return nil
		}

		if len(blocks) >= MaxRecursiveFiles {
			slog.Warn("agentsmd: recursive file cap hit; remaining files skipped",
				"cap", MaxRecursiveFiles, "first_skipped", path)
			stopped = true
			return filepath.SkipAll
		}

		info, statErr := d.Info()
		if statErr == nil && info.Size() > 0 {
			if totalBytes+int(info.Size()) > MaxRecursiveBytes {
				slog.Warn("agentsmd: recursive byte cap hit; file skipped",
					"cap", MaxRecursiveBytes, "path", path, "size", info.Size())
				return nil
			}
		}

		b, ok := readBlock(path, relTo(root, path), SourceProjectSubdir)
		if !ok {
			return nil
		}
		blocks = append(blocks, b)
		if info != nil {
			totalBytes += int(info.Size())
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		slog.Warn("agentsmd: recursive walk failed", "root", root, "err", walkErr)
	}

	// Deterministic order: by relative path (depth-ish then lex).
	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].RelPath < blocks[j].RelPath
	})
	return blocks
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
// For project-subdir blocks the comment uses the relative path
// (e.g. `project/subdir: internal/skill/AGENTS.md`) so the model can
// locate the file inside the working tree.
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
