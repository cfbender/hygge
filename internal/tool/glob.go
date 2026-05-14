package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/cfbender/hygge/internal/permission"
)

const globDefaultMaxResults = 500

type globArgs struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path,omitempty"`
	MaxResults int    `json:"max_results,omitempty"`
}

// globTool implements the "glob" built-in.
type globTool struct{}

func newGlobTool() *globTool { return &globTool{} }

func (t *globTool) Name() string { return "glob" }

func (t *globTool) Description() string {
	return "Find files matching a doublestar glob (e.g. \"**/*.go\"). Results are sorted " +
		"by modification time descending. Skips .git, node_modules, vendor, .venv, " +
		"__pycache__, dist, and target directories. Requires file.read permission for " +
		"the search root."
}

func (t *globTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"pattern"},
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Doublestar glob, e.g. \"**/*.go\".",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Directory to start from. Defaults to the session pwd.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     10000,
				"description": fmt.Sprintf("Maximum results to return (default %d, max 10000).", globDefaultMaxResults),
			},
		},
	}
}

func (t *globTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	var a globArgs
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
		maxResults = globDefaultMaxResults
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

	if !doublestar.ValidatePattern(a.Pattern) {
		return Result{
			IsError:  true,
			Content:  fmt.Sprintf("invalid glob pattern: %s", a.Pattern),
			Metadata: map[string]any{"error": "invalid_pattern", "pattern": a.Pattern},
		}, nil
	}

	// Use doublestar.Glob with the root as the OS filesystem.
	fsys := os.DirFS(root)
	matches, err := doublestar.Glob(fsys, a.Pattern, doublestar.WithFilesOnly(), doublestar.WithFailOnIOErrors())
	if err != nil {
		// Some matches may have been produced before the IO error; fall
		// back to walking with skip-on-error semantics.
		matches = nil
	}

	type entry struct {
		path string
		mod  int64
	}
	var entries []entry
	for _, rel := range matches {
		if ctx.Err() != nil {
			return Result{}, newExecutionFailed("context cancelled", ctx.Err())
		}
		if containsExcluded(rel) {
			continue
		}
		full := filepath.Join(root, rel)
		info, statErr := os.Stat(full)
		if statErr != nil {
			continue
		}
		entries = append(entries, entry{path: full, mod: info.ModTime().UnixNano()})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].mod != entries[j].mod {
			return entries[i].mod > entries[j].mod
		}
		return entries[i].path < entries[j].path
	})

	truncated := false
	if len(entries) > maxResults {
		entries = entries[:maxResults]
		truncated = true
	}

	var out strings.Builder
	for _, e := range entries {
		out.WriteString(e.path)
		out.WriteByte('\n')
	}

	content := out.String()
	if content == "" {
		content = "no matches"
	}

	return Result{
		Content: content,
		Metadata: map[string]any{
			"matches":   len(entries),
			"truncated": truncated,
			"root":      root,
			"pattern":   a.Pattern,
		},
	}, nil
}

// containsExcluded reports whether the path traverses any excluded
// directory.  doublestar may match files deep inside excluded trees if
// the pattern is liberal (e.g. "**/*"); this check applies the same
// filter as the grep walker.
func containsExcluded(rel string) bool {
	parts := strings.Split(rel, string(os.PathSeparator))
	if string(os.PathSeparator) != "/" {
		parts = strings.Split(rel, "/")
	}
	for _, p := range parts {
		if _, skip := excludeDirs[p]; skip {
			return true
		}
	}
	return false
}
