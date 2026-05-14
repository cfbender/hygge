package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/theme"
)

// newTestAppWithStore builds an App wired to a file-based store in a temp
// directory, so parallel tests don't share databases when run in parallel.
// A fresh session is created and seeded with seedMessages; the App's
// SessionID is set to that session's id (the --resume path).
func newTestAppWithStore(
	t *testing.T,
	seedMessages []session.NewMessage,
) (*App, *store.Store, *bus.Bus) {
	t.Helper()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sess, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      session.ModelRef{Provider: "anthropic", Name: "test-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	for _, nm := range seedMessages {
		if _, err := st.AppendMessage(ctx, sess.ID, nm); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		ProfileName:   "default",
		SessionID:     sess.ID,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	return app, st, b
}

// ---------------------------------------------------------------------------
// Test: --resume path (SessionID set in AppOptions)
// ---------------------------------------------------------------------------

// TestHydrate_ResumePopulatesMessages verifies that an App constructed with a
// pre-existing SessionID (the --resume path) populates a.messages from the
// store when Init() is called.
func TestHydrate_ResumePopulatesMessages(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "hello from the past"}},
		},
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "hi there, resumed!"}},
		},
	})

	// Init() should trigger hydration.
	_ = app.Init()

	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages after Init(), got %d", got)
	}
	if app.messages[0].Role != components.RoleUser {
		t.Errorf("messages[0].Role = %q, want user", app.messages[0].Role)
	}
	if app.messages[0].Raw != "hello from the past" {
		t.Errorf("messages[0].Raw = %q, want 'hello from the past'", app.messages[0].Raw)
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("messages[1].Role = %q, want assistant", app.messages[1].Role)
	}
	if app.messages[1].Raw != "hi there, resumed!" {
		t.Errorf("messages[1].Raw = %q, want 'hi there, resumed!'", app.messages[1].Raw)
	}
}

// TestHydrate_ResumeEmptySession verifies that an App resumed with a session
// that has no messages is handled gracefully (empty message list, not error).
func TestHydrate_ResumeEmptySession(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, nil)
	_ = app.Init()
	if got := len(app.messages); got != 0 {
		t.Errorf("expected 0 messages for empty session, got %d", got)
	}
}

// TestHydrate_ResumeIsIdempotent verifies that calling Init (and thus
// hydrateMessagesFromStore) more than once for the same session does not
// produce duplicate messages.
func TestHydrate_ResumeIsIdempotent(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "idempotent"}},
		},
	})

	_ = app.Init()
	before := len(app.messages)

	// Manually call hydrate again to simulate double-init.
	app.hydrateMessagesFromStore(app.opts.SessionID)
	after := len(app.messages)

	if before != 1 || after != 1 {
		t.Errorf("expected 1 message each time; before=%d after=%d", before, after)
	}
}

// TestHydrate_NoStoreNoPanic verifies that an App without a store (nil Store)
// does not panic or error during Init, and starts with empty messages.
func TestHydrate_NoStoreNoPanic(t *testing.T) {
	t.Parallel()
	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		SessionID:     "some-id",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	_ = app.Init() // should not panic

	if got := len(app.messages); got != 0 {
		t.Errorf("expected 0 messages (no store), got %d", got)
	}
}

// ---------------------------------------------------------------------------
// Test: applySwitchSession path (sessions modal)
// ---------------------------------------------------------------------------

// TestHydrate_SwitchSessionPopulatesMessages verifies that applySwitchSession
// hydrates a.messages from the store after resetting state.
func TestHydrate_SwitchSessionPopulatesMessages(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Create the target session and seed two messages.
	sess, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      session.ModelRef{Provider: "anthropic", Name: "test-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	for _, nm := range []session.NewMessage{
		{Role: session.RoleUser, Parts: []session.Part{{Kind: session.PartText, Text: "switch input"}}},
		{Role: session.RoleAssistant, Parts: []session.Part{{Kind: session.PartText, Text: "switch response"}}},
	} {
		if _, err := st.AppendMessage(ctx, sess.ID, nm); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	// Start the App with a different session id; we will switch to sess.
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         theme.ShellTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	// Inject stale messages to confirm they get replaced.
	app.messages = []uiMessage{
		{Role: components.RoleUser, Raw: "stale"},
	}

	// Execute the switch.
	_ = app.applySwitchSession(sess.ID)

	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages after switch, got %d (messages: %+v)", got, app.messages)
	}
	if app.messages[0].Raw != "switch input" {
		t.Errorf("messages[0].Raw = %q, want 'switch input'", app.messages[0].Raw)
	}
	if app.messages[1].Raw != "switch response" {
		t.Errorf("messages[1].Raw = %q, want 'switch response'", app.messages[1].Raw)
	}
	if app.opts.SessionID != sess.ID {
		t.Errorf("opts.SessionID = %q, want %q", app.opts.SessionID, sess.ID)
	}
}

// TestHydrate_SwitchSessionClearsStaleState verifies that switching sessions
// replaces any pre-existing messages with the new session's content.
func TestHydrate_SwitchSessionClearsStaleState(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, nil)
	_ = app.Init()

	// Seed an entirely new session to switch to.
	ctx := context.Background()
	sess2, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      session.ModelRef{Provider: "anthropic", Name: "test-model"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.AppendMessage(ctx, sess2.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "new session message"}},
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	// Pollute with stale messages.
	app.messages = []uiMessage{
		{Role: components.RoleUser, Raw: "old stale content"},
	}

	_ = app.applySwitchSession(sess2.ID)

	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message after switch, got %d", got)
	}
	if app.messages[0].Raw != "new session message" {
		t.Errorf("messages[0].Raw = %q, want 'new session message'", app.messages[0].Raw)
	}
}

// ---------------------------------------------------------------------------
// Test: uiEntryFromStoreMessage converter unit tests
// ---------------------------------------------------------------------------

func TestUiEntryFromStoreMessage_User(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "hello"}},
	}
	entry, ok := uiEntryFromStoreMessage(m)
	if !ok {
		t.Fatal("expected ok=true for user message")
	}
	if entry.Role != components.RoleUser {
		t.Errorf("role = %q, want user", entry.Role)
	}
	if entry.Raw != "hello" {
		t.Errorf("raw = %q, want hello", entry.Raw)
	}
}

func TestUiEntryFromStoreMessage_Assistant(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role: session.RoleAssistant,
		Parts: []session.Part{
			{Kind: session.PartText, Text: "part1 "},
			{Kind: session.PartText, Text: "part2"},
		},
	}
	entry, ok := uiEntryFromStoreMessage(m)
	if !ok {
		t.Fatal("expected ok=true for assistant message")
	}
	if entry.Role != components.RoleAssistant {
		t.Errorf("role = %q, want assistant", entry.Role)
	}
	if entry.Raw != "part1 part2" {
		t.Errorf("raw = %q, want 'part1 part2'", entry.Raw)
	}
	if entry.IsStreaming {
		t.Errorf("expected IsStreaming=false for persisted message")
	}
}

func TestUiEntryFromStoreMessage_Tool(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role: session.RoleTool,
		Parts: []session.Part{
			{
				Kind:      session.PartToolUse,
				ToolID:    "tid1",
				ToolName:  "read",
				ToolInput: []byte(`{"path":"/etc/hosts"}`),
			},
			{
				Kind:      session.PartToolResult,
				ToolUseID: "tid1",
				Content:   "127.0.0.1 localhost",
			},
		},
	}
	entry, ok := uiEntryFromStoreMessage(m)
	if !ok {
		t.Fatal("expected ok=true for tool message")
	}
	if entry.Role != components.RoleTool {
		t.Errorf("role = %q, want tool", entry.Role)
	}
	if entry.ToolName != "read" {
		t.Errorf("toolname = %q, want read", entry.ToolName)
	}
	if entry.Target != "/etc/hosts" {
		t.Errorf("target = %q, want /etc/hosts", entry.Target)
	}
	if entry.Raw != "127.0.0.1 localhost" {
		t.Errorf("raw = %q, want '127.0.0.1 localhost'", entry.Raw)
	}
	if entry.IsError {
		t.Errorf("expected IsError=false")
	}
}

func TestUiEntryFromStoreMessage_ToolError(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role: session.RoleTool,
		Parts: []session.Part{
			{
				Kind:      session.PartToolUse,
				ToolID:    "tid2",
				ToolName:  "write",
				ToolInput: []byte(`{"path":"/ro/file"}`),
			},
			{
				Kind:      session.PartToolResult,
				ToolUseID: "tid2",
				Content:   "permission denied",
				IsError:   true,
			},
		},
	}
	entry, ok := uiEntryFromStoreMessage(m)
	if !ok {
		t.Fatal("expected ok=true for error tool message")
	}
	if !entry.IsError {
		t.Errorf("expected IsError=true for error result")
	}
	if entry.Raw != "permission denied" {
		t.Errorf("raw = %q, want 'permission denied'", entry.Raw)
	}
}

func TestUiEntryFromStoreMessage_EmptyTextSkipped(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: ""}},
	}
	_, ok := uiEntryFromStoreMessage(m)
	if ok {
		t.Errorf("expected ok=false for empty text message")
	}
}

func TestUiEntryFromStoreMessage_Nil(t *testing.T) {
	t.Parallel()
	_, ok := uiEntryFromStoreMessage(nil)
	if ok {
		t.Errorf("expected ok=false for nil message")
	}
}

func TestUiEntryFromStoreMessage_ThinkingPartOnlySkipped(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartThinking, Text: "thinking..."}},
	}
	// Assistant with only thinking parts: allTextParts returns "", so skipped.
	_, ok := uiEntryFromStoreMessage(m)
	if ok {
		t.Errorf("expected ok=false for assistant with only thinking parts")
	}
}
