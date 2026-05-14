package subagent

import (
	"errors"
	"testing"
)

func TestParseModelRef_Valid(t *testing.T) {
	tests := []struct {
		in       string
		wantProv string
		wantID   string
	}{
		{"anthropic/claude-haiku-4-5", "anthropic", "claude-haiku-4-5"},
		{"openai/gpt-4o-mini", "openai", "gpt-4o-mini"},
		{"openrouter/anthropic/claude-haiku-4-5", "openrouter", "anthropic/claude-haiku-4-5"},
		{"  anthropic/claude-haiku-4-5  ", "anthropic", "claude-haiku-4-5"},
		{"x_ai/grok-3", "x_ai", "grok-3"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			prov, id, err := ParseModelRef(tt.in)
			if err != nil {
				t.Fatalf("ParseModelRef(%q): %v", tt.in, err)
			}
			if prov != tt.wantProv {
				t.Errorf("provider: got %q want %q", prov, tt.wantProv)
			}
			if id != tt.wantID {
				t.Errorf("model id: got %q want %q", id, tt.wantID)
			}
		})
	}
}

func TestParseModelRef_Invalid(t *testing.T) {
	tests := []string{
		"",
		"anthropic",            // no slash
		"/claude",              // empty provider
		"Anthropic/claude",     // uppercase
		"1openai/x",            // digit-leading
		"anthropic-foo/claude", // hyphens disallowed in provider name
		"anthropic/",           // empty model
	}
	for _, in := range tests {
		t.Run(in, func(t *testing.T) {
			_, _, err := ParseModelRef(in)
			if err == nil {
				t.Fatalf("ParseModelRef(%q): expected error", in)
			}
			if !errors.Is(err, ErrInvalidModelRef) {
				t.Errorf("error not wrapping ErrInvalidModelRef: %v", err)
			}
		})
	}
}

func TestIsValidModelRef(t *testing.T) {
	if !IsValidModelRef("anthropic/claude-haiku-4-5") {
		t.Error("expected true for valid ref")
	}
	if IsValidModelRef("") {
		t.Error("expected false for empty string")
	}
	if IsValidModelRef("nope") {
		t.Error("expected false for missing slash")
	}
}
