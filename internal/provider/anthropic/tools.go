package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// toWireTools maps provider.Tool to the Anthropic tools array.
func toWireTools(tools []provider.Tool) []wireTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(tools))
	for _, t := range tools {
		schema := t.InputSchema
		if schema == nil {
			// Anthropic requires an object schema; default to a permissive
			// empty-object schema rather than failing.
			schema = map[string]any{"type": "object"}
		}
		out = append(out, wireTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: schema,
		})
	}
	return out
}

// toWireMessages converts a session.Message slice into the role/content shape
// the Messages API expects.  System messages are routed to the systemBlocks
// return value rather than into wireMessages.  Consecutive same-role messages
// (after system extraction) are passed through verbatim — the agent loop is
// responsible for arranging the conversation legally.
//
// applyCache, when true, attaches a cache_control: ephemeral marker to the
// final text block of the final user message and to the system prefix.  This
// anchors Anthropic prompt caching at the longest stable suffix of the
// prompt.
func toWireMessages(msgs []session.Message, applyCache bool) (wire []wireMessage, system []systemBlock, err error) {
	wire = make([]wireMessage, 0, len(msgs))

	var systemTexts []string
	for _, m := range msgs {
		if m.Role == session.RoleSystem {
			for _, p := range m.Parts {
				if p.Kind == session.PartText {
					systemTexts = append(systemTexts, p.Text)
				}
			}
			continue
		}
		wm, err := messageToWire(m)
		if err != nil {
			return nil, nil, err
		}
		wire = append(wire, wm)
	}

	if len(systemTexts) > 0 {
		joined := ""
		for i, s := range systemTexts {
			if i > 0 {
				joined += "\n\n"
			}
			joined += s
		}
		sb := systemBlock{Type: "text", Text: joined}
		if applyCache {
			sb.CacheControl = &cacheControl{Type: "ephemeral"}
		}
		system = []systemBlock{sb}
	}

	if applyCache && len(wire) > 0 {
		attachCacheMarker(&wire[len(wire)-1])
	}

	return wire, system, nil
}

// messageToWire converts a single non-system session.Message.
func messageToWire(m session.Message) (wireMessage, error) {
	role := ""
	switch m.Role {
	case session.RoleUser:
		role = "user"
	case session.RoleAssistant:
		role = "assistant"
	case session.RoleTool:
		// Anthropic models tool results as a user-role message whose
		// content blocks are all tool_result.
		role = "user"
	default:
		return wireMessage{}, fmt.Errorf("anthropic: unsupported role %q", m.Role)
	}

	content := make([]contentItem, 0, len(m.Parts))
	for _, p := range m.Parts {
		ci, err := partToWire(p)
		if err != nil {
			return wireMessage{}, err
		}
		content = append(content, ci)
	}
	return wireMessage{Role: role, Content: content}, nil
}

// partToWire converts a single session.Part into a contentItem.
func partToWire(p session.Part) (contentItem, error) {
	switch p.Kind {
	case session.PartText:
		return contentItem{Type: "text", Text: p.Text}, nil
	case session.PartThinking:
		return contentItem{Type: "thinking", Thinking: p.Text}, nil
	case session.PartToolUse:
		input := p.ToolInput
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return contentItem{
			Type:  "tool_use",
			ID:    p.ToolID,
			Name:  p.ToolName,
			Input: input,
		}, nil
	case session.PartToolResult:
		return contentItem{
			Type:      "tool_result",
			ToolUseID: p.ToolUseID,
			Content:   p.Content,
			IsError:   p.IsError,
		}, nil
	case session.PartImage:
		return contentItem{
			Type: "image",
			Source: &imageSource{
				Type:      "base64",
				MediaType: p.ImageMimeType,
				Data:      p.ImageBase64,
			},
		}, nil
	default:
		return contentItem{}, fmt.Errorf("anthropic: unknown part kind %q", p.Kind)
	}
}

// attachCacheMarker attaches cache_control: ephemeral to the final text-like
// block of the given message, if one exists.  Anthropic caches the prompt
// prefix up to (and including) the block bearing the marker.
func attachCacheMarker(wm *wireMessage) {
	for i := len(wm.Content) - 1; i >= 0; i-- {
		switch wm.Content[i].Type {
		case "text", "tool_result":
			wm.Content[i].CacheControl = &cacheControl{Type: "ephemeral"}
			return
		}
	}
}
