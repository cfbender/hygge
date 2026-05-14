package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/cfbender/hygge/internal/permission"
)

const (
	readDefaultLimit = 2000
	readMaxLineChars = 2000
)

type readArgs struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// readTool implements the "read" built-in.
type readTool struct{ reads *readTracker }

func newReadTool(r *readTracker) *readTool { return &readTool{reads: r} }

func (t *readTool) Name() string { return "read" }

func (t *readTool) Description() string {
	return "Read a file from the local filesystem with optional line offset and limit. " +
		"Returns line-numbered content; long lines are truncated with a marker. " +
		"Requires file.read permission for the path."
}

func (t *readTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"path"},
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Absolute or pwd-relative path to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "1-indexed line to start from (default 1).",
			},
			"limit": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": fmt.Sprintf("Max lines to return (default %d).", readDefaultLimit),
			},
		},
	}
}

func (t *readTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	var a readArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	if a.Path == "" {
		return Result{}, newInvalidArgs("path is required", nil)
	}
	if a.Offset < 0 {
		return Result{}, newInvalidArgs("offset must be >= 1", nil)
	}
	if a.Limit < 0 {
		return Result{}, newInvalidArgs("limit must be >= 1", nil)
	}
	offset := a.Offset
	if offset == 0 {
		offset = 1
	}
	limit := a.Limit
	if limit == 0 {
		limit = readDefaultLimit
	}

	abs, err := resolvePath(ec, a.Path)
	if err != nil {
		return Result{}, newInvalidArgs(err.Error(), err)
	}

	_, denied, perr := askPermission(ctx, ec, permission.Request{
		Category: permission.CategoryFileRead,
		Target:   abs,
		ToolName: t.Name(),
	})
	if perr != nil {
		return Result{}, perr
	}
	if denied != nil {
		return *denied, nil
	}

	f, err := os.Open(abs) //nolint:gosec // path is permission-gated above
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Result{
				IsError: true,
				Content: fmt.Sprintf("file not found: %s", abs),
				Metadata: map[string]any{
					"path":  abs,
					"error": "not_found",
				},
			}, nil
		}
		if errors.Is(err, fs.ErrPermission) {
			return Result{
				IsError: true,
				Content: fmt.Sprintf("permission denied by OS: %s", abs),
				Metadata: map[string]any{
					"path":  abs,
					"error": "os_permission",
				},
			}, nil
		}
		return Result{}, newExecutionFailed(fmt.Sprintf("open %s", abs), err)
	}
	defer f.Close() //nolint:errcheck // read-only file; close error is non-actionable

	var (
		out          strings.Builder
		linesScanned int
		linesEmitted int
		truncated    int
	)

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		linesScanned++
		if linesScanned < offset {
			continue
		}
		if linesEmitted >= limit {
			// Keep scanning to count total_lines.  Cheap enough.
			continue
		}
		line := scanner.Text()
		if len(line) > readMaxLineChars {
			line = line[:readMaxLineChars] + "… (truncated)"
			truncated++
		}
		fmt.Fprintf(&out, "%d: %s\n", linesScanned, line)
		linesEmitted++
	}
	if err := scanner.Err(); err != nil {
		return Result{}, newExecutionFailed(fmt.Sprintf("read %s", abs), err)
	}

	// Mark this file as read for anti-clobber tracking.
	if t.reads != nil {
		t.reads.markRead(ec.SessionID, abs)
	}

	return Result{
		Content: out.String(),
		Metadata: map[string]any{
			"path":            abs,
			"total_lines":     linesScanned,
			"lines_returned":  linesEmitted,
			"truncated_lines": truncated,
			"offset":          offset,
			"limit":           limit,
		},
	}, nil
}
