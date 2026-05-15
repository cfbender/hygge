package agent

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// slowProvider blocks until released, enabling reliable busy-state tests.
type slowProvider struct {
	name    string
	gate    chan struct{} // close to unblock all outstanding Streams
	calls   atomic.Int32
	scripts []fakeScript
	mu      sync.Mutex
}

func newSlowProvider(name string, gate chan struct{}, scripts ...fakeScript) *slowProvider {
	return &slowProvider{name: name, gate: gate, scripts: scripts}
}

func (s *slowProvider) Name() string { return s.name }
func (s *slowProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	s.calls.Add(1)
	s.mu.Lock()
	var script fakeScript
	if len(s.scripts) > 0 {
		script = s.scripts[0]
		s.scripts = s.scripts[1:]
	} else {
		script = scriptText("default", provider.Usage{})
	}
	s.mu.Unlock()

	ch := make(chan provider.Event, 8)
	go func() {
		defer close(ch)
		// Wait for gate to close (or context cancel) before emitting.
		select {
		case <-s.gate:
		case <-ctx.Done():
			return
		}
		for _, ev := range script.events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}
func (s *slowProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (s *slowProvider) ListModels(_ context.Context) ([]provider.Model, error) { return nil, nil }

// TestQueue_EnqueueWhileBusy verifies that a second Send on a busy session
// is queued (returns nil error and nil message) and QueueCount returns 1.
func TestQueue_EnqueueWhileBusy(t *testing.T) {
	env := newTestEnv(t)

	gate := make(chan struct{})
	prov := newSlowProvider("fake", gate,
		scriptText("first", provider.Usage{}),
		scriptText("second", provider.Usage{}),
	)
	a := env.newAgent(prov)

	ctx := context.Background()

	// Start the first send; it blocks at the gate.
	var firstDone sync.WaitGroup
	firstDone.Add(1)
	go func() {
		defer firstDone.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("first message"))
	}()

	// Give the goroutine time to start and mark the session active.
	time.Sleep(30 * time.Millisecond)

	// Second send while first is in flight: should queue.
	msg, err := a.Send(ctx, env.sessionID, userText("second message"))
	if err != nil {
		t.Fatalf("queued Send returned unexpected error: %v", err)
	}
	if msg != nil {
		t.Fatalf("queued Send returned non-nil message (should be nil for queued): %v", msg)
	}
	if got := a.QueueCount(env.sessionID); got != 1 {
		t.Fatalf("QueueCount = %d, want 1", got)
	}

	prompts := a.QueuedPrompts(env.sessionID)
	if len(prompts) != 1 || prompts[0] != "second message" {
		t.Fatalf("QueuedPrompts = %v, want [\"second message\"]", prompts)
	}

	// Release the gate so both complete.
	close(gate)
	firstDone.Wait()

	// After completion the queue should drain.
	time.Sleep(100 * time.Millisecond)
	if got := a.QueueCount(env.sessionID); got != 0 {
		t.Fatalf("QueueCount after completion = %d, want 0", got)
	}
}

// TestQueue_DequeueAfterCompletion verifies that the queued send runs
// automatically once the active send finishes.
func TestQueue_DequeueAfterCompletion(t *testing.T) {
	env := newTestEnv(t)

	gate := make(chan struct{})
	prov := newSlowProvider("fake", gate,
		scriptText("first reply", provider.Usage{}),
		scriptText("second reply", provider.Usage{}),
	)
	a := env.newAgent(prov)

	ctx := context.Background()

	var firstDone sync.WaitGroup
	firstDone.Add(1)
	go func() {
		defer firstDone.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("first"))
	}()

	time.Sleep(30 * time.Millisecond)

	// Enqueue the second message.
	_, _ = a.Send(ctx, env.sessionID, userText("second"))
	if got := a.QueueCount(env.sessionID); got != 1 {
		t.Fatalf("queue count before release = %d, want 1", got)
	}

	// Release the gate so both complete.
	close(gate)
	firstDone.Wait()

	// The dequeued send runs in a goroutine; give it time to finish.
	time.Sleep(200 * time.Millisecond)

	if got := a.QueueCount(env.sessionID); got != 0 {
		t.Fatalf("queue count after all done = %d, want 0", got)
	}

	msgs, err := env.Store.MessagesForSession(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	// Expect: user, assistant, user, assistant (two complete turns).
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	var gotRoles []string
	for _, m := range msgs {
		gotRoles = append(gotRoles, string(m.Role))
	}
	if !equalStrings(gotRoles, wantRoles) {
		t.Fatalf("want %v, got %v", wantRoles, gotRoles)
	}
}

// TestQueue_ClearQueue verifies that ClearQueue drops pending sends and
// returns the correct count.
func TestQueue_ClearQueue(t *testing.T) {
	env := newTestEnv(t)

	gate := make(chan struct{})
	prov := newSlowProvider("fake", gate, scriptText("reply", provider.Usage{}))
	a := env.newAgent(prov)

	ctx := context.Background()

	// Start a blocking send.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("active"))
	}()
	time.Sleep(30 * time.Millisecond)

	// Enqueue two messages.
	_, _ = a.Send(ctx, env.sessionID, userText("q1"))
	_, _ = a.Send(ctx, env.sessionID, userText("q2"))
	if got := a.QueueCount(env.sessionID); got != 2 {
		t.Fatalf("want queue=2, got %d", got)
	}

	dropped := a.ClearQueue(env.sessionID)
	if dropped != 2 {
		t.Fatalf("ClearQueue returned %d, want 2", dropped)
	}
	if got := a.QueueCount(env.sessionID); got != 0 {
		t.Fatalf("QueueCount after clear = %d, want 0", got)
	}

	// Release and let the active send finish.
	close(gate)
	wg.Wait()

	// Only the original message should have been processed.
	time.Sleep(50 * time.Millisecond)
	msgs, err := env.Store.MessagesForSession(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	wantRoles := []string{"user", "assistant"}
	var gotRoles []string
	for _, m := range msgs {
		gotRoles = append(gotRoles, string(m.Role))
	}
	if !equalStrings(gotRoles, wantRoles) {
		t.Fatalf("want %v, got %v", wantRoles, gotRoles)
	}
}

// TestQueue_ConcurrentEnqueue verifies that concurrent enqueues from multiple
// goroutines don't race.  Run with -race.
func TestQueue_ConcurrentEnqueue(t *testing.T) {
	env := newTestEnv(t)

	gate := make(chan struct{})
	// Provide enough scripts for the active run + all queued.
	var scripts []fakeScript
	for i := 0; i < 6; i++ {
		scripts = append(scripts, scriptText(fmt.Sprintf("reply%d", i), provider.Usage{}))
	}
	prov := newSlowProvider("fake", gate, scripts...)
	a := env.newAgent(prov)

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("active"))
	}()
	time.Sleep(30 * time.Millisecond)

	// Five concurrent enqueues.
	const n = 5
	var enqueuers sync.WaitGroup
	enqueuers.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer enqueuers.Done()
			_, _ = a.Send(ctx, env.sessionID, userText(fmt.Sprintf("q%d", i)))
		}(i)
	}
	enqueuers.Wait()

	// Exact queue count is 5 (all enqueued simultaneously).
	if got := a.QueueCount(env.sessionID); got != n {
		t.Fatalf("QueueCount = %d, want %d (race test)", got, n)
	}

	close(gate)
	wg.Wait()
}

// TestQueue_IsSessionBusy verifies the IsSessionBusy predicate.
func TestQueue_IsSessionBusy(t *testing.T) {
	env := newTestEnv(t)

	gate := make(chan struct{})
	prov := newSlowProvider("fake", gate, scriptText("reply", provider.Usage{}))
	a := env.newAgent(prov)

	ctx := context.Background()

	if a.IsSessionBusy(env.sessionID) {
		t.Fatal("want not-busy before first Send")
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("hi"))
	}()
	time.Sleep(30 * time.Millisecond)

	if !a.IsSessionBusy(env.sessionID) {
		t.Fatal("want busy during Send")
	}

	close(gate)
	wg.Wait()

	time.Sleep(20 * time.Millisecond)
	if a.IsSessionBusy(env.sessionID) {
		t.Fatal("want not-busy after Send completes")
	}
}

// TestQueue_MultiSessionIndependence verifies that queues for different
// sessions do not interfere.
func TestQueue_MultiSessionIndependence(t *testing.T) {
	env := newTestEnv(t)

	// Create a second session.
	ctx := context.Background()
	sess2, err := env.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: env.pwd,
		Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	gate1 := make(chan struct{})
	gate2 := make(chan struct{})
	prov := &dualGateProvider{
		gate1: gate1,
		gate2: gate2,
		name:  "fake",
		scripts: []fakeScript{
			scriptText("r1", provider.Usage{}),
			scriptText("r2", provider.Usage{}),
			scriptText("r3", provider.Usage{}),
			scriptText("r4", provider.Usage{}),
		},
	}
	a := env.newAgent(prov)

	// Start blocking sends on both sessions.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("s1-active"))
	}()
	go func() {
		defer wg.Done()
		_, _ = a.Send(ctx, sess2.ID, userText("s2-active"))
	}()
	time.Sleep(30 * time.Millisecond)

	// Enqueue one message for each session.
	_, _ = a.Send(ctx, env.sessionID, userText("s1-queued"))
	_, _ = a.Send(ctx, sess2.ID, userText("s2-queued"))

	// Queues are independent.
	if got := a.QueueCount(env.sessionID); got != 1 {
		t.Fatalf("session1 queue = %d, want 1", got)
	}
	if got := a.QueueCount(sess2.ID); got != 1 {
		t.Fatalf("session2 queue = %d, want 1", got)
	}

	// Clearing one doesn't affect the other.
	a.ClearQueue(env.sessionID)
	if got := a.QueueCount(env.sessionID); got != 0 {
		t.Fatalf("session1 queue after clear = %d, want 0", got)
	}
	if got := a.QueueCount(sess2.ID); got != 1 {
		t.Fatalf("session2 queue after clearing session1 = %d, want 1", got)
	}

	close(gate1)
	close(gate2)
	wg.Wait()
}

// TestQueue_QueueChangedEvents verifies that bus.QueueChanged events are
// published on enqueue and clear.
func TestQueue_QueueChangedEvents(t *testing.T) {
	env := newTestEnv(t)

	events, drainEvents := collectEvents[bus.QueueChanged](t, env.Bus, 16)
	_ = events

	gate := make(chan struct{})
	prov := newSlowProvider("fake", gate, scriptText("reply", provider.Usage{}))
	a := env.newAgent(prov)

	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.Send(ctx, env.sessionID, userText("active"))
	}()
	time.Sleep(30 * time.Millisecond)

	_, _ = a.Send(ctx, env.sessionID, userText("queued1"))
	_, _ = a.Send(ctx, env.sessionID, userText("queued2"))
	a.ClearQueue(env.sessionID)

	close(gate)
	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	got := drainEvents()
	// We expect at least: enqueue(1), enqueue(2), clear(0), dequeue(0)
	if len(got) < 3 {
		t.Fatalf("want ≥3 QueueChanged events, got %d: %+v", len(got), got)
	}
	// The clear event should have Count=0.
	var hasClear bool
	for _, ev := range got {
		if ev.Count == 0 {
			hasClear = true
		}
	}
	if !hasClear {
		t.Fatalf("want at least one QueueChanged{Count:0}, got: %+v", got)
	}
}

// dualGateProvider is a provider that blocks each Stream call on alternating
// gates (gate1 for odd calls, gate2 for even).  This lets tests control two
// concurrent streams independently.
type dualGateProvider struct {
	name    string
	gate1   chan struct{}
	gate2   chan struct{}
	scripts []fakeScript
	mu      sync.Mutex
	calls   atomic.Int32
}

func (d *dualGateProvider) Name() string { return d.name }
func (d *dualGateProvider) Stream(ctx context.Context, _ provider.Request) (<-chan provider.Event, error) {
	n := d.calls.Add(1)
	d.mu.Lock()
	var script fakeScript
	if len(d.scripts) > 0 {
		script = d.scripts[0]
		d.scripts = d.scripts[1:]
	} else {
		script = scriptText("default", provider.Usage{})
	}
	d.mu.Unlock()

	gate := d.gate1
	if n%2 == 0 {
		gate = d.gate2
	}

	ch := make(chan provider.Event, 8)
	go func() {
		defer close(ch)
		select {
		case <-gate:
		case <-ctx.Done():
			return
		}
		for _, ev := range script.events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}
func (d *dualGateProvider) CountTokens(_ context.Context, _ provider.Request) (int64, error) {
	return 0, nil
}
func (d *dualGateProvider) ListModels(_ context.Context) ([]provider.Model, error) { return nil, nil }

// TestQueue_QueuedSendCompletesAfterCallerCtxCancelled verifies that a queued
// send still executes even if the original caller's context is cancelled before
// the queued send begins.  This mirrors the UI pattern where defer cancel()
// fires as soon as Agent.Send returns for a queued message.
func TestQueue_QueuedSendCompletesAfterCallerCtxCancelled(t *testing.T) {
	env := newTestEnv(t)

	gate := make(chan struct{})
	prov := newSlowProvider("fake", gate,
		scriptText("first reply", provider.Usage{}),
		scriptText("second reply", provider.Usage{}),
	)
	a := env.newAgent(prov)

	// Use a cancellable context that simulates the UI's per-send context.
	callerCtx, callerCancel := context.WithCancel(context.Background())

	var firstDone sync.WaitGroup
	firstDone.Add(1)
	go func() {
		defer firstDone.Done()
		// This is goroutine #1 — it owns callerCtx and calls defer cancel()
		// when Send returns (mirroring the UI's startSend goroutine).
		defer callerCancel()
		_, _ = a.Send(callerCtx, env.sessionID, userText("first"))
	}()

	// Give goroutine #1 time to mark the session active.
	time.Sleep(30 * time.Millisecond)

	// Enqueue the second message while first is in flight.
	// This uses a separate context (doesn't matter — it returns nil,nil immediately).
	_, err := a.Send(context.Background(), env.sessionID, userText("second"))
	if err != nil {
		t.Fatalf("enqueue Send returned error: %v", err)
	}
	if got := a.QueueCount(env.sessionID); got != 1 {
		t.Fatalf("QueueCount = %d, want 1", got)
	}

	// Release the gate so the first send completes.  When the first Send
	// goroutine returns, defer callerCancel() fires, cancelling callerCtx.
	// The queue-drain goroutine must use a.ctx (not callerCtx) so the
	// second send still runs.
	close(gate)
	firstDone.Wait()

	// callerCtx is now cancelled.  Give the queued send time to complete.
	time.Sleep(300 * time.Millisecond)

	if got := a.QueueCount(env.sessionID); got != 0 {
		t.Fatalf("QueueCount after drain = %d, want 0 (queued send must have run)", got)
	}

	// Verify both turns actually ran: user, assistant, user, assistant.
	msgs, err := env.Store.MessagesForSession(context.Background(), env.sessionID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	wantRoles := []string{"user", "assistant", "user", "assistant"}
	var gotRoles []string
	for _, m := range msgs {
		gotRoles = append(gotRoles, string(m.Role))
	}
	if !equalStrings(gotRoles, wantRoles) {
		t.Fatalf("want roles %v, got %v (queued send did not complete)", wantRoles, gotRoles)
	}
}
