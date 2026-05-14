package subagent

import (
	"testing"
)

func TestType_BuiltinGeneralIsSane(t *testing.T) {
	if builtinGeneral.Name != "general" {
		t.Fatalf("builtinGeneral.Name: got %q", builtinGeneral.Name)
	}
	if builtinGeneral.Description == "" {
		t.Fatal("builtinGeneral.Description must not be empty")
	}
	if builtinGeneral.SystemPrompt == "" {
		t.Fatal("builtinGeneral.SystemPrompt must not be empty")
	}
	if builtinGeneral.Source != "builtin" {
		t.Fatalf("builtinGeneral.Source: got %q want %q", builtinGeneral.Source, "builtin")
	}
	if len(builtinGeneral.Tools) != 0 {
		t.Fatalf("builtinGeneral.Tools must be empty (means defaults), got %v", builtinGeneral.Tools)
	}
}

func TestResult_ZeroValueIsHarmless(t *testing.T) {
	// Result has no constructor; consumers may inspect a zero value
	// (e.g. when Run returns early).  Confirm every field reads
	// without panicking and reads as the documented zero.
	var r Result
	if r.SessionID != "" || r.FinalText != "" || r.HitIterLimit {
		t.Fatalf("Result zero value is not zero: %+v", r)
	}
	if r.Cost.USD != 0 {
		t.Fatalf("Result.Cost.USD: got %v want 0", r.Cost.USD)
	}
	if r.Duration != 0 {
		t.Fatalf("Result.Duration: got %v want 0", r.Duration)
	}
}
