package state

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Numstat holds the line-level diff counts for a single file.
type Numstat struct {
	Added   int
	Deleted int
}

// NumstatForFiles runs `git diff --numstat HEAD -- <files...>` in projectDir
// and returns a map from absolute path to Numstat.
//
// Safety: the invocation sets GIT_TERMINAL_PROMPT=0, GIT_ASKPASS=/bin/echo,
// SSH_ASKPASS=/bin/echo, and GCM_INTERACTIVE=Never, and blanks
// credential.helper and core.askPass via -c flags so no authentication
// prompt can appear in the terminal.
//
// The call is best-effort: any error (git not found, not a repo, timeout)
// returns an empty map rather than propagating an error to the caller.
// A 2-second context timeout prevents a hanging git from blocking the UI.
func NumstatForFiles(ctx context.Context, projectDir string, files []string) map[string]Numstat {
	if len(files) == 0 || projectDir == "" {
		return nil
	}

	// Apply a short deadline so git can never block the UI render loop.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	args := []string{
		"-c", "credential.helper=",
		"-c", "core.askPass=",
		"diff", "--numstat", "HEAD", "--",
	}
	args = append(args, files...)

	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // projectDir is user-supplied project root
	cmd.Dir = projectDir
	// Inherit the full environment so git can find its own binaries, then
	// override the interactive-prompt variables so no dialog can appear.
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/echo",
		"SSH_ASKPASS=/bin/echo",
		"GCM_INTERACTIVE=Never",
	)
	cmd.Stdin = nil // explicit: no stdin

	out, err := cmd.Output()
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
