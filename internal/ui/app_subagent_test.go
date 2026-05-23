package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/components/anim"
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

	startedAt := time.Now().Add(-2 * time.Second)
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "tool_use_1",
		Type:            "general",
		Description:     "find LICENSE",
		InitialPrompt:   "Find the LICENSE file and summarize it.",
		Model:           "anthropic/claude-haiku-4-5",
		At:              startedAt,
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
	if len(st.Messages) != 1 {
		t.Fatalf("expected initial prompt message, got %d messages", len(st.Messages))
	}
	if st.Messages[0].Role != components.RoleUser {
		t.Fatalf("initial prompt role: got %q want %q", st.Messages[0].Role, components.RoleUser)
	}
	if st.Messages[0].Raw != "Find the LICENSE file and summarize it." {
		t.Fatalf("initial prompt text: got %q", st.Messages[0].Raw)
	}
	if !st.Messages[0].Timestamp.Equal(startedAt) {
		t.Fatalf("initial prompt timestamp: got %v want %v", st.Messages[0].Timestamp, startedAt)
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
	if st.Messages[0].ModelName != "anthropic/claude-haiku-4-5" {
		t.Errorf("streaming subagent assistant ModelName = %q", st.Messages[0].ModelName)
	}

	app.Handle(bus.MessageAppended{SessionID: "sub-1", Role: "assistant"})
	if st.Messages[0].IsStreaming {
		t.Errorf("expected subagent assistant message finalized")
	}
	if st.Messages[0].ModelName != "anthropic/claude-haiku-4-5" {
		t.Errorf("final subagent assistant ModelName = %q", st.Messages[0].ModelName)
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

func TestSubagentViewBashClickExpandsOutput(t *testing.T) {
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", ToolUseID: "parent-tu", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "parent-tu",
		Type:            "general",
		Description:     "inspect logs",
		Model:           "anthropic/claude-haiku-4-5",
		At:              time.Now(),
	})
	app.Handle(bus.ToolCallRequested{
		SessionID: "sub-1",
		ToolName:  "bash",
		ToolUseID: "bash-tu",
		Args:      []byte(`{"command":"go test ./..."}`),
	})
	app.Handle(bus.ToolCallCompleted{
		SessionID:  "sub-1",
		ToolName:   "bash",
		ToolUseID:  "bash-tu",
		Result:     []byte("line 1\nline 2\nline 3\nline 4\nline 5\nline 6"),
		DurationMs: 10,
	})

	app.pushForeground("sub-1")
	_ = app.View()
	if len(app.toolHitZones) != 1 {
		t.Fatalf("toolHitZones len = %d, want 1", len(app.toolHitZones))
	}
	if app.toolHitZones[0].ToolUseID != "bash-tu" {
		t.Fatalf("ToolUseID = %q, want bash-tu", app.toolHitZones[0].ToolUseID)
	}

	visibleLine := app.toolHitZones[0].StartLine
	if offset := app.msgViewport.YOffset(); visibleLine < offset {
		visibleLine = offset
	}
	y := headerHeight + visibleLine - app.msgViewport.YOffset()
	if got := app.toolAtScreen(2, y); got != "bash-tu" {
		t.Fatalf("toolAtScreen = %q, want bash-tu (y=%d zone=%+v offset=%d)", got, y, app.toolHitZones[0], app.msgViewport.YOffset())
	}
	app.Update(tea.MouseClickMsg{X: 2, Y: y, Button: tea.MouseLeft})
	if !app.expandedTools["bash-tu"] {
		t.Fatal("expected bash tool to expand after clicking its subagent-view block")
	}
}

func TestForegroundSubagentEventsStayInSubagentTranscript(t *testing.T) {
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", ToolUseID: "parent-tu", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "parent-tu",
		Type:            "general",
		Description:     "inspect logs",
		Model:           "anthropic/claude-haiku-4-5",
		At:              time.Now(),
	})
	parentLen := len(app.messages)

	app.pushForeground("sub-1")
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-1", Text: "working inside subagent"})
	app.Handle(bus.ToolCallRequested{
		SessionID: "sub-1",
		ToolName:  "bash",
		ToolUseID: "bash-tu",
		Args:      []byte(`{"command":"pwd"}`),
	})

	if len(app.messages) != parentLen {
		t.Fatalf("parent transcript len = %d, want %d; subagent events leaked into parent", len(app.messages), parentLen)
	}
	st := app.subagents["sub-1"]
	if st == nil {
		t.Fatal("subagent state missing")
	}
	if len(st.Messages) != 2 {
		t.Fatalf("subagent transcript len = %d, want 2", len(st.Messages))
	}
	if st.Messages[0].Role != components.RoleAssistant || !strings.Contains(st.Messages[0].Raw, "working inside subagent") {
		t.Fatalf("assistant delta not routed to subagent transcript: %+v", st.Messages[0])
	}
	if st.Messages[1].Role != components.RoleTool || st.Messages[1].ToolUseID != "bash-tu" {
		t.Fatalf("tool request not routed to subagent transcript: %+v", st.Messages[1])
	}
}

// TestParentEventsAccumulateWhileViewingSubagent is the HYGGE-15 regression
// guard: when the user has followed into a subagent, events emitted by the
// parent (root) session must still update a.messages so the parent's history
// is intact when the user pops back out. Before the fix, isForeground() only
// accepted the top of the foreground stack, which silently dropped every
// parent-thread bus event while a subagent was being viewed.
func TestParentEventsAccumulateWhileViewingSubagent(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Open a subagent and follow into it.
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "parent-tu",
		Args:      []byte(`{}`),
	})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "parent-tu",
		Type:            "general",
		Description:     "background work",
		Model:           "anthropic/claude-haiku-4-5",
		At:              time.Now(),
	})
	app.pushForeground("sub-1")
	if !app.viewingSubagent() {
		t.Fatal("precondition: expected to be viewing subagent")
	}
	parentLenBefore := len(app.messages)

	// While viewing the subagent, the parent (root) session continues to
	// produce events: tool calls + assistant text. These must land in
	// a.messages so they are visible when the user pops back.
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "bash",
		ToolUseID: "parent-bash-tu",
		Args:      []byte(`{"command":"ls"}`),
	})
	app.Handle(bus.ToolCallCompleted{
		SessionID:  "fg-session",
		ToolName:   "bash",
		ToolUseID:  "parent-bash-tu",
		Result:     []byte("file.txt"),
		DurationMs: 1,
	})
	app.Handle(bus.AssistantTextDelta{
		SessionID: "fg-session",
		Text:      "parent response",
	})

	if len(app.messages) <= parentLenBefore {
		t.Fatalf("parent transcript len = %d, want > %d; parent events were dropped while viewing subagent",
			len(app.messages), parentLenBefore)
	}

	// Find the parent bash tool row and assistant text in the parent buffer.
	var sawBash, sawText bool
	for i := range app.messages {
		msg := app.messages[i]
		if msg.Role == components.RoleTool && msg.ToolUseID == "parent-bash-tu" {
			sawBash = true
			if msg.IsStreaming {
				t.Errorf("expected parent bash row to be finalised, still streaming: %+v", msg)
			}
		}
		if msg.Role == components.RoleAssistant && strings.Contains(msg.Raw, "parent response") {
			sawText = true
		}
	}
	if !sawBash {
		t.Error("parent bash tool row missing from a.messages while viewing subagent")
	}
	if !sawText {
		t.Error("parent assistant delta missing from a.messages while viewing subagent")
	}

	// The subagent transcript must NOT contain the parent events.
	st := app.subagents["sub-1"]
	if st == nil {
		t.Fatal("subagent state missing")
	}
	for _, msg := range st.Messages {
		if msg.ToolUseID == "parent-bash-tu" {
			t.Errorf("parent tool row leaked into subagent transcript: %+v", msg)
		}
		if msg.Role == components.RoleAssistant && strings.Contains(msg.Raw, "parent response") {
			t.Errorf("parent assistant delta leaked into subagent transcript: %+v", msg)
		}
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

func TestEscAtDepth2_DoesNotPopStack(t *testing.T) {
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

	if len(app.foregroundStack) != 2 {
		t.Fatalf("expected depth 2 after Ctrl+G, got %d: %v", len(app.foregroundStack), app.foregroundStack)
	}

	// Esc should NOT pop the subagent view.
	_, _ = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})

	if len(app.foregroundStack) != 2 {
		t.Errorf("Esc should not pop subagent view; expected stack depth 2, got %d: %v", len(app.foregroundStack), app.foregroundStack)
	}
	if app.foregroundID() != "sub-1" {
		t.Errorf("foregroundID should remain sub-1 after Esc; got %q", app.foregroundID())
	}
}

func TestCtrlGAtDepth2_PopsStack(t *testing.T) {
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

	// Ctrl+G again should pop back to root.
	_, _ = app.Update(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})

	if len(app.foregroundStack) != 1 {
		t.Errorf("expected stack depth 1 after Ctrl+G pop, got %d: %v", len(app.foregroundStack), app.foregroundStack)
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

// TestSubagentAnimStepMsgInvalidatesMsgCache is a regression test for the bug
// where the subagent anim.StepMsg handler advanced the animation frame but did
// not call invalidateMsgCache(), meaning the new frame was never rendered.
//
// Steps:
//  1. Start a subagent (creates Anim, seeds subagentAnims map).
//  2. Prime the message cache by calling View() so msgCacheValid = true.
//  3. Synthesise the correct StepMsg for the running anim (using Anim.ID()).
//  4. Drive Update with that StepMsg — must invalidate the cache.
//  5. Verify msgCacheValid is false (cache was invalidated).
//  6. Verify the re-arm Cmd is non-nil (tick loop continues).
func TestSubagentAnimStepMsgInvalidatesMsgCache(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-cache",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "cache invalidation test",
		Model:           "x/y",
		At:              time.Now(),
	})

	if len(app.subagentAnims) != 1 {
		t.Fatalf("expected 1 anim after SubagentStarted, got %d", len(app.subagentAnims))
	}
	an := app.subagentAnims["sub-cache"]
	if an == nil {
		t.Fatal("expected non-nil Anim for sub-cache")
	}

	// Prime the cache: View() sets msgCacheValid = true.
	_ = app.View()
	if !app.msgCacheValid {
		t.Fatal("expected msgCacheValid=true after View()")
	}

	// Synthesise a StepMsg targeting this exact Anim using the public ID() accessor.
	step := anim.StepMsg{ID: an.ID()}

	_, cmd := app.Update(step)

	// The cache must be invalidated so the new animation frame is rendered.
	if app.msgCacheValid {
		t.Error("expected msgCacheValid=false after anim StepMsg — animation frame would not be rendered without cache invalidation")
	}

	// The re-arm Cmd must be non-nil so the tick loop continues.
	if cmd == nil {
		t.Error("expected non-nil Cmd after anim StepMsg — animation tick loop would stop")
	}
}

// TestSubagentAnimStepMsgStopsAfterCompletion verifies that once a subagent
// completes and its Anim is removed from subagentAnims, a stale StepMsg
// targeting that Anim is silently dropped (no re-arm, no panic).
func TestSubagentAnimStepMsgStopsAfterCompletion(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{SessionID: "fg-session", ToolName: "subagent", Args: []byte(`{}`)})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-stale",
		ParentSessionID: "fg-session",
		Type:            "general",
		Description:     "stale tick test",
		Model:           "x/y",
		At:              time.Now(),
	})
	an := app.subagentAnims["sub-stale"]
	if an == nil {
		t.Fatal("expected non-nil Anim")
	}
	animID := an.ID()

	// Complete the subagent — anim is removed from the map.
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-stale",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})
	if len(app.subagentAnims) != 0 {
		t.Fatalf("expected 0 anims after SubagentCompleted, got %d", len(app.subagentAnims))
	}

	// Prime the cache.
	_ = app.View()
	if !app.msgCacheValid {
		t.Fatal("expected msgCacheValid=true after View()")
	}

	// A stale StepMsg for the now-deleted anim must be silently dropped.
	_, cmd := app.Update(anim.StepMsg{ID: animID})

	if cmd != nil {
		t.Error("expected nil Cmd for stale StepMsg after SubagentCompleted — tick loop should not re-arm")
	}
	// Cache must NOT have been invalidated (no state change occurred).
	if !app.msgCacheValid {
		t.Error("expected msgCacheValid=true after stale StepMsg — no frame advanced, no re-render needed")
	}
}

// TestSubagentCompleted_AppendsContinuingPlaceholder verifies that when a
// subagent finishes and the parent is still mid-turn, a transient "continuing…"
// assistant bubble is appended to the foreground transcript. This bridges the
// gap between the subagent block going to "done" and the parent's next LLM
// reply, which can otherwise leave the user staring at a static screen.
func TestSubagentCompleted_AppendsContinuingPlaceholder(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Parent has started a turn (busy=true is what the real TurnStarted
	// handler would set).
	app.busy = true

	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "tu-1",
		Args:      []byte(`{}`),
	})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "tu-1",
		Type:            "general",
		Description:     "look",
		At:              time.Now(),
	})
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})

	n := len(app.messages)
	if n == 0 {
		t.Fatalf("expected at least one message after SubagentCompleted, got 0")
	}
	last := app.messages[n-1]
	if !last.IsPlaceholder {
		t.Fatalf("trailing message should be the continuing placeholder; got role=%v IsPlaceholder=%v",
			last.Role, last.IsPlaceholder)
	}
	if !last.IsStreaming {
		t.Errorf("placeholder must be IsStreaming=true so the assistant bubble renders")
	}
	if !strings.Contains(last.Raw, "continuing") {
		t.Errorf("placeholder Raw should contain 'continuing'; got %q", last.Raw)
	}
}

// TestContinuingPlaceholder_NotAddedWhenParentIdle verifies that the
// placeholder is skipped when the parent is not busy. This guards against
// stray bubbles appearing on resumed sessions where SubagentCompleted is
// replayed via hydration without an active turn.
func TestContinuingPlaceholder_NotAddedWhenParentIdle(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.busy = false // parent is idle

	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		At:              time.Now(),
	})
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})

	for _, msg := range app.messages {
		if msg.IsPlaceholder {
			t.Fatalf("placeholder should not be appended when parent is idle; messages=%+v", app.messages)
		}
	}
}

// TestContinuingPlaceholder_ClearedByAssistantDelta verifies that the first
// assistant text delta after SubagentCompleted clears the placeholder text
// instead of appending to it.
func TestContinuingPlaceholder_ClearedByAssistantDelta(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.busy = true

	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		At:              time.Now(),
	})
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})

	// Parent's first delta arrives.
	app.Handle(bus.AssistantTextDelta{
		SessionID: "fg-session",
		Text:      "Hello",
		At:        time.Now(),
	})

	n := len(app.messages)
	if n == 0 {
		t.Fatalf("expected an assistant message after the delta, got 0")
	}
	last := app.messages[n-1]
	if last.IsPlaceholder {
		t.Errorf("placeholder flag should be cleared after first delta; raw=%q", last.Raw)
	}
	if last.Raw != "Hello" {
		t.Errorf("placeholder text should have been replaced by delta, got Raw=%q", last.Raw)
	}
}

// TestContinuingPlaceholder_DroppedOnToolCallRequested verifies that a new
// parent tool call after the placeholder removes the placeholder rather than
// stranding it before the new tool row.
func TestContinuingPlaceholder_DroppedOnToolCallRequested(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.busy = true

	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		At:              time.Now(),
	})
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})

	// Parent immediately requests another tool (no text in between).
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "bash",
		ToolUseID: "tu-2",
		Args:      []byte(`{"command":"ls"}`),
	})

	for _, msg := range app.messages {
		if msg.IsPlaceholder {
			t.Fatalf("placeholder should have been dropped before new tool row; messages=%+v", app.messages)
		}
	}
}

// TestContinuingPlaceholder_DroppedOnTurnCompleted verifies that an
// unexpected turn-end after the placeholder cleans up the row rather than
// leaving a permanent muted "continuing…" line.
func TestContinuingPlaceholder_DroppedOnTurnCompleted(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.busy = true
	app.activeTurns = 1

	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		Type:            "general",
		At:              time.Now(),
	})
	app.Handle(bus.SubagentCompleted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		At:              time.Now(),
	})

	app.Handle(bus.TurnCompleted{
		SessionID: "fg-session",
		At:        time.Now(),
	})

	for _, msg := range app.messages {
		if msg.IsPlaceholder {
			t.Fatalf("placeholder should have been dropped on TurnCompleted; messages=%+v", app.messages)
		}
	}
}

// TestNoDuplicate_SubagentAssistantMessageAppendedTwice is a regression test
// for HYGGE-12: a duplicate bus.MessageAppended for a subagent assistant message
// must not insert a second assistant bubble in the subagent's transcript.
//
// Sequence:
//  1. SubagentStarted
//  2. AssistantTextDelta in subagent
//  3. MessageAppended{role:"assistant", MessageID:"sa-m1"} in subagent → flushes stream
//  4. MessageAppended{role:"assistant", MessageID:"sa-m1"} again → must be a no-op
func TestNoDuplicate_SubagentAssistantMessageAppendedTwice(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "subagent",
		ToolUseID: "tu-sa",
		Args:      []byte(`{}`),
	})
	app.Handle(bus.SubagentStarted{
		SubSessionID:    "sub-1",
		ParentSessionID: "fg-session",
		ParentMessageID: "tu-sa",
		Type:            "general",
		Description:     "test dedup",
		Model:           "x/y",
		At:              time.Now(),
	})

	// Subagent assistant delta.
	app.Handle(bus.AssistantTextDelta{SessionID: "sub-1", Text: "subagent answer"})

	// First MessageAppended — should finalize the stream.
	app.Handle(bus.MessageAppended{SessionID: "sub-1", Role: "assistant", MessageID: "sa-m1"})

	st := app.subagents["sub-1"]
	if st == nil {
		t.Fatal("subagent state missing")
	}
	// After flush: exactly one assistant message in the subagent transcript.
	assistantCount := 0
	for _, m := range st.Messages {
		if m.Role == components.RoleAssistant {
			assistantCount++
		}
	}
	if assistantCount != 1 {
		t.Fatalf("after first MessageAppended: expected 1 assistant in subagent transcript, got %d", assistantCount)
	}
	if st.Messages[0].IsStreaming {
		t.Errorf("expected subagent assistant finalized after MessageAppended")
	}
	if st.Messages[0].MessageID != "sa-m1" {
		t.Fatalf("expected subagent assistant MessageID to be stamped, got %q", st.Messages[0].MessageID)
	}

	// Second duplicate MessageAppended for the subagent session — must be a no-op.
	// The MessageID guard should fire before any metadata refresh or insertion.
	before := st.Messages[0]
	app.Handle(bus.MessageAppended{SessionID: "sub-1", Role: "assistant", MessageID: "sa-m1"})

	assistantCount = 0
	for _, m := range st.Messages {
		if m.Role == components.RoleAssistant {
			assistantCount++
		}
	}
	if assistantCount != 1 {
		t.Fatalf("after duplicate MessageAppended: expected 1 assistant in subagent transcript, got %d (duplicate inserted)", assistantCount)
	}
	after := st.Messages[0]
	if after.Raw != before.Raw || after.MessageID != before.MessageID || after.IsStreaming != before.IsStreaming || after.OutputTokens != before.OutputTokens || after.CostUSD != before.CostUSD || after.DurationMs != before.DurationMs {
		t.Fatalf("duplicate MessageAppended mutated subagent assistant: before=%+v after=%+v", before, after)
	}
}
