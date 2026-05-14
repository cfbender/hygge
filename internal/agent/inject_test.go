package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/cfbender/hygge/internal/session"
)

// TestInjectMessage_basic verifies that InjectMessage appends a message to
// the session store.
func TestInjectMessage_basic(t *testing.T) {
	env := newTestEnv(t)
	ag := env.newAgent(newFakeProvider("fake"))

	ctx := context.Background()
	err := ag.InjectMessage(ctx, "test-plugin", env.sessionID, "user", "hello from plugin")
	if err != nil {
		t.Fatalf("InjectMessage: %v", err)
	}

	// Verify the message was appended.
	msgs, err := env.Store.MessagesForSession(ctx, env.sessionID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	found := false
	for _, m := range msgs {
		if m.Role == session.RoleUser {
			for _, p := range m.Parts {
				if p.Kind == session.PartText && p.Text == "hello from plugin" {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Error("injected message not found in session store")
	}
}

// TestInjectMessage_invalidRole verifies that an invalid role is rejected.
func TestInjectMessage_invalidRole(t *testing.T) {
	env := newTestEnv(t)
	ag := env.newAgent(newFakeProvider("fake"))

	err := ag.InjectMessage(context.Background(), "test-plugin", env.sessionID, "unknown", "content")
	if err == nil {
		t.Fatal("expected error for invalid role")
	}
}

// TestInjectMessage_emptyContent verifies that empty content is rejected.
func TestInjectMessage_emptyContent(t *testing.T) {
	env := newTestEnv(t)
	ag := env.newAgent(newFakeProvider("fake"))

	err := ag.InjectMessage(context.Background(), "test-plugin", env.sessionID, "user", "")
	if err == nil {
		t.Fatal("expected error for empty content")
	}
}

// TestInjectMessage_cap verifies that the per-turn injection cap is enforced.
func TestInjectMessage_cap(t *testing.T) {
	env := newTestEnv(t)
	ag := env.newAgent(newFakeProvider("fake"))

	ctx := context.Background()
	const injectCap = maxPluginInjectsPerTurn

	// Inject exactly cap messages — should all succeed.
	for i := 0; i < injectCap; i++ {
		if err := ag.InjectMessage(ctx, "test-plugin", env.sessionID, "user", "msg"); err != nil {
			t.Fatalf("InjectMessage #%d: %v", i, err)
		}
	}

	// Next inject should be rate-limited.
	err := ag.InjectMessage(ctx, "test-plugin", env.sessionID, "user", "overflow")
	if !errors.Is(err, ErrInjectCap) {
		t.Errorf("expected ErrInjectCap, got %v", err)
	}
}

// TestInjectMessage_reset verifies that ResetPluginInjectCounters clears the
// cap so new messages can be injected.
func TestInjectMessage_reset(t *testing.T) {
	env := newTestEnv(t)
	ag := env.newAgent(newFakeProvider("fake"))

	ctx := context.Background()
	const injectCap = maxPluginInjectsPerTurn

	for i := 0; i < injectCap; i++ {
		if err := ag.InjectMessage(ctx, "test-plugin", env.sessionID, "user", "msg"); err != nil {
			t.Fatalf("InjectMessage #%d: %v", i, err)
		}
	}

	// Reset counters.
	ag.ResetPluginInjectCounters(env.sessionID)

	// Should be able to inject again.
	if err := ag.InjectMessage(ctx, "test-plugin", env.sessionID, "user", "after reset"); err != nil {
		t.Errorf("InjectMessage after reset: %v", err)
	}
}

// TestInjectMessage_closedAgent verifies that calling InjectMessage on a
// closed agent returns ErrClosed.
func TestInjectMessage_closedAgent(t *testing.T) {
	env := newTestEnv(t)
	ag := env.newAgent(newFakeProvider("fake"))
	_ = ag.Close()

	err := ag.InjectMessage(context.Background(), "test-plugin", env.sessionID, "user", "msg")
	if !errors.Is(err, ErrClosed) {
		t.Errorf("expected ErrClosed, got %v", err)
	}
}
