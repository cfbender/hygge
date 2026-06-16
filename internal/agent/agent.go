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
// Send appends the user message, then delegates the active turn to Fantasy.
// Fantasy owns model/tool iteration and is intentionally uncapped: cancellation
// is context-driven. A missing Fantasy model is a configuration error.
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
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// ErrNothingToCompact is returned by Compact when the session contains
// too few messages since the latest marker to justify summarising.
var ErrNothingToCompact = errors.New("agent: nothing to compact")

// ErrClosed is returned by Send and Compact after Close.
var ErrClosed = errors.New("agent: closed")

// ErrNoActiveTurn is returned by Steer when there is no active run to guide.
var ErrNoActiveTurn = errors.New("agent: no active turn to steer")

// Options configures an Agent.  Bus, Store, Provider, Permission, Tools,
// and Catalog are required; the rest have sensible defaults.
type Options struct {
	// Bus is the in-process event bus.  Required.
	Bus *bus.Bus
	// Store is the session persistence layer.  Required.
	Store session.Store
	// Provider is the model adapter.  Required.
	Provider provider.Provider
	// FantasyModel, when non-nil, is used by the active turn loop via
	// fantasy.Agent.Stream. Provider remains required for name/model metadata
	// and cost lookup.
	FantasyModel fantasy.LanguageModel
	// TitleFantasyModel, when non-nil, is used for cheap internal session-title
	// generation. Nil falls back to FantasyModel.
	TitleFantasyModel fantasy.LanguageModel
	// Permission is the permission engine the tools call into.  Required.
	Permission *permission.Engine
	// Tools is the registry of callable tools.  Required.
	Tools *tool.Registry
	// Catalog resolves model pricing.  Required.
	Catalog *cost.Catalog
	// SystemPrompt is the optional system prompt sent on every turn.
	SystemPrompt string
	// Pwd is the working directory passed to tools via ExecContext.  Empty
	// means the tool helpers fall back to os.Getwd.
	Pwd string
	// Now is an injectable clock for bus event timestamps.  Nil means time.Now.
	Now func() time.Time
	// ContextWindow is the model's maximum context size in tokens.  When
	// non-zero, [bus.ContextUsageUpdated.PctUsed] is computed against the
	// input-available window (ContextWindow minus MaxOutput).
	ContextWindow int64
	// MaxOutput is the model's reserved output budget in tokens.  The
	// provider deducts this from the context window before accepting input,
	// so the effective ceiling for prompt tokens is ContextWindow-MaxOutput.
	// Reserving it from the PctUsed denominator keeps the gauge from reading
	// optimistically near the limit.  Zero means "unknown" — no reservation.
	MaxOutput int64
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
	// MemoryLoader, when non-nil, loads file-backed global/project memories for
	// prompt injection. Session memories are always loaded from Store.
	MemoryLoader MemoryLoader
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

	// TurnContextDecorator, when non-nil, is called at the start of each
	// Fantasy turn to decorate the context before it is passed to
	// fantasy.Agent.Stream.  Callers use this to inject per-turn values
	// (e.g. session IDs for HTTP transport middleware) without coupling the
	// agent package to any specific provider package.  The returned context
	// replaces the one passed in for the duration of the turn.
	TurnContextDecorator func(ctx context.Context, sessionID string) context.Context
}

// MemoryLoader provides non-session memories in prompt-injection order.
type MemoryLoader interface {
	ListMemories(ctx context.Context) ([]*session.Memory, error)
}

// Agent is the orchestrator.  Construct via [New]; the zero value is not
// usable.
type Agent struct {
	opts    Options
	runtime *Runtime
	session *SessionAgent
	// handle is the active-model bundle (provider/model ref, provider seam,
	// provider id, Fantasy model). Swapped atomically by SetModel and shared
	// with the Runtime, so hot-path readers never take a lock and can never
	// observe a provider/model mismatch.
	handle *atomic.Pointer[modelHandle]

	// mu guards closed, ctx, cancel, locks, pendingLazy,
	// pendingSystemAdditions, activeModeName, thresholdFired, pluginInjects,
	// activeRuns, and queues.
	mu     sync.Mutex
	closed bool

	// ctx / cancel are the agent's own lifetime context.  Used by the
	// queue-drain goroutine so that queued sends survive cancellation of
	// the caller's context (e.g. the UI's per-send cancel fires as soon as
	// Agent.Send returns for a queued message).  Set once at construction
	// and cancelled by Close.
	ctx    context.Context //nolint:containedctx
	cancel context.CancelFunc
	locks  map[string]*sync.Mutex
	// pendingLazy maps sessionID -> lazy context blocks queued for
	// the next Fantasy model step. Drained before model calls in runLoop.
	// Guarded by mu.
	pendingLazy map[string][]agentsmd.Block
	// pendingSystemAdditions maps sessionID -> one-turn system prompt
	// additions queued by pre_message hooks. Drained before each Fantasy step and
	// never persisted into visible message history. Guarded by mu.
	pendingSystemAdditions map[string][]string
	// pendingSteering maps sessionID -> active-turn user guidance queued by /steer.
	// Drained before the next Fantasy step and never persisted as a user message.
	// Guarded by mu.
	pendingSteering map[string][]string
	// activeModeName is included in pre_message hook input so plugins can
	// rebuild system prompt additions for the currently selected UI mode.
	// Guarded by mu.
	activeModeName string
	// thresholdFired tracks which sessions have already received the
	// advisory compaction suggestion for the current threshold crossing.
	// Keyed by session id.  The flag is set the first time usage climbs
	// above CompactionThresholdPct and is reset when:
	//   - usage drops back below (threshold - 5) pp hysteresis, or
	//   - Agent.Compact completes successfully for that session.
	// Guarded by mu.
	thresholdFired map[string]bool
	// latestUsage stores the most recent context usage recorded by
	// recordUsage for each session.  Used to annotate the latest user
	// envelope with latest-known usage so the model
	// does not have to calculate it.  Reset on successful Compact so
	// stale pre-compaction numbers are never shown in the post-compaction
	// turn.  Guarded by mu.
	latestUsage map[string]sessionUsage
	// pluginInjects counts per-plugin per-session message injections for
	// the current turn.  Reset by ResetPluginInjectCounters at turn start.
	// Guarded by mu.
	pluginInjects map[pluginInjectKey]int
	// activeRuns is the set of session IDs currently executing a Send.
	// Used to decide whether an incoming Send should be queued behind the active turn.
	// Guarded by mu.
	activeRuns map[string]struct{}
	// queues holds per-session queues of pending sends that arrived while
	// the session was busy.  Drained (one entry at a time) after each
	// Send completes.  Guarded by mu.
	queues map[string][]QueuedSend
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
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.CompactionMaxTokens <= 0 {
		opts.CompactionMaxTokens = 1024
	}
	ctx, cancel := context.WithCancel(context.Background())
	handle := &atomic.Pointer[modelHandle]{}
	handle.Store(newModelHandle(
		session.ModelRef{Provider: opts.Provider.Name(), Name: modelName(opts.FantasyModel)},
		opts.Provider,
		opts.FantasyModel,
	))
	a := &Agent{
		opts:                   opts,
		handle:                 handle,
		ctx:                    ctx,
		cancel:                 cancel,
		locks:                  make(map[string]*sync.Mutex),
		pendingLazy:            make(map[string][]agentsmd.Block),
		pendingSystemAdditions: make(map[string][]string),
		pendingSteering:        make(map[string][]string),
		thresholdFired:         make(map[string]bool),
		latestUsage:            make(map[string]sessionUsage),
		activeRuns:             make(map[string]struct{}),
		queues:                 make(map[string][]QueuedSend),
	}
	a.runtime = NewRuntime(RuntimeOptions{
		TitleModel: opts.TitleFantasyModel,
		Tools:      opts.Tools,
		Handle:     handle,
	})
	a.session = NewSessionAgent(a, a.runtime)
	return a, nil
}

// SetModel hot-swaps the active provider and Fantasy model used by subsequent
// sends. It does not mutate existing session rows; callers own any UX or config
// persistence around the session-only runtime switch.
func (a *Agent) SetModel(providerName, modelName string, prv provider.Provider, fm fantasy.LanguageModel) error {
	if providerName == "" || modelName == "" {
		return fmt.Errorf("agent: SetModel: provider and model are required")
	}
	if prv == nil {
		return fmt.Errorf("agent: SetModel: Provider is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	a.handle.Store(newModelHandle(session.ModelRef{Provider: providerName, Name: modelName}, prv, fm))
	return nil
}

func modelName(m fantasy.LanguageModel) string {
	if m == nil {
		return ""
	}
	return m.Model()
}

// providerNameFor returns the lower-case provider id for prv, or "" if prv is
// nil. Used to feed RuntimeOptions.ProviderName and a.providerName() so
// per-provider token-accounting normalization in usageFromFantasy stays in
// sync with the active provider.
func providerNameFor(prv provider.Provider) string {
	if prv == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(prv.Name()))
}

// providerName returns the active provider id for use by usageFromFantasy.
// Returns "" when the agent has no provider configured (test fixtures).
//
// Lock-free: reads the atomic model handle. Hot-path callers in the turn
// loop should still capture the value once at turn start so a mid-turn
// SetModel cannot mix providers within one turn's accounting.
func (a *Agent) providerName() string {
	if a == nil {
		return ""
	}
	h := a.handle.Load()
	if h == nil {
		return ""
	}
	return h.providerID
}

// SetSystemPrompt replaces the system prompt used by subsequent sends. It does
// not mutate persisted messages; callers own any UI/config semantics for why the
// prompt changed.
func (a *Agent) SetSystemPrompt(prompt string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	a.opts.SystemPrompt = prompt
	return nil
}

// RefreshHookSystemPromptAdditions invokes pre_message hooks without
// persisting a user message so plugins can rebuild their one-turn system
// prompt additions after external context changes, such as a UI mode switch.
// Deny/modified_message decisions are ignored because there is no user
// message to block or rewrite; system_prompt_append results replace any
// previously queued hook additions for the session.
func (a *Agent) RefreshHookSystemPromptAdditions(ctx context.Context, sessionID, modeName string) error {
	if a.isClosed() {
		return ErrClosed
	}
	a.setActiveModeName(modeName)
	if sessionID == "" {
		return nil
	}
	if a.opts.Hooks == nil {
		return nil
	}

	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}
	out, dec, denier, reason, warns := a.opts.Hooks.RunPre(ctx, hook.EventPreMessage, hook.Input{
		Event:     hook.EventPreMessage,
		SessionID: sessionID,
		HookName:  "pre_message",
		Pwd:       pwd,
		ModeName:  modeName,
	})
	logHookWarns(warns)
	if dec == hook.DecisionDeny {
		slog.Warn("agent: ignoring pre_message deny during system prompt refresh",
			"hook", denier, "reason", reason)
	}
	a.replacePendingSystemAdditions(sessionID, out.SystemPromptAdditions)
	return nil
}

// GenerateTitle returns a concise, display-ready title for prompt using the
// configured internal title model (or the active Fantasy model when no small
// title model is configured). It does not mutate store state; callers own
// persistence.
func (a *Agent) GenerateTitle(ctx context.Context, prompt string) (string, error) {
	if a == nil || a.runtime == nil {
		return "", fmt.Errorf("agent: GenerateTitle: runtime is not configured")
	}
	if a.isClosed() {
		return "", ErrClosed
	}
	messages := []titleMessage{{role: "User", content: prompt}}
	title, err := a.generateTitleFromMessages(ctx, "", messages, false)
	return title, err
}

// RefreshSessionTitle asks the title model whether sessionID's current slug
// still describes the recent conversation. If the topic has changed, it writes a
// new formatted slug and returns changed=true. A KEEP response is a no-op.
func (a *Agent) RefreshSessionTitle(ctx context.Context, sessionID string) (title string, changed bool, err error) {
	if a == nil || a.runtime == nil {
		return "", false, fmt.Errorf("agent: RefreshSessionTitle: runtime is not configured")
	}
	if a.isClosed() {
		return "", false, ErrClosed
	}
	if a.opts.Store == nil {
		return "", false, fmt.Errorf("agent: RefreshSessionTitle: store is not configured")
	}
	sess, err := a.opts.Store.GetSession(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	msgs, err := a.opts.Store.MessagesForSession(ctx, sessionID)
	if err != nil {
		return "", false, err
	}
	messages := titleMessagesFromSession(msgs)
	currentTitle := sess.Slug
	currentCopiesPrompt := titleCopiesUserMessage(currentTitle, messages)
	if currentCopiesPrompt {
		currentTitle = ""
	}
	slog.Debug("agent: refresh session title",
		"session", sessionID, "current_slug", sess.Slug, "msg_count", len(messages), "copies_prompt", currentCopiesPrompt)
	input := titlePrompt(currentTitle, messages)
	if input == "" {
		slog.Debug("agent: title prompt empty, no-op", "session", sessionID)
		return sess.Slug, false, nil
	}
	proposed, _, err := a.runtime.GenerateTitle(ctx, input, 32)
	if err != nil {
		slog.Warn("agent: title model call failed", "session", sessionID, "err", err)
		return "", false, err
	}
	slog.Debug("agent: title model proposed", "session", sessionID, "proposed", proposed)
	proposed = cleanModelTitle(proposed)
	if proposed == "" || strings.EqualFold(proposed, "KEEP") {
		if currentCopiesPrompt {
			proposed, err = a.repairCopiedTitle(ctx, "", messages, sess.Slug)
			if err != nil {
				return "", false, err
			}
			if proposed == "" || strings.EqualFold(proposed, "KEEP") || titleCopiesUserMessage(proposed, messages) {
				return sess.Slug, false, nil
			}
		} else {
			return sess.Slug, false, nil
		}
	}
	if titleCopiesUserMessage(proposed, messages) {
		proposed, err = a.repairCopiedTitle(ctx, currentTitle, messages, proposed)
		if err != nil {
			return "", false, err
		}
		if proposed == "" || strings.EqualFold(proposed, "KEEP") || titleCopiesUserMessage(proposed, messages) {
			return sess.Slug, false, nil
		}
	}
	if strings.EqualFold(proposed, sess.Slug) {
		slog.Debug("agent: title unchanged", "session", sessionID, "title", proposed)
		return sess.Slug, false, nil
	}
	if err := a.opts.Store.RenameSession(ctx, sessionID, proposed); err != nil {
		slog.Warn("agent: title persist failed", "session", sessionID, "err", err)
		return "", false, err
	}
	slog.Info("agent: title updated", "session", sessionID, "old", sess.Slug, "new", proposed)
	bus.Publish(a.opts.Bus, bus.SessionTitleUpdated{
		SessionID: sessionID,
		Title:     proposed,
		Source:    "generated",
		At:        a.opts.Now(),
	})
	return proposed, true, nil
}

func (a *Agent) generateTitleFromMessages(ctx context.Context, currentTitle string, messages []titleMessage, allowKeep bool) (string, error) {
	input := titlePrompt(currentTitle, messages)
	if input == "" {
		return "", nil
	}
	proposed, _, err := a.runtime.GenerateTitle(ctx, input, 32)
	if err != nil {
		return "", err
	}
	proposed = cleanModelTitle(proposed)
	if proposed == "" || (!allowKeep && strings.EqualFold(proposed, "KEEP")) {
		return "", nil
	}
	if titleCopiesUserMessage(proposed, messages) {
		return a.repairCopiedTitle(ctx, currentTitle, messages, proposed)
	}
	return proposed, nil
}

func (a *Agent) repairCopiedTitle(ctx context.Context, currentTitle string, messages []titleMessage, rejected string) (string, error) {
	input := titleRepairPrompt(currentTitle, messages, rejected)
	if input == "" {
		return "", nil
	}
	proposed, _, err := a.runtime.GenerateTitle(ctx, input, 32)
	if err != nil {
		return "", err
	}
	return cleanModelTitle(proposed), nil
}

// maybeSeedPreviewTitle persists a preview slug derived from the user's most
// recent message when sessionID still has no slug. It publishes
// [bus.SessionTitleUpdated] with Source="preview" on success. Best-effort:
// store errors are logged and swallowed so they do not fail the turn.
func (a *Agent) maybeSeedPreviewTitle(ctx context.Context, sessionID string, userParts []session.Part) {
	if a == nil || a.opts.Store == nil || sessionID == "" {
		return
	}
	sess, err := a.opts.Store.GetSession(ctx, sessionID)
	if err != nil || sess == nil || sess.Slug != "" {
		return
	}
	var b strings.Builder
	for _, p := range userParts {
		if p.Kind != session.PartText || strings.TrimSpace(p.Text) == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(p.Text)
	}
	preview := cleanModelTitle(b.String())
	if preview == "" {
		return
	}
	if err := a.opts.Store.RenameSession(ctx, sessionID, preview); err != nil {
		slog.Warn("agent: seed preview title failed", "session", sessionID, "err", err)
		return
	}
	slog.Debug("agent: seeded preview title", "session", sessionID, "preview", preview)
	bus.Publish(a.opts.Bus, bus.SessionTitleUpdated{
		SessionID: sessionID,
		Title:     preview,
		Source:    "preview",
		At:        a.opts.Now(),
	})
}

func (a *Agent) activeModel() session.ModelRef {
	h := a.handle.Load()
	if h == nil {
		return session.ModelRef{}
	}
	return h.ref
}

// latestUsageFor returns the most recently recorded context usage for
// sessionID as (usedTokens, pctUsed).  Returns (0, 0) when no usage has
// been recorded yet.  Guarded by a.mu.
func (a *Agent) latestUsageFor(sessionID string) (int64, float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	u := a.latestUsage[sessionID]
	return u.usedTokens, u.pctUsed
}

func (a *Agent) systemPrompt() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.opts.SystemPrompt
}

func (a *Agent) setActiveModeName(modeName string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.activeModeName = modeName
}

func (a *Agent) activeHookModeName() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeModeName
}

// Close releases the agent.  After Close, Send and Compact return
// [ErrClosed].  Idempotent.
func (a *Agent) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil
	}
	a.closed = true
	a.cancel()
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
// until the assistant produces a final response with no further tool calls.
// Fantasy-backed turns are uncapped. Returns the final committed assistant
// message.
//
// If a Send is already in flight for the session, the new send is queued for
// a future turn and nil is returned. Active-turn guidance should use Steer.
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
//   - bus.TurnCompleted                       after a successful turn
//   - bus.QueueChanged                        when queue depth changes
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

	// Check whether the session is already running. If so, queue this as a
	// future turn instead of modifying the active Fantasy turn.
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil, ErrClosed
	}
	if _, busy := a.activeRuns[sessionID]; busy {
		a.queues[sessionID] = append(a.queues[sessionID], QueuedSend{Parts: append([]session.Part(nil), userParts...)})
		count := len(a.queues[sessionID])
		prompts := queuedPrompts(a.queues[sessionID])
		a.mu.Unlock()
		bus.Publish(a.opts.Bus, bus.QueueChanged{
			SessionID: sessionID,
			Count:     count,
			Prompts:   prompts,
			At:        a.opts.Now(),
		})
		return nil, nil
	}
	// Mark session as active before releasing mu.
	a.activeRuns[sessionID] = struct{}{}
	a.mu.Unlock()

	// Run the send and, when done, pop the next queued entry (if any).
	msg, err := a.doSend(ctx, sessionID, userParts)

	// After the run, dequeue and dispatch the next entry (if any).
	a.mu.Lock()
	delete(a.activeRuns, sessionID)
	var next *QueuedSend
	if len(a.queues[sessionID]) > 0 {
		q := a.queues[sessionID]
		first := q[0]
		next = &first
		a.queues[sessionID] = q[1:]
		count := len(a.queues[sessionID])
		prompts := queuedPrompts(a.queues[sessionID])
		a.mu.Unlock()
		bus.Publish(a.opts.Bus, bus.QueueChanged{
			SessionID: sessionID,
			Count:     count,
			Prompts:   prompts,
			At:        a.opts.Now(),
		})
	} else {
		a.mu.Unlock()
	}

	// Kick off the next queued send.  We use a goroutine so that this
	// Send's caller gets their result immediately; the next send runs
	// independently.  We use a.ctx (the agent's own lifetime context)
	// rather than the caller's ctx, which may already be cancelled by the
	// time this goroutine starts (the UI's defer cancel() fires as soon as
	// Agent.Send returns).
	if next != nil {
		go func() {
			_, _ = a.Send(a.ctx, sessionID, next.Parts)
		}()
	}

	return msg, err
}

// doSend executes the actual send work: hook, persist user message, runLoop.
// It is called only when the session is not currently busy (the caller has
// already set activeRuns[sessionID]).
func (a *Agent) doSend(ctx context.Context, sessionID string, userParts []session.Part) (*session.Message, error) {
	// Re-check closed under mu (racing Close is safe because activeRuns
	// was set before we released the lock).
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
	var userText strings.Builder
	for _, p := range userParts {
		if p.Kind == session.PartText {
			userText.WriteString(p.Text)
		}
	}

	// Run pre_message hook BEFORE persisting the user message.  On Deny,
	// return immediately without appending anything.  On Modify, replace
	// the text in the user parts. Hooks may also return one-turn system
	// prompt additions; those are queued after the user message is safely
	// persisted so plugin context does not alter the visible transcript.
	var systemPromptAdditions []string
	if a.opts.Hooks != nil {
		hookIn := hook.Input{
			Event:     hook.EventPreMessage,
			SessionID: sessionID,
			HookName:  "pre_message",
			Pwd:       pwd,
			Message:   userText.String(),
			ModeName:  a.activeHookModeName(),
		}
		out, dec, denier, reason, warns := a.opts.Hooks.RunPre(ctx, hook.EventPreMessage, hookIn)
		logHookWarns(warns)
		if dec == hook.DecisionDeny {
			return nil, fmt.Errorf("agent: pre_message hook %q denied: %s", denier, reason)
		}
		// Use the (possibly modified) message from the hook output.
		if out.Message != "" && out.Message != userText.String() {
			userParts = replaceTextParts(userParts, out.Message)
		}
		systemPromptAdditions = append(systemPromptAdditions, out.SystemPromptAdditions...)
	}

	// Persist the user message before any provider work.
	userMsg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: append([]session.Part(nil), userParts...),
	})
	if err != nil {
		return nil, fmt.Errorf("agent: Send: append user: %w", err)
	}
	if a.opts.Hooks != nil {
		a.appendPendingSystemAdditions(sessionID, systemPromptAdditions)
		defer a.clearPendingSystemAdditions(sessionID)
	}
	// Seed a preview title from the user's message when the session has no
	// slug yet, so the sidebar updates instantly. The title model
	// (RefreshSessionTitle) treats the verbatim copy as a forced-regen signal
	// and replaces it once a real summary is available.
	a.maybeSeedPreviewTitle(ctx, sessionID, userParts)
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
	active := a.activeModel()
	if active.Name == "" {
		active = sess.Model
	}

	// Publish TurnStarted so the UI can flip to busy before the first token
	// arrives.  This fires regardless of outcome — even if runLoop returns an
	// error, the turn did start.
	bus.Publish(a.opts.Bus, bus.TurnStarted{
		SessionID: sessionID,
		At:        a.opts.Now(),
	})

	result, runErr := a.runLoop(ctx, sessionID, active.Name)

	// Publish TurnCompleted on clean success.
	if runErr == nil {
		bus.Publish(a.opts.Bus, bus.TurnCompleted{
			SessionID: sessionID,
			At:        a.opts.Now(),
		})
	}

	return result, runErr
}

// Steer queues active-turn guidance to be applied at the next Fantasy step.
func (a *Agent) Steer(sessionID string, userParts []session.Part) error {
	if a.isClosed() {
		return ErrClosed
	}
	if sessionID == "" {
		return fmt.Errorf("agent: Steer: sessionID required")
	}
	if len(userParts) == 0 {
		return fmt.Errorf("agent: Steer: userParts required")
	}
	text := strings.TrimSpace(firstTextPart(userParts))
	if text == "" {
		return fmt.Errorf("agent: Steer: text required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return ErrClosed
	}
	if _, busy := a.activeRuns[sessionID]; !busy {
		return ErrNoActiveTurn
	}
	a.pendingSteering[sessionID] = append(a.pendingSteering[sessionID], text)
	return nil
}

func firstTextPart(parts []session.Part) string {
	for _, p := range parts {
		if p.Kind == session.PartText {
			return p.Text
		}
	}
	return ""
}

func steeringUserText(steering []string) string {
	clean := make([]string, 0, len(steering))
	for _, text := range steering {
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Steering for the active turn. Apply this to the current work if still relevant. Do not treat it as a new queued task.")
	for _, text := range clean {
		b.WriteString("\n\n")
		b.WriteString(text)
	}
	return b.String()
}

// queuedPrompts extracts the first PartText from each QueuedSend.
// len(result) == len(q).  Callers must hold a.mu.
func queuedPrompts(q []QueuedSend) []string {
	prompts := make([]string, len(q))
	for i, qs := range q {
		for _, p := range qs.Parts {
			if p.Kind == session.PartText {
				prompts[i] = p.Text
				break
			}
		}
	}
	return prompts
}

// Enqueue adds a prompt to the explicit per-session queue for later execution.
func (a *Agent) Enqueue(sessionID string, userParts []session.Part) error {
	if a.isClosed() {
		return ErrClosed
	}
	if sessionID == "" {
		return fmt.Errorf("agent: Enqueue: sessionID required")
	}
	if len(userParts) == 0 {
		return fmt.Errorf("agent: Enqueue: userParts required")
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return ErrClosed
	}
	a.queues[sessionID] = append(a.queues[sessionID], QueuedSend{Parts: append([]session.Part(nil), userParts...)})
	count := len(a.queues[sessionID])
	prompts := queuedPrompts(a.queues[sessionID])
	a.mu.Unlock()
	bus.Publish(a.opts.Bus, bus.QueueChanged{
		SessionID: sessionID,
		Count:     count,
		Prompts:   prompts,
		At:        a.opts.Now(),
	})
	return nil
}

// QueueCount returns the number of pending queued sends for the session.
func (a *Agent) QueueCount(sessionID string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.queues[sessionID])
}

// QueuedPrompts returns the queued prompt texts (first PartText of each)
// for display in the UI.
func (a *Agent) QueuedPrompts(sessionID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return queuedPrompts(a.queues[sessionID])
}

// ClearQueue drops all pending queued sends for the session.
// Returns the number of sends that were dropped.
func (a *Agent) ClearQueue(sessionID string) int {
	a.mu.Lock()
	n := len(a.queues[sessionID])
	if n == 0 {
		a.mu.Unlock()
		return 0
	}
	delete(a.queues, sessionID)
	a.mu.Unlock()
	bus.Publish(a.opts.Bus, bus.QueueChanged{
		SessionID: sessionID,
		Count:     0,
		Prompts:   nil,
		At:        a.opts.Now(),
	})
	return n
}

// IsSessionBusy reports whether the session has an active run in flight.
func (a *Agent) IsSessionBusy(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, busy := a.activeRuns[sessionID]
	return busy
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

// replacePendingSystemAdditions replaces one-turn system prompt additions for
// sessionID. An empty additions slice clears any previously queued hook context.
// Guarded by a.mu.
func (a *Agent) replacePendingSystemAdditions(sessionID string, additions []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(additions) == 0 {
		delete(a.pendingSystemAdditions, sessionID)
		return
	}
	a.pendingSystemAdditions[sessionID] = append([]string(nil), additions...)
}

func (a *Agent) appendPendingSystemAdditions(sessionID string, additions []string) {
	if len(additions) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.pendingSystemAdditions[sessionID] = append(a.pendingSystemAdditions[sessionID], additions...)
}

// drainPendingSystemAdditions returns and clears queued one-turn system prompt
// additions for sessionID.  Guarded by a.mu.
func (a *Agent) drainPendingSystemAdditions(sessionID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	additions := a.pendingSystemAdditions[sessionID]
	if len(additions) == 0 {
		return nil
	}
	delete(a.pendingSystemAdditions, sessionID)
	return additions
}

func (a *Agent) clearPendingSystemAdditions(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pendingSystemAdditions, sessionID)
}

func (a *Agent) drainPendingSteering(sessionID string) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	steering := a.pendingSteering[sessionID]
	if len(steering) == 0 {
		return nil
	}
	delete(a.pendingSteering, sessionID)
	return steering
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

// maxPluginInjectsPerTurn is the per-plugin per-turn cap for InjectMessage.
// Plugins that inject more than this many messages in a single turn are
// silently rate-limited to prevent runaway feedback loops.
const maxPluginInjectsPerTurn = 10

// InjectMessage appends a message to sessionID on behalf of a plugin.
//
// role must be "user" or "assistant".  Only "user" messages trigger a new
// agent turn; "assistant" messages are persisted but the loop is not re-
// entered (they serve as synthetic context injections).
//
// Each plugin is tracked by pluginName.  At most maxPluginInjectsPerTurn
// calls per pluginName per active turn are processed; additional calls
// return ErrInjectCap without appending anything.
func (a *Agent) InjectMessage(ctx context.Context, pluginName, sessionID, role, content string) error {
	if a.isClosed() {
		return ErrClosed
	}
	if sessionID == "" {
		return fmt.Errorf("agent: InjectMessage: sessionID required")
	}
	if role != "user" && role != "assistant" {
		return fmt.Errorf("agent: InjectMessage: role must be 'user' or 'assistant', got %q", role)
	}
	if content == "" {
		return fmt.Errorf("agent: InjectMessage: content must not be empty")
	}

	// Check and increment the per-turn injection counter.
	a.mu.Lock()
	key := pluginInjectKey{plugin: pluginName, session: sessionID}
	if a.pluginInjects == nil {
		a.pluginInjects = make(map[pluginInjectKey]int)
	}
	n := a.pluginInjects[key]
	if n >= maxPluginInjectsPerTurn {
		a.mu.Unlock()
		slog.Warn("agent: plugin inject cap reached; dropping message",
			"plugin", pluginName, "session", sessionID, "cap", maxPluginInjectsPerTurn)
		return ErrInjectCap
	}
	a.pluginInjects[key] = n + 1
	a.mu.Unlock()

	r := session.Role(role)
	_, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{
		Role: r,
		Parts: []session.Part{
			{Kind: session.PartText, Text: content},
		},
	})
	if err != nil {
		return fmt.Errorf("agent: InjectMessage: append: %w", err)
	}
	return nil
}

// ResetPluginInjectCounters resets the per-turn injection counters for
// sessionID.  Called by the agent loop at the start of each turn so the
// cap applies per-turn, not per-session.
func (a *Agent) ResetPluginInjectCounters(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pluginInjects == nil {
		return
	}
	// Remove all entries for this session.
	for k := range a.pluginInjects {
		if k.session == sessionID {
			delete(a.pluginInjects, k)
		}
	}
}

// pluginInjectKey keys the per-turn injection counter.
type pluginInjectKey struct {
	plugin  string
	session string
}

// sessionUsage holds the last-seen context usage for a session, as
// reported by recordUsage.  Zero value means "no data yet".
type sessionUsage struct {
	usedTokens int64
	pctUsed    float64
}

// ErrInjectCap is returned by InjectMessage when a plugin has injected the
// maximum number of messages for the current turn.
var ErrInjectCap = errors.New("agent: plugin inject cap reached")
