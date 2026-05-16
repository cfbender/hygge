package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/cfbender/hygge/internal/permission"
)

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// writeTool implements the "write" built-in.
type writeTool struct{ reads *readTracker }

func newWriteTool(r *readTracker) *writeTool { return &writeTool{reads: r} }

func (t *writeTool) Name() string { return "write" }

// Parallelizable returns false: write mutates the filesystem and must not
// run concurrently with other tools that may read or write the same file.
func (t *writeTool) Parallelizable() bool { return false }

func (t *writeTool) Description() string {
	return "Overwrite or create a file with the supplied content. Parent directories " +
		"are created as needed. If the file already exists, it must have been read this " +
		"session via the read tool first (anti-clobber). Requires file.write permission."
}

func (t *writeTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path", "content"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or pwd-relative path to write.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Full file contents to write. Replaces any existing content.",
			},
		},
	}
}

func (t *writeTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	// content is required; the raw JSON must contain the key even if empty.
	if !rawHasKey(raw, "content") {
		return Result{}, newInvalidArgs("content is required", nil)
	}
	var a writeArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, newInvalidArgs("path is required", nil)
	}

	abs, err := resolvePath(ec, a.Path)
	if err != nil {
		return Result{}, newInvalidArgs(err.Error(), err)
	}

	existed := true
	info, statErr := os.Stat(abs)
	if statErr != nil {
		if errors.Is(statErr, fs.ErrNotExist) {
			existed = false
		} else {
			return Result{}, newExecutionFailed(fmt.Sprintf("stat %s", abs), statErr)
		}
	} else if info.IsDir() {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("refusing to overwrite directory: %s", abs),
			Metadata: map[string]any{
				"path":  abs,
				"error": "is_directory",
			},
		}, nil
	}

	// Anti-clobber: if the file exists and was not read this session, refuse.
	if existed && t.reads != nil && !t.reads.hasRead(ec.SessionID, abs) {
		return Result{
			IsError: true,
			Content: fmt.Sprintf("refusing to overwrite %s: file exists but was not read this session. Use read first.", abs),
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

	before := ""
	if existed {
		data, err := os.ReadFile(abs) //nolint:gosec // permission-gated above
		if err != nil {
			return Result{}, newExecutionFailed(fmt.Sprintf("read %s", abs), err)
		}
		before = string(data)
	}

	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil { //nolint:gosec // standard repo perms
		return Result{}, newExecutionFailed(fmt.Sprintf("mkdir %s", filepath.Dir(abs)), err)
	}
	if err := os.WriteFile(abs, []byte(a.Content), 0o644); err != nil { //nolint:gosec // user-content tool; permission-gated
		return Result{}, newExecutionFailed(fmt.Sprintf("write %s", abs), err)
	}

	// Mark as read so subsequent edits don't trip anti-clobber.
	if t.reads != nil {
		t.reads.markRead(ec.SessionID, abs)
	}

	summary := fmt.Sprintf("wrote %d bytes to %s", len(a.Content), abs)
	beforeLabel := abs + " (before)"
	if !existed {
		beforeLabel = "/dev/null"
	}
	return Result{
		Content: toolResultWithDiff(summary, abs, beforeLabel, abs+" (after)", before, a.Content),
		Metadata: map[string]any{
			"path":          abs,
			"bytes_written": len(a.Content),
			"created":       !existed,
		},
	}, nil
}
