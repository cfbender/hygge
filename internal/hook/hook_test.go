package hook

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// fakeHook is a test double for Hook.
type fakeHook struct {
	name    string
	events  []Event
	mode    Mode
	timeout time.Duration
	runFn   func(ctx context.Context, in Input) (Action, error)
}

func (f *fakeHook) Name() string           { return f.name }
func (f *fakeHook) Description() string    { return "fake" }
func (f *fakeHook) Source() string         { return "test" }
func (f *fakeHook) Events() []Event        { return f.events }
func (f *fakeHook) Mode() Mode             { return f.mode }
func (f *fakeHook) Timeout() time.Duration { return f.timeout }
func (f *fakeHook) Run(ctx context.Context, in Input) (Action, error) {
	if f.runFn != nil {
		return f.runFn(ctx, in)
	}
	return Action{Decision: DecisionAllow}, nil
}

// allowHook returns a hook that always allows.
func allowHook(name string, events ...Event) *fakeHook {
	return &fakeHook{name: name, events: events, mode: ModeSync}
}

// denyHook returns a hook that always denies with a fixed reason.
func denyHook(name, reason string, events ...Event) *fakeHook {
	return &fakeHook{
		name:   name,
		events: events,
		mode:   ModeSync,
		runFn: func(_ context.Context, _ Input) (Action, error) {
			return Action{Decision: DecisionDeny, Reason: reason}, nil
		},
	}
}

// modifyToolHook returns a hook that replaces tool input with raw.
func modifyToolHook(name string, raw json.RawMessage) *fakeHook {
	return &fakeHook{
		name:   name,
		events: []Event{EventPreTool},
		mode:   ModeSync,
		runFn: func(_ context.Context, _ Input) (Action, error) {
			return Action{Decision: DecisionModify, ModifiedToolInput: raw}, nil
		},
	}
}

// ---------- Registry.RunPre -------------------------------------------------

func TestRunPre_AllAllow(t *testing.T) {
	reg := New()
	_ = reg.Register(allowHook("a", EventPreTool))
	_ = reg.Register(allowHook("b", EventPreTool))

	in := Input{Event: EventPreTool, ToolName: "bash"}
	_, dec, denier, reason, warns := reg.RunPre(context.Background(), EventPreTool, in)
	if dec != DecisionAllow {
		t.Fatalf("want Allow, got %s (denier=%s reason=%s)", dec, denier, reason)
	}
	if len(warns) != 0 {
		t.Fatalf("want no warns, got %d", len(warns))
	}
}

func TestRunPre_DenyShortCircuits(t *testing.T) {
	reg := New()
	secondCalled := false
	_ = reg.Register(denyHook("guard", "blocked by policy", EventPreTool))
	_ = reg.Register(&fakeHook{
		name:   "second",
		events: []Event{EventPreTool},
		mode:   ModeSync,
		runFn: func(_ context.Context, _ Input) (Action, error) {
			secondCalled = true
			return Action{Decision: DecisionAllow}, nil
		},
	})

	in := Input{Event: EventPreTool, ToolName: "bash"}
	_, dec, denier, reason, _ := reg.RunPre(context.Background(), EventPreTool, in)
	if dec != DecisionDeny {
		t.Fatalf("want Deny, got %s", dec)
	}
	if denier != "guard" {
		t.Fatalf("want denier=guard, got %q", denier)
	}
	if reason != "blocked by policy" {
		t.Fatalf("want reason, got %q", reason)
	}
	if secondCalled {
		t.Fatal("second hook must NOT run after a deny")
	}
}

func TestRunPre_ModifyAccumulates(t *testing.T) {
	first := json.RawMessage(`{"cmd":"ls"}`)
	second := json.RawMessage(`{"cmd":"pwd"}`)

	reg := New()
	_ = reg.Register(modifyToolHook("m1", first))
	_ = reg.Register(modifyToolHook("m2", second))

	in := Input{Event: EventPreTool, ToolName: "bash", ToolInput: json.RawMessage(`{}`)}
	out, dec, _, _, _ := reg.RunPre(context.Background(), EventPreTool, in)
	if dec != DecisionAllow {
		t.Fatalf("want Allow, got %s", dec)
	}
	// m2 ran after m1, so out.ToolInput should reflect m2's modification.
	if string(out.ToolInput) != string(second) {
		t.Fatalf("want %s, got %s", second, out.ToolInput)
	}
}

func TestRunPre_NilRegistry(t *testing.T) {
	var reg *Registry
	in := Input{Event: EventPreTool}
	out, dec, _, _, warns := reg.RunPre(context.Background(), EventPreTool, in)
	if dec != DecisionAllow {
		t.Fatalf("nil registry must Allow, got %s", dec)
	}
	if out.Event != in.Event {
		t.Fatal("nil registry must return input unchanged")
	}
	_ = warns
}

// ---------- Registry.RunPost -------------------------------------------------

func TestRunPost_SyncModifyAccumulates(t *testing.T) {
	reg := New()
	_ = reg.Register(&fakeHook{
		name:   "r1",
		events: []Event{EventPostTool},
		mode:   ModeSync,
		runFn: func(_ context.Context, _ Input) (Action, error) {
			return Action{
				Decision:           DecisionModify,
				ModifiedToolResult: &ToolResult{IsError: false, Content: "redacted-1"},
			}, nil
		},
	})
	_ = reg.Register(&fakeHook{
		name:   "r2",
		events: []Event{EventPostTool},
		mode:   ModeSync,
		runFn: func(_ context.Context, in Input) (Action, error) {
			// r2 should see the result as modified by r1.
			if in.ToolResult == nil || in.ToolResult.Content != "redacted-1" {
				t.Errorf("r2: expected to see r1's modification")
			}
			return Action{
				Decision:           DecisionModify,
				ModifiedToolResult: &ToolResult{IsError: false, Content: "redacted-2"},
			}, nil
		},
	})

	in := Input{
		Event:      EventPostTool,
		ToolResult: &ToolResult{IsError: false, Content: "original"},
	}
	out, warns := reg.RunPost(context.Background(), EventPostTool, in)
	if len(warns) != 0 {
		t.Fatalf("unexpected warns: %v", warns)
	}
	if out.ToolResult == nil || out.ToolResult.Content != "redacted-2" {
		t.Fatalf("want redacted-2, got %+v", out.ToolResult)
	}
}

func TestRunPost_AsyncDropsModify(t *testing.T) {
	modifyCalled := false
	reg := New()
	_ = reg.Register(&fakeHook{
		name:   "async-m",
		events: []Event{EventPostTool},
		mode:   ModeAsync,
		runFn: func(_ context.Context, _ Input) (Action, error) {
			modifyCalled = true
			return Action{
				Decision:           DecisionModify,
				ModifiedToolResult: &ToolResult{Content: "should-be-ignored"},
			}, nil
		},
	})

	in := Input{Event: EventPostTool, ToolResult: &ToolResult{Content: "original"}}
	out, _ := reg.RunPost(context.Background(), EventPostTool, in)
	reg.Close() // wait for async
	if out.ToolResult.Content != "original" {
		t.Fatalf("async modify must not change sync output, got %q", out.ToolResult.Content)
	}
	if !modifyCalled {
		t.Fatal("async hook must have been dispatched")
	}
}

// ---------- Close -----------------------------------------------------------

func TestRegistryClose_WaitsForAsync(t *testing.T) {
	reg := New()
	started := make(chan struct{})
	done := make(chan struct{})
	_ = reg.Register(&fakeHook{
		name:   "slow-async",
		events: []Event{EventPostTool},
		mode:   ModeAsync,
		runFn: func(_ context.Context, _ Input) (Action, error) {
			close(started)
			<-done
			return Action{Decision: DecisionAllow}, nil
		},
	})

	in := Input{Event: EventPostTool}
	reg.RunPost(context.Background(), EventPostTool, in)

	<-started // goroutine is running

	// Close in background while the goroutine is blocking.
	closeDone := make(chan struct{})
	go func() {
		reg.Close()
		close(closeDone)
	}()

	// Let the async goroutine finish.
	close(done)

	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return after async goroutine finished")
	}
}

func TestRegistryClose_NilSafe(_ *testing.T) {
	var reg *Registry
	reg.Close() // must not panic
}

// ---------- All / For -------------------------------------------------------

func TestRegistryFor(t *testing.T) {
	reg := New()
	_ = reg.Register(allowHook("a", EventPreTool, EventPostTool))
	_ = reg.Register(allowHook("b", EventPreTool))

	preTool := reg.For(EventPreTool)
	if len(preTool) != 2 {
		t.Fatalf("want 2 pre_tool hooks, got %d", len(preTool))
	}
	postTool := reg.For(EventPostTool)
	if len(postTool) != 1 {
		t.Fatalf("want 1 post_tool hook, got %d", len(postTool))
	}
	preMsg := reg.For(EventPreMessage)
	if len(preMsg) != 0 {
		t.Fatalf("want 0 pre_message hooks, got %d", len(preMsg))
	}
}

func TestRegistryAll_Deduplicates(t *testing.T) {
	reg := New()
	_ = reg.Register(allowHook("multi", EventPreTool, EventPostTool))
	_ = reg.Register(allowHook("single", EventPreTool))

	all := reg.All()
	if len(all) != 2 {
		t.Fatalf("want 2 unique hooks, got %d", len(all))
	}
}
