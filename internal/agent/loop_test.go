package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

func TestBuildAssistantParts_Empty(t *testing.T) {
	got := buildAssistantParts("", "", nil)
	if len(got) != 0 {
		t.Fatalf("want empty parts, got %+v", got)
	}
}

func TestBuildAssistantParts_TextOnly(t *testing.T) {
	got := buildAssistantParts("hello", "", nil)
	if len(got) != 1 || got[0].Kind != session.PartText || got[0].Text != "hello" {
		t.Fatalf("unexpected parts: %+v", got)
	}
}

func TestBuildAssistantParts_TextAndThinking(t *testing.T) {
	got := buildAssistantParts("answer", "ponder", nil)
	if len(got) != 2 {
		t.Fatalf("want 2 parts, got %d", len(got))
	}
	if got[0].Kind != session.PartText {
		t.Fatalf("want text first, got %v", got[0].Kind)
	}
	if got[1].Kind != session.PartThinking {
		t.Fatalf("want thinking second, got %v", got[1].Kind)
	}
}

func TestBuildAssistantParts_TextAndToolUses(t *testing.T) {
	got := buildAssistantParts("text", "", []toolCallEvent{
		{ID: "tu1", Name: "read", Input: []byte(`{}`)},
		{ID: "tu2", Name: "write", Input: []byte(`{}`)},
	})
	if len(got) != 3 {
		t.Fatalf("want 3 parts, got %d", len(got))
	}
	if got[0].Kind != session.PartText {
		t.Fatalf("want text first")
	}
	if got[1].Kind != session.PartToolUse || got[1].ToolID != "tu1" {
		t.Fatalf("want tool_use tu1 second, got %+v", got[1])
	}
	if got[2].Kind != session.PartToolUse || got[2].ToolID != "tu2" {
		t.Fatalf("want tool_use tu2 third, got %+v", got[2])
	}
}

func TestBuildAssistantParts_ToolUsesOnly(t *testing.T) {
	got := buildAssistantParts("", "", []toolCallEvent{{ID: "tu1", Name: "read"}})
	if len(got) != 1 || got[0].Kind != session.PartToolUse {
		t.Fatalf("unexpected parts: %+v", got)
	}
}

// TestExecuteToolCalls_ToolUseIDPropagates verifies that the ToolUseID
// assigned by the provider flows onto both ToolCallRequested and
// ToolCallCompleted events published during tool execution.
func TestExecuteToolCalls_ToolUseIDPropagates(t *testing.T) {
	// Not parallel — newTestEnv calls t.Setenv which is incompatible with
	// t.Parallel inside the same test binary.
	env := newTestEnv(t)

	// Script: two turns — first returns a tool_use, second a text reply.
	prov := newFakeProvider("fake",
		scriptToolUse("", toolUseEvent(t, "tu-abc-123", "read", map[string]any{
			"path": "/tmp/x.txt",
		})),
		scriptText("done", provider.Usage{}),
	)
	ag := env.newAgent(prov)

	// Collect both event types before running.
	_, stopReq := collectEvents[bus.ToolCallRequested](t, env.Bus, 8)
	_, stopDone := collectEvents[bus.ToolCallCompleted](t, env.Bus, 8)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := ag.Send(ctx, env.sessionID, userText("read the file"))
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	reqs := stopReq()
	dones := stopDone()

	// Exactly one Requested and one Completed for our tool call.
	if len(reqs) == 0 {
		t.Fatal("want at least 1 ToolCallRequested, got 0")
	}
	if len(dones) == 0 {
		t.Fatal("want at least 1 ToolCallCompleted, got 0")
	}

	// Find the one for our specific tool call.
	var req *bus.ToolCallRequested
	for i := range reqs {
		if reqs[i].ToolName == "read" {
			req = &reqs[i]
			break
		}
	}
	if req == nil {
		t.Fatal("ToolCallRequested for 'read' not found")
	}
	if req.ToolUseID != "tu-abc-123" {
		t.Errorf("ToolCallRequested.ToolUseID = %q, want %q", req.ToolUseID, "tu-abc-123")
	}

	var done *bus.ToolCallCompleted
	for i := range dones {
		if dones[i].ToolName == "read" {
			done = &dones[i]
			break
		}
	}
	if done == nil {
		t.Fatal("ToolCallCompleted for 'read' not found")
	}
	if done.ToolUseID != "tu-abc-123" {
		t.Errorf("ToolCallCompleted.ToolUseID = %q, want %q", done.ToolUseID, "tu-abc-123")
	}
}
