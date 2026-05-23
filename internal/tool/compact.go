package tool

import (
	"context"
	"encoding/json"
	"fmt"
)

// Compactor is the interface the `compact` tool needs from the agent.
// Defining it here (rather than importing internal/agent) avoids a
// dependency cycle: internal/agent imports internal/tool, so the reverse
// direction must be cut.
//
// *agent.Agent satisfies this interface; cmd/hygge/cli wires the
// concrete agent into [NewCompactTool] at bootstrap.
type Compactor interface {
	// Compact summarises the session's pre-marker history and writes a new
	// compaction marker.  Returns [agent.ErrNothingToCompact] when the
	// session contains too few messages to justify summarising.
	//
	// The concrete implementation is agent.Agent.Compact; see its
	// documentation for full semantics (bus events, marker persistence,
	// error behaviour).
	Compact(ctx context.Context, sessionID string) error
}

// CompactTool allows the model to trigger history compaction on the
// current session.  Invoking the tool summarises older messages, writes
// a compaction marker, and resets the context window so subsequent turns
// start with a concise summary instead of the raw history.
//
// The tool accepts no input parameters; all context is sourced from
// [ExecContext] (session ID).
type CompactTool struct {
	compactor Compactor
}

// NewCompactTool builds a CompactTool backed by compactor.  compactor must
// not be nil; callers are responsible for omitting the tool when no agent
// is wired.
func NewCompactTool(compactor Compactor) *CompactTool {
	return &CompactTool{compactor: compactor}
}

// Name implements [Tool].
func (t *CompactTool) Name() string { return "compact" }

// Parallelizable implements [Tool].  Compaction mutates session state and
// is therefore not safe to run concurrently with other operations.
func (t *CompactTool) Parallelizable() bool { return false }

// Description implements [Tool].
func (t *CompactTool) Description() string {
	return "Compact the current conversation history into a concise summary. " +
		"Use this tool when context usage reaches the configured compaction threshold " +
		"or is approaching the model limit, or when switching to a substantially " +
		"different topic, to free up context " +
		"space. The tool summarises all messages before the latest compaction " +
		"marker and writes a new marker; subsequent turns start with the summary " +
		"instead of the full raw history."
}

// InputSchema implements [Tool].  The tool accepts no parameters.
func (t *CompactTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{},
	}
}

// Execute implements [Tool].
func (t *CompactTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	if t.compactor == nil {
		return Result{}, newExecutionFailed("compact: compactor not configured", nil)
	}
	if ec.SessionID == "" {
		return Result{}, newExecutionFailed("compact: session ID not available in ExecContext", nil)
	}

	// The tool takes no arguments; decode only to reject unexpected fields.
	var args struct{}
	if err := decodeArgs(raw, &args); err != nil {
		return Result{}, err
	}

	err := t.compactor.Compact(ctx, ec.SessionID)
	if err == nil {
		return Result{
			Content: "Conversation history compacted successfully. " +
				"Older messages have been summarised and a compaction marker written. " +
				"Subsequent turns will use the summary as context.",
		}, nil
	}

	// ErrNothingToCompact is a logical no-op, not a failure — surface it
	// as an IsError result so the model knows and can continue normally.
	if isNothingToCompact(err) {
		return Result{
			IsError: true,
			Content: "Nothing to compact: the conversation does not yet have enough " +
				"messages since the last compaction marker to justify summarising (minimum 4).",
			Metadata: map[string]any{"reason": "nothing_to_compact"},
		}, nil
	}

	// Any other error is a genuine failure.
	return Result{
		IsError: true,
		Content: fmt.Sprintf("Compaction failed: %v", err),
		Metadata: map[string]any{
			"reason": "compaction_error",
			"error":  err.Error(),
		},
	}, nil
}

type nothingToCompactError interface {
	IsNothingToCompact() bool
}

func isNothingToCompact(err error) bool {
	if err == nil {
		return false
	}
	if tagged, ok := err.(nothingToCompactError); ok {
		return tagged.IsNothingToCompact()
	}
	return false
}
