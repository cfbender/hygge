package session

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestNewSessionID_Format(t *testing.T) {
	id := NewSessionID()
	if len(id) != 26 {
		t.Fatalf("expected 26-char ULID, got %q (len=%d)", id, len(id))
	}
	// ULID canonical form is Crockford Base32, no lowercase.
	if id != strings.ToUpper(id) {
		t.Fatalf("expected uppercase ULID, got %q", id)
	}
}

func TestNewIDs_MonotonicAndUnique(t *testing.T) {
	const n = 5000
	ids := make([]string, 0, n)
	seen := make(map[string]struct{}, n)
	for range n {
		id := NewMessageID()
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id: %q", id)
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("ids not monotonic at %d: %q <= %q", i, ids[i], ids[i-1])
		}
	}
}

func TestNewIDs_ConcurrentSafe(t *testing.T) {
	const goroutines = 32
	const perG = 200
	var wg sync.WaitGroup
	out := make(chan string, goroutines*perG)
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range perG {
				out <- NewMarkerID()
			}
		}()
	}
	wg.Wait()
	close(out)

	seen := make(map[string]struct{}, goroutines*perG)
	for id := range out {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate id under concurrent use: %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines*perG {
		t.Fatalf("expected %d ids, got %d", goroutines*perG, len(seen))
	}
}

func TestMarshalParts_RoundTrip(t *testing.T) {
	in := []Part{
		{Kind: PartText, Text: "hello"},
		{Kind: PartThinking, Text: "(internal reasoning)"},
		{Kind: PartToolUse, ToolID: "call_1", ToolName: "bash", ToolInput: json.RawMessage(`{"cmd":"ls"}`)},
		{Kind: PartToolResult, ToolUseID: "call_1", Content: "file1\nfile2", IsError: false},
		{Kind: PartImage, ImageMimeType: "image/png", ImageBase64: "iVBORw0KGgo="},
	}
	data, err := MarshalParts(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := UnmarshalParts(data)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len mismatch: got %d want %d", len(out), len(in))
	}
	for i := range in {
		if out[i].Kind != in[i].Kind {
			t.Errorf("part %d kind: got %q want %q", i, out[i].Kind, in[i].Kind)
		}
	}
	// Spot-check the tool_use input survives as raw JSON.
	if string(out[2].ToolInput) != `{"cmd":"ls"}` {
		t.Errorf("tool_use input changed: %q", out[2].ToolInput)
	}
}

func TestMarshalParts_NilAndEmpty(t *testing.T) {
	data, err := MarshalParts(nil)
	if err != nil {
		t.Fatalf("nil marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("nil marshal: got %q want []", data)
	}

	data, err = MarshalParts([]Part{})
	if err != nil {
		t.Fatalf("empty marshal: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("empty marshal: got %q want []", data)
	}
}

func TestUnmarshalParts_StrictUnknownKind(t *testing.T) {
	data := []byte(`[{"type":"video","text":"oops"}]`)
	_, err := UnmarshalParts(data)
	if !errors.Is(err, ErrUnknownPartKind) {
		t.Fatalf("expected ErrUnknownPartKind, got %v", err)
	}
}

func TestUnmarshalParts_MissingTypeField(t *testing.T) {
	data := []byte(`[{"text":"naked"}]`)
	_, err := UnmarshalParts(data)
	if !errors.Is(err, ErrInvalidPart) {
		t.Fatalf("expected ErrInvalidPart, got %v", err)
	}
}

func TestUnmarshalParts_ToolUseRequiresIDAndName(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing id", `[{"type":"tool_use","name":"bash"}]`},
		{"missing name", `[{"type":"tool_use","id":"c1"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalParts([]byte(tc.raw))
			if !errors.Is(err, ErrInvalidPart) {
				t.Fatalf("expected ErrInvalidPart, got %v", err)
			}
		})
	}
}

func TestUnmarshalParts_ToolResultRequiresToolUseID(t *testing.T) {
	data := []byte(`[{"type":"tool_result","content":"out"}]`)
	_, err := UnmarshalParts(data)
	if !errors.Is(err, ErrInvalidPart) {
		t.Fatalf("expected ErrInvalidPart, got %v", err)
	}
}

func TestUnmarshalParts_ImageRequiresBothFields(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"missing mime", `[{"type":"image","image_base64":"AA=="}]`},
		{"missing base64", `[{"type":"image","image_mime_type":"image/png"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := UnmarshalParts([]byte(tc.raw))
			if !errors.Is(err, ErrInvalidPart) {
				t.Fatalf("expected ErrInvalidPart, got %v", err)
			}
		})
	}
}

func TestUnmarshalParts_EmptyInput(t *testing.T) {
	_, err := UnmarshalParts(nil)
	if !errors.Is(err, ErrInvalidPart) {
		t.Fatalf("expected ErrInvalidPart for nil, got %v", err)
	}
	_, err = UnmarshalParts([]byte{})
	if !errors.Is(err, ErrInvalidPart) {
		t.Fatalf("expected ErrInvalidPart for empty, got %v", err)
	}
}

func TestUnmarshalParts_BadJSON(t *testing.T) {
	_, err := UnmarshalParts([]byte("not json"))
	if !errors.Is(err, ErrInvalidPart) {
		t.Fatalf("expected ErrInvalidPart, got %v", err)
	}
}

func TestPart_JSONShape(t *testing.T) {
	p := Part{Kind: PartText, Text: "hi"}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Required: "type" field present.
	if !strings.Contains(string(data), `"type":"text"`) {
		t.Errorf("expected type field, got %s", data)
	}
	// Optional fields omitted when empty.
	if strings.Contains(string(data), "image_mime_type") {
		t.Errorf("expected no image fields, got %s", data)
	}
}
