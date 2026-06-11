package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// Runtime owns the model/tool assembly for active agent turns.
// It is intentionally narrower than Agent: Agent remains the external
// compatibility surface for UI/CLI callers, while Runtime decides which
// turn runner to use and how Fantasy tools are adapted from tool.Registry.
type Runtime struct {
	// handle is the active-model bundle, shared with the owning Agent so a
	// model swap is visible to both through one atomic store. Standalone
	// runtimes (tests) own their handle pointer.
	handle *atomic.Pointer[modelHandle]
	// titleModel is the explicitly configured small/title model, or nil when
	// titles should follow the active model.
	titleModel fantasy.LanguageModel
	tools      *tool.Registry
}

// RuntimeOptions configures Runtime.
type RuntimeOptions struct {
	Model      fantasy.LanguageModel
	TitleModel fantasy.LanguageModel
	Tools      *tool.Registry
	// ProviderName is the hygge provider id (e.g. "openrouter"). Optional;
	// when set, drives provider-specific token-accounting normalization in
	// Runtime's no-tool summary/title calls. Lower-case is expected to match
	// the same convention used by the parent Agent's Provider.Name().
	// Ignored when Handle is supplied.
	ProviderName string
	// Handle, when non-nil, shares the owning Agent's active-model bundle so
	// Agent.SetModel swaps are immediately visible to the runtime. When nil
	// (standalone runtimes in tests), a private handle is built from Model
	// and ProviderName.
	Handle *atomic.Pointer[modelHandle]
}

// NewRuntime constructs the turn runtime. Tools may be nil only in tests that
// do not execute turns; Agent.New validates the production path first.
func NewRuntime(opts RuntimeOptions) *Runtime {
	handle := opts.Handle
	if handle == nil {
		handle = &atomic.Pointer[modelHandle]{}
		handle.Store(&modelHandle{
			providerID:   strings.ToLower(strings.TrimSpace(opts.ProviderName)),
			fantasyModel: opts.Model,
		})
	}
	return &Runtime{
		handle:     handle,
		titleModel: opts.TitleModel,
		tools:      opts.Tools,
	}
}

// SetModel replaces the Fantasy language model used to create future agents.
// Existing fantasy.Agent instances are per-turn and are not reused, so the
// handle swap is enough to invalidate the old model for subsequent sends and
// internal compaction/title calls. The rest of the handle (provider identity)
// is preserved; Agent.SetModel stores a complete new handle instead.
func (r *Runtime) SetModel(model fantasy.LanguageModel) {
	if r == nil || r.handle == nil {
		return
	}
	next := *r.handle.Load()
	next.fantasyModel = model
	r.handle.Store(&next)
}

// model returns the active Fantasy model from the shared handle.
func (r *Runtime) model() fantasy.LanguageModel {
	if r == nil || r.handle == nil {
		return nil
	}
	h := r.handle.Load()
	if h == nil {
		return nil
	}
	return h.fantasyModel
}

// providerID returns the lower-case hygge provider id used by
// usageFromFantasy to pick the right token-accounting normalization. May be
// empty in tests that exercise Runtime without a real provider; the
// conversion is a no-op then.
func (r *Runtime) providerID() string {
	if r == nil || r.handle == nil {
		return ""
	}
	h := r.handle.Load()
	if h == nil {
		return ""
	}
	return h.providerID
}

func (r *Runtime) hasFantasyModel() bool {
	return r.model() != nil
}

func (r *Runtime) newInternalAgent() fantasy.Agent {
	return fantasy.NewAgent(r.model(), fantasy.WithTools())
}

// activeTitleModel resolves the model used for title generation: the
// explicitly configured title model when present, otherwise the active model
// (so titles follow model hot-swaps when small_model is absent).
func (r *Runtime) activeTitleModel() fantasy.LanguageModel {
	if r.titleModel != nil {
		return r.titleModel
	}
	return r.model()
}

func (r *Runtime) newTitleAgent() fantasy.Agent {
	return fantasy.NewAgent(r.activeTitleModel(), fantasy.WithTools())
}

// Summarize runs a no-tool Fantasy agent for internal conversation compaction.
func (r *Runtime) Summarize(ctx context.Context, messages []fantasy.Message, maxTokens int) (string, provider.Usage, error) {
	if !r.hasFantasyModel() {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy model is not configured")
	}
	// Some streaming providers reject max_output_tokens for this internal call;
	// rely on the compaction prompt to keep summaries short instead.
	_ = maxTokens
	var text strings.Builder
	res, err := r.newInternalAgent().Stream(ctx, fantasy.AgentStreamCall{
		Messages: messages,
		OnTextDelta: func(_, delta string) error {
			text.WriteString(delta)
			return nil
		},
	})
	if err != nil {
		return "", provider.Usage{}, err
	}
	if res == nil {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy summary returned nil result")
	}
	summary := text.String()
	if summary == "" {
		summary = res.Response.Content.Text()
	}
	return strings.TrimSpace(summary), usageFromFantasy(r.providerID(), res.TotalUsage), nil
}

// GenerateTitle is the narrow no-tool seam for model-generated session titles.
// Callers decide whether to persist the returned title or treat KEEP as a no-op.
// Uses streaming because some OpenAI-compatible providers reject non-stream
// completions (e.g. "Stream must be set to true"), matching how Summarize runs.
// maxTokens is accepted for API stability but intentionally not forwarded:
// several reasoning-class endpoints reject max_output_tokens entirely. The
// titleSystemInstruction prompt already constrains the model to a single line.
func (r *Runtime) GenerateTitle(ctx context.Context, prompt string, maxTokens int) (string, provider.Usage, error) {
	if !r.hasFantasyModel() {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy model is not configured")
	}
	_ = maxTokens
	modelName := ""
	if m := r.activeTitleModel(); m != nil {
		modelName = m.Model()
	}
	slog.Debug("runtime: title model call", "model", modelName, "title_model_explicit", r.titleModel != nil, "prompt_len", len(prompt))
	var text strings.Builder
	res, err := r.newTitleAgent().Stream(ctx, fantasy.AgentStreamCall{
		Messages: []fantasy.Message{
			fantasy.NewSystemMessage("Follow the user's title-formatting instructions exactly."),
			fantasy.NewUserMessage(prompt),
		},
		OnTextDelta: func(_, delta string) error {
			text.WriteString(delta)
			return nil
		},
	})
	if err != nil {
		slog.Warn("runtime: title model error", "model", modelName, "err", err)
		return "", provider.Usage{}, err
	}
	if res == nil {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy title returned nil result")
	}
	out := text.String()
	if out == "" {
		out = res.Response.Content.Text()
	}
	out = strings.TrimSpace(out)
	slog.Debug("runtime: title model returned", "model", modelName, "text", out)
	return out, usageFromFantasy(r.providerID(), res.TotalUsage), nil
}

func (r *Runtime) buildFantasyTools(opts fantasyToolOptions) []fantasy.AgentTool {
	if r == nil || r.tools == nil {
		return nil
	}
	tools := r.tools.All()
	out := make([]fantasy.AgentTool, 0, len(tools))
	hasRenameSession := false
	for _, t := range tools {
		if t.Name() == renameSessionToolName {
			hasRenameSession = true
		}
		out = append(out, &fantasyTool{t: t, opts: opts})
	}
	if !hasRenameSession && opts.agent != nil {
		if titleTool := opts.agent.titleTool(opts.sessionID); titleTool != nil {
			out = append(out, titleTool)
		}
	}
	return out
}

// SessionAgent owns per-session turn execution. Queueing and busy-state live
// on Agent for API compatibility; this type is the handoff point for the
// eventual Phase 5/6 migration of the rest of the session lifecycle.
type SessionAgent struct {
	agent   *Agent
	runtime *Runtime
}

// NewSessionAgent wires an Agent-compatible session runner.
func NewSessionAgent(agent *Agent, runtime *Runtime) *SessionAgent {
	return &SessionAgent{agent: agent, runtime: runtime}
}

// RunTurn executes one model turn for a session using the configured runtime.
func (s *SessionAgent) RunTurn(ctx context.Context, sessionID, modelName string) (*session.Message, error) {
	if s == nil || s.agent == nil {
		return nil, fmt.Errorf("agent: session agent is not configured")
	}
	if s.runtime != nil && s.runtime.hasFantasyModel() {
		return s.agent.runFantasyLoop(ctx, sessionID, modelName)
	}
	return nil, fmt.Errorf("agent: fantasy model is not configured")
}
