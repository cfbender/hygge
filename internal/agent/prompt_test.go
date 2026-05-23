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
	envelope := buildLatestUserEnvelope("please inspect <file> and keep ]]> intact", nil, 0, 0, 0)
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
	envelope := buildLatestUserEnvelope("raw historical request", nil, 0, 0, 0)
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
	}, 0, 0, 0)
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

func TestBuildLatestUserEnvelopeContextWindowPresent(t *testing.T) {
	envelope := buildLatestUserEnvelope("do work", nil, 200000, 0, 0)
	if !strings.Contains(envelope, "<context_window>") {
		t.Fatalf("envelope missing <context_window>:\n%s", envelope)
	}
	if !strings.Contains(envelope, "<max_tokens>200000</max_tokens>") {
		t.Fatalf("envelope missing <max_tokens>200000</max_tokens>:\n%s", envelope)
	}
	// context_window must appear between memories and critical_turn_reminders.
	assertPromptOrder(t, envelope, "<memories>", "<context_window>", "<critical_turn_reminders>", userRequestOpen)
}

func TestBuildLatestUserEnvelopeContextWindowZeroOmitted(t *testing.T) {
	for _, cw := range []int64{0, -1} {
		envelope := buildLatestUserEnvelope("do work", nil, cw, 0, 0)
		if strings.Contains(envelope, "<context_window>") {
			t.Fatalf("contextWindow=%d: envelope should omit <context_window>, got:\n%s", cw, envelope)
		}
	}
}

// TestBuildLatestUserEnvelopeUsagePresent verifies that latest-known usage
// is rendered inside <context_window> when contextWindow > 0 and usedTokens > 0.
func TestBuildLatestUserEnvelopeUsagePresent(t *testing.T) {
	// 40000 used out of 200000 = 20.0%
	envelope := buildLatestUserEnvelope("do work", nil, 200000, 40000, 0.2)
	if !strings.Contains(envelope, "<context_window>") {
		t.Fatalf("envelope missing <context_window>:\n%s", envelope)
	}
	if !strings.Contains(envelope, "<max_tokens>200000</max_tokens>") {
		t.Fatalf("envelope missing <max_tokens>:\n%s", envelope)
	}
	if !strings.Contains(envelope, "<latest_known_used_tokens>40000</latest_known_used_tokens>") {
		t.Fatalf("envelope missing <latest_known_used_tokens>:\n%s", envelope)
	}
	if !strings.Contains(envelope, "<latest_known_used_percent>20.0</latest_known_used_percent>") {
		t.Fatalf("envelope missing <latest_known_used_percent>:\n%s", envelope)
	}
	// Order: max_tokens, then usage, all inside context_window.
	assertPromptOrder(t, envelope, "<context_window>", "<max_tokens>", "<latest_known_used_tokens>", "<latest_known_used_percent>", "</context_window>")
}

// TestBuildLatestUserEnvelopeUsageOmittedWhenZeroTokens verifies that
// latest-known usage is not rendered when usedTokens == 0, even if
// contextWindow > 0 (i.e. no prior turn yet).
func TestBuildLatestUserEnvelopeUsageOmittedWhenZeroTokens(t *testing.T) {
	envelope := buildLatestUserEnvelope("do work", nil, 200000, 0, 0)
	if !strings.Contains(envelope, "<context_window>") {
		t.Fatalf("envelope missing <context_window>:\n%s", envelope)
	}
	if strings.Contains(envelope, "<latest_known_used_tokens>") {
		t.Fatalf("latest_known_used_tokens should be omitted when usedTokens=0:\n%s", envelope)
	}
	if strings.Contains(envelope, "<latest_known_used_percent>") {
		t.Fatalf("latest_known_used_percent should be omitted when usedTokens=0:\n%s", envelope)
	}
}

// TestBuildLatestUserEnvelopeUsageOmittedWhenNoContextWindow verifies that
// latest-known usage is not rendered when contextWindow == 0, regardless of
// usedTokens.
func TestBuildLatestUserEnvelopeUsageOmittedWhenNoContextWindow(t *testing.T) {
	envelope := buildLatestUserEnvelope("do work", nil, 0, 50000, 0.25)
	if strings.Contains(envelope, "<context_window>") {
		t.Fatalf("context_window block should be omitted when contextWindow=0:\n%s", envelope)
	}
	if strings.Contains(envelope, "<latest_known_used_tokens>") {
		t.Fatalf("latest_known_used_tokens should be omitted when contextWindow=0:\n%s", envelope)
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
