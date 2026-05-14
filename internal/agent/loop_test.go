package agent

import (
	"testing"

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
