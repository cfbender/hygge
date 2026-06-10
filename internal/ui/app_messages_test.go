package ui

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/ui/components"
)

// TestFlushAssistantStream_PostFlushDeltaRefinalizesOnDuplicateMessageAppended
// is the regression test for the fix to the duplicate-MessageAppended guard.
//
// Scenario:
//  1. AssistantTextDelta("hello") starts a streaming bubble.
//  2. MessageAppended(m1) flushes the bubble (IsStreaming=false, FinalMarkdown set).
//     lastAssistantFlushIdx is recorded.
//  3. A stray AssistantTextDelta(" world") arrives after the flush.
//     The lastAssistantFlushIdx branch in appendAssistantDelta re-opens the
//     bubble: IsStreaming is set back to true.
//  4. A duplicate MessageAppended(m1) arrives.
//     Before the fix, the guard skipped it because messageID matched even though
//     the bubble was streaming again. After the fix, the guard checks
//     !IsStreaming, so the bubble falls through and re-finalizes.
//
// Expected outcome: IsStreaming=false, FinalMarkdown populated.
func TestFlushAssistantStream_PostFlushDeltaRefinalizesOnDuplicateMessageAppended(t *testing.T) {
	t.Parallel()
	app, _ := newTestApp(t)
	// No store: persistedAssistantUIMessage will return (uiMessage{}, false),
	// so flushAssistantStream uses the in-memory accumulation.
	app.opts.SessionID = "test-session"
	app.foregroundStack = []string{"test-session"}

	// Step 1: first delta arrives — creates a streaming assistant bubble.
	app.Handle(bus.AssistantTextDelta{SessionID: "test-session", Text: "hello"})
	if len(app.messages) != 1 {
		t.Fatalf("step1: expected 1 message, got %d", len(app.messages))
	}
	if !app.messages[0].IsStreaming {
		t.Fatal("step1: expected IsStreaming=true after first delta")
	}

	// Step 2: MessageAppended(m1) flushes the bubble.
	app.Handle(bus.MessageAppended{SessionID: "test-session", MessageID: "m1", Role: string(session.RoleAssistant)})
	if app.messages[0].IsStreaming {
		t.Fatal("step2: expected IsStreaming=false after first MessageAppended")
	}
	if app.messages[0].FinalMarkdown == "" {
		t.Fatal("step2: expected FinalMarkdown set after first MessageAppended")
	}
	if app.messages[0].MessageID != "m1" {
		t.Fatalf("step2: expected MessageID=m1, got %q", app.messages[0].MessageID)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Fatalf("step2: expected RoleAssistant, got %q", app.messages[0].Role)
	}

	// Step 3: stray post-flush delta re-opens the just-flushed bubble via
	// the lastAssistantFlushIdx branch in appendAssistantDelta.
	app.Handle(bus.AssistantTextDelta{SessionID: "test-session", Text: " world"})
	if !app.messages[0].IsStreaming {
		t.Fatal("step3: expected IsStreaming=true after stray post-flush delta (lastAssistantFlushIdx branch)")
	}
	if !strings.Contains(app.messages[0].Raw, "hello") || !strings.Contains(app.messages[0].Raw, "world") {
		t.Fatalf("step3: expected Raw to contain both deltas, got %q", app.messages[0].Raw)
	}

	// Step 4: duplicate MessageAppended(m1).
	// Before the fix: guard skipped because messageID matched (IsStreaming ignored).
	// After the fix: guard is skipped because IsStreaming=true, so re-finalization runs.
	app.Handle(bus.MessageAppended{SessionID: "test-session", MessageID: "m1", Role: string(session.RoleAssistant)})
	if app.messages[0].IsStreaming {
		t.Fatal("step4: expected IsStreaming=false after duplicate MessageAppended re-finalizes the re-opened bubble")
	}
	if app.messages[0].FinalMarkdown == "" {
		t.Fatal("step4: expected FinalMarkdown populated after re-finalization")
	}
}

func TestExtractTargetDecodesEscapedCommand(t *testing.T) {
	t.Parallel()

	args := []byte(`{"command":"cd \"/tmp/project with spaces\" && go test ./internal/ui -run TestToolCallProgress"}`)
	want := `cd "/tmp/project with spaces" && go test ./internal/ui -run TestToolCallProgress`

	if got := extractTarget(args); got != want {
		t.Fatalf("extractTarget() = %q, want %q", got, want)
	}
}
