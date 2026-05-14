package ui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/ui/components"
)

// makeForegroundApp builds an App with a fixed foreground session id
// so the Stage C filter routes events to either the primary path or a
// matching sub-agent.
func makeForegroundApp(t *testing.T) (*App, *bus.Bus) {
	t.Helper()
	app, b := newTestApp(t)
	app.opts.SessionID = "fg-session"
	return app, b
}

func TestSubagentStarted_AddsCollapsedBlockUnderTaskMessage(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Simulate the parent's `task` tool call landing first.
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "task",
		Args:      []byte(`{"subagent_type":"general","description":"find LICENSE"}`),
	})

	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "tool_use_1",
		Type:            "general",
		Description:     "find LICENSE",
		Model:           "anthropic/claude-haiku-4-5",
		At:              time.Now().Add(-2 * time.Second),
	})

	if got := len(app.subagents); got != 1 {
		t.Fatalf("expected 1 tracked subagent, got %d", got)
	}
	st := app.subagents["sub-1"]
	if st == nil {
		t.Fatal("expected SubagentState for sub-1")
	}
	if st.Expanded {
		t.Errorf("expected block collapsed by default")
	}
	if !st.IsRunning() {
		t.Errorf("expected state to be running")
	}

	// The task tool UIMessage should now carry SubagentID.
	var taskMsg *uiMessage
	for i := range app.messages {
		if app.messages[i].Role == components.RoleTool && app.messages[i].ToolName == "task" {
			taskMsg = &app.messages[i]
			break
		}
	}
	if taskMsg == nil {
		t.Fatal("expected a task UIMessage")
	}
	if taskMsg.SubagentID != "sub-1" {
		t.Errorf("expected task message to be stamped with SubagentID=sub-1, got %q", taskMsg.SubagentID)
	}

	out := app.View()
	for _, want := range []string{"▸", "task[general]", "anthropic/claude-haiku-4-5", "running"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q in:\n%s", want, out)
		}
	}
}

func TestSubagentToggleExpandsLatest(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "task", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "go",
		Model:           "x/y",
		At:              time.Now().Add(-time.Second),
	})

	if app.subagents["sub-1"].Expanded {
		t.Fatal("precondition: expected collapsed")
	}

	// Press Ctrl+T -> Expanded becomes true.
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !app.subagents["sub-1"].Expanded {
		t.Errorf("expected Expanded=true after Ctrl+T")
	}
	out := app.View()
	if !strings.Contains(out, "▾") {
		t.Errorf("expected expanded chevron in view, got:\n%s", out)
	}

	// Toggle back.
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if app.subagents["sub-1"].Expanded {
		t.Errorf("expected Expanded=false after second Ctrl+T")
	}
}

func TestSubagentToggleNoOpWhenNoBlocks(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	// No panic / no state change.
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if len(app.subagents) != 0 {
		t.Errorf("expected no subagents tracked")
	}
}

func TestSubagentStreamingAndCompletion(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "task", Args: []byte(`{}`)})
	startedAt := time.Now().Add(-3 * time.Second)
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "go",
		Model:           "anthropic/claude-haiku-4-5",
		At:              startedAt,
	})

	// Stream text inside the sub-session.
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-1", Text: "I'll look. "})
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-1", Text: "Found it."})

	st := app.subagents["sub-1"]
	if got := len(st.Messages); got != 1 {
		t.Fatalf("expected 1 streaming assistant message, got %d", got)
	}
	if st.Messages[0].Raw != "I'll look. Found it." {
		t.Errorf("Raw = %q, want concatenated", st.Messages[0].Raw)
	}
	if !st.Messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=true mid-stream")
	}

	// Sub-session tool call.
	app.Handle(bus.ToolCallRequested{SessionID: "sub-1", ToolName: "grep", Args: []byte(`{"pattern":"LICENSE"}`)})
	app.Handle(bus.ToolCallCompleted{SessionID: "sub-1", ToolName: "grep", Result: []byte("./LICENSE:1:MIT")})

	if got := len(st.Messages); got != 2 {
		t.Fatalf("expected 2 sub-messages after tool, got %d", got)
	}
	if st.Messages[1].Role != components.RoleTool {
		t.Errorf("expected second sub-message to be a tool, got %v", st.Messages[1].Role)
	}
	if !strings.Contains(st.Messages[1].Raw, "MIT") {
		t.Errorf("tool result missing in sub-message: %q", st.Messages[1].Raw)
	}

	// Running cost via CostUpdated tagged to the sub-session.
	app.Handle(bus.CostUpdated{
		SessionID:    "sub-1",
		InputTokens:  1200,
		OutputTokens: 300,
		DollarsTotal: 0.0023,
	})
	if st.Cost != 0.0023 || st.InputTokens != 1200 || st.OutputTokens != 300 {
		t.Errorf("cost not propagated: cost=%v in=%d out=%d", st.Cost, st.InputTokens, st.OutputTokens)
	}
	// Foreground footer cost should NOT have changed.
	if app.costDollars != 0 {
		t.Errorf("foreground cost should remain 0 when sub-session cost arrives, got %v", app.costDollars)
	}

	// Completion overrides cost and freezes EndedAt.
	endedAt := startedAt.Add(12*time.Second + 400*time.Millisecond)
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		DurationMs:      12400,
		CostUSD:         0.0041,
		At:              endedAt,
	})
	if st.IsRunning() {
		t.Errorf("expected state not running after Completed")
	}
	if st.Cost != 0.0041 {
		t.Errorf("Completed.CostUSD should override running counter; got %v", st.Cost)
	}
}

func TestSubagentIterationLimitMarksFailed(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "task", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "looped",
		Model:           "x/y",
		At:              time.Now().Add(-30 * time.Second),
	})
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		HitIterLimit:    true,
		At:              time.Now(),
	})
	st := app.subagents["sub-1"]
	if !st.HitIterLimit {
		t.Fatal("expected HitIterLimit=true")
	}
	out := app.View()
	if !strings.Contains(out, "failed (iteration limit)") {
		t.Errorf("expected failed-banner in view, got:\n%s", out)
	}
}

func TestMultipleSubagentsTrackedIndependently(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Two `task` tool calls in the same session.
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "task", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-a",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "first",
		Model:           "anthropic/claude-haiku-4-5",
		At:              time.Now().Add(-2 * time.Second),
	})
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "task", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-b",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "second",
		Model:           "openai/gpt-4o-mini",
		At:              time.Now().Add(-1 * time.Second),
	})

	if got := len(app.subagents); got != 2 {
		t.Fatalf("expected 2 sub-agents, got %d", got)
	}

	// Streaming for sub-a should go ONLY to sub-a.
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-a", Text: "alpha"})
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-b", Text: "beta"})

	if got := app.subagents["sub-a"].Messages[0].Raw; got != "alpha" {
		t.Errorf("sub-a got %q, want alpha", got)
	}
	if got := app.subagents["sub-b"].Messages[0].Raw; got != "beta" {
		t.Errorf("sub-b got %q, want beta", got)
	}

	// Toggle expands the LATEST (sub-b).
	app.Update(tea.KeyMsg{Type: tea.KeyCtrlT})
	if !app.subagents["sub-b"].Expanded {
		t.Errorf("expected latest (sub-b) to expand on Ctrl+T")
	}
	if app.subagents["sub-a"].Expanded {
		t.Errorf("expected sub-a to remain collapsed")
	}
}

func TestSubagentNotDescendedFromForegroundIsIgnored(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	// Sub-agent dispatched from a totally unrelated session id.
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "alien-1",
		ParentSessionID: "other-session",
		Type:            "general",
		Description:     "should not appear",
		Model:           "x/y",
		At:              time.Now(),
	})
	if _, ok := app.subagents["alien-1"]; ok {
		t.Errorf("expected non-descendant sub-agent to be ignored")
	}
	// Its events should also be dropped.
	app.Handle(bus.AssistantTextDelta{SessionID: "alien-1", Text: "should not appear"})
	out := app.View()
	if strings.Contains(out, "should not appear") {
		t.Errorf("expected no leak from non-descendant sub-agent, got:\n%s", out)
	}
}

func TestSubagentTickReissuedWhileRunningStopsOnCompletion(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "x",
		Model:           "a/b",
		At:              time.Now(),
	})
	// Mid-run tick: should re-issue.
	_, cmd := app.Update(subagentTickMsg{SubSessionID: "sub-1"})
	if cmd == nil {
		t.Fatal("expected re-issued tick while running")
	}
	// Completed: subsequent ticks must NOT re-issue.
	app.Handle(bus.SubagentCompleted{SubSessionID: "sub-1", ParentSessionID: "fg-session", At: time.Now()})
	_, cmd = app.Update(subagentTickMsg{SubSessionID: "sub-1"})
	if cmd != nil {
		t.Errorf("expected NO re-issued tick after completion")
	}
}

func TestSubagentMessageAppendedFinalisesStream(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "go",
		Model:           "x/y",
		At:              time.Now().Add(-time.Second),
	})
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-1", Text: "draft"})
	app.Handle(bus.MessageAppended{SessionID: "sub-1", Role: "assistant", MessageID: "m1"})

	st := app.subagents["sub-1"]
	if st.Messages[0].IsStreaming {
		t.Errorf("expected nested assistant message marked non-streaming after MessageAppended")
	}
}

func TestEmptyForegroundFallsBackToLegacyRouting(t *testing.T) {
	// Pre-Stage-C behaviour: events with no SessionID flow into the
	// primary path because no foreground id has been bound yet.
	t.Parallel()
	app, _ := newTestApp(t)
	// app.opts.SessionID is "" -- the lazy-create code path.
	app.Handle(bus.AssistantTextDelta{Text: "legacy"})
	if len(app.messages) != 1 || app.messages[0].Raw != "legacy" {
		t.Errorf("expected legacy delta to flow into primary path; messages=%+v", app.messages)
	}
}

func TestSubagentToolErrorRecorded(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "go",
		Model:           "x/y",
		At:              time.Now(),
	})
	app.Handle(bus.ToolCallRequested{SessionID: "sub-1", ToolName: "read", Args: []byte(`{"path":"/missing"}`)})
	app.Handle(bus.ToolCallCompleted{SessionID: "sub-1", ToolName: "read", Err: "no such file"})

	st := app.subagents["sub-1"]
	if len(st.Messages) == 0 {
		t.Fatal("expected at least one nested message")
	}
	last := st.Messages[len(st.Messages)-1]
	if !last.IsError {
		t.Errorf("expected IsError on failed sub-tool")
	}
	if last.Raw != "no such file" {
		t.Errorf("expected error text in Raw, got %q", last.Raw)
	}
}

func TestSubagentSynthesisedToolResultWhenNoStreaming(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "go",
		Model:           "x/y",
		At:              time.Now(),
	})
	// Completed arrives without a matching Requested -- synthesise path.
	app.Handle(bus.ToolCallCompleted{SessionID: "sub-1", ToolName: "grep", Result: []byte("hit")})
	st := app.subagents["sub-1"]
	if len(st.Messages) != 1 {
		t.Fatalf("expected synthesised tool message, got %d entries", len(st.Messages))
	}
	if st.Messages[0].Raw != "hit" || st.Messages[0].Role != components.RoleTool {
		t.Errorf("unexpected synthesised message: %+v", st.Messages[0])
	}
}

func TestRouteToSubagentEmptyIDReturnsFalse(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	if app.routeToSubagent("") {
		t.Errorf("empty session id must not route to a sub-agent")
	}
}

func TestIsInForegroundChainEmptyParentReturnsFalse(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	if app.isInForegroundChain("") {
		t.Errorf("empty parent id must not be considered in-chain")
	}
}
