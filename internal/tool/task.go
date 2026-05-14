package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
)

// SubagentRunner is the interface the `task` tool needs from the
// sub-agent runtime.  Defining it here (rather than importing
// internal/subagent) keeps the tool package free of an agent / store
// dependency loop: internal/subagent imports internal/tool to build a
// per-call tool registry, so the reverse direction must be cut.
//
// internal/subagent.Runner satisfies this interface; cmd/hygge/cli
// wires the concrete runner into [NewTaskTool] at bootstrap.
type SubagentRunner interface {
	// Run executes one sub-agent invocation synchronously and
	// returns the result.  See internal/subagent.Runner.Run for full
	// semantics.
	Run(ctx context.Context, in SubagentRunInput) (SubagentResult, error)

	// Types returns the registered sub-agent types (name +
	// description) so the tool's input-schema enum can be built
	// lazily.  The runner returns a fresh slice on each call.
	Types() []SubagentType
}

// SubagentType is the tool-facing view of an internal/subagent.Type.
// We mirror only the fields the task tool needs for its input schema.
type SubagentType struct {
	Name        string
	Description string
}

// SubagentRunInput is the tool-facing view of
// internal/subagent.RunInput.
type SubagentRunInput struct {
	ParentSessionID string
	ParentToolUseID string
	Type            string
	Description     string
	Prompt          string
	ModelName       string
}

// SubagentResult mirrors internal/subagent.Result with the subset of
// fields the task tool surfaces in its Metadata.
type SubagentResult struct {
	SessionID    string
	FinalText    string
	Usage        provider.Usage
	CostUSD      float64
	Duration     time.Duration
	HitIterLimit bool
}

// TaskTool dispatches a mission to a registered sub-agent type.  The
// tool blocks until the sub-agent finishes (success, iteration-limit
// abort, or hard error) and returns the sub-agent's final assistant
// text as the tool result.
//
// The tool is registered ONLY in the orchestrator's tool registry.
// Sub-agents NEVER see it -- the subagent runtime strips it from
// every sub-agent's tool set regardless of TOML config.  This is the
// recursion guard that prevents a `task` tool from launching another
// `task` tool.
//
// Permission category: [permission.CategoryAgent].  One ask covers
// the entire sub-agent run; individual tools the sub-agent invokes
// still go through their own permission gate (same engine).
type TaskTool struct {
	runner SubagentRunner
}

// NewTaskTool builds a TaskTool backed by runner.  runner must not be
// nil; callers building the orchestrator's tool set are responsible
// for omitting the tool entirely when no runner is configured.
func NewTaskTool(runner SubagentRunner) *TaskTool {
	return &TaskTool{runner: runner}
}

// Name implements [Tool].
func (t *TaskTool) Name() string { return "task" }

// Parallelizable implements [Tool].  Each sub-agent runs in an isolated
// session with its own message history, so concurrent task calls are safe:
// they do not share per-turn state within the parent session.
//
// Note: tools invoked INSIDE a sub-agent still go through their own
// permission checks, and sub-agents share the parent's permission engine.
// The engine's session cache is mutex-guarded, so concurrent sub-agent
// dispatches are safe.
func (t *TaskTool) Parallelizable() bool { return true }

// Description implements [Tool].
func (t *TaskTool) Description() string {
	return "Dispatch a mission to a sub-agent that runs in isolation and returns a single " +
		"summary message. Use this for self-contained work (codebase searches, focused " +
		"refactors, documentation lookups) that would otherwise pollute the main context. " +
		"Sub-agents cannot recursively invoke `task`."
}

// InputSchema implements [Tool].  The schema is built lazily so newly
// loaded TOML types (if any) become visible to the model on the next
// request.
func (t *TaskTool) InputSchema() map[string]any {
	var types []SubagentType
	if t.runner != nil {
		types = t.runner.Types()
	}
	enum := make([]any, 0, len(types))
	descLines := make([]string, 0, len(types))
	for _, ty := range types {
		enum = append(enum, ty.Name)
		descLines = append(descLines, fmt.Sprintf("- %s: %s", ty.Name, ty.Description))
	}
	if len(enum) == 0 {
		// Defensive: at minimum the built-in `general` type should
		// always be present.  Falling back to an empty enum would
		// make the field rejection-only; degrade gracefully instead.
		enum = []any{"general"}
		descLines = []string{"- general: General-purpose sub-agent."}
	}
	typeDesc := "Sub-agent type to dispatch.  Available types:\n" +
		strings.Join(descLines, "\n")

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"subagent_type", "description", "prompt"},
		"properties": map[string]any{
			"subagent_type": map[string]any{
				"type":        "string",
				"enum":        enum,
				"description": typeDesc,
			},
			"description": map[string]any{
				"type": "string",
				"description": "Short 3-5 word human label for this mission (e.g. " +
					"\"search for foo\"). Surfaced to the permission prompt and the audit log.",
			},
			"prompt": map[string]any{
				"type": "string",
				"description": "The full mission text handed to the sub-agent as its first user " +
					"message. Be specific: state the goal, constraints, and the shape of the " +
					"expected answer. The sub-agent returns one final message.",
			},
		},
	}
}

// taskArgs is the decoded shape of the model's tool input.
type taskArgs struct {
	SubagentType string `json:"subagent_type"`
	Description  string `json:"description"`
	Prompt       string `json:"prompt"`
}

// Execute implements [Tool].  See the Stage A design doc for the
// failure mode summary; in short:
//
//   - Unknown subagent_type -> IsError result, no sub-session created.
//   - Permission denied -> IsError result from the shared askPermission
//     helper, no sub-session created.
//   - Sub-agent run failure -> IsError result with the sub-session id
//     in Metadata so the user can inspect the audit trail.
//   - Iteration limit -> non-error Result with hit_iter_limit=true and
//     the agent's abort note as the Content.
//   - Success -> the sub-agent's final assistant text as Content.
func (t *TaskTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	if t.runner == nil {
		return Result{}, newExecutionFailed("subagent runner not configured", nil)
	}

	var a taskArgs
	if err := decodeArgs(raw, &a); err != nil {
		return Result{}, err
	}
	a.SubagentType = strings.TrimSpace(a.SubagentType)
	a.Description = strings.TrimSpace(a.Description)
	a.Prompt = strings.TrimSpace(a.Prompt)

	if a.SubagentType == "" {
		return Result{}, newInvalidArgs("subagent_type is required", nil)
	}
	if a.Description == "" {
		return Result{}, newInvalidArgs("description is required", nil)
	}
	if a.Prompt == "" {
		return Result{}, newInvalidArgs("prompt is required", nil)
	}

	types := t.runner.Types()
	if !subagentTypeKnown(types, a.SubagentType) {
		available := subagentTypeNames(types)
		msg := fmt.Sprintf("unknown subagent_type %q. Available types: %s",
			a.SubagentType, formatNames(available))
		return Result{
			IsError: true,
			Content: msg,
			Metadata: map[string]any{
				"error":         "unknown_subagent_type",
				"subagent_type": a.SubagentType,
				"available":     available,
			},
		}, nil
	}

	// Ask for permission BEFORE creating a sub-session: a deny here
	// should leave no audit row behind (the user explicitly refused).
	// Target is "<type>:<description>" so the permission prompt can
	// show what's about to run.
	target := fmt.Sprintf("%s:%s", a.SubagentType, a.Description)
	_, denied, perr := askPermission(ctx, ec, permission.Request{
		Category: permission.CategoryAgent,
		Target:   target,
		Reason:   a.Description,
		ToolName: t.Name(),
	})
	if perr != nil {
		return Result{}, perr
	}
	if denied != nil {
		return *denied, nil
	}

	// Look up the model name from the agent's ExecContext.  Stage A
	// always reuses the parent's model for the sub-agent; Stage B
	// will switch to per-type overrides resolved from Type.Model.
	modelName := strings.TrimSpace(ec.ModelName)
	if modelName == "" {
		return Result{}, newExecutionFailed("parent model name not available in ExecContext", nil)
	}

	res, runErr := t.runner.Run(ctx, SubagentRunInput{
		ParentSessionID: ec.SessionID,
		ParentToolUseID: ec.ToolUseID,
		Type:            a.SubagentType,
		Description:     a.Description,
		Prompt:          a.Prompt,
		ModelName:       modelName,
	})

	metadata := map[string]any{
		"subagent_type":   a.SubagentType,
		"description":     a.Description,
		"sub_session_id":  res.SessionID,
		"duration_ms":     res.Duration.Milliseconds(),
		"cost_usd":        res.CostUSD,
		"hit_iter_limit":  res.HitIterLimit,
		"input_tokens":    res.Usage.InputTokens,
		"output_tokens":   res.Usage.OutputTokens,
		"cache_read":      res.Usage.CacheReadTokens,
		"cache_write":     res.Usage.CacheWriteTokens,
		"parent_tool_use": ec.ToolUseID,
	}

	if runErr != nil {
		// The sub-session row exists (the runner creates it before
		// any tool work).  Surface the failure as IsError so the
		// parent model can recover, and carry the id in metadata
		// for audit.
		return Result{
			IsError: true,
			Content: fmt.Sprintf("sub-agent run failed: %v\nsub_session_id: %s",
				runErr, res.SessionID),
			Metadata: metadata,
		}, nil
	}

	content := res.FinalText
	if content == "" {
		content = "(sub-agent returned no textual output)"
	}
	return Result{
		Content:  content,
		Metadata: metadata,
	}, nil
}

// subagentTypeKnown reports whether name appears in types.
func subagentTypeKnown(types []SubagentType, name string) bool {
	for _, t := range types {
		if t.Name == name {
			return true
		}
	}
	return false
}

// subagentTypeNames returns the names from types, sorted.
func subagentTypeNames(types []SubagentType) []string {
	names := make([]string, 0, len(types))
	for _, t := range types {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names
}
