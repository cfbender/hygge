package permission

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/config"
	"github.com/cfbender/hygge/internal/state"
)

// newEngine constructs an Engine with a fresh bus and a temp state dir.
// Returns the engine, the bus, and the state.LoadOptions so tests can read
// state.json directly.
func newEngine(t *testing.T, cfg *config.Config) (*Engine, *bus.Bus, state.LoadOptions) {
	t.Helper()
	b := bus.New()
	t.Cleanup(b.Close)

	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}

	e, err := New(EngineOptions{
		Bus:    b,
		Config: cfg,
		State:  stateOpts,
		Clock:  func() time.Time { return time.Unix(1700000000, 0) },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)
	return e, b, stateOpts
}

// fakeResponder subscribes to PermissionAsked and publishes a PermissionReplied
// using the supplied decide function.  Returns a stop function that
// unsubscribes and waits for the goroutine to exit.
func fakeResponder(t *testing.T, b *bus.Bus, decide func(asked bus.PermissionAsked) bus.PermissionReplied) func() {
	t.Helper()
	// Generous buffer so concurrent-Ask stress tests do not drop asks.
	sub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 256})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for asked := range sub.C() {
			reply := decide(asked)
			reply.RequestID = asked.RequestID
			bus.Publish(b, reply)
		}
	}()

	return func() {
		sub.Unsubscribe()
		<-done
	}
}

func defaultCfg() *config.Config {
	return testConfig(config.PermAsk, config.PermAsk, config.PermAsk, config.PermDeny)
}

// --- 7: pre-decided allow returns immediately, no bus publish ---------------

func TestAsk_PreDecidedAllow(t *testing.T) {
	// file.read inside PWD is auto-allowed by the synthesised default rule.
	e, b, _ := newEngine(t, defaultCfg())

	askedSub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 4})
	defer askedSub.Unsubscribe()

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   "/repo/main.go",
		Pwd:      "/repo",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("Action: got %v, want allow", d.Action)
	}
	if d.Scope != ScopeOnce {
		t.Errorf("Scope: got %v, want once", d.Scope)
	}
	if !strings.Contains(d.Reason, "default") {
		t.Errorf("Reason: got %q, want it to mention default", d.Reason)
	}

	// Drain briefly to confirm nothing was published.
	select {
	case asked := <-askedSub.C():
		t.Errorf("PermissionAsked was published unexpectedly: %+v", asked)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- 8: pre-decided deny returns immediately, no bus publish ----------------

func TestAsk_PreDecidedDeny(t *testing.T) {
	// Network is denied by default.
	e, b, _ := newEngine(t, defaultCfg())

	askedSub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 4})
	defer askedSub.Unsubscribe()

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryNetwork,
		Target:   "https://example.com",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionDeny {
		t.Errorf("Action: got %v, want deny", d.Action)
	}

	select {
	case asked := <-askedSub.C():
		t.Errorf("PermissionAsked was published unexpectedly: %+v", asked)
	case <-time.After(50 * time.Millisecond):
	}
}

// --- 9: secrets denylist beats user-allow rules -----------------------------

func TestAsk_SecretsDenylist_BeatsConfigAllow(t *testing.T) {
	// Set everything to "allow" — secrets denylist must still deny.
	e, _, _ := newEngine(t, testConfig(config.PermAllow, config.PermAllow, config.PermAllow, config.PermAllow))

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileRead,
		Target:   "/home/me/.aws/credentials",
		Pwd:      "/home/me/proj",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionDeny {
		t.Errorf("Action: got %v, want deny", d.Action)
	}
	if !strings.Contains(d.Reason, "secrets-denylist") {
		t.Errorf("Reason: got %q, want it to mention secrets-denylist", d.Reason)
	}
}

func TestYolo_AllowsNonSecretRequestsWithoutPrompt(t *testing.T) {
	e, b, _ := newEngine(t, testConfig(config.PermDeny, config.PermDeny, config.PermDeny, config.PermDeny))
	e.SetYolo(true)
	askedSub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 4})
	defer askedSub.Unsubscribe()

	d, err := e.Ask(context.Background(), Request{Category: CategoryShell, Target: "rm -rf build"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow || d.Reason != "yolo mode" {
		t.Fatalf("decision = %+v, want yolo allow", d)
	}
	select {
	case asked := <-askedSub.C():
		t.Fatalf("PermissionAsked published in yolo mode: %+v", asked)
	default:
	}
}

func TestYolo_StillDeniesSecrets(t *testing.T) {
	e, b, _ := newEngine(t, testConfig(config.PermAllow, config.PermAllow, config.PermAllow, config.PermAllow))
	e.SetYolo(true)
	askedSub := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 4})
	defer askedSub.Unsubscribe()

	d, err := e.Ask(context.Background(), Request{Category: CategoryFileRead, Target: "/repo/.env"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionDeny || !strings.Contains(d.Reason, "secrets-denylist") {
		t.Fatalf("decision = %+v, want secrets deny", d)
	}
	select {
	case asked := <-askedSub.C():
		t.Fatalf("PermissionAsked published for yolo secret deny: %+v", asked)
	default:
	}
}

// --- 10: Ask path with a fake responder -------------------------------------

func TestAsk_FlowsThroughBus(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once", At: time.Now()}
	})
	defer stop()

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryShell,
		Target:   "ls -la",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("Action: got %v, want allow", d.Action)
	}
	if d.Scope != ScopeOnce {
		t.Errorf("Scope: got %v, want once", d.Scope)
	}
}

// --- 11: mismatched RequestIDs ignored --------------------------------------

func TestAsk_IgnoresMismatchedRequestID(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	// Subscribe to PermissionAsked so we know when to inject noise.
	asks := bus.Subscribe[bus.PermissionAsked](b, bus.SubscribeOptions{BufferSize: 4})
	defer asks.Unsubscribe()

	// Noise: publish replies with random IDs before the real one arrives.
	done := make(chan struct{})
	go func() {
		defer close(done)
		asked := <-asks.C()
		// First publish three noise replies that don't match.
		for range 3 {
			bus.Publish(b, bus.PermissionReplied{
				RequestID: "noise-" + asked.RequestID + "-x",
				Decision:  "deny", Scope: "once", At: time.Now(),
			})
		}
		// Then the real one.
		bus.Publish(b, bus.PermissionReplied{
			RequestID: asked.RequestID,
			Decision:  "allow", Scope: "once", At: time.Now(),
		})
	}()

	d, err := e.Ask(context.Background(), Request{Category: CategoryShell, Target: "ls"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("Action: got %v, want allow", d.Action)
	}
	<-done
}

// --- 12: ctx cancellation unblocks Ask --------------------------------------

func TestAsk_ContextCancellation(t *testing.T) {
	e, _, _ := newEngine(t, defaultCfg())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := e.Ask(ctx, Request{Category: CategoryShell, Target: "rm -rf /"})
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Ask: got %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask did not return after context cancellation")
	}
}

// --- 13: session-scope cache -------------------------------------------------

func TestAsk_SessionScopeCache(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	var asks atomic.Int32
	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		asks.Add(1)
		return bus.PermissionReplied{Decision: "allow", Scope: "session", At: time.Now()}
	})
	defer stop()

	req := Request{SessionID: "S1", Category: CategoryShell, Target: "go test ./..."}
	d1, err := e.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("first Ask: %v", err)
	}
	if d1.Action != ActionAllow || d1.Scope != ScopeSession {
		t.Errorf("first decision: got %+v, want allow/session", d1)
	}

	d2, err := e.Ask(context.Background(), req)
	if err != nil {
		t.Fatalf("second Ask: %v", err)
	}
	if d2.Action != ActionAllow {
		t.Errorf("second decision: got %+v, want allow (cached)", d2)
	}
	if got := asks.Load(); got != 1 {
		t.Errorf("bus asks: got %d, want 1 (cache should have served the second call)", got)
	}

	// Session cache is shared across sessions (subagents inherit).
	other := req
	other.SessionID = "S2"
	d3, err := e.Ask(context.Background(), other)
	if err != nil {
		t.Fatalf("other Ask: %v", err)
	}
	if d3.Action != ActionAllow {
		t.Errorf("cross-session decision: got %+v, want allow (shared cache)", d3)
	}
	if got := asks.Load(); got != 1 {
		t.Errorf("after cross-session ask: got %d, want 1 (cache shared)", got)
	}
}

// --- 14: always-scope reply persists to state -------------------------------

func TestAsk_AlwaysScopePersists(t *testing.T) {
	e, b, stateOpts := newEngine(t, defaultCfg())

	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})
	defer stop()

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileWrite,
		Target:   "/repo/src/main.go",
		Pwd:      "/repo",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow || d.Scope != ScopeAlways {
		t.Errorf("decision: got %+v, want allow/always", d)
	}

	s, err := state.Load(stateOpts)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	if len(s.AllowedRules) != 1 {
		t.Fatalf("AllowedRules: got %d, want 1", len(s.AllowedRules))
	}
	r := s.AllowedRules[0]
	// Pattern is promoted to a directory glob.
	if r.Category != "file.write" || r.Pattern != "/repo/src/**" {
		t.Errorf("rule: got %+v, want file.write @ /repo/src/**", r)
	}
	if r.CreatedAt == 0 {
		t.Error("CreatedAt should be populated from engine clock")
	}
}

// --- 15: concurrent Asks don't cross-pollinate ------------------------------

func TestAsk_Concurrent_NoCrosstalk(t *testing.T) {
	e, b, _ := newEngine(t, defaultCfg())

	// Responder echoes the Target so we can verify each Ask gets its own reply.
	// We map RequestID -> SessionID via the asked event.
	stop := fakeResponder(t, b, func(asked bus.PermissionAsked) bus.PermissionReplied {
		// Vary decision by target so cross-talk would be detectable.
		decision := "allow"
		if strings.HasSuffix(asked.Target, "deny") {
			decision = "deny"
		}
		return bus.PermissionReplied{Decision: decision, Scope: "once", At: time.Now()}
	})
	defer stop()

	const N = 20
	var wg sync.WaitGroup
	results := make([]Decision, N)
	for i := range N {
		wg.Go(func() {
			target := "cmd-allow"
			if i%2 == 0 {
				target = "cmd-deny"
			}
			d, err := e.Ask(context.Background(), Request{
				SessionID: "S",
				Category:  CategoryShell,
				Target:    target,
			})
			if err != nil {
				t.Errorf("Ask[%d]: %v", i, err)
				return
			}
			results[i] = d
		})
	}
	wg.Wait()

	for i, d := range results {
		want := ActionAllow
		if i%2 == 0 {
			want = ActionDeny
		}
		if d.Action != want {
			t.Errorf("results[%d]: got %v, want %v", i, d.Action, want)
		}
	}
}

// --- Close behaviour --------------------------------------------------------

func TestAsk_AfterClose(t *testing.T) {
	e, _, _ := newEngine(t, defaultCfg())
	e.Close()
	_, err := e.Ask(context.Background(), Request{Category: CategoryShell, Target: "ls"})
	if !errors.Is(err, ErrEngineClosed) {
		t.Errorf("after Close: got %v, want ErrEngineClosed", err)
	}
	// Close again is a no-op.
	e.Close()
}

// --- bus closed before reply ------------------------------------------------

func TestAsk_BusClosed(t *testing.T) {
	b := bus.New()
	dir := t.TempDir()
	e, err := New(EngineOptions{
		Bus:    b,
		Config: defaultCfg(),
		State:  state.LoadOptions{HomeDir: dir},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	// Close the bus while Ask is waiting.
	errCh := make(chan error, 1)
	go func() {
		_, err := e.Ask(context.Background(), Request{Category: CategoryShell, Target: "ls"})
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	b.Close()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrBusClosed) {
			t.Errorf("got %v, want ErrBusClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Ask did not return after bus closed")
	}
}

// --- New error paths --------------------------------------------------------

func TestNew_RequiresBus(t *testing.T) {
	_, err := New(EngineOptions{})
	if err == nil {
		t.Fatal("expected error when Bus is nil")
	}
}

func TestNew_StateLoadError(t *testing.T) {
	// Force state.Load to fail by pointing at a state dir with a corrupt file.
	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}
	p, _ := state.Path(stateOpts)
	if err := writeStateFile(p, "not json"); err != nil {
		t.Fatalf("seed corrupt: %v", err)
	}

	_, err := New(EngineOptions{
		Bus:    bus.New(),
		Config: defaultCfg(),
		State:  stateOpts,
	})
	if err == nil {
		t.Fatal("expected error from corrupt state")
	}
	if !errors.Is(err, state.ErrCorruptState) {
		t.Errorf("got %v, want ErrCorruptState", err)
	}
}

func TestNew_DefaultClock(t *testing.T) {
	e, err := New(EngineOptions{
		Bus:    bus.New(),
		Config: defaultCfg(),
		State:  state.LoadOptions{HomeDir: t.TempDir()},
		// Clock omitted on purpose so the default branch is exercised.
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)
	if e.clock == nil {
		t.Fatal("default clock not installed")
	}
}

// --- StateLoad with existing allow-rule respected ---------------------------

func TestAsk_StateAllowRuleHonored(t *testing.T) {
	dir := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: dir}
	if err := state.AddAllowRule(state.AllowRule{
		Category: "shell",
		Pattern:  "git status",
	}, stateOpts); err != nil {
		t.Fatalf("seed: %v", err)
	}

	b := bus.New()
	t.Cleanup(b.Close)
	e, err := New(EngineOptions{
		Bus:    b,
		Config: defaultCfg(),
		State:  stateOpts,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	d, err := e.Ask(context.Background(), Request{Category: CategoryShell, Target: "git status"})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("Action: got %v, want allow (state rule)", d.Action)
	}
	if !strings.Contains(d.Reason, "state") {
		t.Errorf("Reason: got %q, want mention of state", d.Reason)
	}
}

// --- Always-allow with state save error: warning only, decision still allow -

// Note: state save can fail when the parent directory is unwritable.  This
// case logs a warning and still returns the allow decision (covered by the
// happy-path test).  We exercise the warning branch by pointing the engine
// at a state opts whose target dir is a non-writable parent.
func TestAsk_AlwaysScope_SaveErrorIsWarning(t *testing.T) {
	// Build a state dir where AddAllowRule will fail (read-only home).
	b := bus.New()
	t.Cleanup(b.Close)
	base := t.TempDir()
	stateOpts := state.LoadOptions{HomeDir: base}
	e, err := New(EngineOptions{Bus: b, Config: defaultCfg(), State: stateOpts})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(e.Close)

	// Now make the state dir read-only so the subsequent Save fails.
	if err := makeStateDirReadOnly(stateOpts); err != nil {
		t.Skipf("cannot make state dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = restoreStateDir(stateOpts) })

	stop := fakeResponder(t, b, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "always", At: time.Now()}
	})
	defer stop()

	d, err := e.Ask(context.Background(), Request{
		Category: CategoryFileWrite,
		Target:   "/repo/main.go",
		Pwd:      "/repo",
	})
	if err != nil {
		t.Fatalf("Ask: %v", err)
	}
	if d.Action != ActionAllow {
		t.Errorf("decision: got %v, want allow even on save failure", d.Action)
	}
}
