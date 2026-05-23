package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeCompactor is a test double for the Compactor interface.
type fakeCompactor struct {
	// err is returned by Compact.  nil means success.
	err error
	// called tracks whether Compact was invoked.
	called bool
	// lastSessionID is the session ID passed to Compact.
	lastSessionID string
}

func (f *fakeCompactor) Compact(_ context.Context, sessionID string) error {
	f.called = true
	f.lastSessionID = sessionID
	return f.err
}

// errOtherFailure is a generic error for tests.
var errOtherFailure = errors.New("some internal failure")

type testNothingToCompactError struct{}

func (testNothingToCompactError) Error() string            { return "agent: nothing to compact" }
func (testNothingToCompactError) IsNothingToCompact() bool { return true }

// --- CompactTool unit tests --------------------------------------------------

func TestCompactTool_Name(t *testing.T) {
	tt := NewCompactTool(&fakeCompactor{})
	if got, want := tt.Name(), "compact"; got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestCompactTool_NotParallelizable(t *testing.T) {
	tt := NewCompactTool(&fakeCompactor{})
	if tt.Parallelizable() {
		t.Error("Parallelizable() = true, want false (compact mutates session state)")
	}
}

func TestCompactTool_InputSchema(t *testing.T) {
	tt := NewCompactTool(&fakeCompactor{})
	s := tt.InputSchema()
	if s["type"] != "object" {
		t.Errorf("schema type = %v, want object", s["type"])
	}
	if s["additionalProperties"] != false {
		t.Errorf("schema additionalProperties = %v, want false", s["additionalProperties"])
	}
}

func TestCompactTool_Description(t *testing.T) {
	tt := NewCompactTool(&fakeCompactor{})
	d := tt.Description()
	if d == "" {
		t.Error("Description() is empty")
	}
	for _, want := range []string{"configured compaction threshold", "compact", "context"} {
		if !strings.Contains(d, want) {
			t.Errorf("Description() missing %q:\n%s", want, d)
		}
	}
}

func TestCompactTool_Success(t *testing.T) {
	fc := &fakeCompactor{}
	tt := NewCompactTool(fc)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, t.TempDir())

	res, err := tt.Execute(context.Background(), json.RawMessage(`{}`), ec)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if res.IsError {
		t.Errorf("expected success result, got IsError=true: %s", res.Content)
	}
	if res.Content == "" {
		t.Error("expected non-empty success Content")
	}
	if !fc.called {
		t.Error("Compact was not called on the compactor")
	}
	if fc.lastSessionID != ec.SessionID {
		t.Errorf("Compact called with session ID %q, want %q", fc.lastSessionID, ec.SessionID)
	}
}

func TestCompactTool_NothingToCompact(t *testing.T) {
	fc := &fakeCompactor{err: testNothingToCompactError{}}
	tt := NewCompactTool(fc)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, t.TempDir())

	res, err := tt.Execute(context.Background(), json.RawMessage(`{}`), ec)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true for ErrNothingToCompact, got false")
	}
	if res.Metadata["reason"] != "nothing_to_compact" {
		t.Errorf("metadata.reason = %v, want nothing_to_compact", res.Metadata["reason"])
	}
}

func TestCompactTool_CompactionFailure(t *testing.T) {
	fc := &fakeCompactor{err: errOtherFailure}
	tt := NewCompactTool(fc)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, t.TempDir())

	res, err := tt.Execute(context.Background(), json.RawMessage(`{}`), ec)
	if err != nil {
		t.Fatalf("Execute returned transport error: %v", err)
	}
	if !res.IsError {
		t.Errorf("expected IsError=true for compaction failure, got false")
	}
	if res.Metadata["reason"] != "compaction_error" {
		t.Errorf("metadata.reason = %v, want compaction_error", res.Metadata["reason"])
	}
}

func TestCompactTool_NilCompactor(t *testing.T) {
	tt := NewCompactTool(nil)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, t.TempDir())

	_, err := tt.Execute(context.Background(), json.RawMessage(`{}`), ec)
	if err == nil {
		t.Fatal("expected *ToolError when compactor is nil, got nil")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != CodeExecutionFailed {
		t.Errorf("ToolError.Code = %q, want %q", te.Code, CodeExecutionFailed)
	}
}

func TestCompactTool_EmptySessionID(t *testing.T) {
	fc := &fakeCompactor{}
	tt := NewCompactTool(fc)
	e, b := builtinTestEngine(t, allowAll)
	// Deliberately use an empty session ID.
	ec := ExecContext{
		SessionID:  "",
		Bus:        b,
		Permission: e,
	}

	_, err := tt.Execute(context.Background(), json.RawMessage(`{}`), ec)
	if err == nil {
		t.Fatal("expected *ToolError when session ID is empty, got nil")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != CodeExecutionFailed {
		t.Errorf("ToolError.Code = %q, want %q", te.Code, CodeExecutionFailed)
	}
	// Compactor must NOT have been called.
	if fc.called {
		t.Error("Compact was called despite empty session ID")
	}
}

func TestCompactTool_RejectsUnknownArgs(t *testing.T) {
	fc := &fakeCompactor{}
	tt := NewCompactTool(fc)
	e, b := builtinTestEngine(t, allowAll)
	ec := newExecContext(b, e, t.TempDir())

	_, err := tt.Execute(context.Background(), json.RawMessage(`{"unknown_field": true}`), ec)
	if err == nil {
		t.Fatal("expected *ToolError for unknown field, got nil")
	}
	var te *ToolError
	if !errors.As(err, &te) {
		t.Fatalf("expected *ToolError, got %T: %v", err, err)
	}
	if te.Code != CodeInvalidArgs {
		t.Errorf("ToolError.Code = %q, want %q", te.Code, CodeInvalidArgs)
	}
}

// --- isNothingToCompact unit tests ------------------------------------------

func TestIsNothingToCompact_Direct(t *testing.T) {
	if !isNothingToCompact(testNothingToCompactError{}) {
		t.Error("isNothingToCompact(tagged) = false, want true")
	}
}

func TestIsNothingToCompact_WrappedIsFalse(t *testing.T) {
	wrapped := fmt.Errorf("agent: Compact: load messages: %w", testNothingToCompactError{})
	if isNothingToCompact(wrapped) {
		t.Error("isNothingToCompact(wrapped) = true, want false")
	}
}

func TestIsNothingToCompact_OtherError(t *testing.T) {
	if isNothingToCompact(errOtherFailure) {
		t.Error("isNothingToCompact(other) = true, want false")
	}
}

func TestIsNothingToCompact_Nil(t *testing.T) {
	if isNothingToCompact(nil) {
		t.Error("isNothingToCompact(nil) = true, want false")
	}
}

// --- Tool registry integration tests ----------------------------------------

// TestCompactTool_NotInDefault ensures Default() (no Compactor supplied) does
// NOT include "compact" — the tool requires a wired agent.
func TestCompactTool_NotInDefault(t *testing.T) {
	r := Default()
	if _, ok := r.Get("compact"); ok {
		t.Error("compact tool should not be registered in Default() (requires agent wiring)")
	}
}

// TestCompactTool_RegistrationRoundTrip verifies the tool can be registered
// and retrieved by name with valid schema/description.
func TestCompactTool_RegistrationRoundTrip(t *testing.T) {
	r := Default()
	fc := &fakeCompactor{}
	tt := NewCompactTool(fc)
	if err := r.Register(tt); err != nil {
		t.Fatalf("Register(compact): %v", err)
	}
	got, ok := r.Get("compact")
	if !ok {
		t.Fatal("Get(compact) = false after Register")
	}
	if got.Name() != "compact" {
		t.Errorf("Name() = %q, want compact", got.Name())
	}
	if got.Description() == "" {
		t.Error("Description() is empty after registration")
	}
	s := got.InputSchema()
	if s["type"] != "object" {
		t.Errorf("schema type = %v, want object", s["type"])
	}
}
