package agent

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/session"
)

func TestComposeSystemPrompt_NoMarker(t *testing.T) {
	sys := composeSystemPrompt("be nice", nil, nil, nil)
	if sys != "be nice" {
		t.Fatalf("want unmodified system, got %q", sys)
	}
}

func TestComposeSystemPrompt_WithMarker(t *testing.T) {
	marker := &session.Marker{Summary: "we discussed widgets"}
	sys := composeSystemPrompt("be nice", marker, nil, nil)
	if !strings.Contains(sys, "be nice") {
		t.Fatalf("want original system in result, got %q", sys)
	}
	if !strings.Contains(sys, markerPrefix+"we discussed widgets") {
		t.Fatalf("want marker summary in system, got %q", sys)
	}
}

func TestComposeSystemPrompt_MarkerAloneWhenNoSystemPrompt(t *testing.T) {
	marker := &session.Marker{Summary: "we discussed widgets"}
	sys := composeSystemPrompt("", marker, nil, nil)
	if sys != markerPrefix+"we discussed widgets" {
		t.Fatalf("unexpected system: %q", sys)
	}
}

func TestComposeSystemPrompt_EmptyMarkerSummaryIgnored(t *testing.T) {
	marker := &session.Marker{Summary: "   "}
	sys := composeSystemPrompt("system", marker, nil, nil)
	if sys != "system" {
		t.Fatalf("want unmodified system for empty marker, got %q", sys)
	}
}

// TestComposeSystemPrompt_WithLazyBlocks verifies lazy-loaded subdir context
// is appended to the system prompt under the dedicated header.
func TestComposeSystemPrompt_WithLazyBlocks(t *testing.T) {
	blocks := []agentsmd.Block{
		{Path: "/r/p/AGENTS.md", RelPath: "p/AGENTS.md", Source: agentsmd.SourceProjectSubdir, Content: "subdir rules"},
	}
	sys := composeSystemPrompt("base", nil, blocks, nil)
	if !strings.Contains(sys, "base") {
		t.Fatalf("base prompt missing: %q", sys)
	}
	if !strings.Contains(sys, "## Additional project context (loaded for this turn)") {
		t.Fatalf("lazy header missing: %q", sys)
	}
	if !strings.Contains(sys, "subdir rules") {
		t.Fatalf("lazy content missing: %q", sys)
	}
}

// TestComposeSystemPrompt_WithHookSystemPromptAdditions verifies hook-provided
// context is appended to the system prompt without becoming a visible message.
func TestComposeSystemPrompt_WithHookSystemPromptAdditions(t *testing.T) {
	sys := composeSystemPrompt("base", nil, nil, []string{"plugin context"})
	if !strings.Contains(sys, "base") {
		t.Fatalf("base prompt missing: %q", sys)
	}
	if !strings.Contains(sys, "## Additional hook context (loaded for this turn)") {
		t.Fatalf("hook header missing: %q", sys)
	}
	if !strings.Contains(sys, "plugin context") {
		t.Fatalf("hook context missing: %q", sys)
	}
}

// TestComposeSystemPrompt_LazyBlocksAfterMarker verifies the assembly order
// when both a marker summary and lazy blocks are present.
func TestComposeSystemPrompt_LazyBlocksAfterMarker(t *testing.T) {
	marker := &session.Marker{Summary: "earlier we talked"}
	blocks := []agentsmd.Block{
		{Path: "/r/p/AGENTS.md", RelPath: "p/AGENTS.md", Source: agentsmd.SourceProjectSubdir, Content: "subdir rules"},
	}
	sys := composeSystemPrompt("base", marker, blocks, nil)
	markerIdx := strings.Index(sys, markerPrefix)
	lazyIdx := strings.Index(sys, "## Additional project context")
	if markerIdx < 0 || lazyIdx < 0 {
		t.Fatalf("missing marker or lazy section: %q", sys)
	}
	if markerIdx >= lazyIdx {
		t.Fatalf("want marker before lazy, got marker=%d lazy=%d in %q", markerIdx, lazyIdx, sys)
	}
}

func TestBuildLatestUserEnvelope_OrderAndRawRequest(t *testing.T) {
	envelope := buildLatestUserEnvelope("please inspect <file> and keep ]]> intact", nil)
	for _, want := range []string{
		turnContextOpen,
		"<workspace_state>",
		"<editor_state>",
		"<terminal_state>",
		"<attached_context>",
		"<memories>",
		"<critical_turn_reminders>",
		userRequestOpen,
		turnContextClose,
	} {
		if !strings.Contains(envelope, want) {
			t.Fatalf("envelope missing %q:\n%s", want, envelope)
		}
	}
	assertPromptOrder(t, envelope, "<workspace_state>", "<memories>", "<critical_turn_reminders>", userRequestOpen)
	for _, want := range []string{"propose memories", "session task constraints autonomously", "explicit user confirmation before saving inferred project/global memories"} {
		if !strings.Contains(envelope, want) {
			t.Fatalf("envelope missing autonomous memory guidance %q:\n%s", want, envelope)
		}
	}
	if got := extractUserRequest(envelope); got != "please inspect <file> and keep ]]> intact" {
		t.Fatalf("user request = %q", got)
	}
}

func TestStripHistoricalTurnContextExtractsUserRequest(t *testing.T) {
	envelope := buildLatestUserEnvelope("raw historical request", nil)
	if got := stripHistoricalTurnContext(envelope); got != "raw historical request" {
		t.Fatalf("stripped request = %q", got)
	}
	plain := "user typed a normal message"
	if got := stripHistoricalTurnContext(plain); got != plain {
		t.Fatalf("plain message changed: %q", got)
	}
}

func TestBuildLatestUserEnvelopeIncludesSessionMemories(t *testing.T) {
	envelope := buildLatestUserEnvelope("use memory", []*session.Memory{
		{ID: "01GLOBAL", Scope: session.MemoryScopeGlobal, Title: "Global", Body: "global preference"},
		{ID: "01PROJECT", Scope: session.MemoryScopeProject, Title: "Project", Body: "project preference"},
		{ID: "01MEMORY", Scope: session.MemoryScopeSession, Content: "prefers focused diffs with ]]> preserved"},
	})
	if !strings.Contains(envelope, `<memory scope="session" id="01MEMORY">`) {
		t.Fatalf("session memory wrapper missing:\n%s", envelope)
	}
	assertPromptOrder(t, envelope, `scope="global"`, `scope="project"`, `scope="session"`)
	if !strings.Contains(envelope, "prefers focused diffs with ]]]]><![CDATA[> preserved") {
		t.Fatalf("session memory content missing or not CDATA-split:\n%s", envelope)
	}
	if strings.Contains(envelope, "No active memories") {
		t.Fatalf("unexpected no-memory content in envelope:\n%s", envelope)
	}
}

func assertPromptOrder(t *testing.T, text string, parts ...string) {
	t.Helper()
	last := -1
	for _, part := range parts {
		idx := strings.Index(text, part)
		if idx < 0 {
			t.Fatalf("missing %q in:\n%s", part, text)
		}
		if idx <= last {
			t.Fatalf("%q appears out of order in:\n%s", part, text)
		}
		last = idx
	}
}
