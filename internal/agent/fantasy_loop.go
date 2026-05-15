package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

type fantasyTool struct {
	t            tool.Tool
	a            *Agent
	sessionID    string
	messageIDPtr *string
	modelName    string
	pwd          string
	opts         fantasy.ProviderOptions
	beforeRun    func() error
}

func (f *fantasyTool) Info() fantasy.ToolInfo {
	return fantasy.ToolInfo{Name: f.t.Name(), Description: f.t.Description(), Parameters: f.t.InputSchema(), Parallel: f.t.Parallelizable()}
}

func (f *fantasyTool) ProviderOptions() fantasy.ProviderOptions        { return f.opts }
func (f *fantasyTool) SetProviderOptions(opts fantasy.ProviderOptions) { f.opts = opts }

func (f *fantasyTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if f.beforeRun != nil {
		if err := f.beforeRun(); err != nil {
			return fantasy.ToolResponse{}, err
		}
	}
	msgID := ""
	if f.messageIDPtr != nil {
		msgID = *f.messageIDPtr
	}
	args := json.RawMessage(call.Input)
	bus.Publish(f.a.opts.Bus, bus.ToolCallRequested{SessionID: f.sessionID, MessageID: msgID, ToolUseID: call.ID, ToolName: call.Name, Args: append([]byte(nil), args...), At: f.a.opts.Now()})

	toolInput := args
	if f.a.opts.Hooks != nil {
		hookIn := hook.Input{Event: hook.EventPreTool, SessionID: f.sessionID, HookName: "pre_tool", Pwd: f.pwd, ToolName: call.Name, ToolInput: toolInput}
		out, dec, denier, reason, warns := f.a.opts.Hooks.RunPre(ctx, hook.EventPreTool, hookIn)
		logHookWarns(warns)
		if dec == hook.DecisionDeny {
			content := fmt.Sprintf("hook %q denied tool call: %s", denier, reason)
			bus.Publish(f.a.opts.Bus, bus.ToolCallCompleted{SessionID: f.sessionID, MessageID: msgID, ToolUseID: call.ID, ToolName: call.Name, Err: content, At: f.a.opts.Now()})
			return fantasy.NewTextErrorResponse(content), nil
		}
		if len(out.ToolInput) > 0 {
			toolInput = out.ToolInput
		}
	}

	started := time.Now()
	res, err := f.t.Execute(ctx, toolInput, tool.ExecContext{SessionID: f.sessionID, Pwd: f.a.opts.Pwd, Bus: f.a.opts.Bus, Permission: f.a.opts.Permission, ToolUseID: call.ID, MessageID: msgID, ModelName: f.modelName, Now: f.a.opts.Now})
	if err != nil {
		res = tool.Result{IsError: true, Content: err.Error()}
	}
	if f.a.opts.Hooks != nil {
		hookIn := hook.Input{Event: hook.EventPostTool, SessionID: f.sessionID, HookName: "post_tool", Pwd: f.pwd, ToolName: call.Name, ToolInput: toolInput, ToolResult: &hook.ToolResult{IsError: res.IsError, Content: res.Content}}
		out, warns := f.a.opts.Hooks.RunPost(ctx, hook.EventPostTool, hookIn)
		logHookWarns(warns)
		if out.ToolResult != nil {
			res.IsError = out.ToolResult.IsError
			res.Content = out.ToolResult.Content
		}
	}
	errString := ""
	if res.IsError {
		errString = res.Content
	}
	bus.Publish(f.a.opts.Bus, bus.ToolCallCompleted{SessionID: f.sessionID, MessageID: msgID, ToolUseID: call.ID, ToolName: call.Name, Err: errString, DurationMs: time.Since(started).Milliseconds(), At: f.a.opts.Now()})
	if res.IsError {
		return fantasy.NewTextErrorResponse(res.Content), nil
	}
	return fantasy.NewTextResponse(res.Content), nil
}

func (a *Agent) runFantasyLoop(ctx context.Context, sessionID, modelName string) (*session.Message, error) {
	msgs, marker, err := a.opts.Store.MessagesSinceLatestMarker(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent: load messages: %w", err)
	}
	lazyBlocks := a.drainPendingLazy(sessionID)
	fmsgs := toFantasyMessages(msgs, marker, a.opts.SystemPrompt, lazyBlocks)
	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}

	var currentID string
	ftools := make([]fantasy.AgentTool, 0, len(a.opts.Tools.All()))
	var (
		mu              sync.Mutex
		final           *session.Message
		lastUsage       provider.Usage
		textBuf         strings.Builder
		thinkingBuf     strings.Builder
		streamToolCalls []toolCallEvent
		pendingUsage    provider.Usage
	)
	appendAssistant := func(u provider.Usage) error {
		mu.Lock()
		if currentID != "" {
			mu.Unlock()
			return nil
		}
		parts := buildAssistantParts(textBuf.String(), thinkingBuf.String(), streamToolCalls)
		mu.Unlock()
		msg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{Role: session.RoleAssistant, Parts: parts, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUSD: a.computeCost(ctx, modelName, u).USD})
		if err != nil {
			return fmt.Errorf("agent: append assistant: %w", err)
		}
		mu.Lock()
		currentID = msg.ID
		final = msg
		lastUsage = u
		mu.Unlock()
		bus.Publish(a.opts.Bus, bus.MessageAppended{SessionID: sessionID, MessageID: msg.ID, Role: string(session.RoleAssistant), At: a.opts.Now()})
		a.recordUsage(ctx, sessionID, modelName, u)
		return nil
	}
	for _, t := range a.opts.Tools.All() {
		ftools = append(ftools, &fantasyTool{t: t, a: a, sessionID: sessionID, messageIDPtr: &currentID, modelName: modelName, pwd: pwd, beforeRun: func() error { mu.Lock(); u := pendingUsage; mu.Unlock(); return appendAssistant(u) }})
	}
	ag := fantasy.NewAgent(a.opts.FantasyModel, fantasy.WithTools(ftools...), fantasy.WithStopConditions(fantasy.StepCountIs(a.opts.MaxIterations)))
	call := fantasy.AgentStreamCall{Messages: fmsgs,
		OnTextDelta: func(_ string, delta string) error {
			mu.Lock()
			textBuf.WriteString(delta)
			mu.Unlock()
			bus.Publish(a.opts.Bus, bus.AssistantTextDelta{SessionID: sessionID, Text: delta, At: a.opts.Now()})
			return nil
		},
		OnReasoningDelta: func(_ string, delta string) error {
			mu.Lock()
			thinkingBuf.WriteString(delta)
			mu.Unlock()
			bus.Publish(a.opts.Bus, bus.AssistantThinkingDelta{SessionID: sessionID, Text: delta, At: a.opts.Now()})
			return nil
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			mu.Lock()
			streamToolCalls = append(streamToolCalls, toolCallEvent{ID: tc.ToolCallID, Name: tc.ToolName, Input: []byte(tc.Input)})
			mu.Unlock()
			return nil
		},
		OnStreamFinish: func(usage fantasy.Usage, _ fantasy.FinishReason, _ fantasy.ProviderMetadata) error {
			mu.Lock()
			pendingUsage = usageFromFantasy(usage)
			mu.Unlock()
			return nil
		},
		OnStepFinish: func(step fantasy.StepResult) error {
			u := usageFromFantasy(step.Usage)
			if len(step.Content.ToolCalls()) == 0 {
				return appendAssistant(u)
			}
			mu.Lock()
			lastUsage = u
			currentID = ""
			textBuf.Reset()
			thinkingBuf.Reset()
			streamToolCalls = nil
			mu.Unlock()
			a.recordUsage(ctx, sessionID, modelName, u)
			return nil
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			content, isErr := fantasyToolResultText(result.Result)
			msg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{Role: session.RoleTool, Parts: []session.Part{{Kind: session.PartToolResult, ToolUseID: result.ToolCallID, Content: content, IsError: isErr}}})
			if err != nil {
				return fmt.Errorf("agent: append tool message: %w", err)
			}
			bus.Publish(a.opts.Bus, bus.MessageAppended{SessionID: sessionID, MessageID: msg.ID, Role: string(session.RoleTool), At: a.opts.Now()})
			return nil
		},
	}
	if a.opts.Reasoning.IsOn() {
		call.ProviderOptions = fantasy.ProviderOptions{}
	}
	result, err := ag.Stream(ctx, call)
	if err != nil {
		return final, fmt.Errorf("agent: fantasy stream: %w", err)
	}
	if result != nil && len(result.Steps) >= a.opts.MaxIterations {
		bus.Publish(a.opts.Bus, bus.IterationLimitReached{SessionID: sessionID, Limit: a.opts.MaxIterations, At: a.opts.Now()})
		return final, ErrIterationLimit
	}
	if final != nil && a.opts.Hooks != nil {
		var text string
		for _, p := range final.Parts {
			if p.Kind == session.PartText {
				text += p.Text
			}
		}
		_, warns := a.opts.Hooks.RunPost(ctx, hook.EventPostMessage, hook.Input{Event: hook.EventPostMessage, SessionID: sessionID, HookName: "post_message", Pwd: pwd, Message: text})
		logHookWarns(warns)
	}
	a.recordUsage(ctx, sessionID, modelName, lastUsage)
	return final, nil
}

func toFantasyMessages(msgs []*session.Message, marker *session.Marker, system string, lazy []agentsmd.Block) []fantasy.Message {
	out := []fantasy.Message{}
	if sys := composeSystemPrompt(system, marker, lazy); strings.TrimSpace(sys) != "" {
		out = append(out, fantasy.NewSystemMessage(sys))
	}
	for _, m := range msgs {
		if m == nil {
			continue
		}
		fm := fantasy.Message{Role: toFantasyRole(m.Role)}
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartText:
				fm.Content = append(fm.Content, fantasy.TextPart{Text: p.Text})
			case session.PartThinking:
				fm.Content = append(fm.Content, fantasy.ReasoningPart{Text: p.Text})
			case session.PartToolUse:
				fm.Content = append(fm.Content, fantasy.ToolCallPart{ToolCallID: p.ToolID, ToolName: p.ToolName, Input: string(p.ToolInput)})
			case session.PartToolResult:
				var output fantasy.ToolResultOutputContent = fantasy.ToolResultOutputContentText{Text: p.Content}
				if p.IsError {
					output = fantasy.ToolResultOutputContentError{Error: fmt.Errorf("%s", p.Content)}
				}
				fm.Content = append(fm.Content, fantasy.ToolResultPart{ToolCallID: p.ToolUseID, Output: output})
			}
		}
		out = append(out, fm)
	}
	return out
}

func toFantasyRole(role session.Role) fantasy.MessageRole {
	switch role {
	case session.RoleUser:
		return fantasy.MessageRoleUser
	case session.RoleAssistant:
		return fantasy.MessageRoleAssistant
	case session.RoleTool:
		return fantasy.MessageRoleTool
	default:
		return fantasy.MessageRoleUser
	}
}

func usageFromFantasy(u fantasy.Usage) provider.Usage {
	return provider.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheCreationTokens}
}

func fantasyToolResultText(out fantasy.ToolResultOutputContent) (string, bool) {
	switch v := out.(type) {
	case fantasy.ToolResultOutputContentText:
		return v.Text, false
	case fantasy.ToolResultOutputContentError:
		if v.Error == nil {
			return "", true
		}
		return v.Error.Error(), true
	default:
		b, err := json.Marshal(out)
		if err != nil {
			return fmt.Sprintf("%v", out), false
		}
		return string(b), false
	}
}
