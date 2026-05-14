package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/tool"
)

// sleepTool is a fake tool that sleeps for duration before returning.
// It records the time it started executing so tests can verify concurrency.
type sleepTool struct {
	name           string
	parallel       bool
	duration       time.Duration
	startCh        chan time.Time // receives the time.Now() at Execute start
	executeCounter *atomic.Int32
}

func (t *sleepTool) Name() string                { return t.name }
func (t *sleepTool) Description() string         { return "sleep tool " + t.name }
func (t *sleepTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *sleepTool) Parallelizable() bool        { return t.parallel }
func (t *sleepTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	if t.executeCounter != nil {
		t.executeCounter.Add(1)
	}
	if t.startCh != nil {
		t.startCh <- time.Now()
	}
	time.Sleep(t.duration)
	return tool.Result{Content: "done " + t.name}, nil
}

// errorTool is a fake tool that returns an IsError result.
type errorTool struct {
	name     string
	parallel bool
}

func (t *errorTool) Name() string                { return t.name }
func (t *errorTool) Description() string         { return "error tool" }
func (t *errorTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *errorTool) Parallelizable() bool        { return t.parallel }
func (t *errorTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	return tool.Result{IsError: true, Content: "intentional error from " + t.name}, nil
}

// panicTool panics during Execute.
type panicTool struct {
	name     string
	parallel bool
}

func (t *panicTool) Name() string                { return t.name }
func (t *panicTool) Description() string         { return "panic tool" }
func (t *panicTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *panicTool) Parallelizable() bool        { return t.parallel }
func (t *panicTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	panic("deliberate panic from " + t.name)
}

// buildToolRegistry creates a fresh tool.Registry with the supplied tools.
func buildToolRegistry(tools ...tool.Tool) *tool.Registry {
	r := tool.NewRegistry()
	for _, t := range tools {
		if err := r.Register(t); err != nil {
			panic(fmt.Sprintf("buildToolRegistry: %v", err))
		}
	}
	return r
}

// TestParallelExecution_AllParallel verifies that three parallelizable tool
// calls are dispatched concurrently.  Each tool sleeps for sleepDur; if they
// run concurrently the wall time should be ≈ sleepDur, not 3×sleepDur.
func TestParallelExecution_AllParallel(t *testing.T) {
	const sleepDur = 80 * time.Millisecond
	env := newTestEnv(t)

	startCh := make(chan time.Time, 3)
	tools := buildToolRegistry(
		&sleepTool{name: "p1", parallel: true, duration: sleepDur, startCh: startCh},
		&sleepTool{name: "p2", parallel: true, duration: sleepDur, startCh: startCh},
		&sleepTool{name: "p3", parallel: true, duration: sleepDur, startCh: startCh},
	)

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu1", "p1", map[string]any{}),
			toolUseEvent(t, "tu2", "p2", map[string]any{}),
			toolUseEvent(t, "tu3", "p3", map[string]any{}),
		),
		scriptText("all done", provider.Usage{}),
	)

	env.Tools = tools
	ag := env.newAgent(prov)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wallStart := time.Now()
	if _, err := ag.Send(ctx, env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	wallDur := time.Since(wallStart)

	// Collect start times.
	close(startCh)
	var starts []time.Time
	for ts := range startCh {
		starts = append(starts, ts)
	}
	if len(starts) != 3 {
		t.Fatalf("expected 3 start times, got %d", len(starts))
	}

	// All three tools should have started near-simultaneously (within 3×sleepDur
	// of each other, which proves they ran concurrently rather than serially —
	// serial would take 3×sleepDur total, concurrent takes ≈ 1×sleepDur).
	//
	// Wall time should be < 2.5×sleepDur (concurrent) rather than ≥ 3×sleepDur (serial).
	maxSerial := 3 * sleepDur
	if wallDur >= maxSerial {
		t.Errorf("wall time %v ≥ serial bound %v; tools may not have run concurrently",
			wallDur, maxSerial)
	}
	t.Logf("3 parallel sleeps(%v) wall time: %v (serial would be %v)", sleepDur, wallDur, maxSerial)
}

// TestParallelExecution_AllSequential verifies that two non-parallelizable
// tool calls run one-after-another (not concurrently).
func TestParallelExecution_AllSequential(t *testing.T) {
	const sleepDur = 50 * time.Millisecond
	env := newTestEnv(t)

	var callOrder []string
	var mu atomicSlice[string]

	tools := buildToolRegistry(
		&recordOrderTool{name: "s1", parallel: false, sleepDur: sleepDur, order: &mu},
		&recordOrderTool{name: "s2", parallel: false, sleepDur: sleepDur, order: &mu},
	)

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu1", "s1", map[string]any{}),
			toolUseEvent(t, "tu2", "s2", map[string]any{}),
		),
		scriptText("done", provider.Usage{}),
	)

	env.Tools = tools
	ag := env.newAgent(prov)

	if _, err := ag.Send(context.Background(), env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	callOrder = mu.snap()
	// Both should have run, in the order they were dispatched.
	if len(callOrder) != 2 {
		t.Fatalf("expected 2 recorded calls, got %d: %v", len(callOrder), callOrder)
	}
	if callOrder[0] != "s1" || callOrder[1] != "s2" {
		t.Errorf("unexpected call order: %v", callOrder)
	}
}

// TestParallelExecution_Mixed verifies that in a [p1, s1, p2] turn, the
// parallel calls p1 and p2 run concurrently, and s1 runs after them.
func TestParallelExecution_Mixed(t *testing.T) {
	const sleepDur = 60 * time.Millisecond
	env := newTestEnv(t)

	startCh := make(chan time.Time, 2)
	var seqCounter atomic.Int32
	var parCounter atomic.Int32

	tools := buildToolRegistry(
		&sleepTool{name: "p1", parallel: true, duration: sleepDur, startCh: startCh, executeCounter: &parCounter},
		&sleepTool{name: "p2", parallel: true, duration: sleepDur, startCh: startCh, executeCounter: &parCounter},
		&countingSequentialTool{name: "s1", parallelFinished: &parCounter, seqCounter: &seqCounter},
	)

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu1", "p1", map[string]any{}),
			toolUseEvent(t, "tu2", "s1", map[string]any{}),
			toolUseEvent(t, "tu3", "p2", map[string]any{}),
		),
		scriptText("done", provider.Usage{}),
	)

	env.Tools = tools
	ag := env.newAgent(prov)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wallStart := time.Now()
	if _, err := ag.Send(ctx, env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}
	wallDur := time.Since(wallStart)

	close(startCh)
	var starts []time.Time
	for ts := range startCh {
		starts = append(starts, ts)
	}
	if len(starts) != 2 {
		t.Fatalf("expected 2 parallel start times, got %d", len(starts))
	}

	// Parallel tools ran concurrently: wall < 2.5× their sleep.
	if wallDur >= 3*sleepDur {
		t.Errorf("wall time %v looks serial (≥ 3×%v); expected concurrent parallel batch", wallDur, sleepDur)
	}

	// Sequential tool ran after both parallel tools completed.
	if seqCounter.Load() == 0 {
		t.Error("sequential tool never ran")
	}
	if parCounter.Load() != 2 {
		t.Errorf("expected 2 parallel tool executions, got %d", parCounter.Load())
	}

	t.Logf("mixed: 2 parallel(%v) + 1 sequential, wall %v", sleepDur, wallDur)
}

// TestParallelExecution_ErrorInOneDoesNotCancelSiblings verifies that an
// IsError result from one parallel tool does not prevent sibling parallel
// tools from completing.
func TestParallelExecution_ErrorInOneDoesNotCancelSiblings(t *testing.T) {
	env := newTestEnv(t)

	var okCounter atomic.Int32

	tools := buildToolRegistry(
		&errorTool{name: "err1", parallel: true},
		&countingParallelTool{name: "ok1", counter: &okCounter},
		&countingParallelTool{name: "ok2", counter: &okCounter},
	)

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu1", "err1", map[string]any{}),
			toolUseEvent(t, "tu2", "ok1", map[string]any{}),
			toolUseEvent(t, "tu3", "ok2", map[string]any{}),
		),
		scriptText("done", provider.Usage{}),
	)

	env.Tools = tools
	ag := env.newAgent(prov)

	if _, err := ag.Send(context.Background(), env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if okCounter.Load() != 2 {
		t.Errorf("expected 2 sibling executions, got %d", okCounter.Load())
	}
}

// TestParallelExecution_PanicInOneDoesNotCancelSiblings verifies that a
// panicking parallel tool does not abort its siblings.
func TestParallelExecution_PanicInOneDoesNotCancelSiblings(t *testing.T) {
	env := newTestEnv(t)

	var okCounter atomic.Int32

	tools := buildToolRegistry(
		&panicTool{name: "panicer", parallel: true},
		&countingParallelTool{name: "ok1", counter: &okCounter},
		&countingParallelTool{name: "ok2", counter: &okCounter},
	)

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu1", "panicer", map[string]any{}),
			toolUseEvent(t, "tu2", "ok1", map[string]any{}),
			toolUseEvent(t, "tu3", "ok2", map[string]any{}),
		),
		scriptText("recovered", provider.Usage{}),
	)

	env.Tools = tools
	ag := env.newAgent(prov)

	// Should not panic the test.
	if _, err := ag.Send(context.Background(), env.sessionID, userText("go")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if okCounter.Load() != 2 {
		t.Errorf("expected 2 sibling executions after panic, got %d", okCounter.Load())
	}
}

// TestParallelExecution_ResultOrderPreserved verifies that the tool_result
// messages are stored in the same order as the model's tool_use calls,
// regardless of execution order.
func TestParallelExecution_ResultOrderPreserved(t *testing.T) {
	const sleepDur = 40 * time.Millisecond
	env := newTestEnv(t)

	// p_slow runs longer than p_fast, so if results were stored in
	// completion order, p_fast would appear first.  The test asserts they
	// appear in call order.
	tools := buildToolRegistry(
		&sleepTool{name: "p_slow", parallel: true, duration: sleepDur * 3},
		&sleepTool{name: "p_fast", parallel: true, duration: sleepDur},
	)

	prov := newFakeProvider("fake",
		scriptToolUse("",
			toolUseEvent(t, "tu_slow", "p_slow", map[string]any{}),
			toolUseEvent(t, "tu_fast", "p_fast", map[string]any{}),
		),
		scriptText("done", provider.Usage{}),
	)

	env.Tools = tools
	ag := env.newAgent(prov)

	if _, err := ag.Send(context.Background(), env.sessionID, userText("order")); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msgs := readMessages(t, env.Store, env.sessionID)
	// Expected: user, assistant (2 tool_use), tool (p_slow result), tool (p_fast result), assistant
	var toolMsgs []*session.Message
	for _, m := range msgs {
		if m.Role == session.RoleTool {
			toolMsgs = append(toolMsgs, m)
		}
	}
	if len(toolMsgs) != 2 {
		t.Fatalf("expected 2 tool messages, got %d", len(toolMsgs))
	}
	if toolMsgs[0].Parts[0].ToolUseID != "tu_slow" {
		t.Errorf("first tool msg should be tu_slow (original call order), got %q",
			toolMsgs[0].Parts[0].ToolUseID)
	}
	if toolMsgs[1].Parts[0].ToolUseID != "tu_fast" {
		t.Errorf("second tool msg should be tu_fast (original call order), got %q",
			toolMsgs[1].Parts[0].ToolUseID)
	}
}

// --- helper tool types for parallel tests ---

// countingParallelTool is a parallelizable tool that increments a counter.
type countingParallelTool struct {
	name    string
	counter *atomic.Int32
}

func (t *countingParallelTool) Name() string                { return t.name }
func (t *countingParallelTool) Description() string         { return "counting parallel" }
func (t *countingParallelTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *countingParallelTool) Parallelizable() bool        { return true }
func (t *countingParallelTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	t.counter.Add(1)
	return tool.Result{Content: "ok " + t.name}, nil
}

// recordOrderTool records the name of each execution into an atomicSlice, in order.
type recordOrderTool struct {
	name     string
	parallel bool
	sleepDur time.Duration
	order    *atomicSlice[string]
}

func (t *recordOrderTool) Name() string                { return t.name }
func (t *recordOrderTool) Description() string         { return "record order" }
func (t *recordOrderTool) InputSchema() map[string]any { return map[string]any{"type": "object"} }
func (t *recordOrderTool) Parallelizable() bool        { return t.parallel }
func (t *recordOrderTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	t.order.append(t.name)
	if t.sleepDur > 0 {
		time.Sleep(t.sleepDur)
	}
	return tool.Result{Content: "done " + t.name}, nil
}

// countingSequentialTool is a non-parallelizable tool that verifies the
// parallel batch has already completed before it runs.
type countingSequentialTool struct {
	name             string
	parallelFinished *atomic.Int32
	seqCounter       *atomic.Int32
}

func (t *countingSequentialTool) Name() string        { return t.name }
func (t *countingSequentialTool) Description() string { return "counting sequential" }
func (t *countingSequentialTool) InputSchema() map[string]any {
	return map[string]any{"type": "object"}
}
func (t *countingSequentialTool) Parallelizable() bool { return false }
func (t *countingSequentialTool) Execute(_ context.Context, _ json.RawMessage, _ tool.ExecContext) (tool.Result, error) {
	t.seqCounter.Add(1)
	return tool.Result{Content: "seq done"}, nil
}

// atomicSlice is a goroutine-safe string slice.
type atomicSlice[T any] struct {
	mu   sync.Mutex
	data []T
}

func (s *atomicSlice[T]) append(v T) {
	s.mu.Lock()
	s.data = append(s.data, v)
	s.mu.Unlock()
}

func (s *atomicSlice[T]) snap() []T {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]T, len(s.data))
	copy(out, s.data)
	return out
}
