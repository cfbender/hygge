package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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

func TestSubagentStarted_AddsCollapsedBlockUnderSubagentMessage(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Simulate the parent's `subagent` tool call landing first WITH ToolUseID.
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "tool_use_1",
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

	// The subagent tool UIMessage should now carry SubagentID.
	var subagentMsg *uiMessage
	for i := range app.messages {
		if app.messages[i].Role == components.RoleTool && app.messages[i].ToolName == "subagent" {
			subagentMsg = &app.messages[i]
			break
		}
	}
	if subagentMsg == nil {
		t.Fatal("expected a subagent UIMessage")
	}
	if subagentMsg.SubagentID != "sub-1" {
		t.Errorf("expected subagent message to be stamped with SubagentID=sub-1, got %q", subagentMsg.SubagentID)
	}

	out := app.View().Content
	for _, want := range []string{
		"General Subagent \u2014 find LICENSE", // new compact heading
		"ctrl+g",                               // hint
		"view subagent",                        // hint label
	} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q in:\n%s", want, out)
		}
	}
	// Old format must be gone.
	for _, bad := range []string{"subagent[general]", "anthropic/claude-haiku-4-5", "running"} {
		if strings.Contains(out, bad) {
			t.Errorf("view should not contain old format string %q in:\n%s", bad, out)
		}
	}
}

func TestSubagentStreamingAndCompletion(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
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
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
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
	out := app.View().Content
	if !strings.Contains(out, "failed (iteration limit)") {
		t.Errorf("expected failed-banner in view, got:\n%s", out)
	}
}

func TestMultipleSubagentsTrackedIndependently(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Two `subagent` tool calls in the same session.
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-a",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "first",
		Model:           "anthropic/claude-haiku-4-5",
		At:              time.Now().Add(-2 * time.Second),
	})
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
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
	out := app.View().Content
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

// TestSubagentAnchor_ExactToolUseIDMatch verifies that when ToolUseID is
// set on the subagent UIMessage, attachSubagentToSubagentMessage uses it for
// exact matching rather than the recency fallback.
func TestSubagentAnchor_ExactToolUseIDMatch(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Two subagent tool calls for the same session with distinct ToolUseIDs.
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "tu-first",
		Args:      []byte(`{}`),
	})
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "tu-second",
		Args:      []byte(`{}`),
	})

	// SubagentStarted referencing the FIRST tool use.
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-first",
		ParentSessionID: "fg-session",
		ParentMessageID: "tu-first",
		Type:            "general",
		Description:     "first",
		Model:           "x/y",
		At:              time.Now().Add(-2 * time.Second),
	})

	// SubagentStarted referencing the SECOND tool use.
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-second",
		ParentSessionID: "fg-session",
		ParentMessageID: "tu-second",
		Type:            "general",
		Description:     "second",
		Model:           "x/y",
		At:              time.Now().Add(-time.Second),
	})

	// Verify each UIMessage is bound to the correct subagent.
	var firstMsg, secondMsg *uiMessage
	for i := range app.messages {
		msg := &app.messages[i]
		if msg.Role != components.RoleTool || msg.ToolName != "subagent" {
			continue
		}
		switch msg.ToolUseID {
		case "tu-first":
			firstMsg = msg
		case "tu-second":
			secondMsg = msg
		}
	}
	if firstMsg == nil {
		t.Fatal("expected subagent UIMessage for tu-first")
	}
	if secondMsg == nil {
		t.Fatal("expected subagent UIMessage for tu-second")
	}
	if firstMsg.SubagentID != "sub-first" {
		t.Errorf("tu-first message bound to %q, want sub-first", firstMsg.SubagentID)
	}
	if secondMsg.SubagentID != "sub-second" {
		t.Errorf("tu-second message bound to %q, want sub-second", secondMsg.SubagentID)
	}
}

// TestSubagentAnchor_FallbackWarnsWhenToolUseIDAbsent verifies that when
// SubagentStarted.ParentMessageID is empty (no ToolUseID), the
// recency-heuristic fallback fires and the message is still attached.
func TestSubagentAnchor_FallbackWarnsWhenToolUseIDAbsent(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// A subagent tool call WITHOUT ToolUseID (pre-ToolUseID-field scenario).
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "", // intentionally empty — exercises fallback
		Args:      []byte(`{}`),
	})

	// SubagentStarted also has empty ParentMessageID — forces the fallback.
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-fallback",
		ParentSessionID: "fg-session",
		ParentMessageID: "", // intentionally empty
		Type:            "general",
		Description:     "fallback test",
		Model:           "x/y",
		At:              time.Now().Add(-time.Second),
	})

	// Fallback should still attach the subagent to the subagent message.
	var subagentMsg *uiMessage
	for i := range app.messages {
		if app.messages[i].Role == components.RoleTool && app.messages[i].ToolName == "subagent" {
			subagentMsg = &app.messages[i]
			break
		}
	}
	if subagentMsg == nil {
		t.Fatal("expected subagent UIMessage")
	}
	if subagentMsg.SubagentID != "sub-fallback" {
		t.Errorf("fallback did not attach subagent; SubagentID=%q, want sub-fallback", subagentMsg.SubagentID)
	}
}

// --- T2.2 foreground-stack tests -------------------------------------------

func TestCtrlGNoSubagents_Notice(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// No subagents tracked — Ctrl+G should produce a notice but not crash.
	_, cmd := app.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("expected a notice cmd from Ctrl+G with no subagents")
	}
	// Execute the cmd to get the message (setNotice returns a Cmd).
	_ = cmd()
	// App should not have pushed anything onto the stack.
	if len(app.foregroundStack) != 0 {
		t.Errorf("expected empty foreground stack, got %v", app.foregroundStack)
	}
}

func TestCtrlGFollowsIntoSubagent(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "test",
		Model:           "x/y",
		At:              time.Now(),
	})

	// Add a message to the sub-agent transcript.
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-1", Text: "sub-agent reply"})

	// Press Ctrl+G — should push sub-1 onto the foreground stack.
	_, _ = app.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	// Stack should be ["fg-session", "sub-1"] (depth 2).
	if len(app.foregroundStack) != 2 {
		t.Fatalf("expected foreground stack depth 2 after Ctrl+G, got %d: %v", len(app.foregroundStack), app.foregroundStack)
	}
	if app.foregroundStack[1] != "sub-1" {
		t.Errorf("foreground stack top = %q, want sub-1", app.foregroundStack[1])
	}
	if app.foregroundID() != "sub-1" {
		t.Errorf("foregroundID = %q, want sub-1", app.foregroundID())
	}
	if app.rootSessionID() != "fg-session" {
		t.Errorf("rootSessionID = %q, want fg-session", app.rootSessionID())
	}
}

func TestEscAtDepth1_DoesNotPopStack(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// At root depth (no stack), Esc falls through to existing behaviour.
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	// No stack change.
	if len(app.foregroundStack) != 0 {
		t.Errorf("Esc at depth 1 should not modify stack, got %v", app.foregroundStack)
	}
	_ = cmd
}

func TestEscAtDepth2_PopsStack(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "test",
		Model:           "x/y",
		At:              time.Now(),
	})

	// Follow into sub-1.
	_, _ = app.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	// Stack is ["fg-session", "sub-1"] — depth 2.
	if len(app.foregroundStack) != 2 {
		t.Fatalf("expected depth 2 after Ctrl+G, got %d: %v", len(app.foregroundStack), app.foregroundStack)
	}

	// Esc should pop back to root.
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if len(app.foregroundStack) != 1 {
		t.Errorf("expected stack depth 1 after Esc pop, got %d: %v", len(app.foregroundStack), app.foregroundStack)
	}
	if app.foregroundID() != "fg-session" {
		t.Errorf("foregroundID after pop = %q, want fg-session", app.foregroundID())
	}
}

func TestBreadcrumbHiddenAtRootDepth(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	// No pushes — stack depth 0.
	segs := app.breadcrumbSegments()
	if len(segs) > 1 {
		t.Errorf("expected breadcrumb segments ≤ 1 at root, got %v", segs)
	}
}

func TestBreadcrumbVisibleAtDepth2(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "test",
		Model:           "x/y",
		At:              time.Now(),
	})
	_, _ = app.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	segs := app.breadcrumbSegments()
	if len(segs) < 2 {
		t.Errorf("expected ≥ 2 breadcrumb segments, got %v", segs)
	}
}

func TestFooterCostSubscribesToRoot(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Sub-agent cost event — should NOT change costDollars (root is fg-session).
	app.Handle(bus.CostUpdated{
		SessionID:    "sub-1",
		DollarsTotal: 0.05,
	})
	if app.costDollars != 0 {
		t.Errorf("sub-session cost should not update footer; got %v", app.costDollars)
	}

	// Root session cost event — SHOULD update costDollars.
	app.Handle(bus.CostUpdated{
		SessionID:    "fg-session",
		DollarsTotal: 0.12,
	})
	if app.costDollars != 0.12 {
		t.Errorf("root cost event should update footer; got %v, want 0.12", app.costDollars)
	}
}

// TestSubagentAnimLifecycle verifies:
// - SubagentStarted creates an Anim in subagentAnims.
// - SubagentCompleted removes it (stops ticking).
// - Resumed (hydrated) sessions never create an Anim (EndedAt already set).
func TestSubagentAnimLifecycle(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Before any sub-agent, anims map should be empty.
	if len(app.subagentAnims) != 0 {
		t.Errorf("expected empty subagentAnims initially, got %d", len(app.subagentAnims))
	}

	// SubagentStarted: anim must be created.
	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-anim",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "anim test",
		Model:           "x/y",
		At:              time.Now(),
	})
	if len(app.subagentAnims) != 1 {
		t.Errorf("expected 1 anim after SubagentStarted, got %d", len(app.subagentAnims))
	}
	if app.subagentAnims["sub-anim"] == nil {
		t.Error("expected non-nil Anim for sub-anim after SubagentStarted")
	}

	// SubagentCompleted: anim must be removed.
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-anim",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})
	if len(app.subagentAnims) != 0 {
		t.Errorf("expected 0 anims after SubagentCompleted, got %d", len(app.subagentAnims))
	}
}
