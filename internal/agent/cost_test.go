package agent

import (
	"context"
	"testing"
	"time"

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
