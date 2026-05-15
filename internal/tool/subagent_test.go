package tool

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/provider"
)

// stubRunner is a hand-rolled SubagentRunner used to drive the subagent
// tool's tests without spinning up the full internal/subagent runtime.
type stubRunner struct {
	types  []SubagentType
	res    SubagentResult
	runErr error
	lastIn SubagentRunInput
	called atomic.Int32
}

func (s *stubRunner) Types() []SubagentType { return s.types }
func (s *stubRunner) Run(_ context.Context, in SubagentRunInput) (SubagentResult, error) {
	s.called.Add(1)
	s.lastIn = in
	return s.res, s.runErr
}

func standardTypes() []SubagentType {
	return []SubagentType{
		{Name: "general", Description: "General purpose."},
		{Name: "searcher", Description: "Find things."},
	}
}

func subagentExecCtx(t *testing.T, decide func(bus.PermissionAsked) bus.PermissionReplied) ExecContext {
	t.Helper()
	e, b := testEngine(t, decide)
	return ExecContext{
		SessionID:  "parent_session",
		Pwd:        t.TempDir(),
		Bus:        b,
		Permission: e,
		ToolUseID:  "tool_use_1",
		MessageID:  "msg_1",
		ModelName:  "fake-model",
		Now:        func() time.Time { return time.Unix(0, 0) },
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// ---------- happy path -----------------------------------------------------

func TestSubagentTool_NameDescriptionSchema(t *testing.T) {
	stub := &stubRunner{types: standardTypes()}
	tt := NewSubagentTool(stub)

	if tt.Name() != "subagent" {
		t.Fatalf("Name: got %q want subagent", tt.Name())
	}
	if tt.Description() == "" {
		t.Fatal("Description must not be empty")
	}
	schema := tt.InputSchema()
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing or wrong type: %v", schema)
	}
	subType, ok := props["subagent_type"].(map[string]any)
	if !ok {
		t.Fatalf("subagent_type missing: %v", props)
	}
	enum, ok := subType["enum"].([]any)
	if !ok {
		t.Fatalf("enum missing or wrong type: %v", subType)
	}
	if len(enum) != 2 {
		t.Fatalf("enum: got %v want 2 entries", enum)
	}
}

func TestSubagentTool_InputSchema_EmptyTypesFallback(t *testing.T) {
	stub := &stubRunner{types: nil}
	tt := NewSubagentTool(stub)
	schema := tt.InputSchema()
	props := schema["properties"].(map[string]any)
	subType := props["subagent_type"].(map[string]any)
	enum := subType["enum"].([]any)
	if len(enum) != 1 {
		t.Fatalf("fallback enum should have one entry, got %v", enum)
	}
	if enum[0].(string) != "general" {
		t.Fatalf("fallback enum entry: got %q want general", enum[0])
	}
}

func TestSubagentTool_HappyPath(t *testing.T) {
	stub := &stubRunner{
		types: standardTypes(),
		res: SubagentResult{
			SessionID: "sub_1",
			FinalText: "found 3 files",
			Usage:     provider.Usage{InputTokens: 100, OutputTokens: 20},
			CostUSD:   0.0123,
			Duration:  150 * time.Millisecond,
		},
	}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})

	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "search for foo",
		"prompt":        "Find every file named foo.",
	})
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if res.Content != "found 3 files" {
		t.Fatalf("Content: %q", res.Content)
	}
	if stub.called.Load() != 1 {
		t.Fatalf("runner.Run call count: got %d want 1", stub.called.Load())
	}
	if stub.lastIn.ParentSessionID != "parent_session" {
		t.Errorf("ParentSessionID: %q", stub.lastIn.ParentSessionID)
	}
	if stub.lastIn.ParentToolUseID != "tool_use_1" {
		t.Errorf("ParentToolUseID: %q", stub.lastIn.ParentToolUseID)
	}
	if stub.lastIn.ModelName != "fake-model" {
		t.Errorf("ModelName: %q", stub.lastIn.ModelName)
	}
	if got := res.Metadata["sub_session_id"]; got != "sub_1" {
		t.Errorf("Metadata.sub_session_id: %v", got)
	}
	if got := res.Metadata["cost_usd"]; got != 0.0123 {
		t.Errorf("Metadata.cost_usd: %v", got)
	}
}

func TestSubagentTool_EmptyFinalTextHasPlaceholder(t *testing.T) {
	stub := &stubRunner{
		types: standardTypes(),
		res:   SubagentResult{SessionID: "sub_2", FinalText: ""},
	}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "x",
		"prompt":        "x",
	})
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Content, "no textual output") {
		t.Fatalf("expected placeholder content, got %q", res.Content)
	}
}

// ---------- error paths ----------------------------------------------------

func TestSubagentTool_UnknownTypeIsError(t *testing.T) {
	stub := &stubRunner{types: standardTypes()}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	args := mustJSON(t, map[string]any{
		"subagent_type": "nonexistent",
		"description":   "x",
		"prompt":        "x",
	})
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError result for unknown type")
	}
	if got := res.Metadata["error"]; got != "unknown_subagent_type" {
		t.Errorf("Metadata.error: %v", got)
	}
	if stub.called.Load() != 0 {
		t.Fatal("runner.Run was called for an unknown type")
	}
}

func TestSubagentTool_PermissionDeniedIsError(t *testing.T) {
	stub := &stubRunner{types: standardTypes()}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "deny", Scope: "once"}
	})
	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "x",
		"prompt":        "x",
	})
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError on deny")
	}
	if stub.called.Load() != 0 {
		t.Fatal("runner.Run was called despite permission deny")
	}
}

func TestSubagentTool_RunnerErrorSurfacesAsIsError(t *testing.T) {
	stub := &stubRunner{
		types:  standardTypes(),
		res:    SubagentResult{SessionID: "sub_3"},
		runErr: errors.New("boom"),
	}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "x",
		"prompt":        "x",
	})
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError when runner returns error")
	}
	if !strings.Contains(res.Content, "sub_3") {
		t.Fatalf("Content should mention sub-session id; got %q", res.Content)
	}
	if got := res.Metadata["sub_session_id"]; got != "sub_3" {
		t.Errorf("Metadata.sub_session_id: %v", got)
	}
}

func TestSubagentTool_IterationLimitReturnedToModel(t *testing.T) {
	stub := &stubRunner{
		types: standardTypes(),
		res: SubagentResult{
			SessionID:    "sub_4",
			FinalText:    "iteration limit reached (3)",
			HitIterLimit: true,
		},
	}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "loop",
		"prompt":        "loop",
	})
	res, err := tt.Execute(context.Background(), args, ec)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatal("iteration limit should not surface as IsError -- it's a soft outcome")
	}
	if got := res.Metadata["hit_iter_limit"]; got != true {
		t.Errorf("Metadata.hit_iter_limit: %v", got)
	}
}

func TestSubagentTool_MissingArgs(t *testing.T) {
	stub := &stubRunner{types: standardTypes()}
	tt := NewSubagentTool(stub)
	ec := subagentExecCtx(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	tests := []struct {
		name string
		args map[string]any
	}{
		{"no type", map[string]any{"description": "x", "prompt": "x"}},
		{"no description", map[string]any{"subagent_type": "general", "prompt": "x"}},
		{"no prompt", map[string]any{"subagent_type": "general", "description": "x"}},
	}
	for _, tt2 := range tests {
		t.Run(tt2.name, func(t *testing.T) {
			args := mustJSON(t, tt2.args)
			_, err := tt.Execute(context.Background(), args, ec)
			if err == nil {
				t.Fatal("expected ToolError for missing arg")
			}
			var te *ToolError
			if !errors.As(err, &te) || te.Code != CodeInvalidArgs {
				t.Fatalf("expected invalid_args, got %v", err)
			}
		})
	}
}

func TestSubagentTool_NilRunner(t *testing.T) {
	tt := NewSubagentTool(nil)
	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "x",
		"prompt":        "x",
	})
	_, err := tt.Execute(context.Background(), args, ExecContext{ModelName: "fake-model"})
	if err == nil {
		t.Fatal("expected ToolError for nil runner")
	}
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %v", err)
	}
}

func TestSubagentTool_MissingModelInExecContext(t *testing.T) {
	stub := &stubRunner{types: standardTypes()}
	tt := NewSubagentTool(stub)
	// ec with no permission engine but auto-allow won't matter --
	// build a real engine but leave ModelName empty so the early
	// check fires.
	e, b := testEngine(t, func(_ bus.PermissionAsked) bus.PermissionReplied {
		return bus.PermissionReplied{Decision: "allow", Scope: "once"}
	})
	ec := ExecContext{
		SessionID:  "parent",
		Pwd:        t.TempDir(),
		Bus:        b,
		Permission: e,
	}
	args := mustJSON(t, map[string]any{
		"subagent_type": "general",
		"description":   "x",
		"prompt":        "x",
	})
	_, err := tt.Execute(context.Background(), args, ec)
	if err == nil {
		t.Fatal("expected error for missing ModelName")
	}
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeExecutionFailed {
		t.Fatalf("expected execution_failed, got %v", err)
	}
}
