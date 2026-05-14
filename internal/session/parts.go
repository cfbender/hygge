package session

import (
	"encoding/json"
	"errors"
	"fmt"
)

// PartKind enumerates the supported message content block kinds.  The set is
// closed; UnmarshalParts rejects unknown kinds.
type PartKind string

// Recognised part kinds.
const (
	PartText       PartKind = "text"
	PartToolUse    PartKind = "tool_use"
	PartToolResult PartKind = "tool_result"
	PartThinking   PartKind = "thinking"
	PartImage      PartKind = "image"
)

// Part is a single content block inside a Message.  Modern LLM messages are
// heterogeneous: text, tool calls, tool results, model "thinking", and
// inline images can all appear in a single turn.  This struct uses a tagged
// union: Kind selects which of the other fields are populated.
type Part struct {
	Kind PartKind `json:"type"`

	// Text and Thinking use this field.
	Text string `json:"text,omitempty"`

	// ToolUse — the provider-assigned id of the tool call, its name, and
	// the JSON-encoded input arguments.
	ToolID    string          `json:"id,omitempty"`
	ToolName  string          `json:"name,omitempty"`
	ToolInput json.RawMessage `json:"input,omitempty"`

	// ToolResult — pairs with a prior ToolUse by ToolUseID.  Content is
	// textual; binary tool outputs are out of scope for v0.1.
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// Image — inline image attachment.
	ImageMimeType string `json:"image_mime_type,omitempty"`
	ImageBase64   string `json:"image_base64,omitempty"`
}

// ErrUnknownPartKind is returned by UnmarshalParts when an element's "type"
// field is not one of the recognised PartKind constants.
var ErrUnknownPartKind = errors.New("session: unknown part kind")

// ErrInvalidPart is returned when a part is structurally invalid (missing a
// kind-specific required field, malformed JSON, etc.).
var ErrInvalidPart = errors.New("session: invalid part")

// MarshalParts encodes parts as the JSON document stored in messages.parts.
// A nil or empty slice marshals to "[]".
func MarshalParts(parts []Part) ([]byte, error) {
	if parts == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(parts)
}

// UnmarshalParts decodes the JSON document stored in messages.parts.  It is
// strict: unknown kinds return ErrUnknownPartKind, and kind-specific
// required fields are validated (e.g. tool_use requires both id and name).
func UnmarshalParts(data []byte) ([]Part, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: empty input", ErrInvalidPart)
	}
	var parts []Part
	if err := json.Unmarshal(data, &parts); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidPart, err)
	}
	for i, p := range parts {
		if err := validatePart(p); err != nil {
			return nil, fmt.Errorf("part %d: %w", i, err)
		}
	}
	return parts, nil
}

// validatePart enforces the per-kind required fields.  It is used both by
// UnmarshalParts and indirectly as documentation of the contract.
func validatePart(p Part) error {
	switch p.Kind {
	case PartText, PartThinking:
		// Text content may legitimately be empty (e.g. thinking block with no
		// content); no required fields beyond Kind.
		return nil
	case PartToolUse:
		if p.ToolID == "" {
			return fmt.Errorf("%w: tool_use missing id", ErrInvalidPart)
		}
		if p.ToolName == "" {
			return fmt.Errorf("%w: tool_use missing name", ErrInvalidPart)
		}
		return nil
	case PartToolResult:
		if p.ToolUseID == "" {
			return fmt.Errorf("%w: tool_result missing tool_use_id", ErrInvalidPart)
		}
		return nil
	case PartImage:
		if p.ImageMimeType == "" {
			return fmt.Errorf("%w: image missing image_mime_type", ErrInvalidPart)
		}
		if p.ImageBase64 == "" {
			return fmt.Errorf("%w: image missing image_base64", ErrInvalidPart)
		}
		return nil
	case "":
		return fmt.Errorf("%w: missing type field", ErrInvalidPart)
	default:
		return fmt.Errorf("%w: %q", ErrUnknownPartKind, p.Kind)
	}
}
