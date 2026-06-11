package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"regexp"
	"strings"

	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/tool"
)

// nameSanitizer constrains MCP-derived tool names to the
// ^[a-zA-Z0-9_-]+$ shape providers (OpenAI in particular) accept.
// Anything else collapses to "_".
var nameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// NewMCPTool wraps one MCP tool definition as a hygge tool.Tool.
// The returned tool calls back to the supplied Client on each Execute.
//
// Tool naming: the registered tool's Name() is "<server>_<tool>",
// lowercased with disallowed characters replaced by "_".  The
// underlying RPC still uses def.Name verbatim — the prefix exists so
// multiple MCP servers can advertise tools with the same short name
// without colliding in hygge's registry.
func NewMCPTool(client *Client, def MCPToolDef, permissionCategory permission.Category) tool.Tool {
	if permissionCategory == "" {
		permissionCategory = permission.CategoryMCP
	}
	prefix := strings.ToLower(client.Name())
	if prefix == "" {
		prefix = "mcp"
	}
	prefix = nameSanitizer.ReplaceAllString(prefix, "_")
	short := strings.ToLower(def.Name)
	short = nameSanitizer.ReplaceAllString(short, "_")
	return &mcpTool{
		client:             client,
		def:                def,
		fullName:           prefix + "_" + short,
		serverName:         client.Name(),
		permissionCategory: permissionCategory,
	}
}

// mcpTool implements tool.Tool by RPC-calling the underlying MCP
// server on each Execute.
type mcpTool struct {
	client             *Client
	def                MCPToolDef
	fullName           string
	serverName         string
	permissionCategory permission.Category
}

func (t *mcpTool) Name() string { return t.fullName }

// Parallelizable returns false: MCP tools call external services whose
// side effects are unknown, so they are always executed serially.
func (t *mcpTool) Parallelizable() bool { return false }

func (t *mcpTool) Description() string {
	desc := strings.TrimSpace(t.def.Description)
	label := t.serverName
	if label == "" {
		label = "mcp"
	}
	if desc == "" {
		return fmt.Sprintf("[%s] MCP tool: %s", label, t.def.Name)
	}
	return fmt.Sprintf("[%s] %s", label, desc)
}

func (t *mcpTool) InputSchema() map[string]any {
	if t.def.InputSchema == nil {
		// Provide a minimally-valid schema; the MCP server didn't
		// supply one.
		return map[string]any{"type": "object"}
	}
	// Return a shallow copy so callers can mutate it without
	// affecting the cached definition.
	out := make(map[string]any, len(t.def.InputSchema))
	maps.Copy(out, t.def.InputSchema)
	return out
}

func (t *mcpTool) Execute(ctx context.Context, args json.RawMessage, ec tool.ExecContext) (tool.Result, error) {
	if ec.Permission == nil {
		return tool.Result{}, &tool.ToolError{
			Code:    tool.CodeExecutionFailed,
			Message: "permission engine not configured",
		}
	}

	req := permission.Request{
		SessionID: ec.SessionID,
		Category:  t.permissionCategory,
		Target:    t.fullName,
		ToolName:  t.fullName,
		Pwd:       ec.Pwd,
		Reason:    "MCP tool call to " + t.serverName,
	}
	if err := permission.Gate(ctx, ec.Permission, req); err != nil {
		var denied *permission.DeniedError
		if !errors.As(err, &denied) {
			return tool.Result{}, &tool.ToolError{
				Code:    tool.CodePermissionDenied,
				Message: fmt.Sprintf("permission ask failed: %v", err),
				Wrapped: err,
			}
		}
		reason := denied.Reason
		if reason == "" {
			reason = "denied by policy"
		}
		return tool.Result{
			IsError: true,
			Content: fmt.Sprintf("permission denied: %s", reason),
			Metadata: map[string]any{
				"permission":        "denied",
				"permission_reason": reason,
				"mcp_server":        t.serverName,
				"mcp_tool":          t.def.Name,
			},
		}, nil
	}

	// Permission granted: invoke the MCP server.
	res, err := t.client.CallTool(ctx, t.def.Name, args)
	if err != nil {
		return tool.Result{}, &tool.ToolError{
			Code:    tool.CodeExecutionFailed,
			Message: fmt.Sprintf("mcp call %q on %s failed: %v", t.def.Name, t.serverName, err),
			Wrapped: err,
		}
	}

	// Translate MCP content blocks into a tool.Result.
	content, dropped := renderContent(res.Content)
	if dropped > 0 {
		slog.Warn("mcp: dropped non-text content blocks",
			"server", t.serverName, "tool", t.def.Name, "dropped", dropped)
	}

	return tool.Result{
		Content: content,
		IsError: res.IsError,
		Metadata: map[string]any{
			"mcp_server":     t.serverName,
			"mcp_tool":       t.def.Name,
			"content_blocks": len(res.Content),
		},
	}, nil
}

// renderContent concatenates text content blocks and emits placeholder
// markers for non-text blocks.  Returns the rendered string and the
// number of non-text blocks that were dropped.
func renderContent(blocks []ContentBlock) (string, int) {
	if len(blocks) == 0 {
		return "", 0
	}
	var b strings.Builder
	dropped := 0
	for i, blk := range blocks {
		if i > 0 {
			b.WriteString("\n")
		}
		switch blk.Type {
		case "text":
			b.WriteString(blk.Text)
		case "image":
			mt := blk.MimeType
			if mt == "" {
				mt = "unknown"
			}
			fmt.Fprintf(&b, "[image dropped: %s; binary content not supported in v0.2]", mt)
			dropped++
		default:
			fmt.Fprintf(&b, "[content block dropped: type=%q; not supported in v0.2]", blk.Type)
			dropped++
		}
	}
	return b.String(), dropped
}
