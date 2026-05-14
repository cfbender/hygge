package openaicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/session"
)

func TestToWireMessages_AssistantWithTextAndToolUse(t *testing.T) {
	msgs := []session.Message{{
		Role: session.RoleAssistant,
		Parts: []session.Part{
			{Kind: session.PartText, Text: "thinking out loud"},
			{Kind: session.PartToolUse, ToolID: "call_1", ToolName: "read", ToolInput: json.RawMessage(`{"path":"x"}`)},
		},
	}}

	wire, err := toWireMessages("", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	if len(wire) != 1 {
		t.Fatalf("want 1 wire msg, got %d", len(wire))
	}
	m := wire[0]
	if m.Role != "assistant" {
		t.Errorf("role: %s", m.Role)
	}
	// Content present AND tool_calls present.
	var content string
	if err := json.Unmarshal(m.Content, &content); err != nil {
		t.Fatalf("content not string: %v: %s", err, m.Content)
	}
	if content != "thinking out loud" {
		t.Errorf("content: %q", content)
	}
	if len(m.ToolCalls) != 1 {
		t.Fatalf("tool_calls: %v", m.ToolCalls)
	}
	if m.ToolCalls[0].ID != "call_1" || m.ToolCalls[0].Function.Name != "read" {
		t.Errorf("tool_call: %+v", m.ToolCalls[0])
	}
	if m.ToolCalls[0].Function.Arguments != `{"path":"x"}` {
		t.Errorf("arguments: %q", m.ToolCalls[0].Function.Arguments)
	}
}

func TestToWireMessages_AssistantToolUseOnly_HasNullContent(t *testing.T) {
	msgs := []session.Message{{
		Role: session.RoleAssistant,
		Parts: []session.Part{
			{Kind: session.PartToolUse, ToolID: "call_1", ToolName: "read", ToolInput: json.RawMessage(`{"path":"x"}`)},
		},
	}}

	wire, err := toWireMessages("", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	m := wire[0]
	// Content field should be omitted (nil RawMessage) → no "content" key
	// when serialised, which OpenAI treats as null.
	if len(m.Content) != 0 {
		t.Errorf("content should be empty/null, got %s", m.Content)
	}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(body), `"content"`) {
		t.Errorf("serialised body should omit content key: %s", body)
	}
}

func TestToWireMessages_ToolRoleExpandsMultipleResults(t *testing.T) {
	msgs := []session.Message{{
		Role: session.RoleTool,
		Parts: []session.Part{
			{Kind: session.PartToolResult, ToolUseID: "call_a", Content: "result A"},
			{Kind: session.PartToolResult, ToolUseID: "call_b", Content: "result B"},
		},
	}}

	wire, err := toWireMessages("", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	if len(wire) != 2 {
		t.Fatalf("want 2 messages, got %d", len(wire))
	}
	if wire[0].Role != "tool" || wire[0].ToolCallID != "call_a" {
		t.Errorf("msg0: %+v", wire[0])
	}
	if wire[1].Role != "tool" || wire[1].ToolCallID != "call_b" {
		t.Errorf("msg1: %+v", wire[1])
	}
	var c0 string
	_ = json.Unmarshal(wire[0].Content, &c0)
	if c0 != "result A" {
		t.Errorf("msg0 content: %q", c0)
	}
}

func TestToWireMessages_UserWithImage(t *testing.T) {
	msgs := []session.Message{{
		Role: session.RoleUser,
		Parts: []session.Part{
			{Kind: session.PartText, Text: "what is this?"},
			{Kind: session.PartImage, ImageMimeType: "image/png", ImageBase64: "iVBORw0K"},
		},
	}}

	wire, err := toWireMessages("", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	m := wire[0]
	// Content should be an ARRAY of content blocks.
	var blocks []map[string]any
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		t.Fatalf("content not an array: %v: %s", err, m.Content)
	}
	if len(blocks) != 2 {
		t.Fatalf("blocks: %v", blocks)
	}
	if blocks[0]["type"] != "text" || blocks[0]["text"] != "what is this?" {
		t.Errorf("block[0]: %v", blocks[0])
	}
	if blocks[1]["type"] != "image_url" {
		t.Errorf("block[1] type: %v", blocks[1])
	}
	img := blocks[1]["image_url"].(map[string]any)
	if img["url"] != "data:image/png;base64,iVBORw0K" {
		t.Errorf("image_url: %v", img)
	}
}

func TestToWireMessages_DropsThinking(t *testing.T) {
	msgs := []session.Message{
		{Role: session.RoleAssistant, Parts: []session.Part{
			{Kind: session.PartThinking, Text: "internal monologue"},
			{Kind: session.PartText, Text: "result"},
		}},
	}
	wire, err := toWireMessages("", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	body, _ := json.Marshal(wire)
	if strings.Contains(string(body), "internal monologue") {
		t.Errorf("thinking should be dropped: %s", body)
	}
}

func TestToWireMessages_SystemPromptArgPrepended(t *testing.T) {
	msgs := []session.Message{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}},
	}
	wire, err := toWireMessages("you are friendly", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	if len(wire) != 2 {
		t.Fatalf("want 2 (system+user), got %d", len(wire))
	}
	if wire[0].Role != "system" {
		t.Errorf("msg0 role: %s", wire[0].Role)
	}
	var s string
	_ = json.Unmarshal(wire[0].Content, &s)
	if s != "you are friendly" {
		t.Errorf("system content: %q", s)
	}
}

func TestToWireMessages_SystemRoleMessage(t *testing.T) {
	msgs := []session.Message{
		{Role: session.RoleSystem, Parts: []session.Part{{Kind: session.PartText, Text: "be terse"}}},
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "hi"}}},
	}
	wire, err := toWireMessages("", msgs)
	if err != nil {
		t.Fatalf("toWireMessages: %v", err)
	}
	if len(wire) != 2 {
		t.Fatalf("want 2, got %d", len(wire))
	}
	if wire[0].Role != "system" {
		t.Errorf("msg0 role: %s", wire[0].Role)
	}
}

func TestToWireMessages_RejectsToolResultOnAssistant(t *testing.T) {
	msgs := []session.Message{{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartToolResult, ToolUseID: "x", Content: "y"}},
	}}
	_, err := toWireMessages("", msgs)
	if err == nil {
		t.Fatal("want error for tool_result on assistant")
	}
}

func TestToWireMessages_RejectsImageOnAssistant(t *testing.T) {
	msgs := []session.Message{{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartImage, ImageMimeType: "image/png", ImageBase64: "x"}},
	}}
	_, err := toWireMessages("", msgs)
	if err == nil {
		t.Fatal("want error for image on assistant")
	}
}

func TestToWireMessages_RejectsTextOnToolRole(t *testing.T) {
	msgs := []session.Message{{
		Role:  session.RoleTool,
		Parts: []session.Part{{Kind: session.PartText, Text: "wat"}},
	}}
	_, err := toWireMessages("", msgs)
	if err == nil {
		t.Fatal("want error for text on tool role")
	}
}

func TestToWireMessages_RejectsUnknownRole(t *testing.T) {
	msgs := []session.Message{{Role: session.Role("alien")}}
	_, err := toWireMessages("", msgs)
	if err == nil {
		t.Fatal("want error for unknown role")
	}
}

func TestToWireTools_DefaultSchemaWhenNil(t *testing.T) {
	tools := toWireTools(nil)
	if tools != nil {
		t.Errorf("want nil for no tools, got %v", tools)
	}
}
