package agentsmd

import "path/filepath"

// WalkOption configures WalkUp.  The zero value is a no-op walk.
type WalkOption struct {
	// HomeStop, if non-empty, halts the walk when the current directory
	// equals this path.  Used to prevent project-root detection from
	// walking into the user's home directory and mis-attributing
	// user-layer files as project roots.
	HomeStop string
	// ExcludeDirs is a set of directory basenames whose presence on the
	// path from start to the filesystem root short-circuits the Visit
	// callback for that directory and all directories below it.  The
	// walk still marks excluded directories seen (so LazyTracker does
	// not revisit them) but never calls Visit for them.
	// Empty means no exclusions.
	ExcludeDirs map[string]struct{}
}

// WalkAction is the outcome of one Visit call.
type WalkAction int

const (
	// WalkContinue moves the walk to the parent directory.
	WalkContinue WalkAction = iota
	// WalkStop ends the walk immediately after this step.
	WalkStop
)

// WalkUp walks from start toward the filesystem root, calling visit for
// each directory.  The walk halts when:
//
//   - visit returns WalkStop, OR
//   - the current directory equals opt.HomeStop (exclusive — HomeStop
//     itself is never visited), OR
//   - the next parent equals the current directory (filesystem root), OR
//   - a directory's basename appears in opt.ExcludeDirs (visit is not
//     called for that directory or any of its descendants between start
//     and the root, but they are all still "walked through" so callers
//     can mark them seen).
//
// ExcludeDir short-circuit: if the excluded ancestor is detected before
// the walk reaches it, visit is never called for any directory at or
// below the excluded ancestor.  The walk continues past the excluded
// ancestor toward the root without skipping those parent directories.
//
// Returns the directory where visit returned WalkStop, or "" if the
// walk terminated without visit ever returning WalkStop.
func WalkUp(start string, opt WalkOption, visit func(dir string) WalkAction) string {
	dir := filepath.Clean(start)
	homeStop := ""
	if opt.HomeStop != "" {
		homeStop = filepath.Clean(opt.HomeStop)
	}

	for {
		// HomeStop is a halt boundary — do not visit it.
		if homeStop != "" && dir == homeStop {
			return ""
		}

		// Check whether this directory's basename is excluded.
		_, excluded := opt.ExcludeDirs[filepath.Base(dir)]
		if !excluded {
			if visit(dir) == WalkStop {
				return dir
			}
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Filesystem root.
			return ""
		}
		dir = parent
	}
}

// FindProjectRootFrom walks parents of start looking for the first
// directory that contains one of the project-root markers (AGENTS.md,
// CLAUDE.md, .git, .agents/, .hygge/).  Returns the absolute matching
// directory, or "" if no marker was found before the filesystem root.
//
// FindProjectRootFrom does not stop at $HOME; use findProjectRoot when
// you need the home-stop boundary (Load / agentsmd internal use).  The
// CLI and other callers that just want "the nearest project root from
// here" use this function.
func FindProjectRootFrom(start string) string {
	if start == "" {
		return ""
	}
	return WalkUp(start, WalkOption{}, func(dir string) WalkAction {
		if hasMarker(dir) {
			return WalkStop
		}
		return WalkContinue
	})
}
