package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cfbender/hygge/internal/permission"
)

const grepDefaultMaxResults = 100

// excludeDirs is the hard-coded set of directory names we never descend into
// for grep and glob.  These represent build artefacts and vendored deps where
// matches are almost never what the user wanted.
var excludeDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	"vendor":       {},
	".venv":        {},
	"__pycache__":  {},
	"dist":         {},
	"target":       {},
}

type grepArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	Include    string `json:"include,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

// grepTool implements the "grep" built-in.
type grepTool struct {
	// lookPath lets tests stub out rg discovery.
	lookPath func(name string) (string, error)
}

func newGrepTool() *grepTool {
	return &grepTool{lookPath: exec.LookPath}
}

func (t *grepTool) Name() string { return "grep" }

// Parallelizable returns true: grep is a read-only filesystem scan
// with no mutation, making it safe to run concurrently with sibling tools.
func (t *grepTool) Parallelizable() bool { return true }

func (t *grepTool) Description() string {
	return "Search file contents with a regular expression. Uses ripgrep (rg) when " +
		"available, otherwise walks the directory tree with Go's RE2 engine. Skips " +
		".git, node_modules, vendor, .venv, __pycache__, dist, and target directories. " +
		"Requires file.read permission for the search root."
}

func (t *grepTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"pattern"},
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern. Go RE2 syntax (or ripgrep when present).",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory or file to search. Defaults to the session pwd.",
			},
			"include": map[string]any{
				"type":        "string",
				"description": "Optional glob filter for filenames, e.g. \"*.go\".",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     1000,
				"description": fmt.Sprintf("Maximum matches to return (default %d, max 1000).", grepDefaultMaxResults),
			},
		},
	}
}

// match is one (path, line, text) result before formatting.
type match struct {
	path string
	line int
	text string
	mod  int64 // unix nano for sorting by modtime
}

func (t *grepTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	var a grepArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Pattern == "" {
		return Result{}, newInvalidArgs("pattern is required", nil)
	}
	if a.MaxResults < 0 {
		return Result{}, newInvalidArgs("max_results must be > 0", nil)
	}
	maxResults := a.MaxResults
	if maxResults == 0 {
		maxResults = grepDefaultMaxResults
	}

	rawRoot := a.Path
	if rawRoot == "" {
		rawRoot = ec.Pwd
		if rawRoot == "" {
			rawRoot = "."
		}
	}
	root, err := resolvePath(ec, rawRoot)
	if err != nil {
		return Result{}, newInvalidArgs(err.Error(), err)
	}

	_, denied, perr := askPermission(ctx, ec, permission.Request{
		Category: permission.CategoryFileRead,
		Target:   root,
		ToolName: t.Name(),
	})
	if perr != nil {
		return Result{}, perr
	}
	if denied != nil {
		return *denied, nil
	}

	// Validate the regex up front so an invalid pattern surfaces as an
	// IsError result regardless of which backend runs it.
	if _, err := regexp.Compile(a.Pattern); err != nil {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("invalid regex: %v", err),
			Metadata: map[string]any{
				"error":   "invalid_regex",
				"pattern": a.Pattern,
			},
		}, nil
	}

	// Pick a backend.
	useRg := false
	rgPath := ""
	if t.lookPath != nil {
		if p, err := t.lookPath("rg"); err == nil {
			useRg = true
			rgPath = p
		}
	}

	var (
		matches       []match
		filesSearched int
		runErr        error
	)
	if useRg {
		matches, filesSearched, runErr = grepWithRg(ctx, rgPath, root, a.Pattern, a.Include, maxResults)
	} else {
		matches, filesSearched, runErr = grepWalk(ctx, root, a.Pattern, a.Include, maxResults)
	}
	if runErr != nil {
		// Internal walk/exec failures bubble as transport errors; the
		// pattern itself was already validated.
		return Result{}, newExecutionFailed("grep", runErr)
	}

	// Sort by modtime desc, then path/line asc for stability.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].mod != matches[j].mod {
			return matches[i].mod > matches[j].mod
		}
		if matches[i].path != matches[j].path {
			return matches[i].path < matches[j].path
		}
		return matches[i].line < matches[j].line
	})

	if len(matches) > maxResults {
		matches = matches[:maxResults]
	}

	var out strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&out, "%s:%d:%s\n", m.path, m.line, m.text)
	}

	content := out.String()
	if content == "" {
		content = "no matches"
	}

	return Result{
		Content: content,
		Metadata: map[string]any{
			"matches":        len(matches),
			"files_searched": filesSearched,
			"used_rg":        useRg,
			"root":           root,
			"pattern":        a.Pattern,
		},
	}, nil
}

// grepWalk implements the pure-Go fallback: walk root, filter files,
// scan each with bufio.Scanner.  Suitable for small/medium trees; rg is
// strictly faster on large repos but our ceiling is max_results so this
// stays bounded.
func grepWalk(ctx context.Context, root, pattern, include string, limit int) ([]match, int, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, 0, err
	}

	var (
		results       []match
		filesSearched int
	)
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrPermission) {
				return nil
			}
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if _, skip := excludeDirs[d.Name()]; skip && p != root {
				return filepath.SkipDir
			}
			return nil
		}
		if include != "" {
			ok, err := doublestar.Match(include, d.Name())
			if err != nil || !ok {
				return nil
			}
		}
		// Per-file: open, scan lines, collect matches.
		filesSearched++
		info, err := d.Info()
		if err != nil {
			return nil
		}
		modNano := info.ModTime().UnixNano()

		f, err := os.Open(p) //nolint:gosec // permission-gated at root
		if err != nil {
			return nil // skip unreadable files
		}
		results = scanFile(f, p, re, modNano, results, limit*4)
		_ = f.Close()
		if len(results) >= limit*8 {
			// Hard cap on collection size; sorting trims later.
			return io.EOF
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, io.EOF) {
		return nil, filesSearched, walkErr
	}
	return results, filesSearched, nil
}

// scanFile streams f and appends matches.  The fileCap parameter limits
// how many matches per file to collect (so a runaway grep does not exhaust
// memory before we sort and trim).
func scanFile(r io.Reader, path string, re *regexp.Regexp, mod int64, acc []match, fileCap int) []match {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	added := 0
	lineno := 0
	for scanner.Scan() {
		lineno++
		text := scanner.Text()
		if !re.MatchString(text) {
			continue
		}
		acc = append(acc, match{path: path, line: lineno, text: text, mod: mod})
		added++
		if added >= fileCap {
			break
		}
	}
	return acc
}

// grepWithRg shells out to ripgrep with a stable output format and
// parses the result.  This is just an optimisation; the parse loop is
// careful to be tolerant of whatever rg emits.
func grepWithRg(ctx context.Context, rgPath, root, pattern, include string, limit int) ([]match, int, error) {
	args := []string{
		"--no-heading",
		"--line-number",
		"--with-filename",
		"--color", "never",
		"--no-config",
		"--max-count", fmt.Sprintf("%d", limit),
	}
	for dir := range excludeDirs {
		args = append(args, "--glob", "!"+dir)
	}
	if include != "" {
		args = append(args, "--glob", include)
	}
	args = append(args, "--", pattern, root)

	cmd := exec.CommandContext(ctx, rgPath, args...) //nolint:gosec // rgPath came from exec.LookPath; args are bounded
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, 0, err
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, err
	}

	// Drain stderr to avoid blocking on its pipe buffer.
	go func() { _, _ = io.Copy(io.Discard, stderr) }()

	var results []match
	files := map[string]int64{} // path -> modtime nano
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: path:line:text — but text may contain colons.
		p, ln, text, ok := splitRgLine(line)
		if !ok {
			continue
		}
		mod, ok := files[p]
		if !ok {
			if info, err := os.Stat(p); err == nil {
				mod = info.ModTime().UnixNano()
			}
			files[p] = mod
		}
		results = append(results, match{path: p, line: ln, text: text, mod: mod})
	}

	waitErr := cmd.Wait()
	// rg exits 1 when no matches; that's a normal outcome, not an error.
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) && exitErr.ExitCode() == 1 {
			waitErr = nil
		}
	}
	if waitErr != nil {
		return nil, 0, waitErr
	}
	return results, len(files), nil
}

// splitRgLine parses "path:line:text" leniently — line is the first
// run of digits after the first colon.
func splitRgLine(s string) (path string, line int, text string, ok bool) {
	before, after, ok0 := strings.Cut(s, ":")
	if !ok0 {
		return "", 0, "", false
	}
	path = before
	rest := after
	before, after, ok0 = strings.Cut(rest, ":")
	if !ok0 {
		return "", 0, "", false
	}
	var n int
	if _, err := fmt.Sscanf(before, "%d", &n); err != nil {
		return "", 0, "", false
	}
	return path, n, after, true
}
