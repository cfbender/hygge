package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// runLoop delegates the active turn to the Fantasy session runtime. The user
// message has already been appended by the caller (Send). modelName is sourced
// from the session row.
func (a *Agent) runLoop(ctx context.Context, sessionID, modelName string) (*session.Message, error) {
	if a.session != nil {
		return a.session.RunTurn(ctx, sessionID, modelName)
	}
	return nil, fmt.Errorf("agent: fantasy model is not configured")
}

// collectLazyContext gathers the path-like arguments of every tool_use
// part in asstMsg, hands them to the lazy tracker, and queues any
// newly-discovered subdir AGENTS.md / CLAUDE.md blocks for the next
// turn.  No-op when the lazy tracker is not configured.
func (a *Agent) collectLazyContext(sessionID, pwd string, asstMsg *session.Message) {
	if a.opts.LazyContext == nil || asstMsg == nil {
		return
	}
	var paths []string
	for _, p := range asstMsg.Parts {
		if p.Kind != session.PartToolUse {
			continue
		}
		paths = append(paths, touchedPaths(p.ToolName, p.ToolInput)...)
	}
	if len(paths) == 0 {
		return
	}
	blocks := a.opts.LazyContext.Touch(pwd, paths)
	if len(blocks) == 0 {
		return
	}
	slog.Debug("agent: lazy context loaded for next turn",
		"session", sessionID,
		"blocks", len(blocks),
	)
	a.appendPendingLazy(sessionID, blocks)
}

// toolCallEvent is the agent's internal copy of a Fantasy tool call while a
// streaming assistant message is being assembled.
type toolCallEvent struct {
	ID    string
	Name  string
	Input []byte
}

// buildAssistantParts assembles a Parts slice in the order: text,
// thinking, tool_use blocks.  Empty buffers are omitted.
//
// The order is not preserved relative to the model's emission order:
// for v0.1 we always serialise text first, then thinking, then tool calls.
// The runtime just sees a transcript that includes the assistant's content
// blocks in some order before the next user/tool_result turn.
func buildAssistantParts(text, thinking string, toolUses []toolCallEvent) []session.Part {
	parts := make([]session.Part, 0, 1+1+len(toolUses))
	if text != "" {
		parts = append(parts, session.Part{Kind: session.PartText, Text: text})
	}
	if thinking != "" {
		parts = append(parts, session.Part{Kind: session.PartThinking, Text: thinking})
	}
	for _, tu := range toolUses {
		parts = append(parts, session.Part{
			Kind:      session.PartToolUse,
			ToolID:    tu.ID,
			ToolName:  tu.Name,
			ToolInput: tu.Input,
		})
	}
	return parts
}

// computeCost looks up pricing for the configured provider+model and
// computes a Money for the supplied usage.  Pricing misses are absorbed
// here and logged once per call site; the agent never fails a turn over
// pricing.
func (a *Agent) computeCost(ctx context.Context, modelName string, u provider.Usage) cost.Money {
	if u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadTokens == 0 && u.CacheWriteTokens == 0 {
		return cost.Money{}
	}
	pricing, _, err := a.opts.Catalog.LookUp(ctx, a.opts.Provider.Name(), modelName)
	if err != nil {
		if !errors.Is(err, cost.ErrModelNotPriced) {
			slog.Warn("agent: catalog lookup failed",
				"provider", a.opts.Provider.Name(),
				"model", modelName,
				"err", err,
			)
		}
		pricing = cost.Pricing{}
	}
	return cost.Calculate(cost.Usage{
		InputTokens:      u.InputTokens,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens,
	}, pricing)
}
