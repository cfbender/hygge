package agentsmd

// lazy.go: per-tool-call subdirectory context loader.  See LazyTracker
// for the contract.
//
// # Overview
//
// At startup, Load discovers the user- and project-root layers of
// AGENTS.md / CLAUDE.md / CLAUDE.local.md and folds their content into
// the agent's system prompt unconditionally.  Subdirectory context
// files are NOT loaded at startup — recursively walking the tree would
// over-load the prompt with files the model may never need.
//
// Instead, the agent loop hands a LazyTracker every path its tool calls
// touched.  For each touched directory the tracker walks upward toward
// the project root looking for AGENTS.md / CLAUDE.md / CLAUDE.local.md
// in directories the tracker has not yet visited.  Newly-discovered
// blocks ride along in the NEXT provider turn's system prompt and are
// then forgotten — they are never persisted into session history.
//
// Bounded by MaxLazyContextFiles and MaxLazyContextBytes per session.
// Once either cap fires the tracker permanently disables itself for
// the rest of the session.

import (
	"log/slog"
	"os"
	"path/filepath"
)

// lazyContextFiles enumerates the file names the lazy walker looks for
// inside each candidate directory.  AGENTS.local.md is intentionally
// omitted: it is a project-root-only convention (per-machine override
// of the project-root AGENTS.md) and surfacing it from subdirectories
// is not a workflow we support yet.
var lazyContextFiles = []string{"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md"}

// LazyTracker tracks lazy per-tool-call context loading state for a
// single agent session.  Not safe for concurrent use — callers must
// serialise access (the agent loop already serialises per session).
//
// Construct via NewLazyTracker.  The zero value is not usable.
type LazyTracker struct {
	// projectRoot is the absolute project root.  When empty the tracker
	// is disabled and Touch is a no-op.
	projectRoot string
	// homeDir bounds the walk for parity with Load.  Currently unused —
	// the walk already terminates at projectRoot.
	homeDir string

	// seenDirs records every absolute directory the tracker has
	// considered (whether or not it contained a context file) so we
	// never re-scan or re-inject.
	seenDirs map[string]struct{}
	// seenFiles records every absolute file path already loaded so a
	// directory whose AGENTS.md was loaded at bootstrap is not loaded
	// again if Touch revisits it for a different reason.
	seenFiles map[string]struct{}

	// totalFiles is the running count of blocks the tracker has
	// returned.  totalBytes is the running sum of their Content
	// lengths.  Both feed into the cap check.
	totalFiles int
	totalBytes int

	// capWarned is true once any cap has fired.  Subsequent Touch
	// calls return nil immediately.
	capWarned bool
}

// NewLazyTracker constructs a tracker bounded by projectRoot (the
// first directory containing AGENTS.md / CLAUDE.md / .git / .agents /
// .hygge, as discovered by Load) and seeded with seenDirs — every
// directory whose AGENTS.md / CLAUDE.md was already loaded at
// bootstrap, so those files are never re-injected.
//
// projectRoot == "" disables the tracker: Touch will always return
// nil.  homeDir is reserved for future use (currently unused — the
// walk already terminates at projectRoot).
func NewLazyTracker(homeDir, projectRoot string, seenDirs []string) *LazyTracker {
	t := &LazyTracker{
		homeDir:   filepath.Clean(homeDir),
		seenDirs:  make(map[string]struct{}),
		seenFiles: make(map[string]struct{}),
	}
	if projectRoot != "" {
		t.projectRoot = filepath.Clean(projectRoot)
	}
	for _, d := range seenDirs {
		if d == "" {
			continue
		}
		t.seenDirs[filepath.Clean(d)] = struct{}{}
	}
	return t
}

// Touch reports the directories the agent's most recent tool calls
// referenced.  pwd is the agent's working directory (used to resolve
// relative paths).  Each entry in paths is either a file or directory
// path (absolute or relative to pwd).
//
// For each touched path, the tracker walks upward toward projectRoot
// looking for AGENTS.md, CLAUDE.md, and CLAUDE.local.md in
// directories not yet seen.  Directories named in LazyExcludeDirs are
// skipped (the walk treats them as if they didn't exist).  Every
// visited directory is marked seen whether or not it contained a
// context file.
//
// Returns any newly-discovered blocks with Source =
// SourceProjectSubdir.  When MaxLazyContextFiles or
// MaxLazyContextBytes would be exceeded, the tracker logs slog.Warn
// once and returns nil from then on.
//
// Returns nil when projectRoot is empty, when no new files were
// found, or when the cap has been hit.
func (t *LazyTracker) Touch(pwd string, paths []string) []Block {
	if t == nil || t.projectRoot == "" || t.capWarned {
		return nil
	}
	if len(paths) == 0 {
		return nil
	}

	var out []Block
	for _, raw := range paths {
		if raw == "" {
			continue
		}
		startDir := resolveTouchedDir(pwd, raw)
		if startDir == "" {
			continue
		}
		// Must be inside projectRoot for the walk-up to apply.
		if !withinRoot(startDir, t.projectRoot) {
			continue
		}
		blocks, hitCap, firstSkipped := t.walkUp(startDir)
		out = append(out, blocks...)
		if hitCap {
			slog.Warn("agentsmd: lazy context cap hit; further subdir context disabled for this session",
				"files_loaded", t.totalFiles,
				"bytes_loaded", t.totalBytes,
				"max_files", MaxLazyContextFiles,
				"max_bytes", MaxLazyContextBytes,
				"first_skipped", firstSkipped,
			)
			t.capWarned = true
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// walkUp walks from start toward t.projectRoot, marking each visited
// dir seen and loading any AGENTS.md / CLAUDE.md / CLAUDE.local.md it
// finds.  Returns the new blocks, a flag that indicates a cap was hit
// (caller should stop touching), and (when the cap fired) the path of
// the first file that was skipped.
//
// Directories whose basename appears in LazyExcludeDirs are walked
// through transparently (so the walk can still reach the project
// root) but they never contribute context files, and neither do any
// of their descendants between start and the excluded dir.
func (t *LazyTracker) walkUp(start string) (blocks []Block, hitCap bool, firstSkipped string) {
	// First pass: detect whether any directory between start and
	// projectRoot has an excluded basename.  If yes, every directory
	// at or below it is "under an excluded ancestor" and contributes
	// nothing.  The walk still marks them seen so we don't re-scan.
	excludedFrom := excludedAncestor(start, t.projectRoot)

	dir := start
	for {
		if _, seen := t.seenDirs[dir]; seen {
			return blocks, false, ""
		}
		t.seenDirs[dir] = struct{}{}

		base := filepath.Base(dir)
		_, baseExcluded := LazyExcludeDirs[base]
		// "Under excluded ancestor" tracks whether we are at or
		// below a node whose basename is in the exclude set.  We
		// also skip files in the excluded directory itself.
		underExcluded := baseExcluded || (excludedFrom != "" && withinRoot(dir, excludedFrom))

		if !underExcluded {
			for _, name := range lazyContextFiles {
				p := filepath.Join(dir, name)
				if _, seen := t.seenFiles[p]; seen {
					continue
				}
				info, err := os.Stat(p)
				if err != nil || info.IsDir() {
					continue
				}
				// Cap check BEFORE reading: we don't load
				// partial files when one would push us over.
				size := info.Size()
				if t.totalFiles+1 > MaxLazyContextFiles ||
					int64(t.totalBytes)+size > int64(MaxLazyContextBytes) {
					return blocks, true, p
				}
				data, err := os.ReadFile(p) //nolint:gosec // path comes from discovered project tree
				if err != nil {
					if !os.IsNotExist(err) {
						slog.Warn("agentsmd: lazy read failed", "path", p, "err", err)
					}
					continue
				}
				content := trimSpace(data)
				blk := Block{
					Path:    p,
					RelPath: relTo(t.projectRoot, p),
					Source:  SourceProjectSubdir,
					Content: content,
				}
				t.seenFiles[p] = struct{}{}
				t.totalFiles++
				t.totalBytes += len(content)
				blocks = append(blocks, blk)
			}
		}

		if dir == t.projectRoot {
			return blocks, false, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return blocks, false, ""
		}
		dir = parent
	}
}

// excludedAncestor returns the lowest excluded ancestor of start
// (inclusive) on the walk toward root, or "" when no exclusion
// applies.  Used by walkUp to short-circuit context loading for any
// directory that lives inside (or is) an excluded sub-tree.
func excludedAncestor(start, root string) string {
	dir := start
	for {
		if _, ok := LazyExcludeDirs[filepath.Base(dir)]; ok {
			return dir
		}
		if dir == root {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// trimSpace mirrors readBlock's trim behaviour without copying through
// strings.TrimSpace twice (we have the raw bytes already).
func trimSpace(data []byte) string {
	// strings.TrimSpace handles all unicode whitespace; preserve the
	// same semantics as readBlock for consistency.
	s := string(data)
	// Local copy of strings.TrimSpace for clarity; same behaviour.
	start, end := 0, len(s)
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return s[start:end]
}

// isSpace mirrors ASCII whitespace handling for trimSpace.  Files
// stored on disk are virtually always ASCII whitespace at their edges;
// this avoids a strings import here.
func isSpace(b byte) bool {
	switch b {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// resolveTouchedDir takes the raw path the agent's tool touched and
// returns the absolute directory to start the walk-up from.  Files
// resolve to their parent directory; directories resolve to
// themselves.  Non-existent paths still resolve based on absoluteness
// so a write-to-new-file in a subdir still surfaces that subdir's
// context.
func resolveTouchedDir(pwd, raw string) string {
	abs := raw
	if !filepath.IsAbs(abs) {
		base := pwd
		if base == "" {
			if wd, err := os.Getwd(); err == nil {
				base = wd
			} else {
				return ""
			}
		}
		abs = filepath.Join(base, abs)
	}
	abs = filepath.Clean(abs)
	info, err := os.Stat(abs)
	if err == nil && info.IsDir() {
		return abs
	}
	// File or non-existent path — walk up from the parent.
	return filepath.Dir(abs)
}

// withinRoot reports whether dir is at or below root.  Both inputs
// must already be Clean / Abs.
func withinRoot(dir, root string) bool {
	if dir == root {
		return true
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	// rel == ".." or starts with "../" means dir is above or aside
	// from root.
	if rel == "." {
		return true
	}
	if len(rel) >= 2 && rel[0] == '.' && rel[1] == '.' {
		return false
	}
	return true
}
