package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/memory"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/session"
)

type fakeSessionMemoryStore struct {
	next     int
	memories map[string]*session.Memory
}

func newFakeSessionMemoryStore() *fakeSessionMemoryStore {
	return &fakeSessionMemoryStore{memories: map[string]*session.Memory{}}
}

func (f *fakeSessionMemoryStore) RememberSessionMemory(_ context.Context, sessionID string, in session.NewMemory) (*session.Memory, error) {
	f.next++
	m := &session.Memory{ID: fmt.Sprintf("mem-%d", f.next), Scope: session.MemoryScopeSession, SessionID: sessionID, Content: in.Content}
	f.memories[m.ID] = m
	return m, nil
}

func (f *fakeSessionMemoryStore) ForgetSessionMemory(_ context.Context, sessionID, memoryID string) (*session.Memory, error) {
	m, ok := f.memories[memoryID]
	if !ok || m.SessionID != sessionID {
		return nil, session.ErrMemoryNotFound
	}
	delete(f.memories, memoryID)
	return m, nil
}

func TestRememberToolStoresSessionMemory(t *testing.T) {
	store := newFakeSessionMemoryStore()
	tt := newRememberTool(store, nil)
	res, err := tt.Execute(context.Background(), json.RawMessage(`{"scope":"session","content":"prefers focused tests"}`), ExecContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError || res.Metadata["memory_id"] != "mem-1" || res.Metadata["scope"] != "session" {
		t.Fatalf("result = %+v", res)
	}
	if got := store.memories["mem-1"].Content; got != "prefers focused tests" {
		t.Fatalf("stored content = %q", got)
	}
}

func TestRememberToolRejectsLikelySecretForAllScopes(t *testing.T) {
	store := newFakeSessionMemoryStore()
	tt := newRememberTool(store, nil)
	res, err := tt.Execute(context.Background(), json.RawMessage(`{"content":"api_key=super-secret"}`), ExecContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || res.Metadata["error"] != "secret_detected" {
		t.Fatalf("result = %+v, want secret IsError", res)
	}
	if len(store.memories) != 0 {
		t.Fatalf("stored secret memory: %+v", store.memories)
	}
}

func TestRememberToolProjectMemoryRequiresUserConfirmation(t *testing.T) {
	projectDir := t.TempDir()
	store := memory.NewFileStore(memory.FileStoreOptions{ProjectDir: projectDir, HomeDir: t.TempDir()})
	tt := newRememberTool(nil, store)
	res, err := tt.Execute(context.Background(), json.RawMessage(`{"scope":"project","content":"use mise run precommit"}`), ExecContext{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError || res.Metadata["error"] != "confirmation_required" {
		t.Fatalf("result = %+v, want confirmation_required IsError", res)
	}
	if !strings.Contains(res.Content, "explicit user confirmation") {
		t.Fatalf("content = %q, want confirmation guidance", res.Content)
	}
}

func TestRememberToolProjectMemoryRequiresPermission(t *testing.T) {
	projectDir := t.TempDir()
	rec := newRecordingResponder(permissionDecision(permission.ActionAllow))
	e, b := builtinTestEngine(t, rec.decide)
	tt := newRememberTool(nil, memory.NewFileStore(memory.FileStoreOptions{ProjectDir: projectDir, HomeDir: t.TempDir()}))

	res, err := tt.Execute(context.Background(), json.RawMessage(`{"scope":"project","content":"use mise run precommit","confirmed_by_user":true}`), newExecContext(b, e, projectDir))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError || res.Metadata["path"] == "" || res.Metadata["scope"] != "project" {
		t.Fatalf("result = %+v", res)
	}
	reqs := rec.snapshot()
	if len(reqs) != 1 || reqs[0].Category != string(permission.CategoryFileWrite) || reqs[0].ToolName != "remember" {
		t.Fatalf("permission requests = %+v", reqs)
	}
}

func TestRememberToolSchemaDocumentsAutonomousMemoryPolicy(t *testing.T) {
	tt := newRememberTool(nil, nil)
	desc := tt.Description()
	for _, want := range []string{"session task constraints autonomously", "project/global memories require explicit user confirmation"} {
		if !strings.Contains(desc, want) {
			t.Fatalf("description missing %q:\n%s", want, desc)
		}
	}
	schemaBytes, err := json.Marshal(tt.InputSchema())
	if err != nil {
		t.Fatalf("Marshal schema: %v", err)
	}
	schema := string(schemaBytes)
	for _, want := range []string{"confirmed_by_user", "Required true for project/global memories", "confirmed a suggestion"} {
		if !strings.Contains(schema, want) {
			t.Fatalf("schema missing %q:\n%s", want, schema)
		}
	}
}

func TestForgetToolForgetsSessionMemoryAndReportsMissingAsResult(t *testing.T) {
	store := newFakeSessionMemoryStore()
	remember := newRememberTool(store, nil)
	remembered, err := remember.Execute(context.Background(), json.RawMessage(`{"content":"temporary fact"}`), ExecContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("remember Execute: %v", err)
	}
	memoryID := remembered.Metadata["memory_id"].(string)
	forget := newForgetTool(store, nil)
	res, err := forget.Execute(context.Background(), json.RawMessage(`{"memory_id":"`+memoryID+`"}`), ExecContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("forget Execute: %v", err)
	}
	if res.IsError || res.Metadata["memory_id"] != memoryID {
		t.Fatalf("forget result = %+v", res)
	}
	res, err = forget.Execute(context.Background(), json.RawMessage(`{"memory_id":"`+memoryID+`"}`), ExecContext{SessionID: "sess-1"})
	if err != nil {
		t.Fatalf("second forget Execute: %v", err)
	}
	if !res.IsError || res.Metadata["error"] != "not_found" {
		t.Fatalf("second forget result = %+v, want not_found IsError", res)
	}
}

func permissionDecision(action permission.Action) bus.PermissionReplied {
	return bus.PermissionReplied{Decision: string(action), Scope: string(permission.ScopeOnce)}
}
