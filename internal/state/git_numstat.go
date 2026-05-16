package state

import (
	"bufio"
	"bytes"
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/cfbender/hygge/internal/gitexec"
)

// Numstat holds the line-level diff counts for a single file.
type Numstat struct {
	Added   int
	Deleted int
}

// NumstatForFiles runs `git diff --numstat HEAD -- <files...>` with the
// production non-interactive git runner.
func NumstatForFiles(ctx context.Context, projectDir string, files []string) map[string]Numstat {
	return NumstatForFilesWithGitRunner(ctx, projectDir, files, nil)
}

// NumstatForFilesWithGitRunner runs `git diff --numstat HEAD -- <files...>` in
// projectDir and returns a map from absolute path to Numstat. Passing nil uses
// the production non-interactive git runner.
//
// The call is best-effort: any error (git not found, not a repo, timeout)
// returns an empty map rather than propagating an error to the caller.
// A 2-second context timeout prevents a hanging git from blocking the UI.
func NumstatForFilesWithGitRunner(ctx context.Context, projectDir string, files []string, runner gitexec.Runner) map[string]Numstat {
	if len(files) == 0 || projectDir == "" {
		return nil
	}
	if runner == nil {
		runner = gitexec.DefaultRunner{}
	}

	// Apply a short deadline so git can never block the UI render loop.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	args := []string{"diff", "--numstat", "HEAD", "--"}
	args = append(args, files...)

	out, err := runner.Run(ctx, projectDir, args...)
	if err != nil {
		return nil
	}
	return parseNumstat(out, projectDir)
}

// parseNumstat parses the output of `git diff --numstat HEAD -- ...`.
// Each line is: <added>\t<deleted>\t<path>
// where <added> or <deleted> may be "-" for binary files (treated as 0).
// The returned map uses absolute paths derived from projectDir+path.
func parseNumstat(data []byte, projectDir string) map[string]Numstat {
	result := make(map[string]Numstat)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			continue
		}
		added := parseStatInt(parts[0])
		deleted := parseStatInt(parts[1])
		relPath := parts[2]
		if relPath == "" {
			continue
		}
		// git diff --numstat returns paths relative to the repo root (== projectDir).
		absPath := relPath
		if !strings.HasPrefix(relPath, "/") {
			absPath = projectDir + "/" + relPath
		}
		result[absPath] = Numstat{Added: added, Deleted: deleted}
	}
	return result
}

// parseStatInt parses a numstat field: a decimal integer, or "-" for binary
// (returned as 0).
func parseStatInt(s string) int {
	if s == "-" {
		return 0
	}
	n, _ := strconv.Atoi(s)
	return n
}
