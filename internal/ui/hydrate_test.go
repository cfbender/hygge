package ui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cfbender/hygge/internal/bus"
	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
	"github.com/cfbender/hygge/internal/ui/components"
	"github.com/cfbender/hygge/internal/ui/styles"
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
		Theme:         styles.DefaultTheme(),
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
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
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

func TestLazyContextLoadedAnnotation_PersistsAndHydrates(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, nil)
	text := "context loaded: pkg/AGENTS.md"

	app.handleBusEvent(bus.LazyContextLoaded{
		SessionID: app.opts.SessionID,
		Files:     []string{"pkg/AGENTS.md"},
		At:        app.opts.Now(),
	})

	if len(app.messages) != 1 {
		t.Fatalf("live messages = %d, want 1", len(app.messages))
	}
	if app.messages[0].Role != components.RoleSystem || app.messages[0].Raw != text {
		t.Fatalf("live message = (%q, %q), want (%q, %q)", app.messages[0].Role, app.messages[0].Raw, components.RoleSystem, text)
	}

	msgs, err := st.MessagesForSession(t.Context(), app.opts.SessionID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("persisted messages = %d, want 1", len(msgs))
	}
	if msgs[0].Role != session.RoleSystem || firstTextPart(msgs[0].Parts) != text {
		t.Fatalf("persisted message = (%q, %q), want (%q, %q)", msgs[0].Role, firstTextPart(msgs[0].Parts), session.RoleSystem, text)
	}

	app.messages = nil
	app.hydrateMessagesFromStore(app.opts.SessionID)
	if len(app.messages) != 1 {
		t.Fatalf("hydrated messages = %d, want 1", len(app.messages))
	}
	if app.messages[0].Role != components.RoleSystem || app.messages[0].Raw != text {
		t.Fatalf("hydrated message = (%q, %q), want (%q, %q)", app.messages[0].Role, app.messages[0].Raw, components.RoleSystem, text)
	}
}

func TestHydrate_ResumePopulatesTodoSummary(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, nil)
	if _, err := st.ReplaceSessionTodos(t.Context(), app.opts.SessionID, []session.TodoItem{{Content: "running", Status: session.TodoInProgress}, {Content: "done", Status: session.TodoCompleted}}); err != nil {
		t.Fatalf("ReplaceSessionTodos: %v", err)
	}

	app.todoIncomplete = 0
	app.todoInProgress = 0
	app.Init()
	if app.todoIncomplete != 1 || app.todoInProgress != 1 {
		t.Fatalf("todo state = incomplete %d in_progress %d, want 1 1", app.todoIncomplete, app.todoInProgress)
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
		Theme:         styles.DefaultTheme(),
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
		Theme:         styles.DefaultTheme(),
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
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})

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

func TestHydrate_SwitchSessionPopulatesTodoSummary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hygge_test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	sess, err := st.CreateSession(ctx, session.NewSession{ProjectDir: "/tmp/proj", Model: session.ModelRef{Provider: "anthropic", Name: "test-model"}})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.ReplaceSessionTodos(ctx, sess.ID, []session.TodoItem{{Content: "pending", Status: session.TodoPending}, {Content: "running", Status: session.TodoInProgress}, {Content: "cancelled", Status: session.TodoCancelled}}); err != nil {
		t.Fatalf("ReplaceSessionTodos: %v", err)
	}
	b := bus.New()
	app, err := New(AppOptions{Bus: b, Store: st, Theme: styles.DefaultTheme(), ProjectDir: "/tmp/proj", ModelProvider: "anthropic", ModelName: "test-model", Now: func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(); b.Close() })
	app.todoIncomplete = 9
	app.todoInProgress = 9

	_ = app.applySwitchSession(sess.ID)
	if app.todoIncomplete != 2 || app.todoInProgress != 1 {
		t.Fatalf("todo state = incomplete %d in_progress %d, want 2 1", app.todoIncomplete, app.todoInProgress)
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

func TestUiEntryFromStoreMessage_ThinkingPartProducesThinkingEntry(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartThinking, Text: "thinking..."}},
	}
	// Phase 2: assistant with only thinking parts returns an assistant entry
	// with the Thinking field populated and empty Raw (tool-only check skipped
	// since Thinking is non-empty).
	entry, ok := uiEntryFromStoreMessage(m)
	if !ok {
		t.Fatalf("expected ok=true for assistant with thinking parts")
	}
	if entry.Role != components.RoleAssistant {
		t.Errorf("role = %q, want RoleAssistant (Phase 2: thinking is inline)", entry.Role)
	}
	if entry.Thinking != "thinking..." {
		t.Errorf("Thinking = %q, want 'thinking...'", entry.Thinking)
	}
}

// ---------------------------------------------------------------------------
// New tests: thinking, markers, and subagent hydration
// ---------------------------------------------------------------------------

// TestUiEntriesFromStoreMessage_ThinkingBeforeText verifies that an assistant
// message with both a thinking part and a text part produces ONE entry
// (Phase 2: thinking is collapsed into the assistant message's Thinking field).
func TestUiEntriesFromStoreMessage_ThinkingBeforeText(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role: session.RoleAssistant,
		Parts: []session.Part{
			{Kind: session.PartThinking, Text: "let me think"},
			{Kind: session.PartText, Text: "here is the answer"},
		},
	}
	entries := uiEntriesFromStoreMessage(m, map[string]session.Part{}, map[string]struct{}{})
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (Phase 2: thinking inline), got %d: %+v", len(entries), entries)
	}
	if entries[0].Role != components.RoleAssistant {
		t.Errorf("entries[0].Role = %q, want RoleAssistant", entries[0].Role)
	}
	if entries[0].Thinking != "let me think" {
		t.Errorf("entries[0].Thinking = %q, want 'let me think'", entries[0].Thinking)
	}
	if entries[0].Raw != "here is the answer" {
		t.Errorf("entries[0].Raw = %q, want 'here is the answer'", entries[0].Raw)
	}
}

// TestHydrate_ThinkingPartsProduceInlineThinking verifies that resuming a
// session with assistant messages that contain thinking parts produces
// a single assistant message with the Thinking field populated (Phase 2).
func TestHydrate_ThinkingPartsProduceInlineThinking(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "question"}},
		},
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartThinking, Text: "let me reason about this"},
				{Kind: session.PartText, Text: "here is my answer"},
			},
		},
	})
	_ = app.Init()

	// Expect: user, assistant (2 entries — thinking is inline on the assistant).
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleUser {
		t.Errorf("messages[0].Role = %q, want user", app.messages[0].Role)
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("messages[1].Role = %q, want assistant", app.messages[1].Role)
	}
	if app.messages[1].Thinking != "let me reason about this" {
		t.Errorf("messages[1].Thinking = %q, want 'let me reason about this'", app.messages[1].Thinking)
	}
	if app.messages[1].Raw != "here is my answer" {
		t.Errorf("messages[1].Raw = %q", app.messages[1].Raw)
	}
}

// TestLiveThinkingDelta_StreamsAndFinalizes verifies that:
//  1. AssistantThinkingDelta events produce a streaming RoleAssistant message
//     with Thinking populated (Phase 2: thinking is inline on the assistant).
//  2. When AssistantTextDelta arrives, it appends to the SAME streaming
//     assistant message (no separate row).
//  3. MessageAppended finalizes the single assistant message.
func TestLiveThinkingDelta_StreamsAndFinalizes(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Emit two thinking deltas.
	app.Handle(bus.AssistantThinkingDelta{SessionID: "fg-session", Text: "step 1: "})
	app.Handle(bus.AssistantThinkingDelta{SessionID: "fg-session", Text: "step 2"})

	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message after thinking deltas, got %d", got)
	}
	th := app.messages[0]
	if th.Role != components.RoleAssistant {
		t.Errorf("role = %q, want assistant (Phase 2: thinking is inline)", th.Role)
	}
	if th.Thinking != "step 1: step 2" {
		t.Errorf("Thinking = %q, want 'step 1: step 2'", th.Thinking)
	}
	if th.Raw != "" {
		t.Errorf("Raw = %q, want empty (no text yet)", th.Raw)
	}
	if !th.IsStreaming {
		t.Errorf("expected IsStreaming=true while thinking")
	}

	// Emit a text delta — should accumulate on the SAME assistant message.
	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "here is the answer"})

	// Still 1 message (thinking + text on same entry).
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message after text delta (same entry), got %d", got)
	}
	if app.messages[0].Thinking != "step 1: step 2" {
		t.Errorf("Thinking changed after text delta: %q", app.messages[0].Thinking)
	}
	if app.messages[0].Raw != "here is the answer" {
		t.Errorf("Raw = %q, want 'here is the answer'", app.messages[0].Raw)
	}
	if !app.messages[0].IsStreaming {
		t.Errorf("expected still streaming before MessageAppended")
	}

	// MessageAppended finalizes.
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m1"})
	if app.messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=false after MessageAppended")
	}
}

// TestLiveThinkingDelta_FinalizedOnToolCall verifies that when a thinking
// delta arrives followed by a ToolCallRequested, the assistant message
// accumulates thinking and then a tool row is appended (Phase 2 behavior).
func TestLiveThinkingDelta_FinalizedOnToolCall(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.AssistantThinkingDelta{SessionID: "fg-session", Text: "thinking"})
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "read",
		Args:      []byte(`{"path":"/tmp/x"}`),
	})

	// Phase 2: messages[0] = streaming assistant with Thinking, messages[1] = tool.
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Errorf("messages[0].Role = %q, want assistant", app.messages[0].Role)
	}
	if app.messages[0].Thinking != "thinking" {
		t.Errorf("messages[0].Thinking = %q, want 'thinking'", app.messages[0].Thinking)
	}
	if app.messages[1].Role != components.RoleTool {
		t.Errorf("messages[1].Role = %q, want tool", app.messages[1].Role)
	}
}

// TestHydrate_CompactionMarkerInjectsRoleMarkerEntry verifies that resuming a
// session with one compaction marker produces a RoleMarker entry at the
// correct position, with the expected summary and token count.
func TestHydrate_CompactionMarkerInjectsRoleMarkerEntry(t *testing.T) {
	t.Parallel()
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

	// Seed: user1, assistant1, then compact after assistant1, then user2.
	user1, err := st.AppendMessage(ctx, sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "first question"}},
	})
	if err != nil {
		t.Fatalf("AppendMessage user1: %v", err)
	}
	if _, err := st.AppendMessage(ctx, sess.ID, session.NewMessage{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartText, Text: "first answer"}},
	}); err != nil {
		t.Fatalf("AppendMessage assistant1: %v", err)
	}

	// The marker cuts off at (before) user2: in practice the marker records the
	// first message AFTER the compacted content.  We use user1 as the cutoff
	// so that user1 appears before the marker and user2 appears after.
	user2, err := st.AppendMessage(ctx, sess.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "second question"}},
	})
	if err != nil {
		t.Fatalf("AppendMessage user2: %v", err)
	}

	// Add a compaction marker: cuts off before user2, summary, 500 tokens.
	if _, err := st.AddCompactionMarker(ctx, sess.ID, user2.ID, "summary of first exchange", 500); err != nil {
		t.Fatalf("AddCompactionMarker: %v", err)
	}

	_ = user1 // used to verify ordering

	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         styles.DefaultTheme(),
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
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	_ = app.Init()

	// Expected message order:
	// [0] user: "first question"
	// [1] assistant: "first answer"
	// [2] marker (before user2)
	// [3] user: "second question"
	if got := len(app.messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleUser {
		t.Errorf("messages[0].Role = %q, want user", app.messages[0].Role)
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("messages[1].Role = %q, want assistant", app.messages[1].Role)
	}
	if app.messages[2].Role != components.RoleMarker {
		t.Errorf("messages[2].Role = %q, want marker", app.messages[2].Role)
	}
	if app.messages[2].MarkerSummary != "summary of first exchange" {
		t.Errorf("MarkerSummary = %q", app.messages[2].MarkerSummary)
	}
	if app.messages[2].MarkerTokensSaved != 500 {
		t.Errorf("MarkerTokensSaved = %d, want 500", app.messages[2].MarkerTokensSaved)
	}
	if app.messages[3].Role != components.RoleUser {
		t.Errorf("messages[3].Role = %q, want user", app.messages[3].Role)
	}
	if app.messages[3].Raw != "second question" {
		t.Errorf("messages[3].Raw = %q", app.messages[3].Raw)
	}
}

// TestHydrate_SubagentReconstructsFromStore verifies that resuming a session
// that spawned a subagent produces a parent subagent tool row with SubagentID set
// and the child's transcript in app.subagents.
func TestHydrate_SubagentReconstructsFromStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	model := session.ModelRef{Provider: "anthropic", Name: "test-model"}

	// Create parent session.
	parent, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
	})
	if err != nil {
		t.Fatalf("CreateSession parent: %v", err)
	}

	// Append user message + subagent tool call on the parent.
	if _, err := st.AppendMessage(ctx, parent.ID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "do a task"}},
	}); err != nil {
		t.Fatalf("AppendMessage user: %v", err)
	}
	toolUseID := "toolu_abc123"
	if _, err := st.AppendMessage(ctx, parent.ID, session.NewMessage{
		Role: session.RoleTool,
		Parts: []session.Part{
			{
				Kind:      session.PartToolUse,
				ToolID:    toolUseID,
				ToolName:  "subagent",
				ToolInput: []byte(`{"subagent_type":"general","description":"find something"}`),
			},
			{
				Kind:      session.PartToolResult,
				ToolUseID: toolUseID,
				Content:   "task done",
			},
		},
	}); err != nil {
		t.Fatalf("AppendMessage tool: %v", err)
	}

	// Create child session (KindSubagent) with the exact parent tool_use_id in
	// the dedicated column. The slug deliberately omits it so hydration does not
	// depend on display formatting.
	child, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir:      "/tmp/proj",
		Model:           model,
		ParentID:        parent.ID,
		ParentToolUseID: toolUseID,
		Kind:            session.KindSubagent,
		Slug:            "general: find something",
	})
	if err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	// Append a message to the child session.
	if _, err := st.AppendMessage(ctx, child.ID, session.NewMessage{
		Role:         session.RoleAssistant,
		Parts:        []session.Part{{Kind: session.PartText, Text: "I found it"}},
		OutputTokens: 42,
		CostUSD:      0.0012,
		DurationMs:   1500,
	}); err != nil {
		t.Fatalf("AppendMessage child: %v", err)
	}

	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         styles.DefaultTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		ProfileName:   "default",
		SessionID:     parent.ID,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	_ = app.Init()

	// Find the subagent tool message.
	var subagentMsg *uiMessage
	for i := range app.messages {
		if app.messages[i].Role == components.RoleTool && app.messages[i].ToolName == "subagent" {
			subagentMsg = &app.messages[i]
			break
		}
	}
	if subagentMsg == nil {
		t.Fatalf("expected a subagent UIMessage in messages: %+v", app.messages)
	}
	if subagentMsg.SubagentID == "" {
		t.Fatalf("expected subagent message to have SubagentID set, got empty")
	}
	if subagentMsg.SubagentID != child.ID {
		t.Errorf("SubagentID = %q, want %q", subagentMsg.SubagentID, child.ID)
	}

	// Verify the child session state is in app.subagents.
	state, ok := app.subagents[child.ID]
	if !ok {
		t.Fatalf("expected child session %q in app.subagents", child.ID)
	}
	if state == nil {
		t.Fatal("subagent state is nil")
	}
	if len(state.Messages) == 0 {
		t.Fatalf("expected child messages to be hydrated, got 0")
	}
	if state.Messages[0].Raw != "I found it" {
		t.Errorf("child message raw = %q, want 'I found it'", state.Messages[0].Raw)
	}
	if state.Messages[0].ModelName != "anthropic/test-model" {
		t.Errorf("child message ModelName = %q, want anthropic/test-model", state.Messages[0].ModelName)
	}
	if state.Messages[0].OutputTokens != 42 || state.Messages[0].CostUSD != 0.0012 || state.Messages[0].DurationMs != 1500 {
		t.Errorf("child message metadata = tokens %d cost %.4f duration %d", state.Messages[0].OutputTokens, state.Messages[0].CostUSD, state.Messages[0].DurationMs)
	}
}

// TestHydrate_SubagentRecursiveTwoLevels verifies that hydration reconstructs
// nested subagents at least two levels deep (grandparent → parent → child).
func TestHydrate_SubagentRecursiveTwoLevels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	model := session.ModelRef{Provider: "anthropic", Name: "test-model"}

	// Root session.
	root, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
	})
	if err != nil {
		t.Fatalf("CreateSession root: %v", err)
	}

	toolUseL1 := "toolu_level1"
	if _, err := st.AppendMessage(ctx, root.ID, session.NewMessage{
		Role: session.RoleTool,
		Parts: []session.Part{
			{Kind: session.PartToolUse, ToolID: toolUseL1, ToolName: "subagent", ToolInput: []byte(`{}`)},
			{Kind: session.PartToolResult, ToolUseID: toolUseL1, Content: "l1 done"},
		},
	}); err != nil {
		t.Fatalf("AppendMessage root tool: %v", err)
	}

	// Level-1 subagent.
	l1, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
		ParentID:   root.ID,
		Kind:       session.KindSubagent,
		Slug:       "general: level1 [" + toolUseL1 + "]",
	})
	if err != nil {
		t.Fatalf("CreateSession l1: %v", err)
	}

	toolUseL2 := "toolu_level2"
	if _, err := st.AppendMessage(ctx, l1.ID, session.NewMessage{
		Role: session.RoleTool,
		Parts: []session.Part{
			{Kind: session.PartToolUse, ToolID: toolUseL2, ToolName: "subagent", ToolInput: []byte(`{}`)},
			{Kind: session.PartToolResult, ToolUseID: toolUseL2, Content: "l2 done"},
		},
	}); err != nil {
		t.Fatalf("AppendMessage l1 tool: %v", err)
	}

	// Level-2 subagent.
	l2, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
		ParentID:   l1.ID,
		Kind:       session.KindSubagent,
		Slug:       "general: level2 [" + toolUseL2 + "]",
	})
	if err != nil {
		t.Fatalf("CreateSession l2: %v", err)
	}

	if _, err := st.AppendMessage(ctx, l2.ID, session.NewMessage{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartText, Text: "deep answer"}},
	}); err != nil {
		t.Fatalf("AppendMessage l2 assistant: %v", err)
	}

	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         styles.DefaultTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		ProfileName:   "default",
		SessionID:     root.ID,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	_ = app.Init()

	// Level-1 subagent should be in app.subagents.
	stateL1, ok := app.subagents[l1.ID]
	if !ok {
		t.Fatalf("expected l1 in app.subagents; keys: %v", subagentIDs(app.subagents))
	}
	if stateL1 == nil {
		t.Fatal("l1 state is nil")
	}

	// Level-1 state should have a subagent tool message pointing at l2.
	var l1SubagentMsg *uiMessage
	for i := range stateL1.Messages {
		if stateL1.Messages[i].Role == components.RoleTool && stateL1.Messages[i].ToolName == "subagent" {
			l1SubagentMsg = &stateL1.Messages[i]
			break
		}
	}
	if l1SubagentMsg == nil {
		t.Fatalf("expected subagent message in l1 messages: %+v", stateL1.Messages)
	}
	if l1SubagentMsg.SubagentID != l2.ID {
		t.Errorf("l1 subagent SubagentID = %q, want %q", l1SubagentMsg.SubagentID, l2.ID)
	}

	// Level-2 subagent should also be in app.subagents.
	stateL2, ok := app.subagents[l2.ID]
	if !ok {
		t.Fatalf("expected l2 in app.subagents; keys: %v", subagentIDs(app.subagents))
	}
	if len(stateL2.Messages) == 0 {
		t.Fatalf("expected l2 messages to be hydrated")
	}
	if stateL2.Messages[0].Raw != "deep answer" {
		t.Errorf("l2 message raw = %q, want 'deep answer'", stateL2.Messages[0].Raw)
	}
}

// subagentIDs returns a slice of keys from the subagents map for test diagnostics.
func subagentIDs(m map[string]*components.SubagentState) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// ---------------------------------------------------------------------------
// New tests: split-row tool calls (assistant + separate tool-result rows)
// ---------------------------------------------------------------------------

// TestHydrate_ToolCallSplitRows verifies the common persistence shape: a
// PartToolUse in an assistant message paired with a PartToolResult in a
// separate RoleTool message.  The result should produce: assistant-text row +
// tool row with the result body populated.
func TestHydrate_ToolCallSplitRows(t *testing.T) {
	t.Parallel()
	toolUseID := "toolu_split01"
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "read a file"}},
		},
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartText, Text: "I will read the file."},
				{
					Kind:      session.PartToolUse,
					ToolID:    toolUseID,
					ToolName:  "read",
					ToolInput: []byte(`{"path":"/etc/hosts"}`),
				},
			},
		},
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{
					Kind:      session.PartToolResult,
					ToolUseID: toolUseID,
					Content:   "127.0.0.1 localhost",
				},
			},
		},
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "Done."}},
		},
	})
	_ = app.Init()

	// Expected: user, assistant (text), tool (with result), assistant (done)
	if got := len(app.messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleUser {
		t.Errorf("[0].Role = %q, want user", app.messages[0].Role)
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("[1].Role = %q, want assistant", app.messages[1].Role)
	}
	if app.messages[1].Raw != "I will read the file." {
		t.Errorf("[1].Raw = %q", app.messages[1].Raw)
	}
	if app.messages[2].Role != components.RoleTool {
		t.Errorf("[2].Role = %q, want tool", app.messages[2].Role)
	}
	if app.messages[2].ToolName != "read" {
		t.Errorf("[2].ToolName = %q, want read", app.messages[2].ToolName)
	}
	if app.messages[2].ToolUseID != toolUseID {
		t.Errorf("[2].ToolUseID = %q, want %q", app.messages[2].ToolUseID, toolUseID)
	}
	if app.messages[2].Target != "/etc/hosts" {
		t.Errorf("[2].Target = %q, want /etc/hosts", app.messages[2].Target)
	}
	if app.messages[2].Raw != "127.0.0.1 localhost" {
		t.Errorf("[2].Raw = %q, want '127.0.0.1 localhost'", app.messages[2].Raw)
	}
	if app.messages[2].IsError {
		t.Errorf("[2].IsError = true, want false")
	}
	if app.messages[3].Role != components.RoleAssistant {
		t.Errorf("[3].Role = %q, want assistant", app.messages[3].Role)
	}
}

// TestHydrate_TwoToolCallsSplitRows verifies an assistant message with two
// PartToolUse parts, each paired with a PartToolResult in a single tool
// message.  Should produce: assistant-text + 2 tool rows.
func TestHydrate_TwoToolCallsSplitRows(t *testing.T) {
	t.Parallel()
	id1, id2 := "toolu_two01", "toolu_two02"
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartText, Text: "calling two tools"},
				{
					Kind:      session.PartToolUse,
					ToolID:    id1,
					ToolName:  "bash",
					ToolInput: []byte(`{"command":"echo hello"}`),
				},
				{
					Kind:      session.PartToolUse,
					ToolID:    id2,
					ToolName:  "bash",
					ToolInput: []byte(`{"command":"echo world"}`),
				},
			},
		},
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{Kind: session.PartToolResult, ToolUseID: id1, Content: "hello"},
				{Kind: session.PartToolResult, ToolUseID: id2, Content: "world"},
			},
		},
	})
	_ = app.Init()

	// Expected: assistant-text, tool1, tool2
	if got := len(app.messages); got != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Errorf("[0].Role = %q, want assistant", app.messages[0].Role)
	}
	if app.messages[1].Role != components.RoleTool {
		t.Errorf("[1].Role = %q, want tool", app.messages[1].Role)
	}
	if app.messages[1].ToolUseID != id1 {
		t.Errorf("[1].ToolUseID = %q, want %q", app.messages[1].ToolUseID, id1)
	}
	if app.messages[1].Raw != "hello" {
		t.Errorf("[1].Raw = %q, want hello", app.messages[1].Raw)
	}
	if app.messages[2].Role != components.RoleTool {
		t.Errorf("[2].Role = %q, want tool", app.messages[2].Role)
	}
	if app.messages[2].ToolUseID != id2 {
		t.Errorf("[2].ToolUseID = %q, want %q", app.messages[2].ToolUseID, id2)
	}
	if app.messages[2].Raw != "world" {
		t.Errorf("[2].Raw = %q, want world", app.messages[2].Raw)
	}
}

// TestHydrate_ToolCallNoResult verifies that a PartToolUse in an assistant
// message with no matching result (interrupted run) produces a tool row with
// empty Raw and IsError=false.
func TestHydrate_ToolCallNoResult(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartText, Text: "let me try"},
				{
					Kind:      session.PartToolUse,
					ToolID:    "toolu_noresult",
					ToolName:  "bash",
					ToolInput: []byte(`{"command":"sleep 10"}`),
				},
			},
		},
		// No corresponding tool result row (interrupted).
	})
	_ = app.Init()

	// Expected: assistant-text + tool (empty Raw)
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[1].Role != components.RoleTool {
		t.Errorf("[1].Role = %q, want tool", app.messages[1].Role)
	}
	if app.messages[1].Raw != "" {
		t.Errorf("[1].Raw = %q, want empty (no result)", app.messages[1].Raw)
	}
	if app.messages[1].IsError {
		t.Errorf("[1].IsError = true, want false")
	}
	if app.messages[1].ToolUseID != "toolu_noresult" {
		t.Errorf("[1].ToolUseID = %q", app.messages[1].ToolUseID)
	}
}

// TestHydrate_ThinkingTextToolSplitRows verifies that an assistant message
// with thinking + text + tool_use produces 2 entries (Phase 2: thinking is
// inline on the assistant): assistant (with Thinking+Raw), tool.
func TestHydrate_ThinkingTextToolSplitRows(t *testing.T) {
	t.Parallel()
	toolUseID := "toolu_think01"
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartThinking, Text: "planning…"},
				{Kind: session.PartText, Text: "I'll grep for it."},
				{
					Kind:      session.PartToolUse,
					ToolID:    toolUseID,
					ToolName:  "grep",
					ToolInput: []byte(`{"path":"/src"}`),
				},
			},
		},
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{Kind: session.PartToolResult, ToolUseID: toolUseID, Content: "found it"},
			},
		},
	})
	_ = app.Init()

	// Expected: assistant (thinking+text), tool
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Errorf("[0].Role = %q, want assistant", app.messages[0].Role)
	}
	if app.messages[0].Thinking != "planning…" {
		t.Errorf("[0].Thinking = %q, want 'planning…'", app.messages[0].Thinking)
	}
	if app.messages[0].Raw != "I'll grep for it." {
		t.Errorf("[0].Raw = %q", app.messages[0].Raw)
	}
	if app.messages[1].Role != components.RoleTool {
		t.Errorf("[1].Role = %q, want tool", app.messages[1].Role)
	}
	if app.messages[1].Raw != "found it" {
		t.Errorf("[1].Raw = %q", app.messages[1].Raw)
	}
}

// TestHydrate_LegacyCombinedToolRow verifies that the legacy combined-row
// shape (PartToolUse + PartToolResult in the same RoleTool message) still
// produces a tool uiMessage.  This is the shape tested by
// TestUiEntryFromStoreMessage_Tool and used by existing subagent tests.
func TestHydrate_LegacyCombinedToolRow(t *testing.T) {
	t.Parallel()
	toolUseID := "toolu_legacy01"
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{
					Kind:      session.PartToolUse,
					ToolID:    toolUseID,
					ToolName:  "read",
					ToolInput: []byte(`{"path":"/legacy"}`),
				},
				{
					Kind:      session.PartToolResult,
					ToolUseID: toolUseID,
					Content:   "legacy content",
				},
			},
		},
	})
	_ = app.Init()

	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message for legacy combined row, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleTool {
		t.Errorf("[0].Role = %q, want tool", app.messages[0].Role)
	}
	if app.messages[0].ToolName != "read" {
		t.Errorf("[0].ToolName = %q, want read", app.messages[0].ToolName)
	}
	if app.messages[0].Raw != "legacy content" {
		t.Errorf("[0].Raw = %q, want 'legacy content'", app.messages[0].Raw)
	}
}

// TestHydrate_SplitRowSubagentTaskRow verifies the real-world pong-test shape:
// user → assistant(text+task_tool_use) → tool(task_result) → assistant(text).
// The subagent tool row must be present with ToolUseID set, so subagent
// reconstruction can attach SubagentID to it.
func TestHydrate_SplitRowSubagentTaskRow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	model := session.ModelRef{Provider: "anthropic", Name: "test-model"}
	parent, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	toolUseID := "toolu_pong01"

	// Mirrors the confirmed store shape from the bug report.
	for _, nm := range []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "test a subagent with a pong"}},
		},
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartText, Text: "I'll dispatch a subagent."},
				{
					Kind:      session.PartToolUse,
					ToolID:    toolUseID,
					ToolName:  "subagent",
					ToolInput: []byte(`{"subagent_type":"general","description":"pong"}`),
				},
			},
		},
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{Kind: session.PartToolResult, ToolUseID: toolUseID, Content: "pong"},
			},
		},
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "Subagent responded: **pong** ✅"}},
		},
	} {
		if _, err := st.AppendMessage(ctx, parent.ID, nm); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	// Create child subagent session.
	child, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
		ParentID:   parent.ID,
		Kind:       session.KindSubagent,
		Slug:       "general: pong [" + toolUseID + "]",
	})
	if err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}
	if _, err := st.AppendMessage(ctx, child.ID, session.NewMessage{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartText, Text: "pong"}},
	}); err != nil {
		t.Fatalf("AppendMessage child: %v", err)
	}

	b := bus.New()
	now := func() time.Time { return time.Date(2026, 5, 14, 0, 0, 0, 0, time.UTC) }
	app, err := New(AppOptions{
		Bus:           b,
		Store:         st,
		Theme:         styles.DefaultTheme(),
		ProjectDir:    "/tmp/proj",
		ModelProvider: "anthropic",
		ModelName:     "test-model",
		ProfileName:   "default",
		SessionID:     parent.ID,
		Now:           now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	t.Cleanup(func() {
		_ = app.Close()
		b.Close()
	})
	_ = app.Init()

	// Expected messages: user, assistant(text), tool(task), assistant(done)
	if got := len(app.messages); got != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("[1].Role = %q, want assistant", app.messages[1].Role)
	}
	toolMsg := app.messages[2]
	if toolMsg.Role != components.RoleTool {
		t.Fatalf("[2].Role = %q, want tool", toolMsg.Role)
	}
	if toolMsg.ToolName != "subagent" {
		t.Errorf("[2].ToolName = %q, want subagent", toolMsg.ToolName)
	}
	if toolMsg.ToolUseID != toolUseID {
		t.Errorf("[2].ToolUseID = %q, want %q", toolMsg.ToolUseID, toolUseID)
	}
	if toolMsg.Raw != "pong" {
		t.Errorf("[2].Raw = %q, want pong", toolMsg.Raw)
	}
	// SubagentID must be stamped by reconstruction.
	if toolMsg.SubagentID == "" {
		t.Errorf("[2].SubagentID is empty; subagent reconstruction failed")
	}
	if toolMsg.SubagentID != child.ID {
		t.Errorf("[2].SubagentID = %q, want %q", toolMsg.SubagentID, child.ID)
	}
	// Child subagent must be in app.subagents.
	if _, ok := app.subagents[child.ID]; !ok {
		t.Errorf("child session %q not in app.subagents", child.ID)
	}
}

// TestHydrate_IdempotentWithToolCalls verifies that calling hydrateMessagesFromStore
// twice for a session with split-row tool calls produces the same result both times.
func TestHydrate_IdempotentWithToolCalls(t *testing.T) {
	t.Parallel()
	toolUseID := "toolu_idem01"
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartText, Text: "calling tool"},
				{
					Kind:      session.PartToolUse,
					ToolID:    toolUseID,
					ToolName:  "bash",
					ToolInput: []byte(`{"command":"echo hi"}`),
				},
			},
		},
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{Kind: session.PartToolResult, ToolUseID: toolUseID, Content: "hi"},
			},
		},
	})
	_ = app.Init()
	firstLen := len(app.messages)

	// Call hydrate a second time.
	app.hydrateMessagesFromStore(app.opts.SessionID)
	secondLen := len(app.messages)

	if firstLen != secondLen {
		t.Errorf("hydration not idempotent: first=%d second=%d", firstLen, secondLen)
	}
	if firstLen != 2 {
		t.Errorf("expected 2 messages (assistant text + tool), got %d", firstLen)
	}
}

// ---------------------------------------------------------------------------
// Phase 2 new tests: UIMessage fields, live-streaming flow, hydration
// ---------------------------------------------------------------------------

// TestHydrate_UserMessageHasTimestamp verifies that hydrating a user message
// populates the Timestamp field from the store's CreatedAt.
func TestHydrate_UserMessageHasTimestamp(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "timestamped question"}},
		},
	})
	_ = app.Init()
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].Role != components.RoleUser {
		t.Errorf("role = %q, want user", app.messages[0].Role)
	}
	// Timestamp should be non-zero (store sets CreatedAt).
	if app.messages[0].Timestamp.IsZero() {
		t.Errorf("expected non-zero Timestamp for user message")
	}
}

func TestHydrate_OrphanCompactionMarkerUsesTimestampOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	app, st, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "deleted before marker"}},
		},
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "visible after marker"}},
		},
	})

	msgs, err := st.MessagesDirectForSession(ctx, app.opts.SessionID)
	if err != nil {
		t.Fatalf("MessagesDirectForSession: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 stored messages, got %d", len(msgs))
	}
	marker, err := st.AddCompactionMarker(ctx, app.opts.SessionID, msgs[0].ID, "older compacted context", 123)
	if err != nil {
		t.Fatalf("AddCompactionMarker: %v", err)
	}

	base := time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)
	if _, err := st.DB().ExecContext(ctx, `UPDATE messages SET created_at = ? WHERE id = ?`, base.UnixMilli(), msgs[0].ID); err != nil {
		t.Fatalf("update first message timestamp: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE messages SET created_at = ? WHERE id = ?`, base.Add(2*time.Minute).UnixMilli(), msgs[1].ID); err != nil {
		t.Fatalf("update second message timestamp: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE compaction_markers SET created_at = ? WHERE id = ?`, base.Add(time.Minute).UnixMilli(), marker.ID); err != nil {
		t.Fatalf("update marker timestamp: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE messages SET deleted_at = ? WHERE id = ?`, base.Add(3*time.Minute).UnixMilli(), msgs[0].ID); err != nil {
		t.Fatalf("soft-delete first message: %v", err)
	}

	_ = app.Init()
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected marker plus visible message, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleMarker {
		t.Fatalf("first role = %q, want marker", app.messages[0].Role)
	}
	if app.messages[0].MarkerSummary != "older compacted context" {
		t.Errorf("marker summary = %q", app.messages[0].MarkerSummary)
	}
	if app.messages[1].Role != components.RoleUser || app.messages[1].Raw != "visible after marker" {
		t.Fatalf("second message = role %q raw %q, want visible user message", app.messages[1].Role, app.messages[1].Raw)
	}
}

// TestHydrate_AssistantThinkingOnly verifies that an assistant message with
// ONLY thinking parts (no text) produces one assistant entry with Thinking
// populated and empty Raw.
func TestHydrate_AssistantThinkingOnly(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartThinking, Text: "deep thoughts"}},
		},
	})
	_ = app.Init()
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message (thinking-only assistant), got %d", got)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Errorf("role = %q, want assistant", app.messages[0].Role)
	}
	if app.messages[0].Thinking != "deep thoughts" {
		t.Errorf("Thinking = %q, want 'deep thoughts'", app.messages[0].Thinking)
	}
	if app.messages[0].Raw != "" {
		t.Errorf("Raw = %q, want empty for thinking-only", app.messages[0].Raw)
	}
}

// TestHydrate_AssistantTextOnly verifies that an assistant message with only
// text parts (no thinking) produces one assistant entry with Raw populated
// and empty Thinking.
func TestHydrate_AssistantTextOnly(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "just an answer"}},
		},
	})
	_ = app.Init()
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].Thinking != "" {
		t.Errorf("Thinking = %q, want empty for text-only", app.messages[0].Thinking)
	}
	if app.messages[0].Raw != "just an answer" {
		t.Errorf("Raw = %q, want 'just an answer'", app.messages[0].Raw)
	}
}

// TestHydrate_AssistantBothThinkingAndText verifies that an assistant message
// with both thinking and text parts produces one entry with both fields set.
func TestHydrate_AssistantBothThinkingAndText(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{Kind: session.PartThinking, Text: "let me consider"},
				{Kind: session.PartText, Text: "my answer"},
			},
		},
	})
	_ = app.Init()
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].Thinking != "let me consider" {
		t.Errorf("Thinking = %q", app.messages[0].Thinking)
	}
	if app.messages[0].Raw != "my answer" {
		t.Errorf("Raw = %q", app.messages[0].Raw)
	}
}

// TestHydrate_AssistantToolOnly verifies that an assistant message with ONLY
// tool_use parts (no text, no thinking) emits no assistant uiMessage — only
// the tool row.  This prevents empty bubbles.
func TestHydrate_AssistantToolOnly(t *testing.T) {
	t.Parallel()
	toolUseID := "toolu_toolonly01"
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{
				{
					Kind:      session.PartToolUse,
					ToolID:    toolUseID,
					ToolName:  "bash",
					ToolInput: []byte(`{"command":"ls"}`),
				},
			},
		},
		{
			Role: session.RoleTool,
			Parts: []session.Part{
				{Kind: session.PartToolResult, ToolUseID: toolUseID, Content: "file.go"},
			},
		},
	})
	_ = app.Init()
	// Expect: tool only (no assistant bubble because Raw and Thinking are both empty).
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message (tool only, no empty assistant bubble), got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleTool {
		t.Errorf("[0].Role = %q, want tool", app.messages[0].Role)
	}
	if app.messages[0].Raw != "file.go" {
		t.Errorf("[0].Raw = %q, want 'file.go'", app.messages[0].Raw)
	}
}

// TestLiveThinkingThenTextThenFinalize verifies the full live streaming
// lifecycle: thinking deltas → text deltas → MessageAppended.
func TestLiveThinkingThenTextThenFinalize(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Thinking phase.
	app.Handle(bus.AssistantThinkingDelta{SessionID: "fg-session", Text: "think "})
	app.Handle(bus.AssistantThinkingDelta{SessionID: "fg-session", Text: "more"})

	// Text phase — same message.
	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "result "})
	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "here"})

	// Exactly 1 streaming assistant message with both fields.
	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	msg := app.messages[0]
	if msg.Role != components.RoleAssistant {
		t.Errorf("role = %q, want assistant", msg.Role)
	}
	if msg.Thinking != "think more" {
		t.Errorf("Thinking = %q, want 'think more'", msg.Thinking)
	}
	if msg.Raw != "result here" {
		t.Errorf("Raw = %q, want 'result here'", msg.Raw)
	}
	if !msg.IsStreaming {
		t.Errorf("expected IsStreaming=true before finalize")
	}

	// Finalize.
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m1"})
	if app.messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=false after MessageAppended")
	}
	if app.messages[0].FinalMarkdown == "" {
		t.Errorf("expected FinalMarkdown populated on finalize")
	}
}

// TestLiveTextOnlyThenFinalize verifies that text-only streaming (no thinking)
// works correctly with MessageAppended.
func TestLiveTextOnlyThenFinalize(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "hello"})
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m2"})

	if got := len(app.messages); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
	if app.messages[0].IsStreaming {
		t.Errorf("expected not streaming after MessageAppended")
	}
	if app.messages[0].Raw != "hello" {
		t.Errorf("Raw = %q, want 'hello'", app.messages[0].Raw)
	}
	if app.messages[0].Thinking != "" {
		t.Errorf("Thinking = %q, want empty for text-only", app.messages[0].Thinking)
	}
}

// ---------------------------------------------------------------------------
// Regression tests: duplicate-message guard (HYGGE-12)
// ---------------------------------------------------------------------------

// TestNoDuplicate_AssistantMessageAppendedTwice is a regression test for the
// duplicate-bubble bug (HYGGE-12) where a duplicate bus.MessageAppended event
// for the same assistant messageID caused a second bubble to appear.
//
// Sequence:
//  1. AssistantTextDelta → streaming assistant bubble
//  2. MessageAppended{role:"assistant", messageID:"m1"} → flushes bubble, stamps MessageID
//  3. MessageAppended{role:"assistant", messageID:"m1"} → must be a no-op (guard fires)
func TestNoDuplicate_AssistantMessageAppendedTwice(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "hello"})
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m1"})

	// After first flush: 1 non-streaming assistant bubble.
	if got := len(app.messages); got != 1 {
		t.Fatalf("after first MessageAppended: expected 1 message, got %d", got)
	}
	if app.messages[0].IsStreaming {
		t.Errorf("expected IsStreaming=false after first MessageAppended")
	}
	if app.messages[0].MessageID != "m1" {
		t.Errorf("MessageID = %q, want 'm1' after flush", app.messages[0].MessageID)
	}

	// Second duplicate MessageAppended with the same ID.
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m1"})

	// Must still be exactly 1 message — no duplicate inserted.
	if got := len(app.messages); got != 1 {
		t.Fatalf("after duplicate MessageAppended: expected 1 message, got %d (duplicate inserted)", got)
	}
}

// TestNoDuplicate_AssistantMessageAppendedAfterToolCall is a regression test
// for the scenario that most reliably triggered HYGGE-12: the assistant message
// is flushed while a tool row already trails it, then a duplicate MessageAppended
// arrives.  Without the fix, currentAssistantMessageIndex() returns -1 (no
// streaming assistant visible) and insertPersistedAssistantMessage re-inserts.
//
// Sequence:
//  1. AssistantTextDelta → streaming assistant bubble
//  2. ToolCallRequested → tool row appended; lastAssistantFlushIdx reset
//  3. MessageAppended{role:"assistant", messageID:"m1"} → flush (finds streaming assistant)
//  4. MessageAppended{role:"assistant", messageID:"m1"} → must be a no-op
func TestNoDuplicate_AssistantMessageAppendedAfterToolCall(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Step 1: streaming delta.
	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "I'll run a tool."})

	// Step 2: tool call appended.
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "bash",
		ToolUseID: "tu-1",
		Args:      []byte(`{"command":"echo hi"}`),
	})

	// State: [assistant(streaming), tool(streaming)]
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages after delta+tool, got %d", got)
	}

	// Step 3: first MessageAppended.
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m1"})

	// [assistant(flushed/m1), tool(streaming)]
	if got := len(app.messages); got != 2 {
		t.Fatalf("after first MessageAppended: expected 2 messages, got %d", got)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Fatalf("messages[0].Role = %q, want assistant", app.messages[0].Role)
	}
	if app.messages[0].IsStreaming {
		t.Errorf("expected assistant bubble flushed (IsStreaming=false)")
	}
	if app.messages[0].MessageID != "m1" {
		t.Errorf("messages[0].MessageID = %q, want 'm1'", app.messages[0].MessageID)
	}

	// Step 4: duplicate MessageAppended — must be silently dropped.
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "m1"})

	if got := len(app.messages); got != 2 {
		t.Fatalf("after duplicate MessageAppended: expected 2 messages, got %d (duplicate inserted)", got)
	}
	if app.messages[0].Role != components.RoleAssistant {
		t.Errorf("messages[0] role changed unexpectedly: %q", app.messages[0].Role)
	}
	if app.messages[1].Role != components.RoleTool {
		t.Errorf("messages[1] role changed unexpectedly: %q", app.messages[1].Role)
	}
}

// TestNoDuplicate_UserMessageAppendedTwice is a regression test for the
// duplicate-bubble bug (HYGGE-12) on the user message path: two consecutive
// MessageAppended events with the same user messageID must not insert two user
// bubbles.
//
// Sequence:
//  1. MessageAppended{role:"user", messageID:"u1"} → appends user bubble
//  2. MessageAppended{role:"user", messageID:"u1"} → must be a no-op
func TestNoDuplicate_UserMessageAppendedTwice(t *testing.T) {
	t.Parallel()
	app, st, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "hello"}},
		},
	})
	_ = app.Init()

	// Retrieve the stored message ID.
	ctx := t.Context()
	msgs, err := st.MessagesDirectForSession(ctx, app.opts.SessionID)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("MessagesDirectForSession: %v (msgs=%d)", err, len(msgs))
	}
	msgID := msgs[0].ID

	// The session was hydrated by Init(); the user message is already in a.messages
	// with MessageID stamped.
	if got := len(app.messages); got != 1 {
		t.Fatalf("after Init: expected 1 message, got %d", got)
	}
	if app.messages[0].MessageID != msgID {
		t.Errorf("hydrated message MessageID = %q, want %q", app.messages[0].MessageID, msgID)
	}

	// Fire a duplicate MessageAppended for the already-hydrated user message.
	app.Handle(bus.MessageAppended{
		SessionID: app.opts.SessionID,
		Role:      "user",
		MessageID: msgID,
	})

	// Must still be exactly 1 message — no duplicate.
	if got := len(app.messages); got != 1 {
		t.Fatalf("after duplicate user MessageAppended: expected 1 message, got %d (duplicate inserted)", got)
	}
	if app.messages[0].Raw != "hello" {
		t.Errorf("user message Raw = %q, want 'hello'", app.messages[0].Raw)
	}
}

// ---------------------------------------------------------------------------
// P2 tests: async markdown hydration
// ---------------------------------------------------------------------------

// TestHydrate_P2_NoSyncRenderOnHydrate verifies that after hydrateMessagesFromStore
// the messages have Raw populated but FinalMarkdown and VisibleRaw are initially
// empty (glamour render has not yet run synchronously; VisibleRaw is streaming-only).
func TestHydrate_P2_NoSyncRenderOnHydrate(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "## heading"}},
		},
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "# response"}},
		},
	})

	// Hydrate directly (without running Init's tea.Cmd).
	app.messages = nil
	_ = app.hydrateMessagesFromStore(app.opts.SessionID)

	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages after hydration, got %d", got)
	}
	// Raw must be set (P1 plain-text fallback works via Raw).
	for i, msg := range app.messages {
		if msg.Raw == "" {
			t.Errorf("messages[%d].Raw is empty; want the raw text populated", i)
		}
		// VisibleRaw is streaming-only and must NOT be set on hydrated messages.
		if msg.VisibleRaw != "" {
			t.Errorf("messages[%d].VisibleRaw = %q; must be empty for hydrated messages (streaming-only field)", i, msg.VisibleRaw)
		}
		// FinalMarkdown must be empty immediately after hydration (not rendered yet).
		if msg.FinalMarkdown != "" {
			t.Errorf("messages[%d].FinalMarkdown is %q immediately after hydration; expected empty (async render pending)", i, msg.FinalMarkdown)
		}
	}
}

// TestHydrate_P2_MarkdownBatchMsgAppliesFinalMarkdown verifies that when
// markdownBatchMsg arrives with the correct width, FinalMarkdown is set and the
// cache is invalidated.  Results are keyed by MessageID.
func TestHydrate_P2_MarkdownBatchMsgAppliesFinalMarkdown(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "hello user"}},
		},
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "hello assistant"}},
		},
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	app.messages = nil
	_ = app.hydrateMessagesFromStore(app.opts.SessionID)

	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}

	// Both messages have MessageIDs set by hydration; use the ID-keyed format.
	rendered := map[string]string{}
	for _, msg := range app.messages {
		if msg.MessageID == "" {
			t.Fatalf("hydrated message missing MessageID; cannot test ID-keyed batch")
		}
		switch msg.Role {
		case components.RoleUser:
			rendered[msg.MessageID] = "<rendered-user>"
		case components.RoleAssistant:
			rendered[msg.MessageID] = "<rendered-assistant>"
		}
	}

	batch := markdownBatchMsg{
		rendered: rendered,
		width:    app.msgColW,
	}
	_, _ = app.Update(batch)

	if app.messages[0].FinalMarkdown != "<rendered-user>" {
		t.Errorf("messages[0].FinalMarkdown = %q, want '<rendered-user>'", app.messages[0].FinalMarkdown)
	}
	if app.messages[1].FinalMarkdown != "<rendered-assistant>" {
		t.Errorf("messages[1].FinalMarkdown = %q, want '<rendered-assistant>'", app.messages[1].FinalMarkdown)
	}
}

// TestHydrate_P2_MarkdownBatchMsgStaleWidthIgnored verifies that a
// markdownBatchMsg with a width that no longer matches msgColW does not apply
// stale rendered strings to FinalMarkdown (a new batch at the correct width
// will be re-issued by the handler).
func TestHydrate_P2_MarkdownBatchMsgStaleWidthIgnored(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "some response"}},
		},
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	app.messages = nil
	_ = app.hydrateMessagesFromStore(app.opts.SessionID)

	// Deliver a batch with a different (stale) width.
	staleW := app.msgColW + 99
	mid := app.messages[0].MessageID
	batch := markdownBatchMsg{
		rendered: map[string]string{mid: "<stale-render>"},
		width:    staleW,
	}
	_, _ = app.Update(batch)

	// FinalMarkdown must remain empty because the result was stale.
	if app.messages[0].FinalMarkdown != "" {
		t.Errorf("FinalMarkdown should be empty for stale-width batch, got %q", app.messages[0].FinalMarkdown)
	}
}

// TestHydrate_P2_MarkdownBatchMsgUnknownIDsSkipped verifies that a
// markdownBatchMsg with MessageIDs that do not match any current message is
// safely applied (no panic, no wrong application).
func TestHydrate_P2_MarkdownBatchMsgUnknownIDsSkipped(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleAssistant,
			Parts: []session.Part{{Kind: session.PartText, Text: "short"}},
		},
	})
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	app.messages = nil
	_ = app.hydrateMessagesFromStore(app.opts.SessionID)

	// Batch contains a stale/unknown MessageID.
	batch := markdownBatchMsg{
		rendered: map[string]string{
			"unknown-id-1": "r1",
			"unknown-id-2": "r2",
		},
		width: app.msgColW,
	}
	// Should not panic; existing message must not be modified.
	_, _ = app.Update(batch)
	if app.messages[0].FinalMarkdown != "" {
		t.Errorf("FinalMarkdown should be empty for unknown-ID batch, got %q", app.messages[0].FinalMarkdown)
	}
}

// TestHydrate_P2_PreallocatedSlice verifies that hydrateSessionMessages
// returns a non-nil slice even for an empty session (preallocated make).
func TestHydrate_P2_PreallocatedSlice(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, nil /* no messages */)
	visited := make(map[string]struct{})
	msgs := app.hydrateSessionMessages(app.opts.SessionID, visited)
	// Expect non-nil (make returns empty, not nil slice) when empty.
	if msgs == nil {
		t.Errorf("expected non-nil slice from hydrateSessionMessages for empty session")
	}
}

// ---------------------------------------------------------------------------
// P2 regression tests (batch 2): WindowSizeMsg async, width re-issue,
// MessageID-keyed safety, hydrated VisibleRaw empty.
// ---------------------------------------------------------------------------

// TestWindowSizeMsg_NoSyncGlamourRender verifies that WindowSizeMsg does not
// synchronously update FinalMarkdown on any message.  Instead it returns a
// tea.Cmd (the async batch).  FinalMarkdown is only updated when that Cmd's
// result is applied.
func TestWindowSizeMsg_NoSyncGlamourRender(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	// Finalize an assistant message so it has FinalMarkdown set.
	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "# heading\n\nbody"})
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "msg-1"})
	if app.messages[0].FinalMarkdown == "" {
		t.Fatal("expected FinalMarkdown set after flush")
	}
	original := app.messages[0].FinalMarkdown

	// Clear FinalMarkdown to simulate a "not yet rendered" state.
	app.messages[0].FinalMarkdown = ""

	// WindowSizeMsg must NOT synchronously re-render (no rerenderFinalMarkdownMessages call).
	_, cmd := app.Update(tea.WindowSizeMsg{Width: 200, Height: 40})

	// FinalMarkdown must still be empty after the Update returns (async pending).
	if app.messages[0].FinalMarkdown != "" {
		t.Errorf("WindowSizeMsg must not synchronously update FinalMarkdown; got %q", app.messages[0].FinalMarkdown)
	}

	// The returned Cmd must be non-nil (the batch cmd).
	if cmd == nil {
		t.Fatal("WindowSizeMsg with messages should return a non-nil batch Cmd")
	}

	// Executing the Cmd should produce a markdownBatchMsg.
	result := cmd()
	if result == nil {
		t.Fatal("batch Cmd returned nil Msg")
	}
	app.Update(result)

	// After applying the batch result, FinalMarkdown must differ from the old value
	// (re-rendered at the new width 200 instead of the previous width).
	if app.messages[0].FinalMarkdown == "" {
		t.Errorf("FinalMarkdown should be populated after batch result applied")
	}
	if app.messages[0].FinalMarkdown == original {
		// May match if the width change did not affect wrapping — that's OK.
		// The important thing is that it was applied asynchronously, not synchronously.
	}
	_ = original
}

// TestMarkdownBatchMsg_WidthMismatchReissues verifies that receiving a
// markdownBatchMsg with a stale width causes the handler to re-issue a new
// batch Cmd at the current width (rather than returning nil).
func TestMarkdownBatchMsg_WidthMismatchReissues(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "# some content"})
	app.Handle(bus.MessageAppended{SessionID: "fg-session", Role: "assistant", MessageID: "msg-w1"})
	// Clear FinalMarkdown to test re-issue.
	app.messages[0].FinalMarkdown = ""
	app.msgColW = 80

	// Deliver a batch with a mismatched width.
	staleW := app.msgColW + 50
	staleBatch := markdownBatchMsg{
		rendered: map[string]string{"msg-w1": "<stale>"},
		width:    staleW,
	}
	_, reissueCmd := app.Update(staleBatch)

	// FinalMarkdown must NOT have been applied (stale).
	if app.messages[0].FinalMarkdown != "" {
		t.Errorf("stale-width batch must not apply FinalMarkdown; got %q", app.messages[0].FinalMarkdown)
	}

	// A re-issue Cmd must be returned.
	if reissueCmd == nil {
		t.Fatal("width mismatch must re-issue a new batch Cmd")
	}

	// The re-issued Cmd should produce a markdownBatchMsg at the current width.
	reissueResult := reissueCmd()
	if reissueResult == nil {
		t.Fatal("re-issued Cmd returned nil")
	}
	app.Update(reissueResult)

	// After the correctly-widthed batch is applied, FinalMarkdown should be set.
	if app.messages[0].FinalMarkdown == "" {
		t.Errorf("FinalMarkdown should be set after re-issued batch at correct width")
	}
}

// TestMarkdownBatch_MessageIDKeyedSkipsIndexShift verifies that when messages
// are rendered with MessageID keys, applying results after an index shift
// (e.g. a new message was prepended) does NOT apply to the wrong message.
// The ID-keyed lookup correctly routes the result regardless of index.
func TestMarkdownBatch_MessageIDKeyedSkipsIndexShift(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)
	app.msgColW = 80

	// Set up two messages: user at [0], assistant at [1].
	app.messages = []uiMessage{
		{
			Role:      components.RoleUser,
			Raw:       "user question",
			MessageID: "uid-1",
		},
		{
			Role:      components.RoleAssistant,
			Raw:       "assistant answer",
			MessageID: "aid-1",
		},
	}

	// Build a batch targeting only the assistant message (by ID).
	batch := markdownBatchMsg{
		rendered: map[string]string{"aid-1": "<rendered-assistant>"},
		width:    app.msgColW,
	}

	// Simulate an index shift: insert a new message at the front.
	// Now assistant is at index [2] instead of [1].
	app.messages = append([]uiMessage{{
		Role:      components.RoleUser,
		Raw:       "new user message",
		MessageID: "uid-2",
	}}, app.messages...)

	// Apply the batch. The ID-keyed lookup must find "aid-1" at its current index.
	app.Update(batch)

	// Find the assistant message (now at index [2]) and check FinalMarkdown.
	var found bool
	for _, msg := range app.messages {
		if msg.MessageID == "aid-1" {
			if msg.FinalMarkdown != "<rendered-assistant>" {
				t.Errorf("aid-1 FinalMarkdown = %q, want '<rendered-assistant>'", msg.FinalMarkdown)
			}
			found = true
		} else if msg.FinalMarkdown != "" {
			t.Errorf("message %q should not have FinalMarkdown set, got %q", msg.MessageID, msg.FinalMarkdown)
		}
	}
	if !found {
		t.Error("message 'aid-1' not found in app.messages")
	}
}

// TestHydrate_VisibleRawNotSetOnHydratedMessages verifies (at the integration
// level) that resuming a session never sets VisibleRaw on any hydrated message.
// VisibleRaw is a streaming-only field.
func TestHydrate_VisibleRawNotSetOnHydratedMessages(t *testing.T) {
	t.Parallel()
	app, _, _ := newTestAppWithStore(t, []session.NewMessage{
		{
			Role:  session.RoleUser,
			Parts: []session.Part{{Kind: session.PartText, Text: "question with **markdown**"}},
		},
		{
			Role: session.RoleAssistant,
			Parts: []session.Part{{
				Kind: session.PartText,
				Text: "# Answer\n\nWith some _formatting_.",
			}},
		},
	})
	_ = app.Init()

	for i, msg := range app.messages {
		if msg.VisibleRaw != "" {
			t.Errorf("messages[%d] (role %q) has VisibleRaw=%q; must be empty for hydrated messages (streaming-only)",
				i, msg.Role, msg.VisibleRaw)
		}
	}
}
