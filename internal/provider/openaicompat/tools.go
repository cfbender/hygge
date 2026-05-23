package openaicompat

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/cfbender/hygge/internal/provider"
	"github.com/cfbender/hygge/internal/session"
)

// thinkingWarnOnce ensures we slog.Warn at most once per process about
// PartThinking blocks being dropped — the model emits one warning per
// build, not one per Send.  This is intentionally a global; the warning is
// purely informational.
var thinkingWarnOnce sync.Once

// encodeJSON marshals v with HTML escaping disabled.  HTML escaping flips
// `<` → `\u003c` in tool argument strings, which is technically valid JSON
// but ugly in request logs.
func encodeJSON(v any) ([]byte, error) {
	return json.Marshal(v)
}

// toWireTools maps provider.Tool to the OpenAI tools array.
func toWireTools(tools []provider.Tool) []chatTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]chatTool, 0, len(tools))
	for _, t := range tools {
		schema := sanitizeToolSchema(t.InputSchema)
		out = append(out, chatTool{
			Type: "function",
			Function: chatToolFunctionSchema{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	return out
}

func sanitizeToolSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object"}
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if k == "required" && v == nil {
			continue
		}
		out[k] = v
	}
	return out
}

// toWireMessages converts a session message history plus a system-prompt
// string into the role/content shape OpenAI expects.
//
// System routing: if system is non-empty, a single {"role":"system","content":...}
// message is prepended.  In addition, any session.RoleSystem messages found
// in msgs are emitted as their own system messages, preserving ordering.
//
// Multi-tool-result expansion: a single session.Message of role "tool" that
// contains multiple PartToolResult blocks expands into N separate
// {"role":"tool","tool_call_id":...} messages — OpenAI requires one tool
// message per tool_call_id.
func toWireMessages(system string, msgs []session.Message) ([]chatMessage, error) {
	out := make([]chatMessage, 0, len(msgs)+1)

	if system != "" {
		content, err := encodeJSON(system)
		if err != nil {
			return nil, err
		}
		out = append(out, chatMessage{Role: "system", Content: content})
	}

	for _, m := range msgs {
		switch m.Role {
		case session.RoleSystem:
			text := joinTextParts(m.Parts)
			content, err := encodeJSON(text)
			if err != nil {
				return nil, err
			}
			out = append(out, chatMessage{Role: "system", Content: content})
		case session.RoleUser:
			wm, err := userMessageToWire(m.Parts)
			if err != nil {
				return nil, err
			}
			out = append(out, wm)
		case session.RoleAssistant:
			wm, err := assistantMessageToWire(m.Parts)
			if err != nil {
				return nil, err
			}
			out = append(out, wm)
		case session.RoleTool:
			toolMsgs, err := toolMessageToWire(m.Parts)
			if err != nil {
				return nil, err
			}
			out = append(out, toolMsgs...)
		default:
			return nil, fmt.Errorf("unsupported role %q", m.Role)
		}
	}

	return out, nil
}

// joinTextParts concatenates the text fields of every PartText in parts,
// separated by blank lines.  Used to fold a multi-text system message into
// the single string OpenAI accepts.
func joinTextParts(parts []session.Part) string {
	first := true
	var out strings.Builder
	for _, p := range parts {
		if p.Kind == session.PartText {
			if !first {
				out.WriteString("\n\n")
			}
			out.WriteString(p.Text)
			first = false
		}
	}
	return out.String()
}

// userMessageToWire builds a user-role chatMessage from session parts.
//
// If the parts are all text and there are no images, the content is emitted
// as a single string for compactness and broader compatibility.  As soon as
// an image is present, content becomes a polymorphic array of content
// blocks.
func userMessageToWire(parts []session.Part) (chatMessage, error) {
	hasImage := false
	for _, p := range parts {
		switch p.Kind {
		case session.PartImage:
			hasImage = true
		case session.PartThinking:
			warnThinkingDropped()
		}
	}

	if !hasImage {
		var text strings.Builder
		first := true
		for _, p := range parts {
			if p.Kind == session.PartText {
				if !first {
					text.WriteString("\n\n")
				}
				text.WriteString(p.Text)
				first = false
			}
		}
		content, err := encodeJSON(text.String())
		if err != nil {
			return chatMessage{}, err
		}
		return chatMessage{Role: "user", Content: content}, nil
	}

	blocks := make([]chatContentBlock, 0, len(parts))
	for _, p := range parts {
		switch p.Kind {
		case session.PartText:
			blocks = append(blocks, chatContentBlock{Type: "text", Text: p.Text})
		case session.PartImage:
			url := fmt.Sprintf("data:%s;base64,%s", p.ImageMimeType, p.ImageBase64)
			blocks = append(blocks, chatContentBlock{
				Type:     "image_url",
				ImageURL: &chatImageURL{URL: url},
			})
		case session.PartThinking:
			// already warned above; skip
		default:
			return chatMessage{}, fmt.Errorf("unsupported user part %q", p.Kind)
		}
	}
	content, err := encodeJSON(blocks)
	if err != nil {
		return chatMessage{}, err
	}
	return chatMessage{Role: "user", Content: content}, nil
}

// assistantMessageToWire builds an assistant-role chatMessage from session
// parts.  The wrinkle: an assistant turn may carry text AND/OR tool_calls.
// Per the OpenAI spec:
//
//   - Text only → {"role":"assistant","content":"<text>"}
//   - Tool calls only → {"role":"assistant","content":null,"tool_calls":[...]}
//   - Both → {"role":"assistant","content":"<text>","tool_calls":[...]}
//
// content=null is represented by leaving the Content field nil (omitempty).
func assistantMessageToWire(parts []session.Part) (chatMessage, error) {
	var text strings.Builder
	textFirst := true
	var toolCalls []chatToolCall

	for _, p := range parts {
		switch p.Kind {
		case session.PartText:
			if !textFirst {
				text.WriteString("\n\n")
			}
			text.WriteString(p.Text)
			textFirst = false
		case session.PartToolUse:
			args := string(p.ToolInput)
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, chatToolCall{
				ID:   p.ToolID,
				Type: "function",
				Function: chatToolFunction{
					Name:      p.ToolName,
					Arguments: args,
				},
			})
		case session.PartThinking:
			warnThinkingDropped()
		case session.PartImage:
			return chatMessage{}, fmt.Errorf("openaicompat: image parts are not supported on assistant messages")
		case session.PartToolResult:
			return chatMessage{}, fmt.Errorf("openaicompat: tool_result on assistant message; should be on a tool-role message")
		default:
			return chatMessage{}, fmt.Errorf("unsupported assistant part %q", p.Kind)
		}
	}

	wm := chatMessage{Role: "assistant", ToolCalls: toolCalls}
	if text.String() != "" || len(toolCalls) == 0 {
		content, err := encodeJSON(text.String())
		if err != nil {
			return chatMessage{}, err
		}
		wm.Content = content
	}
	return wm, nil
}

// toolMessageToWire expands a session tool-role message into one OpenAI
// {"role":"tool"} message per PartToolResult.  Other part kinds on a
// tool-role message are rejected.
func toolMessageToWire(parts []session.Part) ([]chatMessage, error) {
	out := make([]chatMessage, 0, len(parts))
	for _, p := range parts {
		if p.Kind != session.PartToolResult {
			return nil, fmt.Errorf("openaicompat: non-tool_result part %q on tool message", p.Kind)
		}
		content, err := encodeJSON(p.Content)
		if err != nil {
			return nil, err
		}
		out = append(out, chatMessage{
			Role:       "tool",
			ToolCallID: p.ToolUseID,
			Content:    content,
		})
	}
	return out, nil
}

// warnThinkingDropped emits a single slog.Warn for the lifetime of the
// process noting that PartThinking blocks are dropped on OpenAI-compatible
// providers.  Spec compliance: OpenAI's API doesn't accept thinking blocks
// in the request shape.
func warnThinkingDropped() {
	thinkingWarnOnce.Do(func() {
		slog.Warn("openaicompat: dropping thinking parts; OpenAI-compatible providers do not accept them in request messages")
	})
}
