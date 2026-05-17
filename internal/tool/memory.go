package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cfbender/hygge/internal/memory"
	"github.com/cfbender/hygge/internal/permission"
	"github.com/cfbender/hygge/internal/session"
)

type sessionMemoryStore interface {
	RememberSessionMemory(ctx context.Context, sessionID string, in session.NewMemory) (*session.Memory, error)
	ForgetSessionMemory(ctx context.Context, sessionID, memoryID string) (*session.Memory, error)
}

type fileMemoryStore interface {
	Remember(ctx context.Context, scope session.MemoryScope, content string) (*session.Memory, error)
	Forget(ctx context.Context, scope session.MemoryScope, memoryID string) (*session.Memory, error)
	MemoryDir(scope session.MemoryScope) (string, error)
}

type rememberTool struct {
	sessionStore sessionMemoryStore
	fileStore    fileMemoryStore
}

func newRememberTool(sessionStore sessionMemoryStore, fileStore fileMemoryStore) *rememberTool {
	return &rememberTool{sessionStore: sessionStore, fileStore: fileStore}
}

func (t *rememberTool) Name() string { return "remember" }

func (t *rememberTool) Description() string {
	return "Persist a durable memory only when the user explicitly asks or clearly confirms a stable preference/fact. Never store secrets, credentials, or transient task details."
}

func (t *rememberTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"content"},
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []any{"session", "project", "global"},
				"description": "Memory scope. Defaults to session when omitted.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The concise fact or preference to remember.",
			},
		},
	}
}

func (t *rememberTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	var in struct {
		Scope   string `json:"scope"`
		Content string `json:"content"`
	}
	if err := decodeArgs(raw, &in); err != nil {
		return Result{}, err
	}
	scope, err := normalizeMemoryScope(in.Scope)
	if err != nil {
		return Result{}, err
	}
	content := strings.TrimSpace(in.Content)
	if content == "" {
		return Result{}, newInvalidArgs("content is required", nil)
	}
	if memory.LooksLikeSecret(content) {
		return Result{IsError: true, Content: "memory not saved: content appears to contain a secret", Metadata: map[string]any{"error": "secret_detected", "scope": string(scope)}}, nil
	}

	var m *session.Memory
	switch scope {
	case session.MemoryScopeSession:
		if t.sessionStore == nil {
			return Result{}, newExecutionFailed("remember: session memory store not configured", nil)
		}
		if ec.SessionID == "" {
			return Result{}, newInvalidArgs("session_id is required in execution context", nil)
		}
		m, err = t.sessionStore.RememberSessionMemory(ctx, ec.SessionID, session.NewMemory{Content: content})
	case session.MemoryScopeProject, session.MemoryScopeGlobal:
		if t.fileStore == nil {
			return Result{}, newExecutionFailed("remember: file memory store not configured", nil)
		}
		denied, perr := t.askFileMemoryPermission(ctx, scope, ec)
		if perr != nil {
			return Result{}, perr
		}
		if denied != nil {
			return *denied, nil
		}
		m, err = t.fileStore.Remember(ctx, scope, content)
	}
	if err != nil {
		if errors.Is(err, memory.ErrSecret) {
			return Result{IsError: true, Content: "memory not saved: content appears to contain a secret", Metadata: map[string]any{"error": "secret_detected", "scope": string(scope)}}, nil
		}
		return Result{}, newExecutionFailed("remember: persist memory", err)
	}
	return memoryResult("remembered", m), nil
}

func (t *rememberTool) askFileMemoryPermission(ctx context.Context, scope session.MemoryScope, ec ExecContext) (*Result, error) {
	target, err := t.fileStore.MemoryDir(scope)
	if err != nil {
		return nil, newExecutionFailed("remember: resolve memory directory", err)
	}
	_, denied, perr := askPermission(ctx, ec, permission.Request{Category: permission.CategoryFileWrite, Target: target, DiffPath: target, ToolName: t.Name(), Reason: "store a persistent memory"})
	if perr != nil {
		return nil, perr
	}
	if denied != nil {
		return denied, nil
	}
	return nil, nil
}

func (t *rememberTool) Parallelizable() bool { return false }

type forgetTool struct {
	sessionStore sessionMemoryStore
	fileStore    fileMemoryStore
}

func newForgetTool(sessionStore sessionMemoryStore, fileStore fileMemoryStore) *forgetTool {
	return &forgetTool{sessionStore: sessionStore, fileStore: fileStore}
}

func (t *forgetTool) Name() string { return "forget" }

func (t *forgetTool) Description() string {
	return "Delete a previously stored memory by id. Use only when the user asks to forget or correct a remembered fact."
}

func (t *forgetTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"memory_id"},
		"properties": map[string]any{
			"scope": map[string]any{
				"type":        "string",
				"enum":        []any{"session", "project", "global"},
				"description": "Memory scope. Defaults to session when omitted.",
			},
			"memory_id": map[string]any{
				"type":        "string",
				"description": "The id attribute from the memory to forget.",
			},
		},
	}
}

func (t *forgetTool) Execute(ctx context.Context, raw json.RawMessage, ec ExecContext) (Result, error) {
	var in struct {
		Scope    string `json:"scope"`
		MemoryID string `json:"memory_id"`
	}
	if err := decodeArgs(raw, &in); err != nil {
		return Result{}, err
	}
	scope, err := normalizeMemoryScope(in.Scope)
	if err != nil {
		return Result{}, err
	}
	memoryID := strings.TrimSpace(in.MemoryID)
	if memoryID == "" {
		return Result{}, newInvalidArgs("memory_id is required", nil)
	}

	var m *session.Memory
	switch scope {
	case session.MemoryScopeSession:
		if t.sessionStore == nil {
			return Result{}, newExecutionFailed("forget: session memory store not configured", nil)
		}
		if ec.SessionID == "" {
			return Result{}, newInvalidArgs("session_id is required in execution context", nil)
		}
		m, err = t.sessionStore.ForgetSessionMemory(ctx, ec.SessionID, memoryID)
	case session.MemoryScopeProject, session.MemoryScopeGlobal:
		if t.fileStore == nil {
			return Result{}, newExecutionFailed("forget: file memory store not configured", nil)
		}
		denied, perr := t.askFileMemoryPermission(ctx, scope, ec)
		if perr != nil {
			return Result{}, perr
		}
		if denied != nil {
			return *denied, nil
		}
		m, err = t.fileStore.Forget(ctx, scope, memoryID)
	}
	if err != nil {
		if errors.Is(err, session.ErrMemoryNotFound) {
			return Result{IsError: true, Content: fmt.Sprintf("memory not found: %s", memoryID), Metadata: map[string]any{"error": "not_found", "scope": string(scope), "memory_id": memoryID}}, nil
		}
		return Result{}, newExecutionFailed("forget: delete memory", err)
	}
	return memoryResult("forgot", m), nil
}

func (t *forgetTool) askFileMemoryPermission(ctx context.Context, scope session.MemoryScope, ec ExecContext) (*Result, error) {
	target, err := t.fileStore.MemoryDir(scope)
	if err != nil {
		return nil, newExecutionFailed("forget: resolve memory directory", err)
	}
	_, denied, perr := askPermission(ctx, ec, permission.Request{Category: permission.CategoryFileWrite, Target: target, DiffPath: target, ToolName: t.Name(), Reason: "delete a persistent memory"})
	if perr != nil {
		return nil, perr
	}
	if denied != nil {
		return denied, nil
	}
	return nil, nil
}

func (t *forgetTool) Parallelizable() bool { return false }

func normalizeMemoryScope(raw string) (session.MemoryScope, error) {
	scope := session.MemoryScope(strings.TrimSpace(raw))
	if scope == "" {
		return session.MemoryScopeSession, nil
	}
	switch scope {
	case session.MemoryScopeSession, session.MemoryScopeProject, session.MemoryScopeGlobal:
		return scope, nil
	default:
		return "", newInvalidArgs("scope must be session, project, or global", nil)
	}
}

func memoryResult(action string, m *session.Memory) Result {
	content := fmt.Sprintf("%s %s memory %s", action, m.Scope, m.ID)
	if strings.TrimSpace(m.Title) != "" {
		content = fmt.Sprintf("%s: %s", content, m.Title)
	}
	metadata := map[string]any{"scope": string(m.Scope), "memory_id": m.ID}
	if m.Path != "" {
		metadata["path"] = m.Path
	}
	if m.Title != "" {
		metadata["title"] = m.Title
	}
	return Result{Content: content, Metadata: metadata}
}
