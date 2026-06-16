package subagent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/agent"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// Runner is the entry point for invoking a sub-agent.  One Runner is
// constructed per CLI bootstrap and shared across all `task` tool
// invocations.  Safe for concurrent calls.
type Runner struct {
	bus           *bus.Bus
	store         session.Store
	provider      provider.Provider
	permission    *permission.Engine
	catalog       *cost.Catalog
	registry      *Registry
	parentTools   *tool.Registry
	resolver      ProviderResolver
	fantasyModel  FantasyModelResolver
	pwd           string
	contextWindow int64
	maxOutput     int64
	now           func() time.Time
}

// RunnerOptions configures a [Runner].  All non-trivial fields are
// required; New returns an error if any are nil.
type RunnerOptions struct {
	// Bus, Store, Provider, Permission, Catalog mirror agent.Options.
	// The sub-agent shares all of them with the parent so events,
	// persistence, permission decisions, and pricing lookups all flow
	// through one set of components.
	Bus        *bus.Bus
	Store      session.Store
	Provider   provider.Provider
	Permission *permission.Engine
	Catalog    *cost.Catalog

	// Registry is the resolved sub-agent catalogue.  Required.
	Registry *Registry

	// ParentTools is the parent agent's tool registry.  The runtime
	// reads built-in tools from here and builds a restricted sub-set
	// for each sub-agent invocation.  Required.
	ParentTools *tool.Registry

	// Pwd is the working directory passed to sub-agent tools.
	Pwd string

	// ContextWindow is the model's maximum context size in tokens,
	// forwarded to the embedded agent loop for ContextUsageUpdated
	// pct-used math.  Zero is legal: the loop just skips PctUsed.
	ContextWindow int64

	// MaxOutput is the model's reserved output budget in tokens,
	// forwarded to the embedded agent loop so PctUsed is computed
	// against the input-available window (ContextWindow-MaxOutput).
	// Zero is legal: no output reservation is applied.
	MaxOutput int64

	// ProviderResolver, when non-nil, is consulted whenever a
	// [Type.Model] override is set on the requested type.  The
	// resolver returns the provider to use and the bare model id to
	// pass into the embedded agent loop.  When nil, or when the
	// requested type has no Model override, the runner reuses
	// Provider + RunInput.ModelName.
	//
	// Resolver errors are surfaced as [Runner.Run] errors so the
	// task tool can render them as an IsError result for the parent
	// model.
	ProviderResolver ProviderResolver

	// FantasyModelResolver returns the Fantasy language model for the resolved
	// provider/model pair.  It is required because sub-agent turns are Fantasy-only.
	FantasyModelResolver FantasyModelResolver

	// Now is an injectable clock for bus events and durations.
	// Defaults to time.Now.
	Now func() time.Time
}

// NewRunner constructs a Runner.  Returns an error if any required
// option is nil.
func NewRunner(opts RunnerOptions) (*Runner, error) {
	if opts.Bus == nil {
		return nil, fmt.Errorf("subagent: NewRunner: Bus is required")
	}
	if opts.Store == nil {
		return nil, fmt.Errorf("subagent: NewRunner: Store is required")
	}
	if opts.Provider == nil {
		return nil, fmt.Errorf("subagent: NewRunner: Provider is required")
	}
	if opts.Permission == nil {
		return nil, fmt.Errorf("subagent: NewRunner: Permission is required")
	}
	if opts.Catalog == nil {
		return nil, fmt.Errorf("subagent: NewRunner: Catalog is required")
	}
	if opts.Registry == nil {
		return nil, fmt.Errorf("subagent: NewRunner: Registry is required")
	}
	if opts.ParentTools == nil {
		return nil, fmt.Errorf("subagent: NewRunner: ParentTools is required")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Runner{
		bus:           opts.Bus,
		store:         opts.Store,
		provider:      opts.Provider,
		permission:    opts.Permission,
		catalog:       opts.Catalog,
		registry:      opts.Registry,
		parentTools:   opts.ParentTools,
		resolver:      opts.ProviderResolver,
		fantasyModel:  opts.FantasyModelResolver,
		pwd:           opts.Pwd,
		contextWindow: opts.ContextWindow,
		maxOutput:     opts.MaxOutput,
		now:           opts.Now,
	}, nil
}

// Registry returns the resolved [Registry] this runner uses.  Exposed
// so the `task` tool can build its input-schema enum lazily, picking
// up types added via TOML after start-up.  (Stage A does not actually
// re-load at runtime, but the indirection keeps the door open.)
func (r *Runner) Registry() *Registry {
	if r == nil {
		return nil
	}
	return r.registry
}

// Types implements [tool.SubagentRunner].  It returns the name +
// description of every registered type, used by the `task` tool to
// build its input-schema enum.
func (r *Runner) Types() []tool.SubagentType {
	if r == nil {
		return nil
	}
	all := r.registry.List()
	out := make([]tool.SubagentType, len(all))
	for i, t := range all {
		out[i] = tool.SubagentType{Name: t.Name, Description: t.Description}
	}
	return out
}

// ToolAdapter returns a [tool.SubagentRunner] that delegates to this
// Runner.  cmd/hygge/cli passes the result to [tool.NewSubagentTool] when
// wiring the orchestrator's tool set.  Keeping the adapter in a tiny
// type lets the tool package stay free of an internal/subagent
// dependency (which would create an import cycle).
func (r *Runner) ToolAdapter() tool.SubagentRunner {
	return toolAdapter{runner: r}
}

// toolAdapter satisfies tool.SubagentRunner by translating between
// the tool-package and subagent-package shapes.
type toolAdapter struct{ runner *Runner }

func (a toolAdapter) Run(ctx context.Context, in tool.SubagentRunInput) (tool.SubagentResult, error) {
	res, err := a.runner.Run(ctx, RunInput{
		ParentSessionID: in.ParentSessionID,
		ParentToolUseID: in.ParentToolUseID,
		Type:            in.Type,
		Description:     in.Description,
		Prompt:          in.Prompt,
		ModelName:       in.ModelName,
	})
	out := tool.SubagentResult{
		SessionID: res.SessionID,
		FinalText: res.FinalText,
		Usage:     res.Usage,
		CostUSD:   res.Cost.USD,
		Duration:  res.Duration,
	}
	return out, err
}

func (a toolAdapter) Types() []tool.SubagentType {
	return a.runner.Types()
}

// RunInput is the per-invocation payload for [Runner.Run].
type RunInput struct {
	// ParentSessionID is the dispatching session's id.  The created
	// sub-session is linked to it via ParentID.  Required.
	ParentSessionID string

	// ParentToolUseID is the `task` tool call's tool_use_id.  Stage A
	// records this on the sub-session's slug for traceability; future
	// stages may surface it more prominently.
	ParentToolUseID string

	// Type is the requested sub-agent type name.  Must exist in the
	// runner's registry.
	Type string

	// Description is a short human-language label (3-5 words) for the
	// mission.  Echoed into bus events and logs.
	Description string

	// Prompt is the user-shaped first message handed to the
	// sub-agent.  Required.
	Prompt string

	// ModelName is the parent agent's current model name.  Used as
	// the default when the requested type has no [Type.Model]
	// override.  When the type pins an override, the resolver
	// returns the model id and this field is ignored for the run
	// (but still recorded on the input for traceability).
	ModelName string
}

// Run executes one sub-agent invocation synchronously.  See [Result]
// for what's returned.
//
// Failure modes:
//
//   - An unknown type name returns an error immediately (no sub-session
//     created).
//   - A failure inside the embedded agent loop returns the wrapped
//     error AND a partially-populated Result.SessionID so the caller
//     can link to the audit trail.  The sub-session is NOT deleted.
func (r *Runner) Run(ctx context.Context, in RunInput) (Result, error) {
	if r == nil {
		return Result{}, fmt.Errorf("subagent: Run: nil runner")
	}
	if in.ParentSessionID == "" {
		return Result{}, fmt.Errorf("subagent: Run: ParentSessionID required")
	}
	if in.Type == "" {
		return Result{}, fmt.Errorf("subagent: Run: Type required")
	}
	if in.Prompt == "" {
		return Result{}, fmt.Errorf("subagent: Run: Prompt required")
	}
	if in.ModelName == "" {
		return Result{}, fmt.Errorf("subagent: Run: ModelName required")
	}

	t, ok := r.registry.Get(in.Type)
	if !ok {
		return Result{}, fmt.Errorf("subagent: Run: unknown type %q", in.Type)
	}

	// Resolve the parent session so we can inherit its project_dir
	// and model provider.  An invalid ParentSessionID surfaces here
	// before any side-effect.
	parent, err := r.store.GetSession(ctx, in.ParentSessionID)
	if err != nil {
		return Result{}, fmt.Errorf("subagent: Run: load parent session: %w", err)
	}

	// Determine the provider + model for this run.  When the type
	// pins a [Type.Model], hand it to the resolver; the resolver
	// returns the provider to use and the bare model id.  When the
	// resolver is absent or the override is empty / malformed, fall
	// back to the parent's provider + RunInput.ModelName.
	//
	// Malformed model strings should not actually reach us -- the
	// registry strips them at load time -- but we treat the case as
	// "fall back" rather than "fail" so a stale Type in memory
	// never breaks an otherwise valid sub-agent run.
	runProvider := r.provider
	runModelName := in.ModelName
	runFantasyModel := fantasy.LanguageModel(nil)
	if t.Model != "" {
		switch {
		case r.resolver == nil:
			slog.Warn("subagent: model override set but no ProviderResolver wired; using parent's provider",
				"type", t.Name, "requested_model", t.Model)
		case !IsValidModelRef(t.Model):
			slog.Warn("subagent: stored model override is malformed; using parent's provider",
				"type", t.Name, "requested_model", t.Model)
		default:
			p, m, err := r.resolver(ctx, t.Model)
			if err != nil {
				return Result{}, fmt.Errorf("subagent: Run: resolve %q for type %q: %w",
					t.Model, t.Name, err)
			}
			runProvider = p
			runModelName = m
		}
	}
	if r.fantasyModel != nil {
		lm, err := r.fantasyModel(ctx, runProvider.Name(), runModelName)
		if err != nil {
			return Result{}, fmt.Errorf("subagent: Run: resolve fantasy model %q/%q: %w", runProvider.Name(), runModelName, err)
		}
		runFantasyModel = lm
	}
	if runFantasyModel == nil {
		return Result{}, fmt.Errorf("subagent: Run: fantasy model is not configured")
	}

	subTools := r.buildToolRegistry(t)

	// Log the resolved run parameters so that on failure (e.g. "invalid
	// request" from OpenRouter) operators can see exactly what was sent:
	// provider, model, agent type, and the tool names registered.
	allSubTools := subTools.All()
	toolNames := make([]string, 0, len(allSubTools))
	for _, tt := range allSubTools {
		toolNames = append(toolNames, tt.Name())
	}
	slog.Debug("subagent: run resolved",
		"type", t.Name,
		"provider", runProvider.Name(),
		"model", runModelName,
		"tools", toolNames,
	)

	// Create the sub-session up front so it's auditable even when the
	// run fails partway through.  We do NOT carry a fork_message_id;
	// sub-agents are not forks of the parent's history -- they branch
	// from a tool_use into a fresh conversation.
	subModel := session.ModelRef{Provider: runProvider.Name(), Name: runModelName}
	sub, err := r.store.CreateSession(ctx, session.NewSession{
		ProjectDir:      parent.ProjectDir,
		Model:           subModel,
		ParentID:        in.ParentSessionID,
		ParentToolUseID: in.ParentToolUseID,
		Kind:            session.KindSubagent,
		Slug:            buildSlug(in.Type, in.Description, in.ParentToolUseID),
	})
	if err != nil {
		return Result{}, fmt.Errorf("subagent: Run: create sub-session: %w", err)
	}

	startedAt := r.now()
	bus.Publish(r.bus, bus.SubagentStarted{
		SubSessionID:    sub.ID,
		ParentSessionID: in.ParentSessionID,
		ParentMessageID: in.ParentToolUseID,
		Type:            t.Name,
		Description:     in.Description,
		InitialPrompt:   in.Prompt,
		Model:           runProvider.Name() + "/" + runModelName,
		At:              startedAt,
	})

	// Build an ephemeral Agent for this single run.  We intentionally
	// do NOT share the parent's Agent: the sub-agent needs a different
	// tool registry and (when the type pins one) a different
	// provider + model.
	ag, err := agent.New(agent.Options{
		Bus:           r.bus,
		Store:         r.store,
		Provider:      runProvider,
		FantasyModel:  runFantasyModel,
		Permission:    r.permission,
		Tools:         subTools,
		Catalog:       r.catalog,
		SystemPrompt:  t.SystemPrompt,
		Pwd:           r.pwd,
		Now:           r.now,
		ContextWindow: r.contextWindow,
		MaxOutput:     r.maxOutput,
		// LazyContext is intentionally nil: sub-agents start with a
		// clean slate.  Stage C may revisit if a sub-agent type
		// wants its own subdir-context tracker.
	})
	if err != nil {
		// Session row already exists -- emit the completion event so
		// observers see a paired Started/Completed even on this rare
		// failure mode.
		r.publishCompleted(sub.ID, in.ParentSessionID, t.Name, in.Description, startedAt, Result{
			SessionID: sub.ID,
		})
		return Result{SessionID: sub.ID}, fmt.Errorf("subagent: Run: build agent: %w", err)
	}
	defer func() { _ = ag.Close() }()

	finalMsg, sendErr := ag.Send(ctx, sub.ID, []session.Part{
		{Kind: session.PartText, Text: in.Prompt},
	})

	if sendErr != nil {
		// Real failure: surface it.  Sub-session messages (whatever
		// was committed before the error) remain in place.
		res := Result{
			SessionID: sub.ID,
			Duration:  r.now().Sub(startedAt),
		}
		r.publishCompleted(sub.ID, in.ParentSessionID, t.Name, in.Description, startedAt, res)
		return res, fmt.Errorf("subagent: Run: %w", sendErr)
	}

	// Build the Result.  We re-read the sub-session totals to capture
	// the embedded agent's cost accounting (the loop updates totals
	// after each turn).
	usage, money := r.summariseSubSession(ctx, sub.ID)
	finalText := extractFinalText(finalMsg)

	res := Result{
		SessionID: sub.ID,
		FinalText: finalText,
		Usage:     usage,
		Cost:      money,
		Duration:  r.now().Sub(startedAt),
	}
	r.publishCompleted(sub.ID, in.ParentSessionID, t.Name, in.Description, startedAt, res)
	return res, nil
}

// buildToolRegistry returns the tool.Registry handed to the embedded
// agent.New for one sub-agent run.  The registry is per-call to avoid
// any cross-run state bleed; built-in tools are pulled from the
// runner's parent registry so they share the read-tracker and other
// cross-tool state.
//
// `subagent` is filtered out unconditionally -- defence in depth even
// after registry.go's normalizeEntry already stripped it.  This is
// the runtime's recursion guard.
func (r *Runner) buildToolRegistry(t *Type) *tool.Registry {
	allowed := t.Tools
	if len(allowed) == 0 {
		allowed = r.registry.DefaultTools()
	}
	filtered := make([]string, 0, len(allowed))
	for _, name := range allowed {
		if name == "subagent" {
			slog.Warn("subagent: subagent tool ignored in tools list (recursion guard)",
				"type", t.Name)
			continue
		}
		filtered = append(filtered, name)
	}

	reg := tool.NewRegistry()
	for _, name := range filtered {
		base, ok := r.parentTools.Get(name)
		if !ok {
			slog.Warn("subagent: requested tool not found in parent registry; skipping",
				"type", t.Name, "tool", name)
			continue
		}
		if err := reg.Register(base); err != nil {
			slog.Warn("subagent: failed to register tool; skipping",
				"type", t.Name, "tool", name, "err", err)
		}
	}
	return reg
}

// summariseSubSession reads the sub-session totals to assemble the
// Result's Usage / Cost.  Pricing lookups are best-effort: an
// uncatalogued model yields a zero cost but never fails the run.
func (r *Runner) summariseSubSession(ctx context.Context, sessionID string) (provider.Usage, cost.Money) {
	sub, err := r.store.GetSession(ctx, sessionID)
	if err != nil {
		slog.Warn("subagent: reload sub-session for totals failed",
			"session", sessionID, "err", err)
		return provider.Usage{}, cost.Money{}
	}
	usage := provider.Usage{
		InputTokens:      sub.Totals.InputTokens,
		OutputTokens:     sub.Totals.OutputTokens,
		CacheReadTokens:  sub.Totals.CacheReadTokens,
		CacheWriteTokens: sub.Totals.CacheWriteTokens,
	}
	return usage, cost.Money{USD: sub.Totals.CostUSD}
}

// publishCompleted emits the paired SubagentCompleted event.
func (r *Runner) publishCompleted(
	subSessionID, parentSessionID, typeName, description string,
	startedAt time.Time, res Result,
) {
	bus.Publish(r.bus, bus.SubagentCompleted{
		SubSessionID:    subSessionID,
		ParentSessionID: parentSessionID,
		Type:            typeName,
		Description:     description,
		DurationMs:      r.now().Sub(startedAt).Milliseconds(),
		CostUSD:         res.Cost.USD,
		At:              r.now(),
	})
}

// extractFinalText pulls the textual content out of an assistant
// message.  The agent loop emits parts in [text, thinking, tool_use]
// order; we concatenate every text part and ignore the rest.  Returns
// the empty string when the message is nil or has no text parts.
func extractFinalText(m *session.Message) string {
	if m == nil {
		return ""
	}
	var out strings.Builder
	for _, p := range m.Parts {
		if p.Kind == session.PartText {
			out.WriteString(p.Text)
		}
	}
	return out.String()
}

// buildSlug crafts a human-meaningful slug for the sub-session row.
// Format: "<type>: <description>" trimmed to a reasonable length so
// `hygge sessions list` output stays tidy.
func buildSlug(typeName, description, toolUseID string) string {
	base := typeName
	if description != "" {
		base += ": " + description
	}
	if toolUseID != "" {
		base += " [" + toolUseID + "]"
	}
	const maxLen = 120
	if len(base) > maxLen {
		base = base[:maxLen-1] + "…"
	}
	return base
}
