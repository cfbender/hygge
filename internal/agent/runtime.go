package agent

import (
	"context"
	"fmt"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// Runtime owns the model/tool assembly for active agent turns.
// It is intentionally narrower than Agent: Agent remains the external
// compatibility surface for UI/CLI callers, while Runtime decides which
// turn runner to use and how Fantasy tools are adapted from tool.Registry.
type Runtime struct {
	model         fantasy.LanguageModel
	tools         *tool.Registry
	maxIterations int
}

// RuntimeOptions configures Runtime.
type RuntimeOptions struct {
	Model         fantasy.LanguageModel
	Tools         *tool.Registry
	MaxIterations int
}

// NewRuntime constructs the turn runtime. Tools may be nil only in tests that
// do not execute turns; Agent.New validates the production path first.
func NewRuntime(opts RuntimeOptions) *Runtime {
	return &Runtime{model: opts.Model, tools: opts.Tools, maxIterations: opts.MaxIterations}
}

func (r *Runtime) hasFantasyModel() bool {
	return r != nil && r.model != nil
}

func (r *Runtime) newFantasyAgent(tools []fantasy.AgentTool) fantasy.Agent {
	return fantasy.NewAgent(r.model, fantasy.WithTools(tools...), fantasy.WithStopConditions(fantasy.StepCountIs(r.maxIterations)))
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
