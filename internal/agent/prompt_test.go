package agent

import (
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

func TestBuildRequest_NoMarker(t *testing.T) {
	msgs := []*session.Message{
		{ID: "m1", Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}},
	}
	req := buildRequest(msgs, nil, "be nice", nil, "model-x", nil)
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
	req := buildRequest(nil, marker, "be nice", nil, "", nil)
	if !strings.Contains(req.System, "be nice") {
		t.Fatalf("want original system in result, got %q", req.System)
	}
	if !strings.Contains(req.System, markerPrefix+"we discussed widgets") {
		t.Fatalf("want marker summary in system, got %q", req.System)
	}
}

func TestBuildRequest_MarkerAloneWhenNoSystemPrompt(t *testing.T) {
	marker := &session.Marker{Summary: "we discussed widgets"}
	req := buildRequest(nil, marker, "", nil, "", nil)
	if req.System != markerPrefix+"we discussed widgets" {
		t.Fatalf("unexpected system: %q", req.System)
	}
}

func TestBuildRequest_EmptyMarkerSummaryIgnored(t *testing.T) {
	marker := &session.Marker{Summary: "   "}
	req := buildRequest(nil, marker, "system", nil, "", nil)
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
	req := buildRequest(msgs, nil, "", nil, "", nil)
	if len(req.Messages) != 1 || req.Messages[0].ID != "m1" {
		t.Fatalf("nil filter broken: %+v", req.Messages)
	}
}

func TestBuildRequest_ForwardsTools(t *testing.T) {
	tools := []provider.Tool{{Name: "read"}, {Name: "write"}}
	req := buildRequest(nil, nil, "", tools, "", nil)
	if len(req.Tools) != 2 {
		t.Fatalf("tools not forwarded: %+v", req.Tools)
	}
}
