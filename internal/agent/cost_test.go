package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/fantasy"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// TestRecordUsage_PropagatesParentCostUpdated verifies that when a sub-agent
// calls recordUsage, a CostUpdated event is published for BOTH the
// sub-session (leaf) AND the parent (root).  This is the T2.1 roll-up
// contract: the TUI footer subscribes to the root id and must see a
// CostUpdated carrying the rolled-up total after each sub-agent turn.
func TestRecordUsage_PropagatesParentCostUpdated(t *testing.T) {
	// newTestEnv calls t.Setenv — cannot use t.Parallel().
	env := newTestEnv(t)
	ctx := context.Background()

	// Create a subagent session parented on env.sessionID.
	sub, err := env.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: env.pwd,
		Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
		ParentID:   env.sessionID,
		Kind:       session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("CreateSession subagent: %v", err)
	}

	// Subscribe to CostUpdated events.
	costSub := bus.Subscribe[bus.CostUpdated](env.Bus, bus.SubscribeOptions{BufferSize: 16})
	defer costSub.Unsubscribe()

	prov := newFakeProvider("fake",
		scriptText("done", provider.Usage{InputTokens: 50, OutputTokens: 25}),
	)
	a := env.newAgent(prov)

	// Call recordUsage directly with the sub-session id.
	usage := provider.Usage{InputTokens: 50, OutputTokens: 25}
	a.recordUsage(ctx, sub.ID, "fake-model", usage)

	// Collect CostUpdated events with a short deadline.
	deadline := time.Now().Add(500 * time.Millisecond)
	received := map[string]bus.CostUpdated{}
	for time.Now().Before(deadline) && len(received) < 2 {
		select {
		case ev := <-costSub.C():
			received[ev.SessionID] = ev
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}

	if _, ok := received[sub.ID]; !ok {
		t.Errorf("expected CostUpdated for sub-session %s, got none; received: %v", sub.ID, received)
	}
	if _, ok := received[env.sessionID]; !ok {
		t.Errorf("expected CostUpdated for parent session %s, got none; received: %v", env.sessionID, received)
	}

	// Parent's event should carry the rolled-up total (same delta because
	// only one turn happened).
	parentEv := received[env.sessionID]
	if parentEv.InputTokens != 50 {
		t.Errorf("parent CostUpdated.InputTokens = %d, want 50", parentEv.InputTokens)
	}
	if parentEv.OutputTokens != 25 {
		t.Errorf("parent CostUpdated.OutputTokens = %d, want 25", parentEv.OutputTokens)
	}
}

// TestRecordUsage_NoDoubleCounting verifies that the parent row's total
// after two sub-agent turns is 2× the delta — not 4× (which would happen
// if the update were applied twice at the parent level).
func TestRecordUsage_NoDoubleCounting(t *testing.T) {
	// newTestEnv calls t.Setenv — cannot use t.Parallel().
	env := newTestEnv(t)
	ctx := context.Background()

	sub, err := env.Store.CreateSession(ctx, session.NewSession{
		ProjectDir: env.pwd,
		Model:      session.ModelRef{Provider: "fake", Name: "fake-model"},
		ParentID:   env.sessionID,
		Kind:       session.KindSubagent,
	})
	if err != nil {
		t.Fatalf("CreateSession subagent: %v", err)
	}

	prov := newFakeProvider("fake",
		scriptText("t1", provider.Usage{InputTokens: 10, OutputTokens: 5}),
		scriptText("t2", provider.Usage{InputTokens: 10, OutputTokens: 5}),
	)
	a := env.newAgent(prov)

	usage := provider.Usage{InputTokens: 10, OutputTokens: 5}
	a.recordUsage(ctx, sub.ID, "fake-model", usage)
	a.recordUsage(ctx, sub.ID, "fake-model", usage)

	parent, err := env.Store.GetSession(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("GetSession parent: %v", err)
	}
	// Two turns × 10 input = 20 — each delta applied once to the parent row.
	if parent.Totals.InputTokens != 20 {
		t.Errorf("parent input tokens = %d, want 20", parent.Totals.InputTokens)
	}

	child, err := env.Store.GetSession(ctx, sub.ID)
	if err != nil {
		t.Fatalf("GetSession child: %v", err)
	}
	if child.Totals.InputTokens != 20 {
		t.Errorf("child input tokens = %d, want 20", child.Totals.InputTokens)
	}
}

// TestRecordUsage_EmptyUsageSkipped verifies that zero-usage calls do not
// publish CostUpdated events or mutate totals.
func TestRecordUsage_EmptyUsageSkipped(t *testing.T) {
	// newTestEnv calls t.Setenv — cannot use t.Parallel().
	env := newTestEnv(t)
	ctx := context.Background()

	costSub := bus.Subscribe[bus.CostUpdated](env.Bus, bus.SubscribeOptions{BufferSize: 8})
	defer costSub.Unsubscribe()

	prov := newFakeProvider("fake")
	a := env.newAgent(prov)

	// All-zero usage should be a no-op.
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{})

	// Give a chance for any stray event to arrive.
	select {
	case ev := <-costSub.C():
		t.Errorf("unexpected CostUpdated for empty usage: %+v", ev)
	case <-time.After(50 * time.Millisecond):
		// Correct: no event.
	}
}

// ---------------------------------------------------------------------------
// T2.3 Compaction lifecycle event tests
// ---------------------------------------------------------------------------

// TestCompact_PublishesStartedCompleted verifies that a successful Compact call
// publishes CompactionStarted followed by CompactionCompleted with the expected
// field values.
func TestCompact_PublishesStartedCompleted(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Seed ≥4 messages so Compact doesn't return ErrNothingToCompact.
	prov := newFakeProvider("fake",
		scriptText("a1", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a2", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a3", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		// Compaction summary turn.
		fakeScript{events: []provider.Event{
			{Type: provider.EventTextDelta, Text: "compact summary"},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 20}},
			{Type: provider.EventDone},
		}},
	)
	a := env.newAgent(prov)

	for i := range 3 {
		if _, err := a.Send(ctx, env.sessionID, userText(fmt.Sprintf("q%d", i))); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	startedCh := make(chan bus.CompactionStarted, 4)
	completedCh := make(chan bus.CompactionCompleted, 4)
	startedSub := bus.Subscribe[bus.CompactionStarted](env.Bus, bus.SubscribeOptions{BufferSize: 4})
	completedSub := bus.Subscribe[bus.CompactionCompleted](env.Bus, bus.SubscribeOptions{BufferSize: 4})
	defer startedSub.Unsubscribe()
	defer completedSub.Unsubscribe()

	go func() {
		for ev := range startedSub.C() {
			startedCh <- ev
		}
	}()
	go func() {
		for ev := range completedSub.C() {
			completedCh <- ev
		}
	}()

	marker, err := a.Compact(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if marker == nil {
		t.Fatal("Compact returned nil marker")
	}

	// Give relay goroutines a moment to forward events.
	time.Sleep(100 * time.Millisecond)

	// Verify CompactionStarted.
	var started bus.CompactionStarted
	select {
	case started = <-startedCh:
	default:
		t.Fatal("CompactionStarted not received")
	}
	if started.SessionID != env.sessionID {
		t.Errorf("CompactionStarted.SessionID = %q, want %q", started.SessionID, env.sessionID)
	}
	if started.MessagesToCompact < 4 {
		t.Errorf("CompactionStarted.MessagesToCompact = %d, want ≥4", started.MessagesToCompact)
	}

	// Verify CompactionCompleted.
	var completed bus.CompactionCompleted
	select {
	case completed = <-completedCh:
	default:
		t.Fatal("CompactionCompleted not received")
	}
	if completed.SessionID != env.sessionID {
		t.Errorf("CompactionCompleted.SessionID = %q, want %q", completed.SessionID, env.sessionID)
	}
	if completed.MarkerID != marker.ID {
		t.Errorf("CompactionCompleted.MarkerID = %q, want %q", completed.MarkerID, marker.ID)
	}
	if completed.DurationMs < 0 {
		t.Errorf("CompactionCompleted.DurationMs = %d, want ≥0", completed.DurationMs)
	}
}

func TestCompact_UsesFantasySummaryWhenConfigured(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	prov := newFakeProvider("fake")
	fantasyModel := &fakeFantasyModel{
		provider:    "fake",
		model:       "fake-model",
		generateErr: errors.New("stream must be set to true"),
		onStream: func(call fantasy.Call) {
			if call.MaxOutputTokens != nil {
				t.Fatalf("streaming compaction should omit MaxOutputTokens; got %d", *call.MaxOutputTokens)
			}
		},
		stream: []fantasy.StreamPart{
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "fantasy compact summary"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 42, OutputTokens: 7}},
		},
	}
	a := env.newAgent(prov, func(o *Options) { o.FantasyModel = fantasyModel })
	for i := range 3 {
		if _, err := env.Store.AppendMessage(ctx, env.sessionID, session.NewMessage{Role: session.RoleUser, Parts: userText(fmt.Sprintf("q%d", i))}); err != nil {
			t.Fatalf("append user %d: %v", i, err)
		}
		if _, err := env.Store.AppendMessage(ctx, env.sessionID, session.NewMessage{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: fmt.Sprintf("a%d", i)}}}); err != nil {
			t.Fatalf("append assistant %d: %v", i, err)
		}
	}

	marker, err := a.Compact(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if marker.Summary != "fantasy compact summary" {
		t.Fatalf("marker summary = %q", marker.Summary)
	}
	if marker.InputTokensSaved != 42 {
		t.Fatalf("marker InputTokensSaved = %d, want 42", marker.InputTokensSaved)
	}
	if fantasyModel.calls.Load() != 1 {
		t.Fatalf("fantasy calls = %d, want 1", fantasyModel.calls.Load())
	}
	if prov.calls.Load() != 0 {
		t.Fatalf("provider calls = %d, want 0", prov.calls.Load())
	}
}

// TestCompact_PublishesStartedFailed verifies that a Compact call which fails
// during summary generation publishes CompactionStarted then CompactionFailed.
func TestCompact_PublishesStartedFailed(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Seed ≥4 messages.
	seeder := newFakeProvider("fake",
		scriptText("a1", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a2", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a3", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	a := env.newAgent(seeder)
	for i := range 3 {
		if _, err := a.Send(ctx, env.sessionID, userText(fmt.Sprintf("q%d", i))); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	// Replace the provider with one that returns a stream init error during
	// the compaction summary turn.
	failProv := newFakeProvider("fake",
		fakeScript{initErr: errors.New("provider explosion")},
	)
	a.opts.Provider = failProv

	startedCh := make(chan bus.CompactionStarted, 4)
	failedCh := make(chan bus.CompactionFailed, 4)
	startedSub := bus.Subscribe[bus.CompactionStarted](env.Bus, bus.SubscribeOptions{BufferSize: 4})
	failedSub := bus.Subscribe[bus.CompactionFailed](env.Bus, bus.SubscribeOptions{BufferSize: 4})
	defer startedSub.Unsubscribe()
	defer failedSub.Unsubscribe()
	go func() {
		for ev := range startedSub.C() {
			startedCh <- ev
		}
	}()
	go func() {
		for ev := range failedSub.C() {
			failedCh <- ev
		}
	}()

	_, err := a.Compact(ctx, env.sessionID)
	if err == nil {
		t.Fatal("expected error from Compact, got nil")
	}

	time.Sleep(100 * time.Millisecond)

	select {
	case <-startedCh:
	default:
		t.Fatal("CompactionStarted not received on failure path")
	}
	select {
	case failed := <-failedCh:
		if failed.SessionID != env.sessionID {
			t.Errorf("CompactionFailed.SessionID = %q, want %q", failed.SessionID, env.sessionID)
		}
		if failed.Reason == "" {
			t.Error("CompactionFailed.Reason should not be empty")
		}
	default:
		t.Fatal("CompactionFailed not received")
	}
}

// TestCompact_NothingToCompact_NoEvents verifies that ErrNothingToCompact
// produces NO CompactionStarted or CompactionFailed events.
func TestCompact_NothingToCompact_NoEvents(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake",
		scriptText("only one turn", provider.Usage{InputTokens: 1, OutputTokens: 1}),
	)
	a := env.newAgent(prov)
	if _, err := a.Send(ctx, env.sessionID, userText("q")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	startedSub := bus.Subscribe[bus.CompactionStarted](env.Bus, bus.SubscribeOptions{BufferSize: 4})
	failedSub := bus.Subscribe[bus.CompactionFailed](env.Bus, bus.SubscribeOptions{BufferSize: 4})
	defer startedSub.Unsubscribe()
	defer failedSub.Unsubscribe()

	_, err := a.Compact(ctx, env.sessionID)
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("want ErrNothingToCompact, got %v", err)
	}

	// Give events a chance to arrive (they shouldn't).
	time.Sleep(30 * time.Millisecond)

	select {
	case ev := <-startedSub.C():
		t.Errorf("unexpected CompactionStarted: %+v", ev)
	default:
	}
	select {
	case ev := <-failedSub.C():
		t.Errorf("unexpected CompactionFailed: %+v", ev)
	default:
	}
}

// ---------------------------------------------------------------------------
// T2.3 Threshold suggestion tests
// ---------------------------------------------------------------------------

// collectThresholdEvents drains CompactionRequested events from sub for up
// to d, returning those with Source=="threshold" for the given sessionID.
func collectThresholdEvents(sub *bus.Subscription[bus.CompactionRequested], sessionID string, d time.Duration) []bus.CompactionRequested {
	var out []bus.CompactionRequested
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-sub.C():
			if !ok {
				return out
			}
			if ev.SessionID == sessionID && ev.Source == "threshold" {
				out = append(out, ev)
			}
		case <-deadline:
			return out
		}
	}
}

// TestThreshold_FiresOnce verifies that the threshold-suggestion event fires
// exactly once when usage first crosses the threshold, not on every subsequent
// call while above it.
func TestThreshold_FiresOnce(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Context window = 1000 tokens; threshold at 80% (800 tokens).
	prov := newFakeProvider("fake")
	a := env.newAgent(prov, func(o *Options) {
		o.ContextWindow = 1000
		o.CompactionThresholdPct = 80
	})

	reqSub := bus.Subscribe[bus.CompactionRequested](env.Bus, bus.SubscribeOptions{BufferSize: 8})
	defer reqSub.Unsubscribe()

	// Simulate three turns above threshold: 850, 900, 950 input tokens used.
	for _, tok := range []int64{850, 900, 950} {
		a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: tok})
	}

	events := collectThresholdEvents(reqSub, env.sessionID, 100*time.Millisecond)
	if len(events) != 1 {
		t.Errorf("threshold suggestion fired %d times, want exactly 1", len(events))
	}
}

// TestThreshold_ReiresAfterHysteresis verifies that after usage drops below
// threshold - 5 and then rises again, the suggestion fires a second time.
func TestThreshold_ReiresAfterHysteresis(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	// Context window = 1000; threshold 80% = 800.
	prov := newFakeProvider("fake")
	a := env.newAgent(prov, func(o *Options) {
		o.ContextWindow = 1000
		o.CompactionThresholdPct = 80
	})

	reqSub := bus.Subscribe[bus.CompactionRequested](env.Bus, bus.SubscribeOptions{BufferSize: 8})
	defer reqSub.Unsubscribe()

	// First crossing: above threshold (85%).
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 850})

	// Drop below threshold - 5 = 75%: 740 tokens (< 750).
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 740})

	// Second crossing: back above threshold (85%).
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 850})

	events := collectThresholdEvents(reqSub, env.sessionID, 100*time.Millisecond)
	if len(events) != 2 {
		t.Errorf("expected 2 threshold suggestions (one per crossing), got %d", len(events))
	}
}

// TestThreshold_RefiresAfterCompaction verifies that after a successful Compact
// the threshold-fired flag is cleared, so the suggestion fires again if usage
// is still above threshold.
func TestThreshold_RefiresAfterCompaction(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake",
		scriptText("a1", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a2", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a3", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		fakeScript{events: []provider.Event{
			{Type: provider.EventTextDelta, Text: "summary"},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 10}},
			{Type: provider.EventDone},
		}},
	)
	a := env.newAgent(prov, func(o *Options) {
		o.ContextWindow = 1000
		o.CompactionThresholdPct = 80
	})

	for i := range 3 {
		if _, err := a.Send(ctx, env.sessionID, userText(fmt.Sprintf("q%d", i))); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	reqSub := bus.Subscribe[bus.CompactionRequested](env.Bus, bus.SubscribeOptions{BufferSize: 8})
	defer reqSub.Unsubscribe()

	// First threshold crossing.
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 850})

	// Compact — this resets the thresholdFired flag.
	if _, err := a.Compact(ctx, env.sessionID); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Second crossing with same agent — should fire again.
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 850})

	events := collectThresholdEvents(reqSub, env.sessionID, 100*time.Millisecond)
	if len(events) < 2 {
		t.Errorf("expected ≥2 threshold suggestions (one before, one after compact), got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Context-usage storage tests
// ---------------------------------------------------------------------------

// TestRecordUsage_StoresLatestUsagePerSession verifies that after recordUsage,
// latestUsageFor returns the expected usedTokens and pctUsed for the session.
func TestRecordUsage_StoresLatestUsagePerSession(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake")
	a := env.newAgent(prov, func(o *Options) {
		o.ContextWindow = 1000
	})

	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 300, CacheReadTokens: 100, OutputTokens: 200})

	usedTokens, pctUsed := a.latestUsageFor(env.sessionID)
	if usedTokens != 600 {
		t.Errorf("latestUsageFor.usedTokens = %d, want 600", usedTokens)
	}
	// 600/1000 = 0.6. Cache-read tokens occupy context and count toward usage.
	if pctUsed != 0.6 {
		t.Errorf("latestUsageFor.pctUsed = %v, want 0.6", pctUsed)
	}
}

// TestRecordUsage_LatestUsageUpdatesOnSubsequentCalls verifies that the stored
// usage is replaced (not accumulated) on subsequent calls.
func TestRecordUsage_LatestUsageUpdatesOnSubsequentCalls(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake")
	a := env.newAgent(prov, func(o *Options) {
		o.ContextWindow = 1000
	})

	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 100, OutputTokens: 50})
	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 400, OutputTokens: 100})

	usedTokens, _ := a.latestUsageFor(env.sessionID)
	// Latest call: 400+100 = 500 (not cumulative 650).
	if usedTokens != 500 {
		t.Errorf("latestUsageFor.usedTokens = %d, want 500 (last call only)", usedTokens)
	}
}

// TestRecordUsage_NoStorageWhenContextWindowZero verifies that when
// ContextWindow is 0 the pct stored is also 0.
func TestRecordUsage_NoStorageWhenContextWindowZero(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake")
	a := env.newAgent(prov) // default ContextWindow == 0

	a.recordUsage(ctx, env.sessionID, "fake-model", provider.Usage{InputTokens: 300, OutputTokens: 200})

	usedTokens, pctUsed := a.latestUsageFor(env.sessionID)
	if usedTokens != 500 {
		t.Errorf("latestUsageFor.usedTokens = %d, want 500", usedTokens)
	}
	// pct should be 0 when window is 0 (division skipped).
	if pctUsed != 0 {
		t.Errorf("latestUsageFor.pctUsed = %v, want 0 when ContextWindow=0", pctUsed)
	}
}

// TestCompact_ClearsLatestUsage verifies that a successful Compact resets the
// stored latest usage so the first post-compaction envelope is clean.
func TestCompact_ClearsLatestUsage(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake",
		scriptText("a1", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a2", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		scriptText("a3", provider.Usage{InputTokens: 1, OutputTokens: 1}),
		fakeScript{events: []provider.Event{
			{Type: provider.EventTextDelta, Text: "compact summary"},
			{Type: provider.EventUsage, Usage: provider.Usage{InputTokens: 20}},
			{Type: provider.EventDone},
		}},
	)
	a := env.newAgent(prov, func(o *Options) {
		o.ContextWindow = 1000
	})

	for i := range 3 {
		if _, err := a.Send(ctx, env.sessionID, userText(fmt.Sprintf("q%d", i))); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	// After sends there should be some usage stored.
	usedBefore, _ := a.latestUsageFor(env.sessionID)
	if usedBefore == 0 {
		t.Fatal("expected non-zero latestUsage after sends")
	}

	if _, err := a.Compact(ctx, env.sessionID); err != nil {
		t.Fatalf("Compact: %v", err)
	}

	usedAfter, pctAfter := a.latestUsageFor(env.sessionID)
	if usedAfter != 0 || pctAfter != 0 {
		t.Errorf("latestUsage not cleared after Compact: usedTokens=%d pctUsed=%v", usedAfter, pctAfter)
	}
}

// TestSend_ContextUsageAppearsInEnvelope verifies the end-to-end threading:
// after the first turn the second Send's model-facing user message should
// include latest-known usage inside <context_window>.
func TestSend_ContextUsageAppearsInEnvelope(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	prov := newFakeProvider("fake",
		scriptText("turn1 reply", provider.Usage{InputTokens: 300, OutputTokens: 200}),
		scriptText("turn2 reply", provider.Usage{InputTokens: 400, OutputTokens: 100}),
	)
	// Set a context window so pctUsed is meaningful.
	model := &fakeFantasyModel{provider: "fake", model: "fake-model", streamBatches: [][]fantasy.StreamPart{
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "turn1 reply"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 300, CacheReadTokens: 100, OutputTokens: 200}},
		},
		{
			{Type: fantasy.StreamPartTypeTextStart, ID: "txt"},
			{Type: fantasy.StreamPartTypeTextDelta, ID: "txt", Delta: "turn2 reply"},
			{Type: fantasy.StreamPartTypeTextEnd, ID: "txt"},
			{Type: fantasy.StreamPartTypeFinish, FinishReason: fantasy.FinishReasonStop, Usage: fantasy.Usage{InputTokens: 400, OutputTokens: 100}},
		},
	}}
	a := env.newAgent(prov, func(o *Options) {
		o.FantasyModel = model
		o.ContextWindow = 1000
	})

	// Turn 1 — no prior usage, so envelope should not have latest-known usage.
	var firstEnvelope string
	model.onStream = func(call fantasy.Call) {
		for _, msg := range call.Prompt {
			if msg.Role == fantasy.MessageRoleUser {
				if text, ok := msg.Content[0].(fantasy.TextPart); ok {
					firstEnvelope = text.Text
				}
			}
		}
	}
	if _, err := a.Send(ctx, env.sessionID, userText("turn 1")); err != nil {
		t.Fatalf("Send turn 1: %v", err)
	}
	if strings.Contains(firstEnvelope, "<latest_known_used_tokens>") {
		t.Fatalf("first turn envelope should not have latest-known usage (no prior usage):\n%s", firstEnvelope)
	}

	// Turn 2 — should include usage from turn 1 (300+100+200 = 600 tokens = 60.0%).
	var secondEnvelope string
	model.onStream = func(call fantasy.Call) {
		for _, msg := range call.Prompt {
			if msg.Role == fantasy.MessageRoleUser {
				if text, ok := msg.Content[0].(fantasy.TextPart); ok {
					secondEnvelope = text.Text
				}
			}
		}
	}
	if _, err := a.Send(ctx, env.sessionID, userText("turn 2")); err != nil {
		t.Fatalf("Send turn 2: %v", err)
	}
	if !strings.Contains(secondEnvelope, "<latest_known_used_tokens>600</latest_known_used_tokens>") {
		t.Errorf("second turn envelope missing <latest_known_used_tokens>600</latest_known_used_tokens>:\n%s", secondEnvelope)
	}
	if !strings.Contains(secondEnvelope, "<latest_known_used_percent>60.0</latest_known_used_percent>") {
		t.Errorf("second turn envelope missing <latest_known_used_percent>60.0</latest_known_used_percent>:\n%s", secondEnvelope)
	}
}
