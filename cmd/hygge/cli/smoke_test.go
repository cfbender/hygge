package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/state"
)

// TestSmoke_EndToEnd is the v0.1 "does this actually work?" gate.  It
// boots the full runtime (bootstrap), wires a scripted fake Fantasy model
// into the agent loop, lets the read tool execute against a real tempdir
// file, and asserts the conversation lands in the store the way the TUI
// would have committed it.
//
// What's exercised end-to-end:
//
//   - bootstrap() wires bus, store, permission, tools, catalog, agent
//   - state.json on-disk write via OnSessionCreated → AddRecentSession
//   - lazy session creation through store.CreateSession
//   - agent.Send → fantasy.LanguageModel.Stream (scripted) → tool.Execute →
//     second fantasy.LanguageModel.Stream → assistant final
//   - cost catalog falls through to fallback pricing without panicking
//     for an unknown model name (live fetch is shorted to a 500-server)
//   - bus events fire in the documented order
//
// What is deliberately NOT exercised: bubbletea (no TTY).  The test
// stands in for the TUI by calling Agent.Send the same way startSend
// does inside internal/ui/app.go.
func TestSmoke_EndToEnd(t *testing.T) {
	ctx := context.Background()

	// ---- hermetic env ---------------------------------------------------

	home := t.TempDir()
	xdgConfig := filepath.Join(home, ".config")
	xdgState := filepath.Join(home, ".local", "state")

	// Project dir for the session.  The fake read tool will read a file
	// inside this dir — the default permission policy auto-allows reads
	// inside Pwd, so no modal is needed (the auto-allow subscriber
	// below catches anything that *does* slip through).
	pwd := t.TempDir()
	const readContents = "package main\n\nfunc main() {}\n"
	readPath := filepath.Join(pwd, "main.go")
	if err := os.WriteFile(readPath, []byte(readContents), 0o600); err != nil {
		t.Fatalf("seed main.go: %v", err)
	}

	// 500-only catalog server so the runtime never reaches Catwalk.
	// LookUp falls back to the hard-coded table; for an unknown model
	// (which we use below) that yields ErrModelNotPriced and the agent
	// records cost = 0.
	catSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(catSrv.Close)

	// Fixed clock so timestamps in committed messages are deterministic.
	now := func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }

	// ---- scripted fake provider ----------------------------------------

	// Use a model name that is not in the hard-coded fallback table so
	// the catalog lookup falls through to ErrModelNotPriced and the
	// agent records cost = 0.  (The task spec calls this case out
	// explicitly — see deliverable 1, assertion 5.)
	const fakeModel = "fake-smoke-model-vX"

	readInput, err := json.Marshal(map[string]any{"path": readPath})
	if err != nil {
		t.Fatalf("marshal read input: %v", err)
	}

	scripts := []smokeScript{
		// Turn 1: emit a read tool call against the seed file, then Done.
		{events: []provider.Event{
			{Type: provider.EventToolUse, ToolID: "tu-1", ToolName: "read", ToolInput: readInput},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 42, OutputTokens: 7}},
			{Type: provider.EventDone},
		}},
		// Turn 2: emit text deltas, usage, Done.
		{events: []provider.Event{
			{Type: provider.EventTextDelta, Text: "the file "},
			{Type: provider.EventTextDelta, Text: "has package declarations"},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 80, OutputTokens: 12}},
			{Type: provider.EventDone},
		}},
	}

	prov := newSmokeProvider("anthropic", fakeModel, nil)
	fantasyModel := newSmokeFantasyModel("anthropic", fakeModel, scripts)

	// ---- bootstrap overrides -------------------------------------------

	SetTestOverrides(&bootstrapOptions{
		HomeDir:        home,
		XDGConfigHome:  xdgConfig,
		XDGStateHome:   xdgState,
		Pwd:            pwd,
		Now:            now,
		SkipTea:        true,
		CatalogBaseURL: catSrv.URL,
		ProviderFactory: func(_ map[string]any) (provider.Provider, error) {
			return prov, nil
		},
		FantasyModel: fantasyModel,
		SystemPrompt: "smoke-test system prompt",
	})
	t.Cleanup(func() { SetTestOverrides(nil) })

	rt, err := bootstrap(ctx, bootstrapOptions{})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	// ---- recording subscribers (built before any Send) -----------------

	rec := newBusRecorder(t, rt.Bus)
	t.Cleanup(rec.stop)

	// ---- auto-allow permission subscriber ------------------------------
	//
	// Default policy allows file.read inside Pwd, so the read tool call
	// in this smoke test won't trigger a modal — but we wire the
	// subscriber anyway so the test would survive a future change that
	// moves the seed file outside Pwd.  Mimics the TUI's `y` keypress.
	permDone := make(chan struct{})
	permSub := bus.Subscribe[bus.PermissionAsked](rt.Bus, bus.SubscribeOptions{BufferSize: 16})
	go func() {
		for {
			select {
			case <-permDone:
				return
			case ev, ok := <-permSub.C():
				if !ok {
					return
				}
				bus.Publish(rt.Bus, bus.PermissionReplied{
					RequestID: ev.RequestID,
					Decision:  string(permission.ActionAllow),
					Scope:     string(permission.ScopeOnce),
					At:        now(),
				})
			}
		}
	}()
	t.Cleanup(func() {
		close(permDone)
		permSub.Unsubscribe()
	})

	// ---- mimic ui.App.ensureSession ------------------------------------
	//
	// The TUI lazily creates a session on first Send and then calls
	// OnSessionCreated.  We do the same here so state.RecentSessions
	// gets written exactly the way the TUI writes it.
	sess, err := rt.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: pwd,
		Model: session.ModelRef{
			Provider: rt.Config.Model.Provider,
			Name:     fakeModel,
		},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	bus.Publish(rt.Bus, bus.SessionStart{
		SessionID: sess.ID,
		Resumed:   false,
		At:        now(),
	})
	if err := state.AddRecentSession(sess.ID, rt.StateOpts); err != nil {
		t.Fatalf("AddRecentSession: %v", err)
	}

	// Assertion 1: session created with the right ProjectDir and Model.
	if sess.ProjectDir != pwd {
		t.Errorf("session ProjectDir = %q, want %q", sess.ProjectDir, pwd)
	}
	if sess.Model.Provider != "anthropic" || sess.Model.Name != fakeModel {
		t.Errorf("session Model = %+v, want {anthropic %s}", sess.Model, fakeModel)
	}

	// ---- the conversation ---------------------------------------------

	final, err := rt.Agent.Send(ctx, sess.ID, []session.Part{
		{Kind: session.PartText, Text: "what's in main.go?"},
	})
	if err != nil {
		t.Fatalf("Agent.Send: %v", err)
	}
	if final == nil {
		t.Fatalf("Agent.Send: nil final message")
	}

	// Give the bus recorder a tick to drain anything still queued before
	// we assert on its contents.
	time.Sleep(100 * time.Millisecond)

	// ---- assertions ----------------------------------------------------

	// Assertion 2: four messages in the documented order.
	msgs, err := rt.Store.MessagesForSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	if got, want := len(msgs), 4; got != want {
		t.Fatalf("len(messages) = %d, want %d. roles=%v", got, want, smokeRoles(msgs))
	}
	wantRoles := []string{
		string(session.RoleUser),
		string(session.RoleAssistant),
		string(session.RoleTool),
		string(session.RoleAssistant),
	}
	if got := smokeRoles(msgs); !reflect.DeepEqual(got, wantRoles) {
		t.Fatalf("message roles = %v, want %v", got, wantRoles)
	}

	// Inner shape: assistant turn 1 must carry a tool_use part.
	asst1 := msgs[1]
	var sawToolUse bool
	for _, p := range asst1.Parts {
		if p.Kind == session.PartToolUse && p.ToolName == "read" && p.ToolID == "tu-1" {
			sawToolUse = true
		}
	}
	if !sawToolUse {
		t.Errorf("assistant turn 1 missing read tool_use part: %+v", asst1.Parts)
	}

	// Assertion 3: tool message contains the file contents.
	toolMsg := msgs[2]
	if len(toolMsg.Parts) != 1 || toolMsg.Parts[0].Kind != session.PartToolResult {
		t.Fatalf("tool message has unexpected parts: %+v", toolMsg.Parts)
	}
	if toolMsg.Parts[0].IsError {
		t.Fatalf("tool result is_error=true; content=%q", toolMsg.Parts[0].Content)
	}
	if !strings.Contains(toolMsg.Parts[0].Content, "package main") {
		t.Fatalf("tool result missing 'package main'; got: %q", toolMsg.Parts[0].Content)
	}

	// Final assistant turn carries the text deltas.
	if !strings.Contains(final.Parts[0].Text, "package declarations") {
		t.Errorf("final assistant text missing expected content; got %q",
			final.Parts[0].Text)
	}

	// Assertion 4: cumulative input/output tokens are non-zero.
	reloaded, err := rt.Store.GetSession(ctx, sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if reloaded.Totals.InputTokens == 0 {
		t.Errorf("Totals.InputTokens = 0; want > 0 (got %+v)", reloaded.Totals)
	}
	if reloaded.Totals.OutputTokens == 0 {
		t.Errorf("Totals.OutputTokens = 0; want > 0 (got %+v)", reloaded.Totals)
	}

	// Assertion 5: cost may be zero for an unknown model — but no panic.
	// (The fact that we got here without panicking is the assertion.)
	if reloaded.Totals.CostUSD < 0 {
		t.Errorf("Totals.CostUSD = %v; want >= 0", reloaded.Totals.CostUSD)
	}

	// Assertion 6: bus events appeared in a sensible shape.
	rec.assertObserved(t, sess.ID)

	// Assertion 7: state.RecentSessions contains the new session id.
	stReloaded, err := state.Load(rt.StateOpts)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	var sawRecent bool
	if slices.Contains(stReloaded.RecentSessions, sess.ID) {
		sawRecent = true
	}
	if !sawRecent {
		t.Errorf("state.RecentSessions = %v; missing %s", stReloaded.RecentSessions, sess.ID)
	}

	// Sanity: Fantasy was called exactly twice (one tool-use turn,
	// one final-text turn).
	if got := fantasyModel.calls.Load(); got != 2 {
		t.Errorf("fantasy model Stream calls = %d, want 2", got)
	}
	if got := prov.calls.Load(); got != 0 {
		t.Errorf("legacy provider Stream calls = %d, want 0", got)
	}
}

// ---------- smoke-test scaffolding -----------------------------------------

// smokeScript is one provider turn's event sequence.
type smokeScript struct {
	events []provider.Event
}

// smokeProvider is a scripted, no-network provider used by the smoke test.
// Each Stream call pops the next script off the front of the queue.
type smokeProvider struct {
	name    string
	model   string
	mu      sync.Mutex
	scripts []smokeScript
	calls   atomic.Int32
}

type smokeFantasyModel struct {
	provider string
	model    string
	mu       sync.Mutex
	scripts  []smokeScript
	calls    atomic.Int32
}

func newSmokeFantasyModel(providerName, model string, scripts []smokeScript) *smokeFantasyModel {
	return &smokeFantasyModel{provider: providerName, model: model, scripts: scripts}
}

func (m *smokeFantasyModel) Generate(context.Context, fantasy.Call) (*fantasy.Response, error) {
	return nil, fmt.Errorf("smokeFantasyModel: Generate not implemented")
}

func (m *smokeFantasyModel) Stream(ctx context.Context, _ fantasy.Call) (fantasy.StreamResponse, error) {
	m.calls.Add(1)
	m.mu.Lock()
	if len(m.scripts) == 0 {
		m.mu.Unlock()
		return nil, fmt.Errorf("smokeFantasyModel: out of scripts")
	}
	s := m.scripts[0]
	m.scripts = m.scripts[1:]
	m.mu.Unlock()

	return func(yield func(fantasy.StreamPart) bool) {
		textOpen := false
		finish := fantasy.FinishReasonStop
		usage := fantasy.Usage{}
		for _, ev := range s.events {
			if ctx.Err() != nil {
				return
			}
			switch ev.Type {
			case provider.EventTextDelta:
				if !textOpen {
					textOpen = true
					if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextStart, ID: "text"}) {
						return
					}
				}
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextDelta, ID: "text", Delta: ev.Text}) {
					return
				}
			case provider.EventToolUse:
				finish = fantasy.FinishReasonToolCalls
				input := string(ev.ToolInput)
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputStart, ID: ev.ToolID, ToolCallName: ev.ToolName}) {
					return
				}
				if input != "" && !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputDelta, ID: ev.ToolID, Delta: input}) {
					return
				}
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolInputEnd, ID: ev.ToolID}) {
					return
				}
				if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeToolCall, ID: ev.ToolID, ToolCallName: ev.ToolName, ToolCallInput: input}) {
					return
				}
			case provider.EventUsage:
				usage = fantasy.Usage{InputTokens: ev.Usage.InputTokens, OutputTokens: ev.Usage.OutputTokens, CacheReadTokens: ev.Usage.CacheReadTokens, CacheCreationTokens: ev.Usage.CacheWriteTokens}
			case provider.EventError:
				_ = yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeError, Error: ev.Err})
				return
			}
		}
		if textOpen {
			if !yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeTextEnd, ID: "text"}) {
				return
			}
		}
		_ = yield(fantasy.StreamPart{Type: fantasy.StreamPartTypeFinish, Usage: usage, FinishReason: finish})
	}, nil
}

func (m *smokeFantasyModel) GenerateObject(context.Context, fantasy.ObjectCall) (*fantasy.ObjectResponse, error) {
	return nil, fmt.Errorf("smokeFantasyModel: GenerateObject not implemented")
}

func (m *smokeFantasyModel) StreamObject(context.Context, fantasy.ObjectCall) (fantasy.ObjectStreamResponse, error) {
	return nil, fmt.Errorf("smokeFantasyModel: StreamObject not implemented")
}

func (m *smokeFantasyModel) Provider() string { return m.provider }
func (m *smokeFantasyModel) Model() string    { return m.model }

func newSmokeProvider(name, model string, scripts []smokeScript) *smokeProvider {
	return &smokeProvider{name: name, model: model, scripts: scripts}
}

func (p *smokeProvider) Name() string { return p.name }

func (p *smokeProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	p.calls.Add(1)
	p.mu.Lock()
	if len(p.scripts) == 0 {
		p.mu.Unlock()
		return nil, fmt.Errorf("smokeProvider: out of scripts")
	}
	s := p.scripts[0]
	p.scripts = p.scripts[1:]
	p.mu.Unlock()

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

func (p *smokeProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}

func (p *smokeProvider) ListModels(_ context.Context) ([]provider.Model, error) {
	return []provider.Model{{
		Name:           p.model,
		ContextWindow:  200_000,
		MaxOutput:      8192,
		SupportsTools:  true,
		SupportsImages: true,
	}}, nil
}

// busRecorder subscribes to the bus events that matter to the smoke
// test and records each one with a monotonically-increasing sequence
// number.  Lets the test assert event ordering after Send returns.
type busRecorder struct {
	mu     sync.Mutex
	events []recordedEvent
	subs   []interface{ Unsubscribe() }
	done   chan struct{}
	seq    atomic.Int64
}

type recordedEvent struct {
	seq  int64
	kind string
	at   any
}

func newBusRecorder(t *testing.T, b *bus.Bus) *busRecorder {
	t.Helper()
	r := &busRecorder{done: make(chan struct{})}

	startSub(b, r, "SessionStart", func(b *bus.Bus) (interface{ Unsubscribe() }, <-chan any) {
		s := bus.Subscribe[bus.SessionStart](b, bus.SubscribeOptions{BufferSize: 32})
		out := make(chan any, 32)
		go func() {
			for ev := range s.C() {
				out <- ev
			}
			close(out)
		}()
		return s, out
	})
	startSub(b, r, "MessageAppended", func(b *bus.Bus) (interface{ Unsubscribe() }, <-chan any) {
		s := bus.Subscribe[bus.MessageAppended](b, bus.SubscribeOptions{BufferSize: 32})
		out := make(chan any, 32)
		go func() {
			for ev := range s.C() {
				out <- ev
			}
			close(out)
		}()
		return s, out
	})
	startSub(b, r, "ToolCallRequested", func(b *bus.Bus) (interface{ Unsubscribe() }, <-chan any) {
		s := bus.Subscribe[bus.ToolCallRequested](b, bus.SubscribeOptions{BufferSize: 32})
		out := make(chan any, 32)
		go func() {
			for ev := range s.C() {
				out <- ev
			}
			close(out)
		}()
		return s, out
	})
	startSub(b, r, "ToolCallCompleted", func(b *bus.Bus) (interface{ Unsubscribe() }, <-chan any) {
		s := bus.Subscribe[bus.ToolCallCompleted](b, bus.SubscribeOptions{BufferSize: 32})
		out := make(chan any, 32)
		go func() {
			for ev := range s.C() {
				out <- ev
			}
			close(out)
		}()
		return s, out
	})
	startSub(b, r, "AssistantTextDelta", func(b *bus.Bus) (interface{ Unsubscribe() }, <-chan any) {
		s := bus.Subscribe[bus.AssistantTextDelta](b, bus.SubscribeOptions{BufferSize: 64})
		out := make(chan any, 64)
		go func() {
			for ev := range s.C() {
				out <- ev
			}
			close(out)
		}()
		return s, out
	})
	return r
}

// startSub wires one subscription into the recorder.  The factory
// returns the subscription handle (for Unsubscribe) and a chan of
// type-erased events.
func startSub(
	b *bus.Bus,
	r *busRecorder,
	kind string,
	factory func(*bus.Bus) (interface{ Unsubscribe() }, <-chan any),
) {
	sub, ch := factory(b)
	r.mu.Lock()
	r.subs = append(r.subs, sub)
	r.mu.Unlock()
	go func() {
		for {
			select {
			case <-r.done:
				return
			case ev, ok := <-ch:
				if !ok {
					return
				}
				r.record(kind, ev)
			}
		}
	}()
}

func (r *busRecorder) record(kind string, ev any) {
	seq := r.seq.Add(1)
	r.mu.Lock()
	r.events = append(r.events, recordedEvent{seq: seq, kind: kind, at: ev})
	r.mu.Unlock()
}

func (r *busRecorder) stop() {
	close(r.done)
	r.mu.Lock()
	subs := r.subs
	r.subs = nil
	r.mu.Unlock()
	for _, s := range subs {
		s.Unsubscribe()
	}
}

// assertObserved checks the documented per-iteration shape for the
// scripted conversation:
//
//	SessionStart
//	MessageAppended (user)
//	MessageAppended (assistant, tool_use)
//	ToolCallRequested
//	ToolCallCompleted
//	MessageAppended (tool)
//	MessageAppended (assistant, final)
//
// The bus has one subscription channel per event type. The recorder drains
// each type in its own goroutine, so cross-type ordering is intentionally not
// observable here: a ToolCallRequested can be recorded before the assistant
// MessageAppended even when it was published after it. We therefore assert
// FIFO order within MessageAppended, plus presence and payload sanity for the
// tool and delta event streams.
func (r *busRecorder) assertObserved(t *testing.T, sessionID string) {
	t.Helper()
	r.mu.Lock()
	events := append([]recordedEvent(nil), r.events...)
	r.mu.Unlock()

	var sawStart bool
	messageRoles := []string{}
	var requested, completed bool
	var deltaText strings.Builder
	for _, e := range events {
		switch ev := e.at.(type) {
		case bus.SessionStart:
			if ev.SessionID == sessionID {
				sawStart = true
			}
		case bus.MessageAppended:
			if ev.SessionID == sessionID {
				messageRoles = append(messageRoles, ev.Role)
			}
		case bus.ToolCallRequested:
			if ev.SessionID == sessionID && ev.ToolUseID == "tu-1" && ev.ToolName == "read" {
				requested = true
			}
		case bus.ToolCallCompleted:
			if ev.SessionID == sessionID && ev.ToolUseID == "tu-1" && ev.ToolName == "read" && ev.Err == "" {
				completed = true
			}
		case bus.AssistantTextDelta:
			if ev.SessionID == sessionID {
				deltaText.WriteString(ev.Text)
			}
		}
	}
	if !sawStart {
		t.Errorf("bus events: missing SessionStart. events=%v", debugKinds(events))
	}
	wantRoles := []string{string(session.RoleUser), string(session.RoleAssistant), string(session.RoleTool), string(session.RoleAssistant)}
	if !reflect.DeepEqual(messageRoles, wantRoles) {
		t.Errorf("bus MessageAppended roles = %v, want %v. events=%v", messageRoles, wantRoles, debugKinds(events))
	}
	if !requested {
		t.Errorf("bus events: missing read ToolCallRequested. events=%v", debugKinds(events))
	}
	if !completed {
		t.Errorf("bus events: missing successful read ToolCallCompleted. events=%v", debugKinds(events))
	}
	if !strings.Contains(deltaText.String(), "package declarations") {
		t.Errorf("bus AssistantTextDelta = %q; missing final text", deltaText.String())
	}
}

func debugKinds(events []recordedEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.kind
	}
	return out
}

// smokeRoles is a small helper mirroring the agent_test.go roles()
// helper, scoped to this file to avoid leaking test-only helpers into
// the cli package.
func smokeRoles(msgs []*session.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = string(m.Role)
	}
	return out
}
