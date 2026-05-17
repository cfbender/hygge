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
