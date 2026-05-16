package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
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
	t    tool.Tool
	opts fantasyToolOptions
	prov fantasy.ProviderOptions
}

type fantasyToolOptions struct {
	agent     *Agent
	sessionID string
	messageID func() string
	modelName string
	pwd       string
	beforeRun func() error
}

func (f *fantasyTool) Info() fantasy.ToolInfo {
	params, required := fantasyToolSchema(f.t.InputSchema())
	return fantasy.ToolInfo{Name: f.t.Name(), Description: f.t.Description(), Parameters: params, Required: required, Parallel: f.t.Parallelizable()}
}

func (f *fantasyTool) ProviderOptions() fantasy.ProviderOptions        { return f.prov }
func (f *fantasyTool) SetProviderOptions(opts fantasy.ProviderOptions) { f.prov = opts }

func fantasyToolSchema(schema map[string]any) (map[string]any, []string) {
	if schema == nil {
		return map[string]any{}, nil
	}
	required := requiredStrings(schema["required"])
	if props, ok := schema["properties"].(map[string]any); ok {
		return cloneSchemaMap(props), required
	}
	if _, isObjectSchema := schema["type"]; isObjectSchema {
		return map[string]any{}, required
	}
	return cloneSchemaMap(schema), required
}

func requiredStrings(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func cloneSchemaMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	maps.Copy(out, in)
	return out
}

func (f *fantasyTool) Run(ctx context.Context, call fantasy.ToolCall) (fantasy.ToolResponse, error) {
	if f.opts.beforeRun != nil {
		if err := f.opts.beforeRun(); err != nil {
			return fantasy.ToolResponse{}, err
		}
	}
	msgID := ""
	if f.opts.messageID != nil {
		msgID = f.opts.messageID()
	}
	args := json.RawMessage(call.Input)
	a := f.opts.agent
	bus.Publish(a.opts.Bus, bus.ToolCallRequested{SessionID: f.opts.sessionID, MessageID: msgID, ToolUseID: call.ID, ToolName: call.Name, Args: append([]byte(nil), args...), At: a.opts.Now()})

	toolInput := args
	if a.opts.Hooks != nil {
		hookIn := hook.Input{Event: hook.EventPreTool, SessionID: f.opts.sessionID, HookName: "pre_tool", Pwd: f.opts.pwd, ToolName: call.Name, ToolInput: toolInput}
		out, dec, denier, reason, warns := a.opts.Hooks.RunPre(ctx, hook.EventPreTool, hookIn)
		logHookWarns(warns)
		if dec == hook.DecisionDeny {
			content := fmt.Sprintf("hook %q denied tool call: %s", denier, reason)
			bus.Publish(a.opts.Bus, bus.ToolCallCompleted{SessionID: f.opts.sessionID, MessageID: msgID, ToolUseID: call.ID, ToolName: call.Name, Err: content, At: a.opts.Now()})
			return fantasy.NewTextErrorResponse(content), nil
		}
		if len(out.ToolInput) > 0 {
			toolInput = out.ToolInput
		}
	}

	started := time.Now()
	res, err := f.t.Execute(ctx, toolInput, tool.ExecContext{SessionID: f.opts.sessionID, Pwd: a.opts.Pwd, Bus: a.opts.Bus, Permission: a.opts.Permission, ToolUseID: call.ID, MessageID: msgID, ModelName: f.opts.modelName, Now: a.opts.Now})
	if err != nil {
		res = tool.Result{IsError: true, Content: err.Error()}
	}
	if a.opts.Hooks != nil {
		hookIn := hook.Input{Event: hook.EventPostTool, SessionID: f.opts.sessionID, HookName: "post_tool", Pwd: f.opts.pwd, ToolName: call.Name, ToolInput: toolInput, ToolResult: &hook.ToolResult{IsError: res.IsError, Content: res.Content}}
		out, warns := a.opts.Hooks.RunPost(ctx, hook.EventPostTool, hookIn)
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
	var resultBytes []byte
	if !res.IsError {
		resultBytes = []byte(res.Content)
	}
	bus.Publish(a.opts.Bus, bus.ToolCallCompleted{SessionID: f.opts.sessionID, MessageID: msgID, ToolUseID: call.ID, ToolName: call.Name, Result: resultBytes, Err: errString, DurationMs: time.Since(started).Milliseconds(), At: a.opts.Now()})
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
	fmsgs := toFantasyMessages(msgs, marker, a.systemPrompt(), lazyBlocks)
	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}

	var currentID string
	var (
		mu              sync.Mutex
		storeMu         sync.Mutex
		appendCond      = sync.NewCond(&mu)
		appendingAsst   bool
		final           *session.Message
		textBuf         strings.Builder
		thinkingBuf     strings.Builder
		streamToolCalls []toolCallEvent
		pendingUsage    provider.Usage
		activeToolCalls = map[string]toolCallEvent{}
	)
	upsertToolCall := func(tc toolCallEvent) {
		for i := range streamToolCalls {
			if streamToolCalls[i].ID == tc.ID {
				streamToolCalls[i] = tc
				return
			}
		}
		streamToolCalls = append(streamToolCalls, tc)
	}
	appendAssistant := func(u provider.Usage) error {
		mu.Lock()
		for appendingAsst && currentID == "" {
			appendCond.Wait()
		}
		if currentID != "" {
			mu.Unlock()
			return nil
		}
		appendingAsst = true
		parts := buildAssistantParts(textBuf.String(), thinkingBuf.String(), streamToolCalls)
		mu.Unlock()
		storeMu.Lock()
		msg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{Role: session.RoleAssistant, Parts: parts, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens, CacheWriteTokens: u.CacheWriteTokens, CostUSD: a.computeCost(ctx, modelName, u).USD})
		if err != nil {
			storeMu.Unlock()
			mu.Lock()
			appendingAsst = false
			appendCond.Broadcast()
			mu.Unlock()
			return fmt.Errorf("agent: append assistant: %w", err)
		}
		a.recordUsage(ctx, sessionID, modelName, u)
		storeMu.Unlock()
		mu.Lock()
		currentID = msg.ID
		final = msg
		appendingAsst = false
		appendCond.Broadcast()
		mu.Unlock()
		bus.Publish(a.opts.Bus, bus.MessageAppended{SessionID: sessionID, MessageID: msg.ID, Role: string(session.RoleAssistant), At: a.opts.Now()})
		return nil
	}
	ftools := a.runtime.buildFantasyTools(fantasyToolOptions{agent: a, sessionID: sessionID, messageID: func() string { mu.Lock(); defer mu.Unlock(); return currentID }, modelName: modelName, pwd: pwd, beforeRun: func() error { mu.Lock(); u := pendingUsage; mu.Unlock(); return appendAssistant(u) }})
	ag := a.runtime.newFantasyAgent(ftools)
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
		OnReasoningStart: func(_ string, reasoning fantasy.ReasoningContent) error {
			if reasoning.Text == "" {
				return nil
			}
			mu.Lock()
			thinkingBuf.WriteString(reasoning.Text)
			mu.Unlock()
			bus.Publish(a.opts.Bus, bus.AssistantThinkingDelta{SessionID: sessionID, Text: reasoning.Text, At: a.opts.Now()})
			return nil
		},
		OnToolInputStart: func(id string, toolName string) error {
			mu.Lock()
			activeToolCalls[id] = toolCallEvent{ID: id, Name: toolName}
			mu.Unlock()
			return nil
		},
		OnToolInputDelta: func(id string, delta string) error {
			mu.Lock()
			tc := activeToolCalls[id]
			tc.ID = id
			tc.Input = append(tc.Input, []byte(delta)...)
			activeToolCalls[id] = tc
			mu.Unlock()
			return nil
		},
		OnToolInputEnd: func(id string) error {
			mu.Lock()
			if tc, ok := activeToolCalls[id]; ok && tc.Name != "" {
				upsertToolCall(tc)
			}
			mu.Unlock()
			return nil
		},
		OnToolCall: func(tc fantasy.ToolCallContent) error {
			mu.Lock()
			event := toolCallEvent{ID: tc.ToolCallID, Name: tc.ToolName, Input: []byte(tc.Input)}
			activeToolCalls[tc.ToolCallID] = event
			upsertToolCall(event)
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
			// Tool-call steps are persisted by beforeRun -> appendAssistant(u),
			// which also records usage. Do not record the same step again here.
			mu.Lock()
			currentID = ""
			textBuf.Reset()
			thinkingBuf.Reset()
			streamToolCalls = nil
			activeToolCalls = map[string]toolCallEvent{}
			mu.Unlock()
			return nil
		},
		OnToolResult: func(result fantasy.ToolResultContent) error {
			content, isErr := fantasyToolResultText(result.Result)
			storeMu.Lock()
			msg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{Role: session.RoleTool, Parts: []session.Part{{Kind: session.PartToolResult, ToolUseID: result.ToolCallID, Content: content, IsError: isErr}}})
			storeMu.Unlock()
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
	_, err = ag.Stream(ctx, call)
	if err != nil {
		logFantasyStreamError(err, sessionID, modelName)
		mu.Lock()
		u := pendingUsage
		hasPartial := len(buildAssistantParts(textBuf.String(), thinkingBuf.String(), streamToolCalls)) > 0
		mu.Unlock()
		if hasPartial {
			if appendErr := appendAssistant(u); appendErr != nil {
				return final, appendErr
			}
			if appendErr := a.appendSyntheticToolResults(ctx, sessionID, final, orphanedToolCallMessage); appendErr != nil {
				return final, appendErr
			}
		}
		return final, wrapFantasyStreamError(err)
	}
	if final != nil && a.opts.Hooks != nil {
		var text strings.Builder
		for _, p := range final.Parts {
			if p.Kind == session.PartText {
				text.WriteString(p.Text)
			}
		}
		_, warns := a.opts.Hooks.RunPost(ctx, hook.EventPostMessage, hook.Input{Event: hook.EventPostMessage, SessionID: sessionID, HookName: "post_message", Pwd: pwd, Message: text.String()})
		logHookWarns(warns)
	}
	return final, nil
}

func toFantasyMessages(msgs []*session.Message, marker *session.Marker, system string, lazy []agentsmd.Block) []fantasy.Message {
	out := []fantasy.Message{}
	if sys := composeSystemPrompt(system, marker, lazy); strings.TrimSpace(sys) != "" {
		out = append(out, fantasy.NewSystemMessage(sys))
	}
	knownToolCallIDs := make(map[string]struct{})
	knownToolResultIDs := make(map[string]struct{})
	for _, m := range msgs {
		if m == nil {
			continue
		}
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartToolUse:
				if p.ToolID != "" {
					knownToolCallIDs[p.ToolID] = struct{}{}
				}
			case session.PartToolResult:
				if p.ToolUseID != "" {
					knownToolResultIDs[p.ToolUseID] = struct{}{}
				}
			}
		}
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
				if _, known := knownToolCallIDs[p.ToolUseID]; !known {
					slog.Warn("agent: dropping orphaned tool result with no matching tool call", "tool_call_id", p.ToolUseID)
					continue
				}
				var output fantasy.ToolResultOutputContent = fantasy.ToolResultOutputContentText{Text: p.Content}
				if p.IsError {
					output = fantasy.ToolResultOutputContentError{Error: fmt.Errorf("%s", p.Content)}
				}
				fm.Content = append(fm.Content, fantasy.ToolResultPart{ToolCallID: p.ToolUseID, Output: output})
			}
		}
		out = append(out, fm)
		if m.Role == session.RoleAssistant {
			if synthetic, ok := syntheticToolResultsForOrphanedCalls(m, knownToolResultIDs); ok {
				out = append(out, synthetic)
			}
		}
	}
	return out
}

const orphanedToolCallMessage = "tool call was interrupted and did not produce a result, you may retry this call if the result is still needed"

func syntheticToolResultsForOrphanedCalls(m *session.Message, knownToolResultIDs map[string]struct{}) (fantasy.Message, bool) {
	if m == nil {
		return fantasy.Message{}, false
	}
	var syntheticParts []fantasy.MessagePart
	for _, p := range m.Parts {
		if p.Kind != session.PartToolUse || p.ToolID == "" {
			continue
		}
		if _, hasResult := knownToolResultIDs[p.ToolID]; hasResult {
			continue
		}
		slog.Warn("agent: injecting synthetic tool result for orphaned tool call", "tool_call_id", p.ToolID, "tool_name", p.ToolName)
		syntheticParts = append(syntheticParts, fantasy.ToolResultPart{
			ToolCallID: p.ToolID,
			Output: fantasy.ToolResultOutputContentError{
				Error: errors.New(orphanedToolCallMessage),
			},
		})
	}
	if len(syntheticParts) == 0 {
		return fantasy.Message{}, false
	}
	return fantasy.Message{Role: fantasy.MessageRoleTool, Content: syntheticParts}, true
}

func (a *Agent) appendSyntheticToolResults(ctx context.Context, sessionID string, asstMsg *session.Message, content string) error {
	if asstMsg == nil {
		return nil
	}
	for _, p := range asstMsg.Parts {
		if p.Kind != session.PartToolUse || p.ToolID == "" {
			continue
		}
		msg, err := a.opts.Store.AppendMessage(ctx, sessionID, session.NewMessage{Role: session.RoleTool, Parts: []session.Part{{Kind: session.PartToolResult, ToolUseID: p.ToolID, Content: content, IsError: true}}})
		if err != nil {
			return fmt.Errorf("agent: append synthetic tool result: %w", err)
		}
		bus.Publish(a.opts.Bus, bus.MessageAppended{SessionID: sessionID, MessageID: msg.ID, Role: string(session.RoleTool), At: a.opts.Now()})
	}
	return nil
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

type detailedFantasyError struct {
	message string
	cause   error
}

func (e detailedFantasyError) Error() string { return e.message }
func (e detailedFantasyError) Unwrap() error { return e.cause }

func wrapFantasyStreamError(err error) error {
	if err == nil {
		return nil
	}
	if detail := fantasyErrorDetail(err); detail != "" {
		return detailedFantasyError{message: "agent: fantasy stream: " + detail, cause: err}
	}
	return fmt.Errorf("agent: fantasy stream: %w", err)
}

func logFantasyStreamError(err error, sessionID, modelName string) {
	if err == nil {
		return
	}
	fields := []any{"session", sessionID, "model", modelName, "err", err}
	var providerErr *fantasy.ProviderError
	if errors.As(err, &providerErr) {
		fields = append(fields,
			"status", providerErr.StatusCode,
			"title", providerErr.Title,
			"message", providerErr.Message,
			"url", providerErr.URL,
		)
		if len(providerErr.ResponseBody) > 0 {
			fields = append(fields, "response_body", string(providerErr.ResponseBody))
		}
	}
	slog.Error("agent: fantasy stream failed", fields...)
}

func fantasyErrorDetail(err error) string {
	var providerErr *fantasy.ProviderError
	if !errors.As(err, &providerErr) {
		return ""
	}
	msg := strings.TrimSpace(providerErr.Message)
	if nested := nestedProviderErrorMessage(providerErr.ResponseBody); nested != "" && nested != msg {
		if provider := nestedProviderName(providerErr.ResponseBody); provider != "" {
			return fmt.Sprintf("provider returned %d from %s: %s", providerErr.StatusCode, provider, nested)
		}
		return fmt.Sprintf("provider returned %d: %s", providerErr.StatusCode, nested)
	}
	if msg == "" {
		msg = strings.TrimSpace(providerErr.Title)
	}
	if msg == "" {
		return ""
	}
	if providerErr.StatusCode != 0 {
		return fmt.Sprintf("provider returned %d: %s", providerErr.StatusCode, msg)
	}
	return msg
}

func nestedProviderName(body []byte) string {
	var payload struct {
		Error struct {
			Metadata struct {
				ProviderName string `json:"provider_name"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Error.Metadata.ProviderName)
}

func nestedProviderErrorMessage(body []byte) string {
	var payload struct {
		Error struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return strings.TrimSpace(string(body))
	}
	if raw := strings.TrimSpace(payload.Error.Metadata.Raw); raw != "" {
		var nested struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(raw), &nested); err == nil {
			if msg := strings.TrimSpace(nested.Error.Message); msg != "" {
				return msg
			}
		}
		return raw
	}
	return strings.TrimSpace(payload.Error.Message)
}
