package ui

// Tests for the busy-state reconciliation loop (busyReconcileTickMsg).
//
// These tests cover the desync scenarios described in the bug report:
// a system lock during active streaming can cause dropped bus events that
// leave the UI and agent with mismatched busy states.
//
// The harness uses the existing testAgentIsSessionBusyFn injection point
// to control the agent's reported busy state without a live *agent.Agent.

import (
	"testing"

	"github.com/cfbender/hygge/internal/bus"
)

// TestBusyReconcile_RecoversWhenAgentBusyButUIIdle verifies the recovery path:
// the agent has an active run but the UI thinks it is idle (TurnStarted was
// dropped).  Sending a busyReconcileTickMsg must flip UI busy=true.
func TestBusyReconcile_RecoversWhenAgentBusyButUIIdle(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	// Give the app a session so rootSessionID returns a non-empty string.
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// Inject stub: agent reports the session is busy.
	app.testAgentIsSessionBusyFn = func(string) bool { return true }

	// Pre-condition: UI thinks it is idle.
	if app.busy {
		t.Fatal("expected app.busy=false initially")
	}

	_, cmd := app.Update(busyReconcileTickMsg{})

	if !app.busy {
		t.Error("expected app.busy=true after reconcile tick with agent busy")
	}
	if app.activeTurns < 1 {
		t.Errorf("expected activeTurns >= 1, got %d", app.activeTurns)
	}
	if app.workingVerb == "" {
		t.Error("expected workingVerb non-empty after recovery")
	}
	// The reconcile tick should re-arm (busy is now true).
	if cmd == nil {
		t.Error("expected non-nil cmd (re-arm + workingVerbTick) after recovery")
	}
}

// TestBusyReconcile_ClearsWhenAgentIdleButUIBusy verifies the cleanup path:
// the agent has no active run but the UI still shows busy (TurnCompleted was
// dropped).  Sending a busyReconcileTickMsg must flip UI busy=false.
func TestBusyReconcile_ClearsWhenAgentIdleButUIBusy(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// Force the UI into a stuck-busy state.
	app.busy = true
	app.activeTurns = 1
	app.workingVerb = "Thinking"
	app.input.SetBusy(true, "")

	// Inject stub: agent reports the session is idle.
	app.testAgentIsSessionBusyFn = func(string) bool { return false }

	// Pre-conditions.
	if app.queueCount != 0 {
		t.Fatalf("precondition: queueCount = %d, want 0", app.queueCount)
	}
	if len(app.queuedDrafts) != 0 {
		t.Fatalf("precondition: queuedDrafts = %d, want 0", len(app.queuedDrafts))
	}

	_, cmd := app.Update(busyReconcileTickMsg{})

	if app.busy {
		t.Error("expected app.busy=false after reconcile tick with agent idle")
	}
	if app.activeTurns != 0 {
		t.Errorf("expected activeTurns=0, got %d", app.activeTurns)
	}
	if app.workingVerb != "" {
		t.Errorf("expected workingVerb empty, got %q", app.workingVerb)
	}
	// No re-arm: nothing to watch (busy=false, queueCount=0, no drafts, no cancel).
	if cmd != nil {
		t.Errorf("expected nil cmd (no re-arm) after full cleanup, got %T", cmd)
	}
}

// TestBusyReconcile_DoesNotClearWhileQueuedDraftsRemain verifies that when
// the UI has locally-queued drafts still pending, the reconcile tick does NOT
// flip busy=false even if the agent reports idle.  The drafts will flush
// shortly and start a new turn.
func TestBusyReconcile_DoesNotClearWhileQueuedDraftsRemain(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// UI: busy with an active turn and a locally-queued draft.
	app.busy = true
	app.activeTurns = 1
	app.workingVerb = "Thinking"
	app.input.SetBusy(true, "")
	app.queuedDrafts = []queuedPromptDraft{{Text: "pending draft"}}

	// Agent: idle.
	app.testAgentIsSessionBusyFn = func(string) bool { return false }

	_, cmd := app.Update(busyReconcileTickMsg{})

	// busy must stay true while queued drafts remain.
	if !app.busy {
		t.Error("expected app.busy=true (queued drafts still pending)")
	}
	// The tick should re-arm because queuedDrafts is non-empty.
	if cmd == nil {
		t.Error("expected non-nil cmd (re-arm) while queued drafts remain")
	}
}

// TestBusyReconcile_DoesNotClearWhileQueueCountNonZero verifies the same
// guard but via queueCount instead of queuedDrafts.
func TestBusyReconcile_DoesNotClearWhileQueueCountNonZero(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	app.busy = true
	app.activeTurns = 1
	app.workingVerb = "Thinking"
	app.input.SetBusy(true, "")
	app.queueCount = 1 // agent queue has a pending send

	app.testAgentIsSessionBusyFn = func(string) bool { return false }

	_, cmd := app.Update(busyReconcileTickMsg{})

	// busy must stay true — queueCount > 0 means a new turn is imminent.
	if !app.busy {
		t.Error("expected app.busy=true (queueCount > 0)")
	}
	// The tick should re-arm because queueCount is non-zero.
	if cmd == nil {
		t.Error("expected non-nil cmd (re-arm) while queueCount > 0")
	}
}

// TestQueueChanged_TriggersReconcileWhenUINotBusy verifies that a QueueChanged
// event with Count > 0 while the UI thinks it's idle schedules a reconcile
// tick.  We then drive that tick and confirm busy is recovered.
func TestQueueChanged_TriggersReconcileWhenUINotBusy(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// UI: idle.
	if app.busy {
		t.Fatal("precondition: expected app.busy=false")
	}

	// Agent: busy (for the subsequent reconcile tick to pick up).
	app.testAgentIsSessionBusyFn = func(string) bool { return true }

	// Deliver QueueChanged with Count > 0 while UI is idle.
	cmd := app.Handle(bus.QueueChanged{SessionID: "test-session", Count: 1, Prompts: []string{"msg"}})
	if cmd == nil {
		t.Fatal("expected non-nil cmd from QueueChanged{Count:1} when UI is idle")
	}

	// Execute the cmd; it should be (or produce) a busyReconcileTickMsg.
	// In tests the tea.Tick timer fires immediately when executed.
	// We drive the tick by sending busyReconcileTickMsg directly.
	_, _ = app.Update(busyReconcileTickMsg{})

	if !app.busy {
		t.Error("expected app.busy=true after driving reconcile tick")
	}
}

// TestBusyReconcile_SelfTerminatesWhenIdle verifies that the tick does NOT
// re-arm itself when there is nothing to watch (agent idle, UI idle, no queue,
// no in-flight cancel).
func TestBusyReconcile_SelfTerminatesWhenIdle(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// Everything idle.
	app.testAgentIsSessionBusyFn = func(string) bool { return false }

	_, cmd := app.Update(busyReconcileTickMsg{})

	if cmd != nil {
		t.Errorf("expected nil cmd (self-terminate) when fully idle, got %T", cmd)
	}
}

// TestBusyReconcile_NoSessionDoesNotArm verifies that the tick is a no-op
// (returns nil) when there is no session yet.
func TestBusyReconcile_NoSessionDoesNotArm(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	// No session: opts.SessionID and foregroundStack are both empty.

	app.testAgentIsSessionBusyFn = func(string) bool { return true }

	_, cmd := app.Update(busyReconcileTickMsg{})

	if cmd != nil {
		t.Errorf("expected nil cmd when no session, got %T", cmd)
	}
	// No busy flip either.
	if app.busy {
		t.Error("expected app.busy=false when no session")
	}
}

// TestTurnStarted_ArmsReconcileTick verifies that bus.TurnStarted includes
// a reconcile tick in its returned cmd batch.
func TestTurnStarted_ArmsReconcileTick(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// Deliver TurnStarted for the foreground session.
	cmd := app.Handle(bus.TurnStarted{SessionID: "test-session"})

	if !app.busy {
		t.Error("expected app.busy=true after TurnStarted")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd from TurnStarted (should include reconcile tick)")
	}
}

// TestBusyReconcile_RearmsDuringInflightCancel verifies that the tick re-arms
// while inflightCancel is set, even when agent and UI agree on busy=false.
// (Edge case: send goroutine is still running but not yet cancelled.)
func TestBusyReconcile_RearmsDuringInflightCancel(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// UI is idle but there is still an in-flight cancel handle.
	app.busy = false
	app.inflightCancel = func() {}

	app.testAgentIsSessionBusyFn = func(string) bool { return false }

	_, cmd := app.Update(busyReconcileTickMsg{})

	if cmd == nil {
		t.Error("expected re-arm cmd while inflightCancel is non-nil")
	}

	// Cleanup: drop the cancel to avoid leaking.
	app.inflightCancel = nil
}
