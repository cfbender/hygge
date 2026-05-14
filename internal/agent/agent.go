// Package agent is the orchestrator that wires session storage, provider
// streaming, permission gating, tool execution, and cost accounting into a
// single turn-by-turn loop.
//
// # Layering
//
// internal/agent depends on every package below it: bus, session, store,
// provider, permission, tool, cost.  It is the keystone of the v0.1
// architecture.  It must NOT import internal/ui or cmd/...; those import
// it.
//
// # The Send loop in one paragraph
//
// Send appends the user message, then enters a loop bounded by
// Options.MaxIterations.  Each iteration: build a [provider.Request] from
// session history (compaction summary folded in as part of the system
// prompt), Stream the response, fan out streaming deltas to the bus,
// assemble an assistant message, persist it, charge cost.  If the
// assistant emitted any tool_use blocks, execute them SEQUENTIALLY,
// persisting each tool_result message, then loop again.  If no tool_use
// blocks appear, the loop terminates and Send returns the assistant
// message.  Hitting MaxIterations publishes [bus.IterationLimitReached]
// and returns [ErrIterationLimit] alongside an "iteration limit reached"
// assistant message.
//
// # Sequential tool execution
//
// Even when the provider returns multiple tool_use blocks in a single
// response, the agent executes them one at a time.  Permission prompts
// are interactive — stacking modals on top of each other is hostile.
// Parallel execution is a v0.2 concern.  This is safe because the
// Anthropic tool-use protocol does not require any specific ordering of
// tool_result blocks within a tool_result message.
//
// # Per-session serialisation
//
// At most one Send is in flight per session ID.  Concurrent Sends on the
// same session block on a per-session mutex; Sends on different sessions
// run independently.  Compact participates in the same lock so it cannot
// race a Send on the same session.
//
// # Streaming-error and cancellation semantics
//
// A mid-stream [provider.EventError] commits a partial assistant message
// containing whatever text/thinking arrived before the error, plus a
// stream_error metadata flag, and returns the wrapped error to the caller.
// This is intentional: the model's partial output is interesting to the
// user, and the conversation can be inspected before retrying.  Tool calls
// that arrived before the error are NOT executed — we want a clean failure
// boundary.
//
// Context cancellation, by contrast, commits NOTHING.  When ctx is
// cancelled mid-stream the agent returns ctx.Err immediately; no message
// is appended.  Rationale: cancellation is the user's explicit signal that
// they don't want this turn — preserving a half-formed assistant message
// would undo that intent and pollute history.
//
// # Cost lookups are best-effort
//
// Pricing lookups go through the catalog.  If the catalog returns
// [cost.ErrModelNotPriced], the agent logs a slog.Warn and records the
// token usage with cost_usd = 0.  A turn is never failed over pricing.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// defaultMaxIterations is used when [Options.MaxIterations] is zero.
const defaultMaxIterations = 25

// ErrIterationLimit is returned by Send when the agent loop hits its
// configured iteration cap without converging.  An assistant message
// noting the limit is appended to the session before the error is
// returned.
var ErrIterationLimit = errors.New("agent: iteration limit reached")

// ErrNothingToCompact is returned by Compact when the session contains
// too few messages since the latest marker to justify summarising.
var ErrNothingToCompact = errors.New("agent: nothing to compact")

// ErrClosed is returned by Send and Compact after Close.
var ErrClosed = errors.New("agent: closed")

// Options configures an Agent.  Bus, Store, Provider, Permission, Tools,
// and Catalog are required; the rest have sensible defaults.
type Options struct {
	// Bus is the in-process event bus.  Required.
	Bus *bus.Bus
	// Store is the session persistence layer.  Required.
	Store session.Store
	// Provider is the model adapter.  Required.
	Provider provider.Provider
	// Permission is the permission engine the tools call into.  Required.
	Permission *permission.Engine
	// Tools is the registry of callable tools.  Required.
	Tools *tool.Registry
	// Catalog resolves model pricing.  Required.
	Catalog *cost.Catalog
	// SystemPrompt is the optional system prompt sent on every turn.
	SystemPrompt string
	// MaxIterations bounds the tool-use loop.  Zero means defaultMaxIterations (25).
	MaxIterations int
	// Pwd is the working directory passed to tools via ExecContext.  Empty
	// means the tool helpers fall back to os.Getwd.
	Pwd string
	// Now is an injectable clock for bus event timestamps.  Nil means time.Now.
	Now func() time.Time
	// ContextWindow is the model's maximum context size in tokens.  When
	// non-zero, [bus.ContextUsageUpdated.PctUsed] is computed against it.
	ContextWindow int64
	// CompactionMaxTokens caps the size of the generated summary in
	// Compact.  Zero means 1024.
	CompactionMaxTokens int
	// LazyContext, when non-nil, enables the per-tool-call subdir
	// AGENTS.md / CLAUDE.md loader (see agentsmd.LazyTracker).  Nil
	// means the feature is off — the agent loop never injects subdir
	// context.  Tracker state is per-Agent, but the agent maintains
	// a per-session pending-block buffer so multiple sessions
	// sharing one Agent do not bleed context into each other (only
	// the seen-dir set is shared, which is the intended behaviour
	// for one workspace).
	LazyContext *agentsmd.LazyTracker
	// Reasoning is the session-scoped reasoning knob copied onto
	// every [provider.Request] this agent issues.  The zero value
	// means "no reasoning" — adapters that support reasoning will
	// not enable it.  CLI / config plumb a [provider.Reasoning]
	// into this field at bootstrap.
	Reasoning provider.Reasoning
	// Hooks, when non-nil, gates each turn through the hook
	// framework (pre_message, pre_tool, post_tool, post_message).
	// A nil Hooks means "no hooks" — the agent loop treats it as a
	// no-op without any nil-deref risk.
	Hooks *hook.Registry
	// CompactionThresholdPct, when > 0, enables the advisory compaction
	// suggestion.  After each turn the agent checks context usage against
	// this percentage.  If usage is at or above the threshold and the flag
	// has not already fired for this session × crossing, it publishes a
	// [bus.CompactionRequested] with Source="threshold".  Valid range 1–99;
	// 0 disables the suggestion.  Default 80 (supplied by cmd/hygge/cli
	// from config.Compaction.ThresholdPct).
	CompactionThresholdPct float64
}

// Agent is the orchestrator.  Construct via [New]; the zero value is not
// usable.
type Agent struct {
	opts Options

	// mu guards closed, locks, pendingLazy, and thresholdFired.
	mu     sync.Mutex
	closed bool
	locks  map[string]*sync.Mutex
	// pendingLazy maps sessionID -> lazy context blocks queued for
	// the next provider turn.  Drained before each buildRequest in
	// runLoop.  Guarded by mu.
	pendingLazy map[string][]agentsmd.Block
	// thresholdFired tracks which sessions have already received the
	// advisory compaction suggestion for the current threshold crossing.
	// Keyed by session id.  The flag is set the first time usage climbs
	// above CompactionThresholdPct and is reset when:
	//   - usage drops back below (threshold - 5) pp hysteresis, or
	//   - Agent.Compact completes successfully for that session.
	// Guarded by mu.
	thresholdFired map[string]bool
}

// New constructs an Agent.  Returns an error if any required option is nil.
func New(opts Options) (*Agent, error) {
	if opts.Bus == nil {
		return nil, fmt.Errorf("agent: New: Bus is required")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("agent: New: Store is required")
	}
	if opts.Provider == nil {
		return nil, fmt.Errorf("agent: New: Provider is required")
	}
	if opts.Permission == nil {
		return nil, fmt.Errorf("agent: New: Permission is required")
	}
	if opts.Tools == nil {
		return nil, fmt.Errorf("agent: New: Tools is required")
	}
	if opts.Catalog == nil {
		return nil, fmt.Errorf("agent: New: Catalog is required")
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = defaultMaxIterations
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CompactionMaxTokens <= 0 {
		opts.CompactionMaxTokens = 1024
	}
	return &Agent{
		opts:           opts,
		locks:          make(map[string]*sync.Mutex),
		pendingLazy:    make(map[string][]agentsmd.Block),
		thresholdFired: make(map[string]bool),
	}, nil
}

// Close releases the agent.  After Close, Send and Compact return
// [ErrClosed].  Idempotent.
func (a *Agent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.closed = true
	if a.opts.Hooks != nil {
		a.opts.Hooks.Close()
	}
	return nil
}

// sessionLock returns the per-session mutex, allocating one on first
// access.  Callers Lock/Unlock the returned mutex around the work that
// needs serialisation for that session id.
func (a *Agent) sessionLock(sessionID string) *sync.Mutex {
	a.mu.Lock()
	defer a.mu.Unlock()
	if m, ok := a.locks[sessionID]; ok {
		return m
	}
	m := &sync.Mutex{}
	a.locks[sessionID] = m
	return m
}

// isClosed returns true if Close was called.
func (a *Agent) isClosed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.closed
}

// Send appends a user message to the session and runs the agent loop
// until the assistant produces a final response with no further tool
// calls (or hits the iteration limit).  Returns the final committed
// assistant message.
//
// Bus events emitted, in order per iteration:
//
//   - bus.MessageAppended (user)              once at start
//   - bus.AssistantTextDelta                  streamed
//   - bus.AssistantThinkingDelta              streamed
//   - bus.MessageAppended (assistant)         per iteration end
//   - bus.CostUpdated                         after each provider response
//   - bus.ContextUsageUpdated                 after each provider response
//   - bus.ToolCallRequested                   per tool call
//   - bus.ToolCallCompleted                   per tool call
//   - bus.MessageAppended (tool result)       per tool call
//   - bus.IterationLimitReached               if the cap is hit
//
// Permission asks and tool progress events come from the tools themselves
// while their Execute method runs.
func (a *Agent) Send(ctx context.Context, sessionID string, userParts []session.Part) (*session.Message, error) {
	if a.isClosed() {
		return nil, ErrClosed
	}
	if sessionID == "" {
		return nil, fmt.Errorf("agent: Send: sessionID required")
	}
	if len(userParts) == 0 {
		return nil, fmt.Errorf("agent: Send: userParts required")
	}

	lock := a.sessionLock(sessionID)
	lock.Lock()
	defer lock.Unlock()

	// Re-check closed under the per-session lock: a Close racing with the
	// lock acquisition should still bail out cleanly.
	if a.isClosed() {
		return nil, ErrClosed
	}

	// Resolve a working directory for hook input.
	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}

	// Extract text from the user parts for the pre_message hook.
	var userText string
	for _, p := range userParts {
		if p.Kind == session.PartText {
			userText += p.Text
		}
	}

	// Run pre_message hook BEFORE persisting the user message.  On Deny,
	// return immediately without appending anything.  On Modify, replace
	// the text in the user parts.
	if a.opts.Hooks != nil {
		hookIn := hook.Input{
			Event:     hook.EventPreMessage,
			SessionID: sessionID,
			HookName:  "pre_message",
			Pwd:       pwd,
			Message:   userText,
		}
		out, dec, denier, reason, warns := a.opts.Hooks.RunPre(ctx, hook.EventPreMessage, hookIn)
		logHookWarns(warns)
		if dec == hook.DecisionDeny {
			return nil, fmt.Errorf("agent: pre_message hook %q denied: %s", denier, reason)
		}
		// Use the (possibly modified) message from the hook output.
		if out.Message != "" && out.Message != userText {
			userParts = replaceTextParts(userParts, out.Message)
		}
	}

	// Persist the user message before any provider work.
	userMsg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: append([]session.Part(nil), userParts...),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: Send: append user: %w", err)
	}
	bus.Publish(a.opts.Bus, bus.MessageAppended{
		SessionID: sessionID,
		MessageID: userMsg.ID,
		Role:      string(session.RoleUser),
		At:        a.opts.Now(),
	})

	sess, err := a.opts.Store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent: Send: load session: %w", err)
	}

	return a.runLoop(ctx, sessionID, sess.Model.Name)
}

// appendPendingLazy queues blocks to be injected as a system-prompt
// addition on the next provider turn for sessionID.  Guarded by a.mu.
func (a *Agent) appendPendingLazy(sessionID string, blocks []agentsmd.Block) {
	if len(blocks) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingLazy[sessionID] = append(a.pendingLazy[sessionID], blocks...)
}

// drainPendingLazy returns and clears the queued lazy blocks for
// sessionID.  Returns nil when nothing is queued.  Guarded by a.mu.
func (a *Agent) drainPendingLazy(sessionID string) []agentsmd.Block {
	a.mu.Lock()
	defer a.mu.Unlock()
	blocks := a.pendingLazy[sessionID]
	if len(blocks) == 0 {
		return nil
	}
	delete(a.pendingLazy, sessionID)
	return blocks
}

// replaceTextParts returns a copy of parts where every PartText entry
// is replaced with a single PartText carrying newText.  Non-text parts
// are preserved in order.  If no text parts exist, a new text part is
// prepended.
func replaceTextParts(parts []session.Part, newText string) []session.Part {
	out := make([]session.Part, 0, len(parts))
	inserted := false
	for _, p := range parts {
		if p.Kind == session.PartText {
			if !inserted {
				out = append(out, session.Part{Kind: session.PartText, Text: newText})
				inserted = true
			}
			// Additional text parts are dropped (merged into the single
			// replacement).
		} else {
			out = append(out, p)
		}
	}
	if !inserted {
		out = append([]session.Part{{Kind: session.PartText, Text: newText}}, out...)
	}
	return out
}

// logHookWarns emits slog.Warn for each non-fatal hook execution error.
func logHookWarns(warns []hook.Warning) {
	for _, w := range warns {
		slog.Warn("agent: hook execution warning (fail-open)",
			"hook", w.HookName, "err", w.Err)
	}
}
