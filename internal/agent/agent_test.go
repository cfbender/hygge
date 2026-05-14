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
func pricelessCatalog(t *testing.T) *cost.Catalog {
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
	t         *testing.T
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
func newTestEnv(t *testing.T) *testEnv {
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
func autoAllow(t *testing.T, b *bus.Bus) func() {
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

// 5. Iteration limit: provider always returns a tool_use.
func TestSend_IterationLimit(t *testing.T) {
	env := newTestEnv(t)
	target := filepath.Join(env.pwd, "loop.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Three tool-use scripts; the 4th iteration would fire but the cap
	// is 3 so we never reach it.
	scripts := make([]fakeScript, 3)
	for i := range scripts {
		scripts[i] = scriptToolUse("", toolUseEvent(t, fmt.Sprintf("tu%d", i+1), "read", map[string]any{
			"path": target,
		}))
	}
	prov := newFakeProvider("fake", scripts...)
	a := env.newAgent(prov, func(o *Options) { o.MaxIterations = 3 })

	limits, drain := collectEvents[bus.IterationLimitReached](t, env.Bus, 4)
	_ = limits

	_, err := a.Send(context.Background(), env.sessionID, userText("loop"))
	if !errors.Is(err, ErrIterationLimit) {
		t.Fatalf("want ErrIterationLimit, got %v", err)
	}

	time.Sleep(50 * time.Millisecond)
	got := drain()
	if len(got) != 1 || got[0].Limit != 3 {
		t.Fatalf("want one IterationLimitReached(3), got %+v", got)
	}

	// Last message should be the abort note.
	msgs := readMessages(t, env.Store, env.sessionID)
	last := msgs[len(msgs)-1]
	if last.Role != session.RoleAssistant || !strings.Contains(last.Parts[0].Text, "iteration limit") {
		t.Fatalf("want abort assistant msg, got %+v", last)
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

// 9. Serialised Sends on the same session: second waits for the first.
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

	gotRoles := roles(readMessages(t, env.Store, env.sessionID))
	// Two complete user→assistant pairs.
	want := []string{"user", "assistant", "user", "assistant"}
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
	for i := 0; i < 3; i++ {
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
// a non-zero Options.Reasoning copies that value onto every
// provider.Request issued during the turn — both the initial call and
// the post-tool-call follow-up.
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
	wantReasoning := provider.Reasoning{Effort: "medium"}
	var seen []provider.Reasoning
	var mu sync.Mutex
	prov.onStream = func(req provider.Request) {
		mu.Lock()
		seen = append(seen, req.Reasoning)
		mu.Unlock()
	}

	a := env.newAgent(prov, func(o *Options) { o.Reasoning = wantReasoning })

	if _, err := a.Send(context.Background(), env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 {
		t.Fatalf("want 2 provider calls, got %d (%+v)", len(seen), seen)
	}
	for i, r := range seen {
		if r != wantReasoning {
			t.Errorf("call %d Reasoning=%+v, want %+v", i, r, wantReasoning)
		}
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
		Bus:        env.Bus,
		Store:      env.Store,
		Provider:   prov,
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
		Bus:        env.Bus,
		Store:      env.Store,
		Provider:   prov,
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
