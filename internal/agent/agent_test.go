package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/cost"
	"github.com/cfbender/hygge/internal/hook"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/state"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/tool"
)

func TestRuntimeBuildsFantasyToolsFromRegistry(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(&countingTool{name: "fake_plugin", counter: &atomic.Int32{}}); err != nil {
		t.Fatalf("register fake plugin-like tool: %v", err)
	}
	rt := NewRuntime(RuntimeOptions{Tools: reg})
	ftools := rt.buildFantasyTools(fantasyToolOptions{})
	if len(ftools) != 1 {
		t.Fatalf("fantasy tools len = %d, want 1", len(ftools))
	}
	info := ftools[0].Info()
	if info.Name != "fake_plugin" {
		t.Fatalf("fantasy tool name = %q, want fake_plugin", info.Name)
	}
	if info.Parameters == nil {
		t.Fatalf("fantasy tool parameters nil")
	}
}

func TestFantasyToolSchemaRequiredIsNeverNil(t *testing.T) {
	cases := []struct {
		name string
		raw  any
	}{
		{name: "nil", raw: nil},
		{name: "typed_nil_string_slice", raw: []string(nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params, required := fantasyToolSchema(map[string]any{
				"type":       "object",
				"properties": map[string]any{},
				"required":   tc.raw,
			})
			if params == nil {
				t.Fatal("params nil")
			}
			if required == nil {
				t.Fatal("required nil; provider schemas must serialize an array, not null")
			}
			if len(required) != 0 {
				t.Fatalf("required = %+v, want empty", required)
			}
		})
	}
}

func TestRuntimeConvertsFullJSONSchemaForFantasyTools(t *testing.T) {
	reg := tool.Default()
	rt := NewRuntime(RuntimeOptions{Tools: reg})
	var bashInfo fantasy.ToolInfo
	for _, ft := range rt.buildFantasyTools(fantasyToolOptions{}) {
		if ft.Info().Name == "bash" {
			bashInfo = ft.Info()
			break
		}
	}
	if bashInfo.Name == "" {
		t.Fatal("bash fantasy tool not found")
	}
	if _, hasNestedProperties := bashInfo.Parameters["properties"]; hasNestedProperties {
		t.Fatalf("fantasy parameters should be the properties map, got nested schema: %+v", bashInfo.Parameters)
	}
	desc, ok := bashInfo.Parameters["description"].(map[string]any)
	if !ok {
		t.Fatalf("description property = %T, want map[string]any", bashInfo.Parameters["description"])
	}
	if desc["type"] != "string" {
		t.Fatalf("description property type = %v, want string", desc["type"])
	}
	if !slices.Contains(bashInfo.Required, "command") {
		t.Fatalf("bash required = %+v, want command", bashInfo.Required)
	}
}

func TestSessionAgentRejectsMissingFantasyModel(t *testing.T) {
	var model fantasy.LanguageModel
	rt := NewRuntime(RuntimeOptions{Model: model, Tools: tool.NewRegistry()})
	if rt.hasFantasyModel() {
		t.Fatalf("runtime reported fantasy model with nil model")
	}
}

// ---------- test harness ----------------------------------------------------

// fakeProvider is a scripted provider.Provider.  Each call to Stream pops
// the next script off the front of the queue and emits its events on a
// channel.  Tests build a slice of scripts (one per expected provider
// turn) and assemble them with newFakeProvider.
type fakeProvider struct {
	name    string
	mu      sync.Mutex
	scripts []fakeScript
	calls   atomic.Int32

	// onStream, if non-nil, is invoked with the request before any events
	// are emitted.  Tests use it to assert on the system prompt or the
	// message history.
	onStream func(req provider.Request)
}

type fakeFantasyModel struct {
	provider      string
	model         string
	text          string
	generateTexts []string
	usage         fantasy.Usage
	stream        []fantasy.StreamPart
	streamBatches [][]fantasy.StreamPart
	streamErr     error
	generateErr   error
	onGenerate    func(fantasy.Call)
	onStream      func(fantasy.Call)
	calls         atomic.Int32
	mu            sync.Mutex
	streamCalls   []fantasy.Call
}

type providerFantasyModel struct {
	provider provider.Provider
	model    string
}

func (m *providerFantasyModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, fmt.Errorf("providerFantasyModel: Generate not implemented")
}

func (m *providerFantasyModel) Stream(ctx context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	req := provider.Request{ModelName: m.model, Tools: make([]provider.Tool, 0, len(call.Tools))}
	for _, msg := range call.Prompt {
		text := fantasyMessageText(msg)
		switch msg.Role {
		case fantasy.MessageRoleSystem:
			if req.System == "" {
				req.System = text
			} else if text != "" {
				req.System += "\n\n" + text
			}
		case fantasy.MessageRoleUser:
			req.Messages = append(req.Messages, session.Message{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: text}}})
		case fantasy.MessageRoleAssistant:
			req.Messages = append(req.Messages, session.Message{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: text}}})
		case fantasy.MessageRoleTool:
			req.Messages = append(req.Messages, session.Message{Role: session.RoleTool, Parts: []session.Part{{Kind: session.PartToolResult, Content: text}}})
		}
	}
	ch, err := m.provider.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	var parts []fantasy.StreamPart
	sawTool := false
	var lastUsage provider.Usage
	for ev := range ch {
		switch ev.Type {
		case provider.EventTextDelta:
			parts = append(parts, fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: ev.Text})
		case provider.EventThinkingDelta:
			parts = append(parts, fantasy.StreamPart{Type: fantasy.StreamPartTypeReasoningDelta, ID: "reasoning", Delta: ev.Text})
		case provider.EventToolUse:
			sawTool = true
			parts = append(parts, fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: ev.ToolID, ToolCallName: ev.ToolName, ToolCallInput: string(ev.ToolInput)})
		case provider.EventUsage, provider.EventMessageStart:
			if ev.Usage.InputTokens != 0 || ev.Usage.OutputTokens != 0 {
				lastUsage = ev.Usage
			}
		case provider.EventError:
			parts = append(parts, fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: ev.Err})
		case provider.EventDone:
			finish := fantasy.FinishReasonStop
			if sawTool {
				finish = fantasy.FinishReasonToolCalls
			}
			parts = append(parts, fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, FinishReason: finish, Usage: fantasy.Usage{InputTokens: lastUsage.InputTokens, OutputTokens: lastUsage.OutputTokens}})
		}
	}
	if err := ctx.Err(); err != nil {
		parts = append(parts, fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: err})
	}
	return func(yield func(fantasy.StreamPart) bool) {
		for _, part := range parts {
			if !yield(part) {
				return
			}
		}
	}, nil
}

func fantasyMessageText(msg fantasy.Message) string {
	var b strings.Builder
	for _, part := range msg.Content {
		if text, ok := fantasy.AsMessagePart[fantasy.TextPart](part); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func (m *providerFantasyModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("providerFantasyModel: GenerateObject not implemented")
}

func (m *providerFantasyModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("providerFantasyModel: StreamObject not implemented")
}

func (m *providerFantasyModel) Provider() string { return m.provider.Name() }
func (m *providerFantasyModel) Model() string    { return m.model }

func (f *fakeFantasyModel) Generate(_ context.Context, call fantasy.Call) (*fantasy.Response, error) {
	f.calls.Add(1)
	if f.onGenerate != nil {
		f.onGenerate(call)
	}
	if f.generateErr != nil {
		return nil, f.generateErr
	}
	f.mu.Lock()
	text := f.text
	if len(f.generateTexts) > 0 {
		text = f.generateTexts[0]
		f.generateTexts = f.generateTexts[1:]
	}
	f.mu.Unlock()
	return &fantasy.Response{Content: fantasy.ResponseContent{fantasy.TextContent{Text: text}}, Usage: f.usage}, nil
}

func (f *fakeFantasyModel) Stream(_ context.Context, call fantasy.Call) (fantasy.StreamResponse, error) {
	f.calls.Add(1)
	f.mu.Lock()
	f.streamCalls = append(f.streamCalls, call)
	f.mu.Unlock()
	if f.onStream != nil {
		f.onStream(call)
	}
	f.mu.Lock()
	stream := f.stream
	if len(f.streamBatches) > 0 {
		stream = f.streamBatches[0]
		f.streamBatches = f.streamBatches[1:]
	}
	// When the test only configured `text`/`generateTexts` (the Generate path),
	// synthesize a minimal stream so the same fake serves Stream callers.
	if len(stream) == 0 {
		text := f.text
		if len(f.generateTexts) > 0 {
			text = f.generateTexts[0]
			f.generateTexts = f.generateTexts[1:]
		}
		if text != "" {
			stream = []fantasy.StreamPart{
				{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
				{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: text},
				{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
				{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: f.usage},
			}
		}
	}
	f.mu.Unlock()
	return func(yield func(fantasy.StreamPart) bool) {
		for _, part := range stream {
			if !yield(part) {
				return
			}
		}
		if f.streamErr != nil {
			yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: f.streamErr})
		}
	}, nil
}

func (f *fakeFantasyModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("fakeFantasyModel: GenerateObject not implemented")
}

func (f *fakeFantasyModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("fakeFantasyModel: StreamObject not implemented")
}

func (f *fakeFantasyModel) Provider() string { return f.provider }
func (f *fakeFantasyModel) Model() string    { return f.model }

func (f *fakeFantasyModel) steeringPromptRole(callIndex int) fantasy.MessageRole {
	f.mu.Lock()
	defer f.mu.Unlock()
	if callIndex < 0 || callIndex >= len(f.streamCalls) {
		return ""
	}
	for _, msg := range f.streamCalls[callIndex].Prompt {
		if strings.Contains(fantasyMessageText(msg), "prefer the direct fix") {
			return msg.Role
		}
	}
	return ""
}

func TestAgentGenerateTitleUsesTitleFantasyModel(t *testing.T) {
	env := newTestEnv(t)
	var gotPrompt string
	titleModel := &fakeFantasyModel{provider: "test", model: "small", text: "Fix auth flow"}
	titleModel.onStream = func(call fantasy.Call) {
		msgs := call.Prompt
		if len(msgs) >= 2 && len(msgs[1].Content) > 0 {
			if text, ok := msgs[1].Content[0].(fantasy.TextPart); ok {
				gotPrompt = text.Text
			}
		}
	}
	ag := env.newAgent(newFakeProvider("test"), func(opts *Options) {
		opts.FantasyModel = &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
		opts.TitleFantasyModel = titleModel
	})

	title, err := ag.GenerateTitle(t.Context(), "please fix the login redirect")
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if title != "Fix auth flow" {
		t.Fatalf("title = %q", title)
	}
	if titleModel.calls.Load() != 1 {
		t.Fatalf("title model calls = %d, want 1", titleModel.calls.Load())
	}
	if !strings.Contains(gotPrompt, "Commands overview") || !strings.Contains(gotPrompt, "Current title: (none)") {
		t.Fatalf("title prompt missing formatting instructions/current title:\n%s", gotPrompt)
	}
}

func TestAgentRefreshSessionTitlePersistsFormattedSlug(t *testing.T) {
	env := newTestEnv(t)
	titleModel := &fakeFantasyModel{provider: "test", model: "small", text: "Commands overview"}
	ag := env.newAgent(newFakeProvider("test"), func(opts *Options) {
		opts.FantasyModel = &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
		opts.TitleFantasyModel = titleModel
	})
	if _, err := env.Store.AppendMessage(t.Context(), env.sessionID, session.NewMessage{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "generate a high level overview of / commands in this project"}}}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	title, changed, err := ag.RefreshSessionTitle(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("RefreshSessionTitle: %v", err)
	}
	if !changed || title != "Commands overview" {
		t.Fatalf("title=%q changed=%v, want Commands overview/true", title, changed)
	}
	sess, err := env.Store.GetSession(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Slug != "Commands overview" {
		t.Fatalf("Slug = %q, want Commands overview", sess.Slug)
	}
}

func TestAgentRefreshSessionTitleRepromptsWhenModelEchoesPrompt(t *testing.T) {
	env := newTestEnv(t)
	prompt := "generate a high level overview of / commands here"
	titleModel := &fakeFantasyModel{provider: "test", model: "small", generateTexts: []string{prompt, "Commands overview"}}
	ag := env.newAgent(newFakeProvider("test"), func(opts *Options) {
		opts.FantasyModel = &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
		opts.TitleFantasyModel = titleModel
	})
	if _, err := env.Store.AppendMessage(t.Context(), env.sessionID, session.NewMessage{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: prompt}}}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	title, changed, err := ag.RefreshSessionTitle(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("RefreshSessionTitle: %v", err)
	}
	if !changed || title != "Commands overview" {
		t.Fatalf("title=%q changed=%v, want Commands overview/true", title, changed)
	}
	sess, err := env.Store.GetSession(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Slug != "Commands overview" {
		t.Fatalf("Slug = %q, want Commands overview", sess.Slug)
	}
	if titleModel.calls.Load() != 2 {
		t.Fatalf("title model calls = %d, want initial + repair", titleModel.calls.Load())
	}
}

func TestAgentRefreshSessionTitleRepromptsWhenVerbatimSlugGetsKeep(t *testing.T) {
	env := newTestEnv(t)
	prompt := "generate a high level overview of / commands here"
	titleModel := &fakeFantasyModel{provider: "test", model: "small", generateTexts: []string{"KEEP", "Commands overview"}}
	ag := env.newAgent(newFakeProvider("test"), func(opts *Options) {
		opts.FantasyModel = &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
		opts.TitleFantasyModel = titleModel
	})
	if err := env.Store.RenameSession(t.Context(), env.sessionID, prompt); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if _, err := env.Store.AppendMessage(t.Context(), env.sessionID, session.NewMessage{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: prompt}}}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	title, changed, err := ag.RefreshSessionTitle(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("RefreshSessionTitle: %v", err)
	}
	if !changed || title != "Commands overview" {
		t.Fatalf("title=%q changed=%v, want Commands overview/true", title, changed)
	}
}

func TestAgentRefreshSessionTitleKeepsCurrentSlug(t *testing.T) {
	env := newTestEnv(t)
	titleModel := &fakeFantasyModel{provider: "test", model: "small", text: "KEEP"}
	ag := env.newAgent(newFakeProvider("test"), func(opts *Options) {
		opts.FantasyModel = &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
		opts.TitleFantasyModel = titleModel
	})
	if err := env.Store.RenameSession(t.Context(), env.sessionID, "Commands overview"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	if _, err := env.Store.AppendMessage(t.Context(), env.sessionID, session.NewMessage{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "explain the command registry"}}}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	title, changed, err := ag.RefreshSessionTitle(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("RefreshSessionTitle: %v", err)
	}
	if changed || title != "Commands overview" {
		t.Fatalf("title=%q changed=%v, want existing title/false", title, changed)
	}
}

func TestAgentRefreshSessionTitlePublishesEvent(t *testing.T) {
	env := newTestEnv(t)
	events, drain := collectEvents[bus.SessionTitleUpdated](t, env.Bus, 4)
	_ = events
	titleModel := &fakeFantasyModel{provider: "test", model: "small", text: "Commands overview"}
	ag := env.newAgent(newFakeProvider("test"), func(opts *Options) {
		opts.FantasyModel = &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
		opts.TitleFantasyModel = titleModel
	})
	if _, err := env.Store.AppendMessage(t.Context(), env.sessionID, session.NewMessage{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "generate a high level overview of / commands in this project"}}}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	if _, _, err := ag.RefreshSessionTitle(t.Context(), env.sessionID); err != nil {
		t.Fatalf("RefreshSessionTitle: %v", err)
	}
	// Give the bus goroutine a moment to flush.
	time.Sleep(50 * time.Millisecond)
	got := drain()
	if len(got) != 1 {
		t.Fatalf("SessionTitleUpdated events = %d, want 1: %+v", len(got), got)
	}
	if got[0].Title != "Commands overview" || got[0].Source != "generated" || got[0].SessionID != env.sessionID {
		t.Fatalf("event = %+v", got[0])
	}
}

func TestAgentSeedPreviewTitleOnFirstUserMessage(t *testing.T) {
	env := newTestEnv(t)
	events, drain := collectEvents[bus.SessionTitleUpdated](t, env.Bus, 4)
	_ = events
	// Use a streaming model that yields a short reply so Send completes.
	mainModel := &fakeFantasyModel{provider: "fake", model: "large", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "ack"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	titleModel := &fakeFantasyModel{provider: "fake", model: "small", text: "Commands overview"}
	ag := env.newAgent(newFakeProvider("fake"), func(opts *Options) {
		opts.FantasyModel = mainModel
		opts.TitleFantasyModel = titleModel
	})

	const prompt = "fix the click to expand on bash tool blocks"
	if _, err := ag.Send(t.Context(), env.sessionID, userText(prompt)); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sess, err := env.Store.GetSession(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Slug == "" {
		t.Fatalf("slug not set after Send")
	}
	time.Sleep(50 * time.Millisecond)
	got := drain()
	if len(got) == 0 {
		t.Fatalf("expected at least one SessionTitleUpdated event")
	}
	if got[0].Source != "preview" {
		t.Fatalf("first event Source = %q, want preview: %+v", got[0].Source, got[0])
	}
	if got[0].Title != prompt {
		t.Fatalf("preview title = %q, want %q", got[0].Title, prompt)
	}
}

func TestAgentDoesNotOverwriteExistingSlug(t *testing.T) {
	env := newTestEnv(t)
	if err := env.Store.RenameSession(t.Context(), env.sessionID, "Existing title"); err != nil {
		t.Fatalf("RenameSession: %v", err)
	}
	mainModel := &fakeFantasyModel{provider: "fake", model: "large", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "ack"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	ag := env.newAgent(newFakeProvider("fake"), func(opts *Options) {
		opts.FantasyModel = mainModel
	})
	if _, err := ag.Send(t.Context(), env.sessionID, userText("new question")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sess, err := env.Store.GetSession(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Slug != "Existing title" {
		t.Fatalf("slug overwritten: %q", sess.Slug)
	}
}

func TestSend_FantasyRenameSessionToolUsesTitleModel(t *testing.T) {
	env := newTestEnv(t)
	mainModel := &fakeFantasyModel{provider: "fake", model: "large", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeToolCall, ID: "tu-title", ToolCallName: renameSessionToolName, ToolCallInput: `{"topic":"overview of slash commands"}`},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "renamed"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	titleModel := &fakeFantasyModel{provider: "fake", model: "small", text: "Commands overview"}
	ag := env.newAgent(newFakeProvider("fake"), func(opts *Options) {
		opts.FantasyModel = mainModel
		opts.TitleFantasyModel = titleModel
	})

	if _, err := ag.Send(t.Context(), env.sessionID, userText("topic changed")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sess, err := env.Store.GetSession(t.Context(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Slug != "Commands overview" {
		t.Fatalf("Slug = %q, want Commands overview", sess.Slug)
	}
	if titleModel.calls.Load() != 1 {
		t.Fatalf("title model calls = %d, want 1", titleModel.calls.Load())
	}
}

type fakeScript struct {
	events []provider.Event
	// initErr, if non-nil, is returned from Stream itself (Stream returns
	// (nil, err) for transport-level failures before any byte arrives).
	initErr error
}

func newFakeProvider(name string, scripts ...fakeScript) *fakeProvider {
	return &fakeProvider{name: name, scripts: scripts}
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	f.calls.Add(1)
	f.mu.Lock()
	if len(f.scripts) == 0 {
		f.mu.Unlock()
		return nil, fmt.Errorf("fakeProvider: out of scripts")
	}
	s := f.scripts[0]
	f.scripts = f.scripts[1:]
	cb := f.onStream
	f.mu.Unlock()

	if cb != nil {
		cb(req)
	}
	if s.initErr != nil {
		return nil, s.initErr
	}

	ch := make(chan provider.Event, len(s.events)+1)
	go func() {
		defer close(ch)
		for _, ev := range s.events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func (f *fakeProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}

func (f *fakeProvider) ListModels(_ context.Context) ([]provider.Model, error) { return nil, nil }

// scriptText builds a script for one provider turn: textual reply with
// optional terminal usage.
func scriptText(text string, usage provider.Usage) fakeScript {
	evs := []provider.Event{{Type: provider.EventTextDelta, Text: text}}
	if usage != (provider.Usage{}) {
		evs = append(evs, provider.Event{Type: provider.EventUsage, Usage: usage})
	}
	evs = append(evs, provider.Event{Type: provider.EventDone})
	return fakeScript{events: evs}
}

func TestSetSystemPromptChangesSubsequentSendPrompt(t *testing.T) {
	env := newTestEnv(t)
	prov := newFakeProvider("test",
		scriptText("ok", provider.Usage{}),
		scriptText("ok again", provider.Usage{}),
	)
	a := env.newAgent(prov, func(o *Options) { o.SystemPrompt = "base\n\nmode one" })
	sess, err := env.Store.CreateSession(t.Context(), session.NewSession{
		ProjectDir: env.pwd,
		Model:      session.ModelRef{Provider: "test", Name: "test-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var seen []string
	prov.onStream = func(req provider.Request) {
		seen = append(seen, req.System)
	}
	if _, err := a.Send(t.Context(), sess.ID, userText("first")); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := a.SetSystemPrompt("base\n\nmode two"); err != nil {
		t.Fatalf("SetSystemPrompt: %v", err)
	}
	if _, err := a.Send(t.Context(), sess.ID, userText("second")); err != nil {
		t.Fatalf("second Send: %v", err)
	}

	if len(seen) != 2 {
		t.Fatalf("stream calls = %d, want 2", len(seen))
	}
	if seen[0] != "base\n\nmode one" {
		t.Fatalf("first system prompt = %q", seen[0])
	}
	if seen[1] != "base\n\nmode two" {
		t.Fatalf("second system prompt = %q", seen[1])
	}
	if strings.Contains(seen[1], "mode one") {
		t.Fatalf("switched prompt retained old mode prompt: %q", seen[1])
	}
}

type staticMemoryLoader []*session.Memory

func (l staticMemoryLoader) ListMemories(context.Context) ([]*session.Memory, error) {
	return append([]*session.Memory(nil), l...), nil
}

func TestSendInjectsSessionMemoriesIntoLatestUserEnvelope(t *testing.T) {
	env := newTestEnv(t)
	if _, err := env.Store.RememberSessionMemory(t.Context(), env.sessionID, session.NewMemory{Content: "prefers focused verification"}); err != nil {
		t.Fatalf("RememberSessionMemory: %v", err)
	}
	prov := newFakeProvider("fake", scriptText("ok", provider.Usage{}))
	a := env.newAgent(prov, func(o *Options) {
		o.MemoryLoader = staticMemoryLoader{
			{ID: "01GLOBAL", Scope: session.MemoryScopeGlobal, Content: "global preference"},
			{ID: "01PROJECT", Scope: session.MemoryScopeProject, Content: "project preference"},
		}
	})

	var latestUser string
	prov.onStream = func(req provider.Request) {
		if len(req.Messages) == 0 {
			t.Fatal("provider request had no messages")
		}
		latest := req.Messages[len(req.Messages)-1]
		if latest.Role != session.RoleUser || len(latest.Parts) == 0 {
			t.Fatalf("latest provider message = %+v, want user text", latest)
		}
		latestUser = latest.Parts[0].Text
	}
	if _, err := a.Send(t.Context(), env.sessionID, userText("please do it")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(latestUser, `<memory scope="session"`) || !strings.Contains(latestUser, "prefers focused verification") {
		t.Fatalf("latest user envelope missing session memory:\n%s", latestUser)
	}
	assertPromptOrder(t, latestUser, `scope="global"`, `scope="project"`, `scope="session"`)
	if !strings.Contains(latestUser, "please do it") || !strings.Contains(latestUser, userRequestOpen) {
		t.Fatalf("latest user envelope missing raw request:\n%s", latestUser)
	}
}

func TestSetModelChangesSubsequentSendProviderAndModel(t *testing.T) {
	env := newTestEnv(t)
	first := newFakeProvider("first", scriptText("one", provider.Usage{}))
	a := env.newAgent(first)
	sess, err := env.Store.CreateSession(t.Context(), session.NewSession{
		ProjectDir: env.pwd,
		Model:      session.ModelRef{Provider: "first", Name: "first-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	var gotProvider, gotModel string
	second := newFakeProvider("second", scriptText("two", provider.Usage{}))
	second.onStream = func(req provider.Request) {
		gotProvider = second.Name()
		gotModel = req.ModelName
	}
	if err := a.SetModel("second", "second-model", second, &providerFantasyModel{provider: second, model: "second-model"}); err != nil {
		t.Fatalf("SetModel: %v", err)
	}
	if _, err := a.Send(t.Context(), sess.ID, userText("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotProvider != "second" || gotModel != "second-model" {
		t.Fatalf("stream target = %s/%s, want second/second-model", gotProvider, gotModel)
	}
	if first.calls.Load() != 0 {
		t.Fatalf("old provider calls = %d, want 0", first.calls.Load())
	}
}

// scriptToolUse builds a script that emits text + N tool_use blocks + done.
func scriptToolUse(text string, calls ...provider.Event) fakeScript {
	evs := []provider.Event{}
	if text != "" {
		evs = append(evs, provider.Event{Type: provider.EventTextDelta, Text: text})
	}
	evs = append(evs, calls...)
	evs = append(evs, provider.Event{Type: provider.EventDone})
	return fakeScript{events: evs}
}

// toolUseEvent constructs a provider.EventToolUse with a JSON-encoded
// input map.
func toolUseEvent(t *testing.T, id, name string, input map[string]any) provider.Event {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatalf("marshal tool input: %v", err)
	}
	return provider.Event{
		Type:      provider.EventToolUse,
		ToolID:    id,
		ToolName:  name,
		ToolInput: raw,
	}
}

// pricelessCatalog returns a Catalog whose live URL points at an httptest
// server returning 500, with a cache path under t.TempDir.  Every LookUp
// returns ErrModelNotPriced via the fallback path because the fakeProvider
// model name is not in the hard-coded fallback table.
func pricelessCatalog(t testing.TB) *cost.Catalog {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return cost.NewCatalog(cost.CatalogOptions{
		BaseURL:   srv.URL,
		CachePath: filepath.Join(t.TempDir(), "catalog.json"),
	})
}

// testEnv bundles the pieces every test needs.  Construct with newTestEnv.
type testEnv struct {
	t         testing.TB
	Bus       *bus.Bus
	Store     *store.Store
	Perm      *permission.Engine
	Tools     *tool.Registry
	Catalog   *cost.Catalog
	Now       func() time.Time
	pwd       string
	sessionID string

	// autoAllowCancel is the cleanup function for the auto-allow
	// permission responder.  Returned to the caller in case it wants to
	// disable auto-allow part-way through a test.
	autoAllowCancel func()
}

// newTestEnv wires every dependency in memory: a fresh in-memory SQLite
// store, an isolated XDG-style state dir, a permission engine, the six
// builtin tools, and a fixed clock.
func newTestEnv(t testing.TB) *testEnv {
	t.Helper()
	ctx := context.Background()

	b := bus.New()
	t.Cleanup(b.Close)

	st, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Isolate state — permission engine reads state.AllowedRules.
	tmpState := t.TempDir()
	t.Setenv("XDG_STATE_HOME", tmpState)
	t.Setenv("HOME", tmpState)

	pe, err := permission.New(permission.EngineOptions{
		Bus:   b,
		State: state.LoadOptions{},
	})
	if err != nil {
		t.Fatalf("permission.New: %v", err)
	}
	t.Cleanup(pe.Close)

	tools := tool.Default()

	// Auto-allow responder: any PermissionAsked is answered with
	// PermissionReplied{allow, session}.  Tests that want denies opt in
	// per-test by replacing this responder.
	cancel := autoAllow(t, b)

	// Create a session.
	pwd := t.TempDir()
	sess, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: pwd,
		Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	return &testEnv{
		t:               t,
		Bus:             b,
		Store:           st,
		Perm:            pe,
		Tools:           tools,
		Catalog:         pricelessCatalog(t),
		Now:             func() time.Time { return time.Unix(1, 0).UTC() },
		pwd:             pwd,
		sessionID:       sess.ID,
		autoAllowCancel: cancel,
	}
}

// newAgent builds an Agent on the testEnv, applying any user overrides.
func (e *testEnv) newAgent(prov provider.Provider, optFns ...func(*Options)) *Agent {
	opts := Options{
		Bus:        e.Bus,
		Store:      e.Store,
		Provider:   prov,
		Permission: e.Perm,
		Tools:      e.Tools,
		Catalog:    e.Catalog,
		Pwd:        e.pwd,
		Now:        e.Now,
	}
	for _, fn := range optFns {
		fn(&opts)
	}
	if opts.FantasyModel == nil {
		opts.FantasyModel = &providerFantasyModel{provider: prov, model: "fake-model"}
	}
	a, err := New(opts)
	if err != nil {
		e.t.Fatalf("agent.New: %v", err)
	}
	e.t.Cleanup(func() { _ = a.Close() })
	return a
}

// autoAllow subscribes to PermissionAsked events and replies with an
// allow-once decision for every one.  Returns a cancel func for tests
// that want to switch to a custom responder mid-test.
func autoAllow(t testing.TB, b *bus.Bus) func() {
	t.Helper()
	sub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 64})
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				bus.Publish(b, bus.PermissionReplied{
					RequestID: ev.RequestID,
					Decision:  string(permission.ActionAllow),
					Scope:     string(permission.ScopeOnce),
					At:        time.Now(),
				})
			}
		}
	}()
	return func() {
		close(done)
		sub.Unsubscribe()
	}
}

// autoDeny replaces the test env's auto-allow with an auto-deny
// responder.  Used by the permission-denied scenario.
func autoDeny(t *testing.T, b *bus.Bus) func() {
	t.Helper()
	sub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 64})
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				bus.Publish(b, bus.PermissionReplied{
					RequestID: ev.RequestID,
					Decision:  string(permission.ActionDeny),
					Scope:     string(permission.ScopeOnce),
					At:        time.Now(),
				})
			}
		}
	}()
	return func() {
		close(done)
		sub.Unsubscribe()
	}
}

// userText builds a single-text-part user message for Send.
func userText(s string) []session.Part {
	return []session.Part{{Kind: session.PartText, Text: s}}
}

// collectEvents subscribes to T and returns a channel of received events,
// along with a stop func.  Collects up to bufSize events before blocking.
func collectEvents[T any](t *testing.T, b *bus.Bus, bufSize int) (chan T, func() []T) {
	t.Helper()
	sub := bus.Subscribe[T](b, bus.SubscribeOptions{BufferSize: bufSize})
	out := make(chan T, bufSize)
	done := make(chan struct{})
	go func() {
		defer close(out)
		for {
			select {
			case <-done:
				return
			case ev, ok := <-sub.C():
				if !ok {
					return
				}
				out <- ev
			}
		}
	}()
	return out, func() []T {
		close(done)
		sub.Unsubscribe()
		var collected []T
		// Drain whatever the goroutine had already enqueued.
		for {
			select {
			case ev, ok := <-out:
				if !ok {
					return collected
				}
				collected = append(collected, ev)
			default:
				return collected
			}
		}
	}
}

// readMessages dumps every message currently in the session, in order.
func readMessages(t *testing.T, st *store.Store, sessionID string) []*session.Message {
	t.Helper()
	msgs, err := st.MessagesForSession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	return msgs
}

func waitForRoles(t *testing.T, st *store.Store, sessionID string, want []string) []string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var got []string
	for time.Now().Before(deadline) {
		got = roles(readMessages(t, st, sessionID))
		if equalStrings(got, want) {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return got
}

// roles is a convenience to extract the role sequence from a slice of messages.
func roles(msgs []*session.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role)
	}
	return out
}

// ---------- scenario tests --------------------------------------------------

// 1. Simple text turn: provider emits text + Done.
func TestSend_SimpleTextTurn(t *testing.T) {
	env := newTestEnv(t)

	prov := newFakeProvider("fake", scriptText(
		"hello world",
		provider.Usage{InputTokens: 10, OutputTokens: 5},
	))
	a := env.newAgent(prov)

	deltas, drainDeltas := collectEvents[bus.AssistantTextDelta](t, env.Bus, 16)
	_ = deltas
	costs, drainCosts := collectEvents[bus.CostUpdated](t, env.Bus, 4)
	_ = costs
	ctxEvts, drainCtx := collectEvents[bus.ContextUsageUpdated](t, env.Bus, 4)
	_ = ctxEvts

	finalMsg, err := a.Send(context.Background(), env.sessionID, userText("hi"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if finalMsg.Role != session.RoleAssistant {
		t.Fatalf("want assistant role, got %q", finalMsg.Role)
	}
	if len(finalMsg.Parts) != 1 || finalMsg.Parts[0].Text != "hello world" {
		t.Fatalf("unexpected final parts: %+v", finalMsg.Parts)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	gotRoles := roles(msgs)
	if !equalStrings(gotRoles, []string{"user", "assistant"}) {
		t.Fatalf("want [user assistant], got %v", gotRoles)
	}

	// Give the collector goroutines a tick to receive.
	time.Sleep(50 * time.Millisecond)

	gotDeltas := drainDeltas()
	if len(gotDeltas) == 0 || gotDeltas[0].Text != "hello world" {
		t.Fatalf("want text delta, got %+v", gotDeltas)
	}
	if got := drainCosts(); len(got) == 0 || got[0].InputTokens != 10 {
		t.Fatalf("want cost event with input=10, got %+v", got)
	}
	if got := drainCtx(); len(got) == 0 {
		t.Fatalf("want context usage event")
	}
}

// 2. Tool call turn: read tool runs once, then second iteration returns text.
func TestSend_ToolCallTurn(t *testing.T) {
	env := newTestEnv(t)

	// Create a file the read tool will succeed on.
	target := filepath.Join(env.pwd, "hello.txt")
	if err := os.WriteFile(target, []byte("alpha\nbravo\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	prov := newFakeProvider("fake",
		scriptToolUse("looking at hello.txt", toolUseEvent(t, "tu1", "read", map[string]any{
			"path": target,
		})),
		scriptText("file says alpha and bravo", provider.Usage{InputTokens: 20, OutputTokens: 10}),
	)
	a := env.newAgent(prov)

	final, err := a.Send(context.Background(), env.sessionID, userText("read it"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if final.Parts[0].Text != "file says alpha and bravo" {
		t.Fatalf("unexpected final text: %q", final.Parts[0].Text)
	}

	gotRoles := roles(readMessages(t, env.Store, env.sessionID))
	want := []string{"user", "assistant", "tool", "assistant"}
	if !equalStrings(gotRoles, want) {
		t.Fatalf("want %v, got %v", want, gotRoles)
	}
	if prov.calls.Load() != 2 {
		t.Fatalf("want 2 provider calls, got %d", prov.calls.Load())
	}
}

// 3. Two tool calls in a single turn (executed sequentially).
func TestSend_TwoToolCallsSequential(t *testing.T) {
	env := newTestEnv(t)

	f1 := filepath.Join(env.pwd, "a.txt")
	f2 := filepath.Join(env.pwd, "b.txt")
	for _, p := range []string{f1, f2} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu1", "read", map[string]any{"path": f1}),
			toolUseEvent(t, "tu2", "read", map[string]any{"path": f2}),
		),
		scriptText("done", provider.Usage{InputTokens: 5, OutputTokens: 2}),
	)
	a := env.newAgent(prov)

	if _, err := a.Send(context.Background(), env.sessionID, userText("read both")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	gotRoles := roles(readMessages(t, env.Store, env.sessionID))
	want := []string{"user", "assistant", "tool", "tool", "assistant"}
	if !equalStrings(gotRoles, want) {
		t.Fatalf("want %v, got %v", want, gotRoles)
	}
	// Only two provider calls (the first response contained both tool_use
	// blocks; the second is the final answer).
	if prov.calls.Load() != 2 {
		t.Fatalf("want 2 provider calls, got %d", prov.calls.Load())
	}
}

// 4. Permission denied: tool returns IsError; conversation continues.
func TestSend_PermissionDenied(t *testing.T) {
	env := newTestEnv(t)
	// Swap the auto-allow responder for auto-deny.
	env.autoAllowCancel()
	cancel := autoDeny(t, env.Bus)
	t.Cleanup(cancel)

	// Place the target OUTSIDE pwd: the default policy allows file.read
	// inside pwd unconditionally, so an inside-pwd read never reaches
	// the auto-deny responder.
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("nope"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptText("can't read it, sorry", provider.Usage{InputTokens: 3, OutputTokens: 5}),
	)
	a := env.newAgent(prov)

	if _, err := a.Send(context.Background(), env.sessionID, userText("read denied")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	// Find the tool message and assert IsError.
	var toolMsg *session.Message
	for _, m := range msgs {
		if m.Role == session.RoleTool {
			toolMsg = m
			break
		}
	}
	if toolMsg == nil {
		t.Fatalf("no tool message in transcript")
	}
	if len(toolMsg.Parts) != 1 || !toolMsg.Parts[0].IsError {
		t.Fatalf("want IsError tool result, got %+v", toolMsg.Parts)
	}
	if !strings.Contains(toolMsg.Parts[0].Content, "permission denied") {
		t.Fatalf("want permission-denied content, got %q", toolMsg.Parts[0].Content)
	}
}

func TestSend_MissingFantasyModelFailsClearly(t *testing.T) {
	env := newTestEnv(t)
	a, err := New(Options{Bus: env.Bus, Store: env.Store, Provider: newFakeProvider("fake"), Permission: env.Perm, Tools: env.Tools, Catalog: env.Catalog, Pwd: env.pwd, Now: env.Now})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })
	_, err = a.Send(context.Background(), env.sessionID, userText("loop"))
	if err == nil || !strings.Contains(err.Error(), "fantasy model is not configured") {
		t.Fatalf("want missing fantasy model error, got %v", err)
	}
}

// 6. Stream error mid-flight: partial assistant gets committed, error wrapped.
func TestSend_StreamErrorMidFlight(t *testing.T) {
	env := newTestEnv(t)

	streamErr := errors.New("upstream blew up")
	prov := newFakeProvider("fake", fakeScript{events: []provider.Event{
		{Type: provider.EventTextDelta, Text: "partial reply..."},
		{Type: provider.EventError, Err: streamErr},
	}})
	a := env.newAgent(prov)

	_, err := a.Send(context.Background(), env.sessionID, userText("die"))
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !errors.Is(err, streamErr) {
		t.Fatalf("want wrapped streamErr, got %v", err)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	if len(msgs) != 2 || msgs[1].Role != session.RoleAssistant {
		t.Fatalf("want partial assistant committed, got roles=%v", roles(msgs))
	}
	if msgs[1].Parts[0].Text != "partial reply..." {
		t.Fatalf("unexpected partial text: %q", msgs[1].Parts[0].Text)
	}
}

func TestSend_FantasyStreamErrorCommitsPartialText(t *testing.T) {
	env := newTestEnv(t)
	streamErr := errors.New("upstream blew up")
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamErr: streamErr, stream: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "partial reply..."},
	}}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) { o.FantasyModel = model })

	_, err := a.Send(context.Background(), env.sessionID, userText("die"))
	if !errors.Is(err, streamErr) {
		t.Fatalf("want wrapped streamErr, got %v", err)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	if len(msgs) != 2 || msgs[1].Role != session.RoleAssistant {
		t.Fatalf("want partial assistant committed, got roles=%v", roles(msgs))
	}
	if len(msgs[1].Parts) != 1 || msgs[1].Parts[0].Text != "partial reply..." {
		t.Fatalf("unexpected partial parts: %+v", msgs[1].Parts)
	}
}

func TestSend_FantasyStreamErrorCommitsPartialThinking(t *testing.T) {
	env := newTestEnv(t)
	streamErr := errors.New("reasoning failed")
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamErr: streamErr, stream: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeReasoningStart, ID: "r", Delta: "I "},
		{Type: fantasy.StreamPartTypeReasoningDelta, ID: "r", Delta: "am thinking..."},
	}}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) { o.FantasyModel = model })

	_, err := a.Send(context.Background(), env.sessionID, userText("think"))
	if !errors.Is(err, streamErr) {
		t.Fatalf("want wrapped streamErr, got %v", err)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	if len(msgs) != 2 || len(msgs[1].Parts) != 1 || msgs[1].Parts[0].Kind != session.PartThinking || msgs[1].Parts[0].Text != "I am thinking..." {
		t.Fatalf("unexpected messages: roles=%v parts=%+v", roles(msgs), msgs[len(msgs)-1].Parts)
	}
}

func TestSend_FantasyStreamErrorWithoutPartialDoesNotAppendAssistant(t *testing.T) {
	env := newTestEnv(t)
	streamErr := errors.New("empty failure")
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamErr: streamErr}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) { o.FantasyModel = model })

	_, err := a.Send(context.Background(), env.sessionID, userText("die empty"))
	if !errors.Is(err, streamErr) {
		t.Fatalf("want wrapped streamErr, got %v", err)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	if got := roles(msgs); !equalStrings(got, []string{"user"}) {
		t.Fatalf("want only user message, got roles=%v", got)
	}
}

func TestSend_FantasySuccessfulStreamAppendsOnce(t *testing.T) {
	env := newTestEnv(t)
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", stream: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
		{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "done"},
		{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
		{Type: fantasy.StreamPartTypeFinish, Usage: fantasy.Usage{InputTokens: 2, OutputTokens: 3}},
	}}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) { o.FantasyModel = model })

	final, err := a.Send(context.Background(), env.sessionID, userText("ok"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if final == nil || len(final.Parts) != 1 || final.Parts[0].Text != "done" {
		t.Fatalf("unexpected final: %+v", final)
	}
	msgs := readMessages(t, env.Store, env.sessionID)
	if got := roles(msgs); !equalStrings(got, []string{"user", "assistant"}) {
		t.Fatalf("want single assistant append, got roles=%v", got)
	}
}

func TestSend_FantasyParallelToolsAppendAssistantOnce(t *testing.T) {
	env := newTestEnv(t)
	tools := tool.NewRegistry()
	if err := tools.Register(&parallelNoopTool{name: "alpha"}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	if err := tools.Register(&parallelNoopTool{name: "beta"}); err != nil {
		t.Fatalf("register beta: %v", err)
	}
	wrappedStore := &assistantAppendCountingStore{Store: env.Store}
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeToolCall, ID: "tu-alpha", ToolCallName: "alpha", ToolCallInput: `{}`},
			{Type: fantasy.StreamPartTypeToolCall, ID: "tu-beta", ToolCallName: "beta", ToolCallInput: `{}`},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "done"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	a, err := New(Options{Bus: env.Bus, Store: wrappedStore, Provider: newFakeProvider("fake"), FantasyModel: model, Permission: env.Perm, Tools: tools, Catalog: env.Catalog, Pwd: env.pwd, Now: env.Now})
	if err != nil {
		t.Fatalf("agent.New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Send(context.Background(), env.sessionID, userText("run tools")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if got := wrappedStore.toolUseAssistantAppends.Load(); got != 1 {
		t.Fatalf("tool-use assistant appends = %d, want 1", got)
	}
	sess, err := env.Store.GetSession(context.Background(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Totals.InputTokens != 2 || sess.Totals.OutputTokens != 2 {
		t.Fatalf("totals = input %d output %d, want 2/2 without double-counting tool-use step", sess.Totals.InputTokens, sess.Totals.OutputTokens)
	}
}

func TestSend_FantasyToolLoopIsUncapped(t *testing.T) {
	env := newTestEnv(t)
	tools := tool.NewRegistry()
	if err := tools.Register(&parallelNoopTool{name: "alpha"}); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeToolCall, ID: "tu-alpha", ToolCallName: "alpha", ToolCallInput: `{}`},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "done"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) {
		o.FantasyModel = model
		o.Tools = tools
	})

	final, err := a.Send(context.Background(), env.sessionID, userText("run tools"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if final == nil || len(final.Parts) != 1 || final.Parts[0].Text != "done" {
		t.Fatalf("unexpected final: %+v", final)
	}
}

func TestSend_FantasyStreamErrorCommitsObservedToolCallWithSyntheticResult(t *testing.T) {
	env := newTestEnv(t)
	streamErr := errors.New("tool stream failed")
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamErr: streamErr, stream: []fantasy.StreamPart{
		{Type: fantasy.StreamPartTypeToolInputStart, ID: "tu1", ToolCallName: "read"},
		{Type: fantasy.StreamPartTypeToolInputDelta, ID: "tu1", Delta: `{"path":"a.txt"}`},
		{Type: fantasy.StreamPartTypeToolInputEnd, ID: "tu1"},
	}}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) { o.FantasyModel = model })

	_, err := a.Send(context.Background(), env.sessionID, userText("read"))
	if !errors.Is(err, streamErr) {
		t.Fatalf("want wrapped streamErr, got %v", err)
	}
	msgs := readMessages(t, env.Store, env.sessionID)
	if len(msgs) != 3 || len(msgs[1].Parts) != 1 || msgs[1].Parts[0].Kind != session.PartToolUse || msgs[2].Role != session.RoleTool {
		t.Fatalf("want assistant tool_use followed by synthetic tool result, got roles=%v parts=%+v", roles(msgs), msgs[len(msgs)-1].Parts)
	}
	part := msgs[1].Parts[0]
	if part.ToolID != "tu1" || part.ToolName != "read" || string(part.ToolInput) != `{"path":"a.txt"}` {
		t.Fatalf("unexpected tool_use: %+v", part)
	}
	result := msgs[2].Parts[0]
	if result.Kind != session.PartToolResult || result.ToolUseID != "tu1" || !result.IsError || !strings.Contains(result.Content, "interrupted") {
		t.Fatalf("unexpected synthetic result: %+v", result)
	}
}

func TestToFantasyMessagesSynthesizesMissingToolResults(t *testing.T) {
	msgs := []*session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "use a tool"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartToolUse, ToolID: "call_missing", ToolName: "read", ToolInput: []byte(`{"path":"a.txt"}`)}}},
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "try again"}}},
	}

	fmsgs := toFantasyMessages(msgs, nil, "", nil, nil, nil, 0, 0, 0)
	if len(fmsgs) != 4 {
		t.Fatalf("fantasy messages len = %d, want 4: %+v", len(fmsgs), fmsgs)
	}
	synthetic := fmsgs[2]
	if synthetic.Role != fantasy.MessageRoleTool || len(synthetic.Content) != 1 {
		t.Fatalf("want synthetic tool message at index 2, got %+v", synthetic)
	}
	part, ok := fantasy.AsMessagePart[fantasy.ToolResultPart](synthetic.Content[0])
	if !ok {
		t.Fatalf("synthetic content = %T, want ToolResultPart", synthetic.Content[0])
	}
	if part.ToolCallID != "call_missing" {
		t.Fatalf("tool call id = %q, want call_missing", part.ToolCallID)
	}
	if _, ok := part.Output.(fantasy.ToolResultOutputContentError); !ok {
		t.Fatalf("synthetic output = %T, want error output", part.Output)
	}
}

func TestToFantasyMessagesWrapsOnlyLatestUserMessage(t *testing.T) {
	msgs := []*session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "older request"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: "older answer"}}},
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "latest request"}}},
	}

	fmsgs := toFantasyMessages(msgs, nil, "", nil, nil, nil, 0, 0, 0)
	if len(fmsgs) != 3 {
		t.Fatalf("fantasy messages len = %d, want 3: %+v", len(fmsgs), fmsgs)
	}
	older := fantasyMessageText(fmsgs[0])
	latest := fantasyMessageText(fmsgs[2])
	if strings.Contains(older, turnContextOpen) {
		t.Fatalf("older user message should remain unwrapped, got:\n%s", older)
	}
	all := fantasyMessageText(fmsgs[0]) + fantasyMessageText(fmsgs[1]) + latest
	if count := strings.Count(all, turnContextOpen); count != 1 {
		t.Fatalf("turn context count = %d, want 1 in:\n%s", count, all)
	}
	if !strings.Contains(latest, "latest request") || !strings.Contains(latest, userRequestOpen) {
		t.Fatalf("latest user message missing envelope/raw request:\n%s", latest)
	}
	assertPromptOrder(t, latest, "<workspace_state>", "<memories>", "<critical_turn_reminders>", userRequestOpen)
}

func TestToFantasyMessagesDropsHistoricalAssistantThinking(t *testing.T) {
	msgs := []*session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "older request"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{
			{Kind: session.PartThinking, Text: "private reasoning that should not be replayed"},
			{Kind: session.PartText, Text: "visible answer"},
		}},
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "latest request"}}},
	}

	fmsgs := toFantasyMessages(msgs, nil, "", nil, nil, nil, 0, 0, 0)
	if len(fmsgs) != 3 {
		t.Fatalf("fantasy messages len = %d, want 3: %+v", len(fmsgs), fmsgs)
	}
	assistant := fmsgs[1]
	if len(assistant.Content) != 1 {
		t.Fatalf("assistant content len = %d, want only visible text: %+v", len(assistant.Content), assistant.Content)
	}
	if _, ok := fantasy.AsMessagePart[fantasy.ReasoningPart](assistant.Content[0]); ok {
		t.Fatalf("historical assistant thinking should not be replayed: %+v", assistant.Content)
	}
	if got := fantasyMessageText(assistant); got != "visible answer" {
		t.Fatalf("assistant text = %q, want visible answer", got)
	}
}

func TestToFantasyMessagesStripsHistoricalTurnContext(t *testing.T) {
	msgs := []*session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: buildLatestUserEnvelope("historical raw request", nil, 0, 0, 0)}}},
		{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: "older answer"}}},
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "latest request"}}},
	}

	fmsgs := toFantasyMessages(msgs, nil, "", nil, nil, nil, 0, 0, 0)
	older := fantasyMessageText(fmsgs[0])
	if older != "historical raw request" {
		t.Fatalf("older user message = %q, want raw request", older)
	}
	all := older + fantasyMessageText(fmsgs[1]) + fantasyMessageText(fmsgs[2])
	if count := strings.Count(all, turnContextOpen); count != 1 {
		t.Fatalf("turn context count = %d, want 1 in:\n%s", count, all)
	}
}

func TestToFantasyMessagesPreservesLatestUserImages(t *testing.T) {
	msgs := []*session.Message{
		{Role: session.RoleUser, Parts: []session.Part{
			{Kind: session.PartText, Text: "what is in this image?"},
			{Kind: session.PartImage, ImageMimeType: "image/png", ImageBase64: "cG5nIGJ5dGVz"},
		}},
	}

	fmsgs := toFantasyMessages(msgs, nil, "", nil, nil, nil, 0, 0, 0)
	if len(fmsgs) != 1 {
		t.Fatalf("fantasy messages len = %d, want 1", len(fmsgs))
	}
	if len(fmsgs[0].Content) != 2 {
		t.Fatalf("latest user content len = %d, want envelope text plus image: %+v", len(fmsgs[0].Content), fmsgs[0].Content)
	}
	if _, ok := fantasy.AsMessagePart[fantasy.TextPart](fmsgs[0].Content[0]); !ok {
		t.Fatalf("first content = %T, want TextPart", fmsgs[0].Content[0])
	}
	file, ok := fantasy.AsMessagePart[fantasy.FilePart](fmsgs[0].Content[1])
	if !ok {
		t.Fatalf("second content = %T, want FilePart", fmsgs[0].Content[1])
	}
	if file.MediaType != "image/png" || string(file.Data) != "png bytes" {
		t.Fatalf("file part = %+v, want decoded png image", file)
	}
}

func TestWrapFantasyStreamErrorIncludesNestedProviderDetail(t *testing.T) {
	inner := `{"error":{"message":"No tool output found for function call call_123.","type":"invalid_request_error"}}`
	body := fmt.Sprintf(`{"error":{"message":"Provider returned error","metadata":{"raw":%q,"provider_name":"Azure"}}}`, inner)
	providerErr := &fantasy.ProviderError{StatusCode: 400, Message: "Provider returned error", ResponseBody: []byte(body)}

	err := wrapFantasyStreamError(providerErr)
	if !errors.Is(err, providerErr) {
		t.Fatalf("wrapped error should preserve cause, got %v", err)
	}
	got := err.Error()
	if !strings.Contains(got, "Azure") || !strings.Contains(got, "No tool output found") {
		t.Fatalf("wrapped error missing provider detail: %q", got)
	}
}

// 7. Cost catalog miss: pricing returns ErrModelNotPriced, usage still records.
func TestSend_CostCatalogMiss(t *testing.T) {
	env := newTestEnv(t)

	prov := newFakeProvider("fake", scriptText(
		"yo", provider.Usage{InputTokens: 100, OutputTokens: 50},
	))
	a := env.newAgent(prov)

	final, err := a.Send(context.Background(), env.sessionID, userText("cost?"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if final.InputTokens != 100 || final.OutputTokens != 50 {
		t.Fatalf("want tokens persisted on message, got in=%d out=%d",
			final.InputTokens, final.OutputTokens)
	}
	if final.CostUSD != 0 {
		t.Fatalf("want $0 cost on catalog miss, got %v", final.CostUSD)
	}

	sess, err := env.Store.GetSession(context.Background(), env.sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess.Totals.InputTokens != 100 {
		t.Fatalf("totals not updated: %+v", sess.Totals)
	}
}

// 8. Concurrent Sends on different sessions complete without interference.
func TestSend_ConcurrentDifferentSessions(t *testing.T) {
	env := newTestEnv(t)

	// Need a second session.
	sess2, err := env.Store.CreateSession(context.Background(), session.NewSession{
		ProjectDir: env.pwd,
		Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	prov := newFakeProvider("fake",
		scriptText("one", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("two", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	a := env.newAgent(prov)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := a.Send(context.Background(), env.sessionID, userText("a")); err != nil {
			t.Errorf("Send1: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := a.Send(context.Background(), sess2.ID, userText("b")); err != nil {
			t.Errorf("Send2: %v", err)
		}
	}()
	wg.Wait()

	if prov.calls.Load() != 2 {
		t.Fatalf("want 2 provider calls, got %d", prov.calls.Load())
	}
}

// 9. Serialised Sends on the same session: second queues behind the first.
func TestSend_SerialisedSameSession(t *testing.T) {
	env := newTestEnv(t)

	prov := newFakeProvider("fake",
		scriptText("first", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("second", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	a := env.newAgent(prov)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := a.Send(context.Background(), env.sessionID, userText("one")); err != nil {
			t.Errorf("Send1: %v", err)
		}
	}()
	// Give Send1 a head-start so the per-session lock is taken.
	time.Sleep(10 * time.Millisecond)
	go func() {
		defer wg.Done()
		if _, err := a.Send(context.Background(), env.sessionID, userText("two")); err != nil {
			t.Errorf("Send2: %v", err)
		}
	}()
	wg.Wait()

	// Two complete user→assistant pairs.
	want := []string{"user", "assistant", "user", "assistant"}
	gotRoles := waitForRoles(t, env.Store, env.sessionID, want)
	if !equalStrings(gotRoles, want) {
		t.Fatalf("want %v, got %v", want, gotRoles)
	}
}

// 10. Compact happy path.
func TestCompact_HappyPath(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Seed 6 messages: 3 user/assistant pairs.
	prov := newFakeProvider("fake",
		scriptText("a1", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a2", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a3", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		// Compaction call: emits a summary text + usage.
		fakeScript{events: []provider.Event{
			{Type: provider.EventTextDelta, Text: "summary of three turns"},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 30}},
			{Type: provider.EventDone},
		}},
		// Post-compaction Send: assert system prompt carries marker.
		fakeScript{events: []provider.Event{
			{Type: provider.EventTextDelta, Text: "post-compaction reply"},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 1, OutputTokens: 1}},
			{Type: provider.EventDone},
		}},
	)

	a := env.newAgent(prov)
	for i := range 3 {
		if _, err := a.Send(ctx, env.sessionID, userText(fmt.Sprintf("q%d", i))); err != nil {
			t.Fatalf("Send: %v", err)
		}
	}

	marker, err := a.Compact(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if marker == nil || marker.Summary != "summary of three turns" {
		t.Fatalf("unexpected marker: %+v", marker)
	}

	// Verify the next Send sees the summary in the system prompt.
	var seenSystem string
	prov.mu.Lock()
	prov.onStream = func(req provider.Request) { seenSystem = req.System }
	prov.mu.Unlock()

	if _, err := a.Send(ctx, env.sessionID, userText("after")); err != nil {
		t.Fatalf("Send after compact: %v", err)
	}
	if !strings.Contains(seenSystem, "summary of three turns") {
		t.Fatalf("want marker summary in system prompt, got %q", seenSystem)
	}
}

// 11. Compact with too few messages.
func TestCompact_NothingToCompact(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake",
		scriptText("a1", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	a := env.newAgent(prov)
	if _, err := a.Send(ctx, env.sessionID, userText("q")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if _, err := a.Compact(ctx, env.sessionID); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("want ErrNothingToCompact, got %v", err)
	}
}

// 12. Context cancellation mid-stream: nothing committed beyond user msg.
func TestSend_ContextCancelMidStream(t *testing.T) {
	env := newTestEnv(t)

	// Provider blocks indefinitely after one delta — we cancel ctx
	// while it's stalled.
	block := make(chan struct{})
	defer close(block)
	prov := newFakeProvider("fake", fakeScript{events: nil})
	// Override Stream behavior: emit one delta then wait on block.
	prov.scripts = nil // clear; we'll handle Stream manually
	customProv := &customStreamProvider{
		name: "fake",
		stream: func(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
			ch := make(chan provider.Event, 2)
			go func() {
				defer close(ch)
				ch <- provider.Event{Type: provider.EventTextDelta, Text: "partial"}
				select {
				case <-ctx.Done():
				case <-block:
				}
			}()
			return ch, nil
		},
	}
	a := env.newAgent(customProv)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	_, err := a.Send(ctx, env.sessionID, userText("cancel me"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}

	// Only the user message should be persisted; nothing assistant.
	gotRoles := roles(readMessages(t, env.Store, env.sessionID))
	if !equalStrings(gotRoles, []string{"user"}) {
		t.Fatalf("want only [user] message after cancel, got %v", gotRoles)
	}
}

// ---------- option-validation tests -----------------------------------------

func TestNew_RequiredOptions(t *testing.T) {
	env := newTestEnv(t)
	cases := []struct {
		name string
		mod  func(*Options)
	}{
		{"bus", func(o *Options) { o.Bus = nil }},
		{"store", func(o *Options) { o.Store = nil }},
		{"provider", func(o *Options) { o.Provider = nil }},
		{"permission", func(o *Options) { o.Permission = nil }},
		{"tools", func(o *Options) { o.Tools = nil }},
		{"catalog", func(o *Options) { o.Catalog = nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := Options{
				Bus:        env.Bus,
				Store:      env.Store,
				Provider:   newFakeProvider("fake"),
				Permission: env.Perm,
				Tools:      env.Tools,
				Catalog:    env.Catalog,
			}
			tc.mod(&opts)
			if _, err := New(opts); err == nil {
				t.Fatalf("want error when %s nil", tc.name)
			}
		})
	}
}

// ---------- helpers ---------------------------------------------------------

// customStreamProvider is a minimal Provider for tests that need a custom
// Stream implementation (used by context-cancellation test).
type customStreamProvider struct {
	name   string
	stream func(ctx context.Context, req provider.Request) (<-chan provider.Event, error)
}

func (c *customStreamProvider) Name() string { return c.name }
func (c *customStreamProvider) Stream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	return c.stream(ctx, req)
}
func (c *customStreamProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (c *customStreamProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return nil, nil
}

func equalStrings(a, b []string) bool {
	return slices.Equal(a, b)
}

// TestSend_ReasoningOptionPropagates verifies that an Agent built with
// a non-zero Options.Reasoning still completes a Fantasy tool turn.
func TestSend_ReasoningOptionPropagates(t *testing.T) {
	env := newTestEnv(t)

	target := filepath.Join(env.pwd, "hello.txt")
	if err := os.WriteFile(target, []byte("hi\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	prov := newFakeProvider("fake",
		scriptToolUse("looking", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptText("done", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	var seen int
	var mu sync.Mutex
	prov.onStream = func(provider.Request) {
		mu.Lock()
		seen++
		mu.Unlock()
	}

	wantReasoning := provider.Reasoning{Effort: "medium"}
	a := env.newAgent(prov, func(o *Options) { o.Reasoning = wantReasoning })

	if _, err := a.Send(context.Background(), env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if seen != 2 {
		t.Fatalf("want 2 model calls, got %d", seen)
	}
}

// ---------- Hook integration tests ------------------------------------------

// hookReg builds a *hook.Registry from a single fake hook.
func singleHookReg(h hook.Hook) *hook.Registry {
	reg := hook.New()
	_ = reg.Register(h)
	return reg
}

// staticHook is a simple Hook implementation for tests.
type staticHook struct {
	name   string
	events []hook.Event
	mode   hook.Mode
	action hook.Action
	err    error
	called *atomic.Int32
}

func (h *staticHook) Name() string           { return h.name }
func (h *staticHook) Description() string    { return "test" }
func (h *staticHook) Source() string         { return "test" }
func (h *staticHook) Events() []hook.Event   { return h.events }
func (h *staticHook) Mode() hook.Mode        { return h.mode }
func (h *staticHook) Timeout() time.Duration { return 5 * time.Second }
func (h *staticHook) Run(_ context.Context, _ hook.Input) (hook.Action, error) {
	if h.called != nil {
		h.called.Add(1)
	}
	return h.action, h.err
}

type inputHook struct {
	name   string
	events []hook.Event
	mode   hook.Mode
	called *atomic.Int32
	run    func(hook.Input) (hook.Action, error)
}

func (h *inputHook) Name() string           { return h.name }
func (h *inputHook) Description() string    { return "test" }
func (h *inputHook) Source() string         { return "test" }
func (h *inputHook) Events() []hook.Event   { return h.events }
func (h *inputHook) Mode() hook.Mode        { return h.mode }
func (h *inputHook) Timeout() time.Duration { return 5 * time.Second }
func (h *inputHook) Run(_ context.Context, in hook.Input) (hook.Action, error) {
	if h.called != nil {
		h.called.Add(1)
	}
	if h.run == nil {
		return hook.Action{Decision: hook.DecisionAllow}, nil
	}
	return h.run(in)
}

// TestHook_PreToolDeny verifies that a pre_tool deny surfaces as an IsError
// tool result and the underlying tool is never executed.
func TestHook_PreToolDeny(t *testing.T) {
	env := newTestEnv(t)
	target := filepath.Join(env.pwd, "x.txt")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var toolExecuted atomic.Int32
	// Override the read tool with one that increments toolExecuted.
	tools := tool.NewRegistry()
	_ = tools.Register(&countingTool{name: "read", counter: &toolExecuted})

	denyHookImpl := &staticHook{
		name:   "deny-all",
		events: []hook.Event{hook.EventPreTool},
		mode:   hook.ModeSync,
		action: hook.Action{Decision: hook.DecisionDeny, Reason: "blocked by policy"},
	}

	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": target})),
		scriptText("done", provider.Usage{}),
	)
	a, err := New(Options{
		Bus:      env.Bus,
		Store:    env.Store,
		Provider: prov,
		FantasyModel: &providerFantasyModel{provider: prov,
			model: "fake-model"},
		Permission: env.Perm,
		Tools:      tools,
		Catalog:    env.Catalog,
		Pwd:        env.pwd,
		Now:        env.Now,
		Hooks:      singleHookReg(denyHookImpl),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	if _, err := a.Send(context.Background(), env.sessionID, userText("read it")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if toolExecuted.Load() != 0 {
		t.Fatal("tool must NOT execute when pre_tool hook denies")
	}

	// The tool result message should have IsError=true.
	msgs := readMessages(t, env.Store, env.sessionID)
	var toolMsg *session.Message
	for _, m := range msgs {
		if m.Role == session.RoleTool {
			toolMsg = m
			break
		}
	}
	if toolMsg == nil {
		t.Fatal("want a tool result message")
	}
	if !toolMsg.Parts[0].IsError {
		t.Fatalf("want IsError=true, got false; content=%q", toolMsg.Parts[0].Content)
	}
	if !strings.Contains(toolMsg.Parts[0].Content, "blocked by policy") {
		t.Fatalf("want deny reason in content, got %q", toolMsg.Parts[0].Content)
	}
}

// TestHook_PreToolModify verifies that a pre_tool modify hook changes
// the args that reach the tool.
func TestHook_PreToolModify(t *testing.T) {
	env := newTestEnv(t)

	var receivedInput []byte
	tools := tool.NewRegistry()
	_ = tools.Register(&capturingTool{name: "read", received: &receivedInput})

	newArgs := json.RawMessage(`{"path":"/modified/path"}`)
	modifyHookImpl := &staticHook{
		name:   "modify-input",
		events: []hook.Event{hook.EventPreTool},
		mode:   hook.ModeSync,
		action: hook.Action{Decision: hook.DecisionModify, ModifiedToolInput: newArgs},
	}

	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu1", "read", map[string]any{"path": "/original"})),
		scriptText("done", provider.Usage{}),
	)
	a, err := New(Options{
		Bus:      env.Bus,
		Store:    env.Store,
		Provider: prov,
		FantasyModel: &providerFantasyModel{provider: prov,
			model: "fake-model"},
		Permission: env.Perm,
		Tools:      tools,
		Catalog:    env.Catalog,
		Pwd:        env.pwd,
		Now:        env.Now,
		Hooks:      singleHookReg(modifyHookImpl),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// Auto-allow permission for the modified path too.
	if _, err := a.Send(context.Background(), env.sessionID, userText("read")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if string(receivedInput) != string(newArgs) {
		t.Fatalf("want modified args %s, got %s", newArgs, receivedInput)
	}
}

// TestHook_PreMessageDeny verifies that a pre_message deny aborts the
// turn without persisting any messages.
func TestHook_PreMessageDeny(t *testing.T) {
	env := newTestEnv(t)

	denyHookImpl := &staticHook{
		name:   "msg-deny",
		events: []hook.Event{hook.EventPreMessage},
		mode:   hook.ModeSync,
		action: hook.Action{Decision: hook.DecisionDeny, Reason: "message blocked"},
	}

	prov := newFakeProvider("fake",
		scriptText("should not run", provider.Usage{}),
	)
	a, err := New(Options{
		Bus:        env.Bus,
		Store:      env.Store,
		Provider:   prov,
		Permission: env.Perm,
		Tools:      env.Tools,
		Catalog:    env.Catalog,
		Pwd:        env.pwd,
		Now:        env.Now,
		Hooks:      singleHookReg(denyHookImpl),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = a.Close() })

	_, err = a.Send(context.Background(), env.sessionID, userText("hello"))
	if err == nil {
		t.Fatal("want error when pre_message hook denies")
	}
	if !strings.Contains(err.Error(), "message blocked") {
		t.Fatalf("want deny reason in error, got %v", err)
	}

	// No messages should have been persisted.
	msgs := readMessages(t, env.Store, env.sessionID)
	if len(msgs) != 0 {
		t.Fatalf("want 0 messages after pre_message deny, got %d", len(msgs))
	}
}

// TestHook_PreMessageSystemPromptAppendIsNotRendered verifies plugin hook
// context rides in the system prompt without rewriting the persisted user text.
func TestHook_PreMessageSystemPromptAppendIsNotRendered(t *testing.T) {
	env := newTestEnv(t)

	hookImpl := &inputHook{
		name:   "context-hook",
		events: []hook.Event{hook.EventPreMessage},
		mode:   hook.ModeSync,
		run: func(in hook.Input) (hook.Action, error) {
			if in.Message != "  hello\n\n" {
				t.Fatalf("hook message = %q, want whitespace-preserved hello", in.Message)
			}
			return hook.Action{Decision: hook.DecisionAllow, SystemPromptAppend: "plugin-only context"}, nil
		},
	}

	var seenSystem string
	prov := newFakeProvider("fake", scriptText("ok", provider.Usage{}))
	prov.onStream = func(req provider.Request) { seenSystem = req.System }
	a := env.newAgent(prov, func(o *Options) { o.Hooks = singleHookReg(hookImpl) })

	if _, err := a.Send(context.Background(), env.sessionID, userText("  hello\n\n")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !strings.Contains(seenSystem, "plugin-only context") {
		t.Fatalf("system prompt missing hook context: %q", seenSystem)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	if len(msgs) == 0 || msgs[0].Role != session.RoleUser {
		t.Fatalf("first message = %+v", msgs)
	}
	if len(msgs[0].Parts) != 1 || msgs[0].Parts[0].Kind != session.PartText {
		t.Fatalf("first user message shape = %+v, want one text part", msgs[0].Parts)
	}
	if got := msgs[0].Parts[0].Text; got != "  hello\n\n" {
		t.Fatalf("persisted user text = %q, want whitespace-preserved hello", got)
	}
	if strings.Contains(msgs[0].Parts[0].Text, "plugin-only context") {
		t.Fatalf("hook context leaked into visible user text: %+v", msgs[0].Parts)
	}
}

// TestHook_ModeSwitchRefreshInvokesPreMessage verifies mode switches call the
// pre_message hook so plugins can refresh system prompt additions for the active
// mode, and subsequent sends expose that mode to the hook too.
func TestHook_ModeSwitchRefreshInvokesPreMessage(t *testing.T) {
	env := newTestEnv(t)

	var called atomic.Int32
	var mu sync.Mutex
	var seenHookInputs []hook.Input
	hookImpl := &inputHook{
		name:   "mode-context-hook",
		events: []hook.Event{hook.EventPreMessage},
		mode:   hook.ModeSync,
		called: &called,
		run: func(in hook.Input) (hook.Action, error) {
			mu.Lock()
			seenHookInputs = append(seenHookInputs, in)
			mu.Unlock()
			return hook.Action{Decision: hook.DecisionAllow, SystemPromptAppend: "mode context: " + in.ModeName}, nil
		},
	}

	var seenSystem string
	prov := newFakeProvider("fake", scriptText("ok", provider.Usage{}))
	prov.onStream = func(req provider.Request) { seenSystem = req.System }
	a := env.newAgent(prov, func(o *Options) { o.Hooks = singleHookReg(hookImpl) })

	if err := a.RefreshHookSystemPromptAdditions(context.Background(), env.sessionID, "Deep"); err != nil {
		t.Fatalf("RefreshHookSystemPromptAdditions: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("hook calls after refresh = %d, want 1", called.Load())
	}

	if _, err := a.Send(context.Background(), env.sessionID, userText("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if called.Load() != 2 {
		t.Fatalf("hook calls after send = %d, want 2", called.Load())
	}
	if !strings.Contains(seenSystem, "mode context: Deep") {
		t.Fatalf("system prompt missing refreshed mode context: %q", seenSystem)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seenHookInputs) != 2 {
		t.Fatalf("seenHookInputs len = %d", len(seenHookInputs))
	}
	if seenHookInputs[0].ModeName != "Deep" || seenHookInputs[0].Message != "" {
		t.Fatalf("refresh hook input = %+v, want mode Deep and empty message", seenHookInputs[0])
	}
	if seenHookInputs[1].ModeName != "Deep" || seenHookInputs[1].Message != "hello" {
		t.Fatalf("send hook input = %+v, want mode Deep and message hello", seenHookInputs[1])
	}
}

// TestHook_ModeSwitchRefreshWithoutSessionDoesNotFail verifies mode switches
// before the first session-backed send do not surface an error, but still prime
// the active mode name for the first real pre_message hook invocation.
func TestHook_ModeSwitchRefreshWithoutSessionDoesNotFail(t *testing.T) {
	env := newTestEnv(t)

	var called atomic.Int32
	var seen hook.Input
	hookImpl := &inputHook{
		name:   "mode-context-hook",
		events: []hook.Event{hook.EventPreMessage},
		mode:   hook.ModeSync,
		called: &called,
		run: func(in hook.Input) (hook.Action, error) {
			seen = in
			return hook.Action{Decision: hook.DecisionAllow}, nil
		},
	}
	prov := newFakeProvider("fake", scriptText("ok", provider.Usage{}))
	a := env.newAgent(prov, func(o *Options) { o.Hooks = singleHookReg(hookImpl) })

	if err := a.RefreshHookSystemPromptAdditions(context.Background(), "", "Deep"); err != nil {
		t.Fatalf("RefreshHookSystemPromptAdditions with no session: %v", err)
	}
	if called.Load() != 0 {
		t.Fatalf("hook calls after no-session refresh = %d, want 0", called.Load())
	}

	if _, err := a.Send(context.Background(), env.sessionID, userText("hello")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("hook calls after send = %d, want 1", called.Load())
	}
	if seen.ModeName != "Deep" || seen.Message != "hello" {
		t.Fatalf("first real hook input = %+v, want mode Deep and message hello", seen)
	}
}

// TestHook_NilRegistrySafe verifies that opts.Hooks=nil is handled
// without any nil-deref or panic.
func TestHook_NilRegistrySafe(t *testing.T) {
	env := newTestEnv(t)
	prov := newFakeProvider("fake", scriptText("hello", provider.Usage{}))
	a := env.newAgent(prov) // no Hooks option → nil

	_, err := a.Send(context.Background(), env.sessionID, userText("hi"))
	if err != nil {
		t.Fatalf("Send with nil Hooks: %v", err)
	}
}

// ---------- tool stubs for hook tests ---------------------------------------

// countingTool counts how many times Execute is called.
type countingTool struct {
	name    string
	counter *atomic.Int32
}

func (c *countingTool) Name() string        { return c.name }
func (c *countingTool) Description() string { return "counting" }
func (c *countingTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (c *countingTool) Parallelizable() bool { return false }
func (c *countingTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	c.counter.Add(1)
	return tool.Result{Content: "ok"}, nil
}

type parallelNoopTool struct{ name string }

func (p *parallelNoopTool) Name() string        { return p.name }
func (p *parallelNoopTool) Description() string { return "parallel noop" }
func (p *parallelNoopTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (p *parallelNoopTool) Parallelizable() bool { return true }
func (p *parallelNoopTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

type assistantAppendCountingStore struct {
	session.Store
	toolUseAssistantAppends atomic.Int32
}

func (s *assistantAppendCountingStore) AppendMessage(ctx context.Context, sessionID string, in session.NewMessage) (*session.Message, error) {
	if in.Role == session.RoleAssistant {
		for _, p := range in.Parts {
			if p.Kind == session.PartToolUse {
				if s.toolUseAssistantAppends.Add(1) == 1 {
					time.Sleep(50 * time.Millisecond)
				}
				break
			}
		}
	}
	return s.Store.AppendMessage(ctx, sessionID, in)
}

// capturingTool stores the raw input bytes of its last Execute call.
type capturingTool struct {
	name     string
	received *[]byte
}

func (c *capturingTool) Name() string        { return c.name }
func (c *capturingTool) Description() string { return "capturing" }
func (c *capturingTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (c *capturingTool) Parallelizable() bool { return false }
func (c *capturingTool) Execute(_ context.Context, input json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	*c.received = append([]byte(nil), input...)
	return tool.Result{Content: "ok"}, nil
}
func TestSteerAppliesAtNextFantasyStepOnly(t *testing.T) {
	env := newTestEnv(t)
	a := env.newAgent(newFakeProvider("fake", scriptText("ok", provider.Usage{})))

	a.mu.Lock()
	a.activeRuns[env.sessionID] = struct{}{}
	a.mu.Unlock()

	if err := a.Steer(env.sessionID, userText("prefer the direct fix")); err != nil {
		t.Fatalf("Steer: %v", err)
	}

	messages := []fantasy.Message{fantasy.NewSystemMessage("base system")}
	_, prepared, err := a.prepareFantasyStep(context.Background(), env.sessionID, fantasy.PrepareStepFunctionOptions{Messages: messages})
	if err != nil {
		t.Fatalf("prepareFantasyStep: %v", err)
	}
	if len(prepared.Messages) == 0 {
		t.Fatal("expected prepared messages")
	}
	if got := fantasyMessageText(prepared.Messages[0]); !strings.Contains(got, "base system") {
		t.Fatalf("prepared system = %q", got)
	}
	if len(prepared.Messages) < 2 || prepared.Messages[1].Role != fantasy.MessageRoleUser || !strings.Contains(fantasyMessageText(prepared.Messages[1]), "prefer the direct fix") {
		t.Fatalf("prepared steering message = %#v", prepared.Messages)
	}

	_, prepared, err = a.prepareFantasyStep(context.Background(), env.sessionID, fantasy.PrepareStepFunctionOptions{Messages: messages})
	if err != nil {
		t.Fatalf("second prepareFantasyStep: %v", err)
	}
	if prepared.Messages != nil {
		t.Fatalf("steering should be one-shot, got prepared messages: %#v", prepared.Messages)
	}
}

func TestSteerRequiresActiveTurn(t *testing.T) {
	env := newTestEnv(t)
	a := env.newAgent(newFakeProvider("fake", scriptText("ok", provider.Usage{})))

	err := a.Steer(env.sessionID, userText("too late"))
	if !errors.Is(err, ErrNoActiveTurn) {
		t.Fatalf("Steer err = %v, want ErrNoActiveTurn", err)
	}
}

type blockingTool struct {
	name    string
	started chan struct{}
	release chan struct{}
}

func (b *blockingTool) Name() string        { return b.name }
func (b *blockingTool) Description() string { return "blocking" }
func (b *blockingTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (b *blockingTool) Parallelizable() bool { return false }
func (b *blockingTool) Execute(ctx context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	close(b.started)
	select {
	case <-b.release:
		return tool.Result{Content: "tool done"}, nil
	case <-ctx.Done():
		return tool.Result{}, ctx.Err()
	}
}

func TestSteerDuringToolExecutionReachesNextFantasyStep(t *testing.T) {
	env := newTestEnv(t)
	block := &blockingTool{name: "block_for_steer", started: make(chan struct{}), release: make(chan struct{})}
	if err := env.Tools.Register(block); err != nil {
		t.Fatalf("Register blocking tool: %v", err)
	}

	var callsMu sync.Mutex
	var prompts []string
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeToolCall, ID: "tu-steer", ToolCallName: "block_for_steer", ToolCallInput: `{}`},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonToolCalls, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "saw it"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}}
	model.onStream = func(call fantasy.Call) {
		callsMu.Lock()
		defer callsMu.Unlock()
		var b strings.Builder
		for _, msg := range call.Prompt {
			b.WriteString(fantasyMessageText(msg))
			b.WriteString("\n")
		}
		prompts = append(prompts, b.String())
	}
	a := env.newAgent(newFakeProvider("fake"), func(o *Options) { o.FantasyModel = model })

	done := make(chan error, 1)
	go func() {
		_, err := a.Send(context.Background(), env.sessionID, userText("use the tool"))
		done <- err
	}()

	select {
	case <-block.started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool did not start")
	}
	if err := a.Steer(env.sessionID, userText("prefer the direct fix")); err != nil {
		t.Fatalf("Steer while tool running: %v", err)
	}
	close(block.release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Send: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Send did not complete")
	}

	callsMu.Lock()
	defer callsMu.Unlock()
	if len(prompts) < 2 {
		t.Fatalf("provider calls = %d, want at least 2", len(prompts))
	}
	if !strings.Contains(prompts[1], "prefer the direct fix") {
		t.Fatalf("second provider prompt did not include steering:\n%s", prompts[1])
	}
	if got := model.steeringPromptRole(1); got != fantasy.MessageRoleUser {
		t.Fatalf("steering prompt role = %s, want user", got)
	}
}

// --- Runtime small-model wiring tests ----------------------------------------

// TestRuntimeUsesExplicitTitleModelForGenerateTitle verifies that when
// TitleModel is set explicitly in RuntimeOptions, GenerateTitle uses it and
// NOT the main model.  This is the core HYGGE-6 contract: small_model /
// small_provider reaches the title generation path.
func TestRuntimeUsesExplicitTitleModelForGenerateTitle(t *testing.T) {
	mainModel := &fakeFantasyModel{provider: "test", model: "large", text: "SHOULD NOT APPEAR"}
	titleModel := &fakeFantasyModel{provider: "test", model: "small", text: "Small model title"}

	rt := NewRuntime(RuntimeOptions{
		Model:      mainModel,
		TitleModel: titleModel,
	})

	got, _, err := rt.GenerateTitle(t.Context(), "what should the title be?", 32)
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if got != "Small model title" {
		t.Fatalf("GenerateTitle = %q, want %q", got, "Small model title")
	}
	if mainModel.calls.Load() != 0 {
		t.Fatalf("main model was called %d times; want 0 (title model should be used instead)", mainModel.calls.Load())
	}
	if titleModel.calls.Load() != 1 {
		t.Fatalf("title model calls = %d, want 1", titleModel.calls.Load())
	}
}

// TestRuntimeFallsBackToMainModelWhenNoTitleModel verifies that when
// TitleModel is absent (nil), GenerateTitle falls back to the main model.
// This preserves the existing behaviour when small_model is not configured.
func TestRuntimeFallsBackToMainModelWhenNoTitleModel(t *testing.T) {
	mainModel := &fakeFantasyModel{provider: "test", model: "large", text: "Main model title"}

	rt := NewRuntime(RuntimeOptions{
		Model:      mainModel,
		TitleModel: nil, // no small model configured
	})

	got, _, err := rt.GenerateTitle(t.Context(), "what should the title be?", 32)
	if err != nil {
		t.Fatalf("GenerateTitle: %v", err)
	}
	if got != "Main model title" {
		t.Fatalf("GenerateTitle = %q, want %q", got, "Main model title")
	}
	if mainModel.calls.Load() != 1 {
		t.Fatalf("main model calls = %d, want 1 (should fall back when no title model)", mainModel.calls.Load())
	}
}

// TestRuntimeSetModelDoesNotOverrideExplicitTitleModel verifies that when a
// TitleModel was supplied at construction (titleModelExplicit=true), calling
// SetModel on the runtime does not replace it.  This prevents a model hot-swap
// from silently reverting the small_model configuration.
func TestRuntimeSetModelDoesNotOverrideExplicitTitleModel(t *testing.T) {
	mainModel := &fakeFantasyModel{provider: "test", model: "large", text: "unused"}
	titleModel := &fakeFantasyModel{provider: "test", model: "small", text: "Small model title"}
	newMainModel := &fakeFantasyModel{provider: "test", model: "large-v2", text: "Large v2"}

	rt := NewRuntime(RuntimeOptions{
		Model:      mainModel,
		TitleModel: titleModel,
	})

	// Hot-swap the main model (simulates a mode switch).
	rt.SetModel(newMainModel)

	// GenerateTitle must still use the explicit title model.
	got, _, err := rt.GenerateTitle(t.Context(), "prompt", 32)
	if err != nil {
		t.Fatalf("GenerateTitle after SetModel: %v", err)
	}
	if got != "Small model title" {
		t.Fatalf("GenerateTitle = %q, want %q (explicit title model should be preserved)", got, "Small model title")
	}
	if newMainModel.calls.Load() != 0 {
		t.Fatalf("new main model called %d times after SetModel; title model should still be in use", newMainModel.calls.Load())
	}
}

// TestRuntimeSetModelUpdatesImplicitTitleModel verifies that when no explicit
// TitleModel was configured (titleModelExplicit=false), SetModel replaces
// both the main model AND the title model.  This is the correct behaviour
// when small_model is absent: the title model tracks the main model.
func TestRuntimeSetModelUpdatesImplicitTitleModel(t *testing.T) {
	mainModel := &fakeFantasyModel{provider: "test", model: "large", text: "old"}
	newMainModel := &fakeFantasyModel{provider: "test", model: "large-v2", text: "New main model title"}

	rt := NewRuntime(RuntimeOptions{
		Model:      mainModel,
		TitleModel: nil, // implicit: tracks main model
	})

	rt.SetModel(newMainModel)

	got, _, err := rt.GenerateTitle(t.Context(), "prompt", 32)
	if err != nil {
		t.Fatalf("GenerateTitle after SetModel: %v", err)
	}
	if got != "New main model title" {
		t.Fatalf("GenerateTitle = %q, want %q (implicit title model should follow SetModel)", got, "New main model title")
	}
	if mainModel.calls.Load() != 0 {
		t.Fatalf("old main model called %d times; should have been replaced by SetModel", mainModel.calls.Load())
	}
}
