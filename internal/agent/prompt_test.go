package agent

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/agentsmd"
	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

func TestBuildRequest_NoMarker(t *testing.T) {
	msgs := []*session.Message{
		{ID: "m1", Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}},
	}
	req := buildRequest(msgs, nil, "be nice", nil, "model-x", nil, nil)
	if req.System != "be nice" {
		t.Fatalf("want unmodified system, got %q", req.System)
	}
	if len(req.Messages) != 1 || req.Messages[0].ID != "m1" {
		t.Fatalf("messages not forwarded: %+v", req.Messages)
	}
	if req.ModelName != "model-x" {
		t.Fatalf("model name not forwarded: %q", req.ModelName)
	}
}

func TestBuildRequest_WithMarker(t *testing.T) {
	marker := &session.Marker{Summary: "we discussed widgets"}
	req := buildRequest(nil, marker, "be nice", nil, "", nil, nil)
	if !strings.Contains(req.System, "be nice") {
		t.Fatalf("want original system in result, got %q", req.System)
	}
	if !strings.Contains(req.System, markerPrefix+"we discussed widgets") {
		t.Fatalf("want marker summary in system, got %q", req.System)
	}
}

func TestBuildRequest_MarkerAloneWhenNoSystemPrompt(t *testing.T) {
	marker := &session.Marker{Summary: "we discussed widgets"}
	req := buildRequest(nil, marker, "", nil, "", nil, nil)
	if req.System != markerPrefix+"we discussed widgets" {
		t.Fatalf("unexpected system: %q", req.System)
	}
}

func TestBuildRequest_EmptyMarkerSummaryIgnored(t *testing.T) {
	marker := &session.Marker{Summary: "   "}
	req := buildRequest(nil, marker, "system", nil, "", nil, nil)
	if req.System != "system" {
		t.Fatalf("want unmodified system for empty marker, got %q", req.System)
	}
}

func TestBuildRequest_NilMessagesAreSkipped(t *testing.T) {
	msgs := []*session.Message{
		nil,
		{ID: "m1", Role: session.RoleUser},
		nil,
	}
	req := buildRequest(msgs, nil, "", nil, "", nil, nil)
	if len(req.Messages) != 1 || req.Messages[0].ID != "m1" {
		t.Fatalf("nil filter broken: %+v", req.Messages)
	}
}

func TestBuildRequest_ForwardsTools(t *testing.T) {
	tools := []provider.Tool{{Name: "read"}, {Name: "write"}}
	req := buildRequest(nil, nil, "", tools, "", nil, nil)
	if len(req.Tools) != 2 {
		t.Fatalf("tools not forwarded: %+v", req.Tools)
	}
}

// TestBuildRequest_WithLazyBlocks verifies lazy-loaded subdir context
// is appended to the system prompt under the dedicated header.
func TestBuildRequest_WithLazyBlocks(t *testing.T) {
	blocks := []agentsmd.Block{
		{Path: "/r/p/AGENTS.md", RelPath: "p/AGENTS.md", Source: agentsmd.SourceProjectSubdir, Content: "subdir rules"},
	}
	req := buildRequest(nil, nil, "base", nil, "", nil, blocks)
	if !strings.Contains(req.System, "base") {
		t.Fatalf("base prompt missing: %q", req.System)
	}
	if !strings.Contains(req.System, "## Additional project context (loaded for this turn)") {
		t.Fatalf("lazy header missing: %q", req.System)
	}
	if !strings.Contains(req.System, "subdir rules") {
		t.Fatalf("lazy content missing: %q", req.System)
	}
}

// TestBuildRequest_LazyBlocksAfterMarker verifies the assembly order
// when both a marker summary and lazy blocks are present.
func TestBuildRequest_LazyBlocksAfterMarker(t *testing.T) {
	marker := &session.Marker{Summary: "earlier we talked"}
	blocks := []agentsmd.Block{
		{Path: "/r/p/AGENTS.md", RelPath: "p/AGENTS.md", Source: agentsmd.SourceProjectSubdir, Content: "subdir rules"},
	}
	req := buildRequest(nil, marker, "base", nil, "", nil, blocks)
	markerIdx := strings.Index(req.System, markerPrefix)
	lazyIdx := strings.Index(req.System, "## Additional project context")
	if markerIdx < 0 || lazyIdx < 0 {
		t.Fatalf("missing marker or lazy section: %q", req.System)
	}
	if markerIdx >= lazyIdx {
		t.Fatalf("want marker before lazy, got marker=%d lazy=%d in %q", markerIdx, lazyIdx, req.System)
	}
}
