package permission

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeAsker returns a canned decision/error from Ask and records the
// request it received.
type fakeAsker struct {
	decision Decision
	err      error
	got      Request
}

func (f *fakeAsker) Ask(_ context.Context, req Request) (Decision, error) {
	f.got = req
	return f.decision, f.err
}

func TestGate_NilEngineAllows(t *testing.T) {
	t.Parallel()
	if err := Gate(context.Background(), nil, Request{ToolName: "bash"}); err != nil {
		t.Fatalf("Gate with nil engine: %v", err)
	}
}

func TestGate_AllowReturnsNil(t *testing.T) {
	t.Parallel()
	eng := &fakeAsker{decision: Decision{Action: ActionAllow, Scope: ScopeOnce}}
	req := Request{Category: CategoryShell, Target: "rm -rf /tmp/x", ToolName: "bash"}
	if err := Gate(context.Background(), eng, req); err != nil {
		t.Fatalf("Gate on allow: %v", err)
	}
	if eng.got.Target != req.Target {
		t.Fatalf("request not forwarded: got %+v", eng.got)
	}
}

func TestGate_DenyWrapsErrDenied(t *testing.T) {
	t.Parallel()
	eng := &fakeAsker{decision: Decision{Action: ActionDeny, Reason: "outside pwd"}}
	req := Request{Category: CategoryFileWrite, Target: "/etc/passwd", ToolName: "edit"}
	err := Gate(context.Background(), eng, req)
	if err == nil {
		t.Fatal("Gate on deny: want error, got nil")
	}
	if !errors.Is(err, ErrDenied) {
		t.Fatalf("errors.Is(err, ErrDenied) = false for %v", err)
	}
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("errors.As *DeniedError = false for %T", err)
	}
	if denied.Reason != "outside pwd" || denied.Target != "/etc/passwd" || denied.Category != CategoryFileWrite {
		t.Fatalf("DeniedError fields: %+v", denied)
	}
	if !strings.Contains(err.Error(), "edit") {
		t.Fatalf("error message missing tool name: %q", err.Error())
	}
}

func TestGate_DenyEmptyReasonDefaultsInMessage(t *testing.T) {
	t.Parallel()
	eng := &fakeAsker{decision: Decision{Action: ActionDeny}}
	err := Gate(context.Background(), eng, Request{ToolName: "read"})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "denied by policy") {
		t.Fatalf("error message missing default reason: %q", err.Error())
	}
	var denied *DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("errors.As *DeniedError = false for %T", err)
	}
	if denied.Reason != "" {
		t.Fatalf("Reason should stay empty for callers, got %q", denied.Reason)
	}
}

func TestGate_AskErrorPropagates(t *testing.T) {
	t.Parallel()
	eng := &fakeAsker{err: ErrEngineClosed}
	err := Gate(context.Background(), eng, Request{ToolName: "bash"})
	if !errors.Is(err, ErrEngineClosed) {
		t.Fatalf("want ErrEngineClosed, got %v", err)
	}
	if errors.Is(err, ErrDenied) {
		t.Fatalf("Ask failure must not look like a denial: %v", err)
	}
}
