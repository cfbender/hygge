package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/bus"
)

const renameSessionToolName = "rename_session"

type sessionTitleTool struct {
	agent     *Agent
	sessionID string
	prov      fantasy.ProviderOptions
}

type renameSessionInput struct {
	Topic string `json:"topic"`
}

func (t *sessionTitleTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{
		Name:        renameSessionToolName,
		Description: "Rename the current session when the conversation topic has changed. Provide the new topic; Hygge formats and persists the final title with its small title model.",
		Parameters: map[string]any{
			"topic": map[string]any{
				"type":        "string",
				"description": "A short plain-language description of the current topic. Do not pre-format it as a title.",
			},
		},
		Required: []string{"topic"},
		Parallel: false,
	}
}

func (t *sessionTitleTool) ProviderOptions() fantasy.ProviderOptions { return t.prov }

func (t *sessionTitleTool) SetProviderOptions(opts fantasy.ProviderOptions) { t.prov = opts }

func (t *sessionTitleTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if t == nil || t.agent == nil {
		return fantasy.NewTextErrorResponse("rename_session is not configured"), nil
	}
	var in renameSessionInput
	if err := json.Unmarshal([]byte(call.Input), &in); err != nil {
		return fantasy.NewTextErrorResponse(fmt.Sprintf("invalid rename_session input: %v", err)), nil
	}
	topic := strings.TrimSpace(in.Topic)
	if topic == "" {
		return fantasy.NewTextErrorResponse("topic is required"), nil
	}
	title, err := t.agent.GenerateTitle(ctx, topic)
	if err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	if title == "" || strings.EqualFold(title, "KEEP") {
		return fantasy.NewTextErrorResponse("title model did not return a usable title"), nil
	}
	if err := t.agent.opts.Store.RenameSession(ctx, t.sessionID, title); err != nil {
		return fantasy.NewTextErrorResponse(err.Error()), nil
	}
	slog.Info("agent: title renamed by tool", "session", t.sessionID, "topic", topic, "title", title)
	bus.Publish(t.agent.opts.Bus, bus.SessionTitleUpdated{
		SessionID: t.sessionID,
		Title:     title,
		Source:    "tool",
		At:        t.agent.opts.Now(),
	})
	return fantasy.NewTextResponse(fmt.Sprintf("Renamed session to %q", title)), nil
}

func (a *Agent) titleTool(sessionID string) fantasy.AgentTool {
	if a == nil || a.opts.Store == nil || a.runtime == nil || sessionID == "" {
		return nil
	}
	return &sessionTitleTool{agent: a, sessionID: sessionID}
}
