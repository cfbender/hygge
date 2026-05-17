package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

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
	model              fantasy.LanguageModel
	titleModel         fantasy.LanguageModel
	titleModelExplicit bool
	tools              *tool.Registry
}

// RuntimeOptions configures Runtime.
type RuntimeOptions struct {
	Model      fantasy.LanguageModel
	TitleModel fantasy.LanguageModel
	Tools      *tool.Registry
}

// NewRuntime constructs the turn runtime. Tools may be nil only in tests that
// do not execute turns; Agent.New validates the production path first.
func NewRuntime(opts RuntimeOptions) *Runtime {
	titleModel := opts.TitleModel
	return &Runtime{model: opts.Model, titleModel: titleModel, titleModelExplicit: titleModel != nil, tools: opts.Tools}
}

// SetModel replaces the Fantasy language model used to create future agents.
// Existing fantasy.Agent instances are per-turn and are not reused, so assigning
// here is enough to invalidate the old model for subsequent sends and internal
// compaction/title calls.
func (r *Runtime) SetModel(model fantasy.LanguageModel) {
	if r == nil {
		return
	}
	r.model = model
	if !r.titleModelExplicit {
		r.titleModel = model
	}
}

func (r *Runtime) hasFantasyModel() bool {
	return r != nil && r.model != nil
}

func (r *Runtime) newInternalAgent() fantasy.Agent {
	return fantasy.NewAgent(r.model, fantasy.WithTools())
}

func (r *Runtime) newTitleAgent() fantasy.Agent {
	model := r.titleModel
	if model == nil {
		model = r.model
	}
	return fantasy.NewAgent(model, fantasy.WithTools())
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
	return strings.TrimSpace(summary), usageFromFantasy(res.TotalUsage), nil
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
	if r.titleModel != nil {
		modelName = r.titleModel.Model()
	} else if r.model != nil {
		modelName = r.model.Model()
	}
	slog.Debug("runtime: title model call", "model", modelName, "title_model_explicit", r.titleModelExplicit, "prompt_len", len(prompt))
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
	return out, usageFromFantasy(res.TotalUsage), nil
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
