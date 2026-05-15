package agent

import (
	"context"
	"fmt"
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
	model fantasy.LanguageModel
	tools *tool.Registry
}

// RuntimeOptions configures Runtime.
type RuntimeOptions struct {
	Model fantasy.LanguageModel
	Tools *tool.Registry
	// MaxIterations is accepted for legacy construction compatibility. Fantasy
	// active turns are intentionally uncapped; cancellation is context-driven.
	MaxIterations int
}

// NewRuntime constructs the turn runtime. Tools may be nil only in tests that
// do not execute turns; Agent.New validates the production path first.
func NewRuntime(opts RuntimeOptions) *Runtime {
	return &Runtime{model: opts.Model, tools: opts.Tools}
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
}

func (r *Runtime) hasFantasyModel() bool {
	return r != nil && r.model != nil
}

func (r *Runtime) newFantasyAgent(tools []fantasy.AgentTool) fantasy.Agent {
	return fantasy.NewAgent(r.model, fantasy.WithTools(tools...))
}

func (r *Runtime) newInternalAgent() fantasy.Agent {
	return fantasy.NewAgent(r.model, fantasy.WithTools())
}

// Summarize runs a no-tool Fantasy agent for internal conversation compaction.
func (r *Runtime) Summarize(ctx context.Context, messages []fantasy.Message, maxTokens int) (string, provider.Usage, error) {
	if !r.hasFantasyModel() {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy model is not configured")
	}
	maxOutputTokens := int64(maxTokens)
	res, err := r.newInternalAgent().Generate(ctx, fantasy.AgentCall{Messages: messages, MaxOutputTokens: &maxOutputTokens})
	if err != nil {
		return "", provider.Usage{}, err
	}
	if res == nil {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy summary returned nil result")
	}
	return strings.TrimSpace(res.Response.Content.Text()), usageFromFantasy(res.TotalUsage), nil
}

// GenerateTitle is the narrow no-tool seam for future model-generated session
// titles/slugs. Hygge currently displays FirstMessagePreview or a user-edited
// Slug, so this is intentionally not wired into UI/store mutation yet.
func (r *Runtime) GenerateTitle(ctx context.Context, prompt string, maxTokens int) (string, provider.Usage, error) {
	if !r.hasFantasyModel() {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy model is not configured")
	}
	maxOutputTokens := int64(maxTokens)
	res, err := r.newInternalAgent().Generate(ctx, fantasy.AgentCall{Messages: []fantasy.Message{
		fantasy.NewSystemMessage("Generate a concise session title. Return only the title."),
		fantasy.NewUserMessage(prompt),
	}, MaxOutputTokens: &maxOutputTokens})
	if err != nil {
		return "", provider.Usage{}, err
	}
	if res == nil {
		return "", provider.Usage{}, fmt.Errorf("agent: fantasy title returned nil result")
	}
	return strings.TrimSpace(res.Response.Content.Text()), usageFromFantasy(res.TotalUsage), nil
}

func (r *Runtime) buildFantasyTools(opts fantasyToolOptions) []fantasy.AgentTool {
	if r == nil || r.tools == nil {
		return nil
	}
	tools := r.tools.All()
	out := make([]fantasy.AgentTool, 0, len(tools))
	for _, t := range tools {
		out = append(out, &fantasyTool{t: t, opts: opts})
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
	return s.agent.runLegacyLoop(ctx, sessionID, modelName)
}
