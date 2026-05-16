package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/cfbender/hygge/internal/permission"
)

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll bool   `json:"replaceAll,omitempty"`
}

// editTool implements the "edit" built-in.
type editTool struct{ reads *readTracker }

func newEditTool(r *readTracker) *editTool { return &editTool{reads: r} }

func (t *editTool) Name() string { return "edit" }

// Parallelizable returns false: edit mutates the filesystem and must not
// run concurrently with other tools that may read or write the same file.
func (t *editTool) Parallelizable() bool { return false }

func (t *editTool) Description() string {
	return "Perform an exact-string replacement in a file. oldString must match the file " +
		"contents verbatim and be unique unless replaceAll is true. The file must have " +
		"been read this session first. Requires file.write permission."
}

func (t *editTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path", "oldString", "newString"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or pwd-relative path to edit.",
			},
			"oldString": map[string]any{
				"type":        "string",
				"description": "Exact text to find. Must be unique in the file unless replaceAll is true.",
			},
			"newString": map[string]any{
				"type":        "string",
				"description": "Replacement text. Must differ from oldString.",
			},
			"replaceAll": map[string]any{
				"type":        "boolean",
				"description": "Replace every occurrence. Default false.",
			},
		},
	}
}

func (t *editTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	// Required-field presence checks before decode: oldString and newString
	// have string types and Go cannot distinguish "missing" from "empty".
	for _, k := range []string{"path", "oldString", "newString"} {
		if !rawHasKey(raw, k) {
			return Result{}, newInvalidArgs(k+" is required", nil)
		}
	}
	var a editArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, newInvalidArgs("path is required", nil)
	}
	if a.OldString == "" {
		return Result{
			IsError:  true,
			Content:  "oldString is empty",
			Metadata: map[string]any{"error": "empty_old_string"},
		}, nil
	}
	if a.OldString == a.NewString {
		return Result{
			IsError:  true,
			Content:  "oldString and newString are identical; nothing to do",
			Metadata: map[string]any{"error": "no_change"},
		}, nil
	}

	abs, err := resolvePath(ec, a.Path)
	if err != nil {
		return Result{}, newInvalidArgs(err.Error(), err)
	}

	if t.reads == nil || !t.reads.hasRead(ec.SessionID, abs) {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("refusing to edit %s: file was not read this session. Use read first.", abs),
			Metadata: map[string]any{
				"path":  abs,
				"error": "not_read_this_session",
			},
		}, nil
	}

	_, denied, perr := askPermission(ctx, ec, permission.Request{
		Category: permission.CategoryFileWrite,
		Target:   abs,
		DiffPath: abs,
		ToolName: t.Name(),
	})
	if perr != nil {
		return Result{}, perr
	}
	if denied != nil {
		return *denied, nil
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{
				IsError:  true,
				Content:  fmt.Sprintf("file not found: %s", abs),
				Metadata: map[string]any{"path": abs, "error": "not_found"},
			}, nil
		}
		return Result{}, newExecutionFailed(fmt.Sprintf("stat %s", abs), err)
	}
	if info.IsDir() {
		return Result{
			IsError:  true,
			Content:  fmt.Sprintf("refusing to edit directory: %s", abs),
			Metadata: map[string]any{"path": abs, "error": "is_directory"},
		}, nil
	}
	mode := info.Mode().Perm()

	data, err := os.ReadFile(abs) //nolint:gosec // permission-gated above
	if err != nil {
		return Result{}, newExecutionFailed(fmt.Sprintf("read %s", abs), err)
	}
	content := string(data)
	count := strings.Count(content, a.OldString)
	switch {
	case count == 0:
		return Result{
			IsError:  true,
			Content:  fmt.Sprintf("oldString not found in %s", abs),
			Metadata: map[string]any{"path": abs, "error": "not_found_in_file"},
		}, nil
	case count > 1 && !a.ReplaceAll:
		return Result{
			IsError: true,
			Content: fmt.Sprintf("found %d matches for oldString in %s; provide more context or set replaceAll=true", count, abs),
			Metadata: map[string]any{
				"path":    abs,
				"matches": count,
				"error":   "ambiguous",
			},
		}, nil
	}

	var updated string
	var replacements int
	if a.ReplaceAll {
		updated = strings.ReplaceAll(content, a.OldString, a.NewString)
		replacements = count
	} else {
		updated = strings.Replace(content, a.OldString, a.NewString, 1)
		replacements = 1
	}

	if err := os.WriteFile(abs, []byte(updated), mode); err != nil { //nolint:gosec // permission-gated above
		return Result{}, newExecutionFailed(fmt.Sprintf("write %s", abs), err)
	}

	summary := fmt.Sprintf("edited %s: %d replacement(s)", abs, replacements)
	return Result{
		Content: toolResultWithDiff(summary, abs, abs+" (before)", abs+" (after)", a.OldString, a.NewString),
		Metadata: map[string]any{
			"path":          abs,
			"replacements":  replacements,
			"bytes_written": len(updated),
		},
	}, nil
}
