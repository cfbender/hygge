package agent

import (
	"context"
	"encoding/base64"
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
		return map[string]any{}, []string{}
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
		return append([]string{}, v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return []string{}
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
	a.collectLazyContext(f.opts.sessionID, f.opts.pwd, &session.Message{Parts: []session.Part{{Kind: session.PartToolUse, ToolID: call.ID, ToolName: call.Name, ToolInput: args}}})
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
	res, err := func() (res tool.Result, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("tool panic: %v", r)
			}
		}()
		return f.t.Execute(ctx, toolInput, tool.ExecContext{SessionID: f.opts.sessionID, Pwd: a.opts.Pwd, Bus: a.opts.Bus, Permission: a.opts.Permission, ToolUseID: call.ID, MessageID: msgID, ModelName: f.opts.modelName, Now: a.opts.Now})
	}()
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
	// Apply per-turn context decoration (e.g. injecting session ID for
	// HTTP-transport-level header middleware).
	if a.opts.TurnContextDecorator != nil {
		ctx = a.opts.TurnContextDecorator(ctx, sessionID)
	}

	// Freeze the active provider id for the whole turn.  Callbacks
	// (OnStreamFinish, OnStepFinish) consult usageFromFantasy
	// repeatedly during a stream, and Agent.SetModel can swap the
	// provider between turns; reading a.providerName() inside the
	// callbacks would race the swap and could even mix providers
	// within a single turn's accounting.
	providerID := a.providerName()

	msgs, marker, err := a.opts.Store.MessagesSinceLatestMarker(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent: load messages: %w", err)
	}
	var memories []*session.Memory
	if a.opts.MemoryLoader != nil {
		loaded, err := a.opts.MemoryLoader.ListMemories(ctx)
		if err != nil {
			return nil, fmt.Errorf("agent: load file-backed memories: %w", err)
		}
		memories = append(memories, loaded...)
	}
	sessionMemories, err := a.opts.Store.ListSessionMemories(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("agent: load session memories: %w", err)
	}
	memories = append(memories, sessionMemories...)
	usedTokens, pctUsed := a.latestUsageFor(sessionID)
	fmsgs := toFantasyMessages(msgs, marker, a.systemPrompt(), nil, nil, memories, a.opts.ContextWindow, usedTokens, pctUsed)
	pwd := a.opts.Pwd
	if pwd == "" {
		if wd, err := os.Getwd(); err == nil {
			pwd = wd
		}
	}

	var currentID string
	var (
		mu                     sync.Mutex
		storeMu                sync.Mutex
		appendCond             = sync.NewCond(&mu)
		appendingAsst          bool
		final                  *session.Message
		textBuf                strings.Builder
		thinkingBuf            strings.Builder
		streamToolCalls        []toolCallEvent
		pendingUsage           provider.Usage
		activeToolCalls        = map[string]toolCallEvent{}
		persistedTextLen       int
		persistedThinkingLen   int
		persistedToolCallCount int
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
			textLen := textBuf.Len()
			thinkingLen := thinkingBuf.Len()
			toolCallCount := len(streamToolCalls)
			existing := currentID
			grewText := textLen - persistedTextLen
			grewThinking := thinkingLen - persistedThinkingLen
			grewToolCalls := toolCallCount - persistedToolCallCount
			mu.Unlock()
			// Suppressed re-entry: a prior appendAssistant in this step
			// already persisted. If text/thinking/tool_calls grew since
			// that persist, it is currently dropped — log so the next
			// phantom-bubble reproduction shows the silent loss.
			if grewText > 0 || grewThinking > 0 || grewToolCalls > 0 {
				slog.Debug("agent: appendAssistant dropped buffered content (currentID guard)",
					"session", sessionID,
					"existing_message_id", existing,
					"new_text_bytes", grewText,
					"new_thinking_bytes", grewThinking,
					"new_tool_calls", grewToolCalls,
				)
			}
			return nil
		}
		appendingAsst = true
		parts := buildAssistantParts(textBuf.String(), thinkingBuf.String(), streamToolCalls)
		// Snapshot what we're about to persist so subsequent re-entry can
		// detect content that grew since this persist (and is therefore at
		// risk of being silently dropped).
		snapTextLen := textBuf.Len()
		snapThinkingLen := thinkingBuf.Len()
		snapToolCallCount := len(streamToolCalls)
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
		persistedTextLen = snapTextLen
		persistedThinkingLen = snapThinkingLen
		persistedToolCallCount = snapToolCallCount
		appendingAsst = false
		appendCond.Broadcast()
		mu.Unlock()
		bus.Publish(a.opts.Bus, bus.MessageAppended{SessionID: sessionID, MessageID: msg.ID, Role: string(session.RoleAssistant), At: a.opts.Now()})
		return nil
	}
	ftools := a.runtime.buildFantasyTools(fantasyToolOptions{agent: a, sessionID: sessionID, messageID: func() string { mu.Lock(); defer mu.Unlock(); return currentID }, modelName: modelName, pwd: pwd, beforeRun: func() error {
		mu.Lock()
		u := pendingUsage
		mu.Unlock()
		if err := appendAssistant(u); err != nil {
			return err
		}
		mu.Lock()
		toolMsg := final
		mu.Unlock()
		a.collectLazyContext(sessionID, pwd, toolMsg)
		return nil
	}})
	ag := fantasy.NewAgent(a.runtime.model, fantasy.WithTools(ftools...))
	call := fantasy.AgentStreamCall{Messages: fmsgs,
		PrepareStep: func(ctx context.Context, opts fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
			return a.prepareFantasyStep(ctx, sessionID, opts)
		},
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
			a.collectLazyContext(sessionID, pwd, &session.Message{Parts: []session.Part{{Kind: session.PartToolUse, ToolID: tc.ToolCallID, ToolName: tc.ToolName, ToolInput: []byte(tc.Input)}}})
			mu.Lock()
			event := toolCallEvent{ID: tc.ToolCallID, Name: tc.ToolName, Input: []byte(tc.Input)}
			activeToolCalls[tc.ToolCallID] = event
			upsertToolCall(event)
			mu.Unlock()
			return nil
		},
		OnStreamFinish: func(usage fantasy.Usage, _ fantasy.FinishReason, _ fantasy.ProviderMetadata) error {
			mu.Lock()
			pendingUsage = usageFromFantasy(providerID, usage)
			mu.Unlock()
			return nil
		},
		OnStepFinish: func(step fantasy.StepResult) error {
			u := usageFromFantasy(providerID, step.Usage)
			if len(step.Content.ToolCalls()) == 0 {
				return appendAssistant(u)
			}
			mu.Lock()
			toolMsg := final
			mu.Unlock()
			a.collectLazyContext(sessionID, pwd, toolMsg)
			// Tool-call steps are persisted by beforeRun -> appendAssistant(u),
			// which also records usage. Do not record the same step again here.
			mu.Lock()
			currentID = ""
			textBuf.Reset()
			thinkingBuf.Reset()
			streamToolCalls = nil
			activeToolCalls = map[string]toolCallEvent{}
			persistedTextLen = 0
			persistedThinkingLen = 0
			persistedToolCallCount = 0
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

func (a *Agent) prepareFantasyStep(ctx context.Context, sessionID string, opts fantasy.PrepareStepFunctionOptions) (context.Context, fantasy.PrepareStepResult, error) {
	lazy := a.drainPendingLazy(sessionID)
	additions := a.drainPendingSystemAdditions(sessionID)
	steering := a.drainPendingSteering(sessionID)
	if len(lazy) == 0 && len(additions) == 0 && len(steering) == 0 {
		return ctx, fantasy.PrepareStepResult{}, nil
	}

	messages := append([]fantasy.Message(nil), opts.Messages...)
	if extra := composeSystemPrompt("", nil, lazy, additions); strings.TrimSpace(extra) != "" {
		messages = appendFantasySystemText(messages, extra)
	}
	if steeringText := steeringUserText(steering); strings.TrimSpace(steeringText) != "" {
		messages = append(messages, fantasy.NewUserMessage(steeringText))
	}
	return ctx, fantasy.PrepareStepResult{Messages: messages}, nil
}

func appendFantasySystemText(messages []fantasy.Message, text string) []fantasy.Message {
	out := append([]fantasy.Message(nil), messages...)
	for i := range out {
		if out[i].Role == fantasy.MessageRoleSystem {
			out[i].Content = append(out[i].Content, fantasy.TextPart{Text: "\n\n" + text})
			return out
		}
	}
	return append([]fantasy.Message{fantasy.NewSystemMessage(text)}, out...)
}

func toFantasyMessages(
	msgs []*session.Message,
	marker *session.Marker,
	system string,
	lazy []agentsmd.Block,
	systemPromptAdditions []string,
	memories []*session.Memory,
	contextWindow int64,
	usedTokens int64,
	pctUsed float64,
) []fantasy.Message {
	out := []fantasy.Message{}
	if sys := composeSystemPrompt(system, marker, lazy, systemPromptAdditions); strings.TrimSpace(sys) != "" {
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
	latestUserIdx := -1
	for i, m := range msgs {
		if m != nil && m.Role == session.RoleUser {
			latestUserIdx = i
		}
	}
	for i, m := range msgs {
		if m == nil {
			continue
		}
		fm := fantasy.Message{Role: toFantasyRole(m.Role)}
		if i == latestUserIdx && m.Role == session.RoleUser {
			fm.Content = append(fm.Content, latestUserFantasyParts(m, memories, contextWindow, usedTokens, pctUsed)...)
			out = append(out, fm)
			continue
		}
		for _, p := range m.Parts {
			switch p.Kind {
			case session.PartText:
				text := p.Text
				if m.Role == session.RoleUser {
					text = stripHistoricalTurnContext(text)
				}
				fm.Content = append(fm.Content, fantasy.TextPart{Text: text})
			case session.PartThinking:
				// Thinking is persisted for UI hydration and auditability, but it is
				// model-internal scratchpad from a previous step. Replaying it as
				// historical assistant content can amplify malformed partial reasoning
				// and degrade later turns, especially across provider boundaries.
				continue
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
		if len(fm.Content) == 0 {
			continue
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

func latestUserFantasyParts(m *session.Message, memories []*session.Memory, contextWindow int64, usedTokens int64, pctUsed float64) []fantasy.MessagePart {
	parts := []fantasy.MessagePart{fantasy.TextPart{Text: buildLatestUserEnvelope(modelFacingUserText(m), memories, contextWindow, usedTokens, pctUsed)}}
	if m == nil {
		return parts
	}
	for _, p := range m.Parts {
		if p.Kind != session.PartImage {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(p.ImageBase64)
		if err != nil {
			slog.Warn("agent: dropping invalid image attachment", "err", err)
			continue
		}
		parts = append(parts, fantasy.FilePart{Data: data, MediaType: p.ImageMimeType})
	}
	return parts
}

func modelFacingUserText(m *session.Message) string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	for _, p := range m.Parts {
		if p.Kind != session.PartText {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(stripHistoricalTurnContext(p.Text))
	}
	return b.String()
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

// usageFromFantasy converts fantasy.Usage to provider.Usage, normalizing
// per-provider quirks in how cached prompt tokens are reported.
//
// Hygge's downstream cost and context-window math (see internal/cost and
// internal/agent.recordUsage) follow the Anthropic-native convention:
// InputTokens are NEW prompt tokens not served from cache; CacheReadTokens
// are reported separately and ADDITIVE to the prompt size.
//
// OpenAI's chat-completions API (and OpenRouter, which proxies it) reports
// prompt_tokens INCLUSIVE of cached tokens, with the cached portion broken
// out under prompt_tokens_details.cached_tokens. Fantasy's OpenAI provider
// subtracts the cached portion before exposing the value as InputTokens, but
// its OpenRouter provider does NOT — see
// charm.land/fantasy/providers/openrouter/language_model_hooks.go. Without
// the subtraction here, OpenRouter cached tokens would be billed twice (once
// at the full input rate, once at the cache-read rate) and counted twice in
// the displayed context window percentage.
//
// providerID is the hygge provider id (lower-case, e.g. "openrouter"). When
// it identifies a provider known to report inclusive prompt tokens, we
// subtract CacheReadTokens from InputTokens, clamped to zero. Unknown or
// empty providerIDs leave the value untouched so the conservative default
// matches existing Anthropic behaviour.
func usageFromFantasy(providerID string, u fantasy.Usage) provider.Usage {
	input := u.InputTokens
	if inputIncludesCachedTokens(providerID) && u.CacheReadTokens > 0 {
		input = max(u.InputTokens-u.CacheReadTokens, 0)
	}
	return provider.Usage{
		InputTokens:      input,
		OutputTokens:     u.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens,
		CacheWriteTokens: u.CacheCreationTokens,
	}
}

// inputIncludesCachedTokens reports whether a provider's reported input/prompt
// token count already includes cached prompt tokens. Returning true causes
// usageFromFantasy to subtract CacheReadTokens from InputTokens before billing
// and context-window math.
//
// Today only OpenRouter is in this set: Fantasy's OpenAI provider already
// subtracts before exporting fantasy.Usage, and Anthropic-native reports
// InputTokens exclusive of cached tokens by API convention.
func inputIncludesCachedTokens(providerID string) bool {
	switch providerID {
	case "openrouter":
		return true
	default:
		return false
	}
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
