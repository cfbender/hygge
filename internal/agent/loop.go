package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// runLoop drives the streaming provider/tool-execution loop until the
// assistant returns a response with no tool_use blocks (final answer) or
// the iteration cap is hit.  The user message has already been appended
// by the caller (Send).  modelName is sourced from the session row.
func (a *Agent) runLoop(ctx context.Context, sessionID, modelName string) (*session.Message, error) {
	// Resolve a stable working directory once per loop.  The agent's
	// configured Pwd wins; if absent we fall back to os.Getwd so the
	// lazy tracker can still resolve relative paths.  The fallback
	// result is cached for the lifetime of the loop.
	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}

	for iter := 1; iter <= a.opts.MaxIterations; iter++ {
		// Honor cancellation between iterations promptly.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		msgs, marker, err := a.opts.Store.MessagesSinceLatestMarker(ctx, sessionID)
		if err != nil {
			return nil, fmt.Errorf("agent: load messages: %w", err)
		}

		// Drain any subdir context queued by the previous
		// iteration's tool batch.  These ride along in the system
		// prompt for THIS turn only — never persisted to history.
		lazyBlocks := a.drainPendingLazy(sessionID)

		req := buildRequest(
			msgs, marker,
			a.opts.SystemPrompt,
			a.opts.Tools.AsProviderTools(),
			modelName,
			nil,
			lazyBlocks,
			a.opts.Reasoning,
		)

		asstMsg, hasTools, err := a.runOneTurn(ctx, sessionID, req, modelName)
		if err != nil {
			return nil, err
		}

		if !hasTools {
			return asstMsg, nil
		}

		// Execute tool_use blocks one at a time, persisting each
		// tool_result back to the session.  Stop on context cancel.
		if err := a.executeToolCalls(ctx, sessionID, asstMsg); err != nil {
			return nil, err
		}

		// After the tool batch, harvest the directories the tools
		// touched and queue any newly-discovered subdir context for
		// the next iteration's system prompt.
		a.collectLazyContext(sessionID, pwd, asstMsg)
	}

	// Iteration limit hit — publish the event and commit an abort note.
	bus.Publish(a.opts.Bus, bus.IterationLimitReached{
		SessionID: sessionID,
		Limit:     a.opts.MaxIterations,
		At:        a.opts.Now(),
	})
	abortMsg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{
		Role: session.RoleAssistant,
		Parts: []session.Part{{
			Kind: session.PartText,
			Text: fmt.Sprintf("iteration limit reached (%d)", a.opts.MaxIterations),
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("agent: append abort message: %w", err)
	}
	bus.Publish(a.opts.Bus, bus.MessageAppended{
		SessionID: sessionID,
		MessageID: abortMsg.ID,
		Role:      string(session.RoleAssistant),
		At:        a.opts.Now(),
	})
	return abortMsg, ErrIterationLimit
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

// runOneTurn issues one provider Stream call, accumulates events, commits
// an assistant message, and emits the cost/context bus events.  The
// returned bool reports whether the assistant requested any tool calls.
//
// On context cancellation mid-stream, nothing is committed and the error
// is returned.  On a mid-stream EventError, a partial assistant message
// IS committed (text/thinking only — pending tool_use blocks are
// discarded) and the wrapped error is returned.
func (a *Agent) runOneTurn(
	ctx context.Context, sessionID string, req provider.Request, modelName string,
) (*session.Message, bool, error) {
	ch, err := a.opts.Provider.Stream(ctx, req)
	if err != nil {
		return nil, false, fmt.Errorf("agent: provider stream: %w", err)
	}

	var (
		textBuf, thinkBuf strings.Builder
		toolUses          []toolCallEvent
		lastUsage         provider.Usage
		streamErr         error
	)

drain:
	for {
		select {
		case <-ctx.Done():
			// Drain in the background so the provider can release
			// its goroutine, but do not commit anything.
			go discardStream(ch)
			return nil, false, ctx.Err()
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			switch ev.Type {
			case provider.EventTextDelta:
				textBuf.WriteString(ev.Text)
				bus.Publish(a.opts.Bus, bus.AssistantTextDelta{
					SessionID: sessionID,
					Text:      ev.Text,
					At:        a.opts.Now(),
				})
			case provider.EventThinkingDelta:
				thinkBuf.WriteString(ev.Text)
				bus.Publish(a.opts.Bus, bus.AssistantThinkingDelta{
					SessionID: sessionID,
					Text:      ev.Text,
					At:        a.opts.Now(),
				})
			case provider.EventToolUse:
				toolUses = append(toolUses, toolCallEvent{
					ID:    ev.ToolID,
					Name:  ev.ToolName,
					Input: append([]byte(nil), ev.ToolInput...),
				})
			case provider.EventUsage, provider.EventMessageStart:
				if ev.Usage.InputTokens != 0 || ev.Usage.OutputTokens != 0 ||
					ev.Usage.CacheReadTokens != 0 || ev.Usage.CacheWriteTokens != 0 {
					lastUsage = ev.Usage
				}
			case provider.EventError:
				streamErr = ev.Err
				// Discard any pending tool_use blocks: we want a
				// clean failure boundary, not a half-executed tool
				// call against a model that errored out.
				toolUses = nil
				break drain
			case provider.EventDone:
				break drain
			}
		}
	}

	asstParts := buildAssistantParts(textBuf.String(), thinkBuf.String(), toolUses)

	// Even on stream error we commit whatever partial content we have
	// (provided it has any content at all) so the user can see what the
	// model managed to say before it failed.  If there is nothing at all,
	// skip the commit and just surface the error.
	if streamErr != nil && len(asstParts) == 0 {
		return nil, false, fmt.Errorf("agent: stream error: %w", streamErr)
	}

	asstMsg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{
		Role:             session.RoleAssistant,
		Parts:            asstParts,
		InputTokens:      lastUsage.InputTokens,
		OutputTokens:     lastUsage.OutputTokens,
		CacheReadTokens:  lastUsage.CacheReadTokens,
		CacheWriteTokens: lastUsage.CacheWriteTokens,
		CostUSD:          a.computeCost(ctx, modelName, lastUsage).USD,
	})
	if err != nil {
		return nil, false, fmt.Errorf("agent: append assistant: %w", err)
	}
	bus.Publish(a.opts.Bus, bus.MessageAppended{
		SessionID: sessionID,
		MessageID: asstMsg.ID,
		Role:      string(session.RoleAssistant),
		At:        a.opts.Now(),
	})
	a.recordUsage(ctx, sessionID, modelName, lastUsage)

	if streamErr != nil {
		return asstMsg, false, fmt.Errorf("agent: stream error: %w", streamErr)
	}

	// Post-message hook: always async (the registry coerces sync→async
	// for post_message at load time).  Fire-and-forget; does not affect
	// the return value.
	if a.opts.Hooks != nil {
		var asstText string
		for _, p := range asstParts {
			if p.Kind == session.PartText {
				asstText += p.Text
			}
		}
		hookIn := hook.Input{
			Event:     hook.EventPostMessage,
			SessionID: sessionID,
			HookName:  "post_message",
			Pwd:       a.opts.Pwd,
			Message:   asstText,
		}
		_, warns := a.opts.Hooks.RunPost(ctx, hook.EventPostMessage, hookIn)
		logHookWarns(warns)
	}

	return asstMsg, len(toolUses) > 0, nil
}

// executeToolCalls runs every tool_use block in the assistant message,
// appending a tool_result message for each one.  Stops on context
// cancellation and returns ctx.Err.
//
// Execution policy: parallelizable tool calls run first as a concurrent
// batch (all goroutines launched, then sync.WaitGroup.Wait()).  Sequential
// (non-parallelizable) calls run serially after the parallel batch
// completes.  Results are stitched back into the original call order
// before being committed to the store, preserving the order the provider
// expects.
//
// Bus events (ToolCallRequested, ToolCallProgress, ToolCallCompleted) fire
// from within each call.  For the parallel batch, events from sibling
// calls arrive in undefined order; subscribers must not rely on
// intra-batch ordering.  Each individual tool's events still arrive in
// order relative to that tool.
//
// A panic inside a parallelizable call is recovered: the slot receives an
// IsError result with the panic message, and siblings still run to
// completion.
func (a *Agent) executeToolCalls(
	ctx context.Context, sessionID string, asstMsg *session.Message,
) error {
	now := a.opts.Now
	var modelName string
	if sess, err := a.opts.Store.GetSession(ctx, sessionID); err == nil {
		modelName = sess.Model.Name
	}

	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}

	// Collect all tool_use parts.
	type callSlot struct {
		part session.Part
	}
	var calls []callSlot
	for _, p := range asstMsg.Parts {
		if p.Kind == session.PartToolUse {
			calls = append(calls, callSlot{part: p})
		}
	}
	if len(calls) == 0 {
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Results indexed by original position.
	type slotResult struct {
		content  string
		isError  bool
		durMs    int64
		toolName string
		toolID   string
	}
	results := make([]slotResult, len(calls))

	// runOne executes a single tool_use part and writes into results[idx].
	// It is safe to call from multiple goroutines concurrently.
	runOne := func(idx int, p session.Part) {
		bus.Publish(a.opts.Bus, bus.ToolCallRequested{
			SessionID: sessionID,
			MessageID: asstMsg.ID,
			ToolUseID: p.ToolID,
			ToolName:  p.ToolName,
			Args:      append([]byte(nil), p.ToolInput...),
			At:        now(),
		})

		// Pre-tool hook.
		toolInput := p.ToolInput
		if a.opts.Hooks != nil {
			hookIn := hook.Input{
				Event:     hook.EventPreTool,
				SessionID: sessionID,
				HookName:  "pre_tool",
				Pwd:       pwd,
				ToolName:  p.ToolName,
				ToolInput: toolInput,
			}
			out, dec, denier, reason, warns := a.opts.Hooks.RunPre(ctx, hook.EventPreTool, hookIn)
			logHookWarns(warns)
			if dec == hook.DecisionDeny {
				result := tool.Result{
					IsError: true,
					Content: fmt.Sprintf("hook %q denied tool call: %s", denier, reason),
				}
				durMs := int64(0)
				bus.Publish(a.opts.Bus, bus.ToolCallCompleted{
					SessionID:  sessionID,
					MessageID:  asstMsg.ID,
					ToolUseID:  p.ToolID,
					ToolName:   p.ToolName,
					Err:        result.Content,
					DurationMs: durMs,
					At:         now(),
				})
				results[idx] = slotResult{
					content:  result.Content,
					isError:  true,
					durMs:    durMs,
					toolName: p.ToolName,
					toolID:   p.ToolID,
				}
				return
			}
			if len(out.ToolInput) > 0 {
				toolInput = out.ToolInput
			}
		}

		t, ok := a.opts.Tools.Get(p.ToolName)
		var (
			result  tool.Result
			toolErr error
		)
		started := time.Now()
		if !ok {
			result = tool.Result{
				IsError: true,
				Content: fmt.Sprintf("unknown tool: %s", p.ToolName),
			}
		} else {
			ec := tool.ExecContext{
				SessionID:  sessionID,
				Pwd:        a.opts.Pwd,
				Bus:        a.opts.Bus,
				Permission: a.opts.Permission,
				ToolUseID:  p.ToolID,
				MessageID:  asstMsg.ID,
				ModelName:  modelName,
				Now:        a.opts.Now,
			}
			result, toolErr = t.Execute(ctx, toolInput, ec)
			if toolErr != nil {
				result = tool.Result{
					IsError: true,
					Content: toolErr.Error(),
				}
			}
		}
		durMs := time.Since(started).Milliseconds()

		// Post-tool hook.
		if a.opts.Hooks != nil {
			hookIn := hook.Input{
				Event:     hook.EventPostTool,
				SessionID: sessionID,
				HookName:  "post_tool",
				Pwd:       pwd,
				ToolName:  p.ToolName,
				ToolInput: toolInput,
				ToolResult: &hook.ToolResult{
					IsError: result.IsError,
					Content: result.Content,
				},
			}
			out, warns := a.opts.Hooks.RunPost(ctx, hook.EventPostTool, hookIn)
			logHookWarns(warns)
			if out.ToolResult != nil {
				result.IsError = out.ToolResult.IsError
				result.Content = out.ToolResult.Content
			}
		}

		var errString string
		if result.IsError {
			errString = result.Content
		}
		bus.Publish(a.opts.Bus, bus.ToolCallCompleted{
			SessionID:  sessionID,
			MessageID:  asstMsg.ID,
			ToolUseID:  p.ToolID,
			ToolName:   p.ToolName,
			Err:        errString,
			DurationMs: durMs,
			At:         now(),
		})

		results[idx] = slotResult{
			content:  result.Content,
			isError:  result.IsError,
			durMs:    durMs,
			toolName: p.ToolName,
			toolID:   p.ToolID,
		}
	}

	// runOneSafe wraps runOne with panic recovery so a panicking tool does
	// not abort sibling goroutines.
	runOneSafe := func(idx int, p session.Part) {
		defer func() {
			if r := recover(); r != nil {
				msg := fmt.Sprintf("tool panicked: %v", r)
				slog.Error("agent: tool call panicked",
					"tool", p.ToolName,
					"session", sessionID,
					"panic", r,
				)
				bus.Publish(a.opts.Bus, bus.ToolCallCompleted{
					SessionID:  sessionID,
					MessageID:  asstMsg.ID,
					ToolUseID:  p.ToolID,
					ToolName:   p.ToolName,
					Err:        msg,
					DurationMs: 0,
					At:         now(),
				})
				results[idx] = slotResult{
					content:  msg,
					isError:  true,
					durMs:    0,
					toolName: p.ToolName,
					toolID:   p.ToolID,
				}
			}
		}()
		runOne(idx, p)
	}

	// Partition into parallel and sequential groups by original index.
	var parallel, sequential []int
	for i, c := range calls {
		t, ok := a.opts.Tools.Get(c.part.ToolName)
		if ok && t.Parallelizable() {
			parallel = append(parallel, i)
		} else {
			sequential = append(sequential, i)
		}
	}

	// Phase 1: run parallel batch concurrently.
	if len(parallel) > 0 {
		var wg sync.WaitGroup
		for _, idx := range parallel {
			wg.Add(1)
			idx := idx // capture
			go func() {
				defer wg.Done()
				runOneSafe(idx, calls[idx].part)
			}()
		}
		wg.Wait()
	}

	// Phase 2: run sequential group serially.
	for _, idx := range sequential {
		if err := ctx.Err(); err != nil {
			return err
		}
		runOne(idx, calls[idx].part)
	}

	// Commit results to the store in original call order.
	for i, r := range results {
		if r.toolName == "" {
			// Slot was never filled (shouldn't happen, but guard).
			r.toolName = calls[i].part.ToolName
			r.toolID = calls[i].part.ToolID
		}
		toolMsg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{
			Role: session.RoleTool,
			Parts: []session.Part{{
				Kind:      session.PartToolResult,
				ToolUseID: r.toolID,
				Content:   r.content,
				IsError:   r.isError,
			}},
			DurationMs: r.durMs,
		})
		if err != nil {
			return fmt.Errorf("agent: append tool message: %w", err)
		}
		bus.Publish(a.opts.Bus, bus.MessageAppended{
			SessionID: sessionID,
			MessageID: toolMsg.ID,
			Role:      string(session.RoleTool),
			At:        now(),
		})
	}
	return nil
}

// toolCallEvent is the agent's internal copy of a provider.EventToolUse.
// We hold our own copy so the provider's channel buffer can be released
// before we commit anything.
type toolCallEvent struct {
	ID    string
	Name  string
	Input []byte
}

// buildAssistantParts assembles a Parts slice in the order: text,
// thinking, tool_use blocks.  Empty buffers are omitted.
//
// The order is not preserved relative to the provider's emission order:
// for v0.1 we always serialise text first, then thinking, then tool calls.
// Anthropic does not require strict interleaving in subsequent turns; the
// provider just sees a transcript that includes the assistant's content
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

// discardStream drains a provider stream until it closes.  Used after
// context cancellation so the provider's goroutine can exit promptly.
func discardStream(ch <-chan provider.Event) {
	for range ch { //nolint:revive // intentional drain
	}
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
