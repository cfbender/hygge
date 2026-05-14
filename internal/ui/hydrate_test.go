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

func TestUiEntryFromStoreMessage_ThinkingPartProducesThinkingEntry(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartThinking, Text: "thinking..."}},
	}
	// Assistant with only thinking parts: uiEntryFromStoreMessage returns the
	// thinking entry (first entry from uiEntriesFromStoreMessage).
	entry, ok := uiEntryFromStoreMessage(m)
	if !ok {
		t.Fatalf("expected ok=true for assistant with thinking parts")
	}
	if entry.Role != components.RoleThinking {
		t.Errorf("role = %q, want RoleThinking", entry.Role)
	}
	if entry.Raw != "thinking..." {
		t.Errorf("raw = %q, want 'thinking...'", entry.Raw)
	}
}

// ---------------------------------------------------------------------------
// New tests: thinking, markers, and subagent hydration
// ---------------------------------------------------------------------------

// TestUiEntriesFromStoreMessage_ThinkingBeforeText verifies that an assistant
// message with both a thinking part and a text part produces two entries in
// order: thinking first, then text.
func TestUiEntriesFromStoreMessage_ThinkingBeforeText(t *testing.T) {
	t.Parallel()
	m := &session.Message{
		Role: session.RoleAssistant,
		Parts: []session.Part{
			{Kind: session.PartThinking, Text: "let me think"},
			{Kind: session.PartText, Text: "here is the answer"},
		},
	}
	entries := uiEntriesFromStoreMessage(m)
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
	if entries[0].Role != components.RoleThinking {
		t.Errorf("entries[0].Role = %q, want RoleThinking", entries[0].Role)
	}
	if entries[0].Raw != "let me think" {
		t.Errorf("entries[0].Raw = %q", entries[0].Raw)
	}
	if entries[1].Role != components.RoleAssistant {
		t.Errorf("entries[1].Role = %q, want RoleAssistant", entries[1].Role)
	}
	if entries[1].Raw != "here is the answer" {
		t.Errorf("entries[1].Raw = %q", entries[1].Raw)
	}
}

// TestHydrate_ThinkingPartsProduceRoleThinkingEntries verifies that resuming a
// session with assistant messages that contain thinking parts produces
// RoleThinking entries in the correct chronological position.
func TestHydrate_ThinkingPartsProduceRoleThinkingEntries(t *testing.T) {
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

	// Expect: user, thinking, assistant (3 entries).
	if got := len(app.messages); got != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleUser {
		t.Errorf("messages[0].Role = %q, want user", app.messages[0].Role)
	}
	if app.messages[1].Role != components.RoleThinking {
		t.Errorf("messages[1].Role = %q, want thinking", app.messages[1].Role)
	}
	if app.messages[1].Raw != "let me reason about this" {
		t.Errorf("messages[1].Raw = %q", app.messages[1].Raw)
	}
	if app.messages[2].Role != components.RoleAssistant {
		t.Errorf("messages[2].Role = %q, want assistant", app.messages[2].Role)
	}
	if app.messages[2].Raw != "here is my answer" {
		t.Errorf("messages[2].Raw = %q", app.messages[2].Raw)
	}
}

// TestLiveThinkingDelta_StreamsAndFinalizes verifies that:
//  1. AssistantThinkingDelta events produce a streaming RoleThinking message.
//  2. When AssistantTextDelta arrives, the thinking message is finalized
//     (IsStreaming=false) and text streaming begins as a new assistant entry.
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
	if th.Role != components.RoleThinking {
		t.Errorf("role = %q, want thinking", th.Role)
	}
	if th.Raw != "step 1: step 2" {
		t.Errorf("raw = %q, want 'step 1: step 2'", th.Raw)
	}
	if !th.IsStreaming {
		t.Errorf("expected IsStreaming=true while thinking")
	}

	// Emit a text delta — thinking should finalize.
	app.Handle(bus.AssistantTextDelta{SessionID: "fg-session", Text: "here is the answer"})

	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages after text delta, got %d", got)
	}
	if app.messages[0].IsStreaming {
		t.Errorf("thinking message should be finalized after text delta")
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("messages[1].Role = %q, want assistant", app.messages[1].Role)
	}
	if app.messages[1].Raw != "here is the answer" {
		t.Errorf("messages[1].Raw = %q", app.messages[1].Raw)
	}
}

// TestLiveThinkingDelta_FinalizedOnToolCall verifies that a trailing thinking
// block is finalized when a ToolCallRequested event arrives.
func TestLiveThinkingDelta_FinalizedOnToolCall(t *testing.T) {
	t.Parallel()
	app, _ := makeForegroundApp(t)

	app.Handle(bus.AssistantThinkingDelta{SessionID: "fg-session", Text: "thinking"})
	app.Handle(bus.ToolCallRequested{
		SessionID: "fg-session",
		ToolName:  "read",
		Args:      []byte(`{"path":"/tmp/x"}`),
	})

	// messages[0] = thinking (finalized), messages[1] = tool
	if got := len(app.messages); got != 2 {
		t.Fatalf("expected 2 messages, got %d", got)
	}
	if app.messages[0].IsStreaming {
		t.Errorf("thinking should be finalized on ToolCallRequested")
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
// that spawned a subagent produces a parent task tool row with SubagentID set
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

	// Append user message + task tool call on the parent.
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
				ToolName:  "task",
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

	// Create child session (KindSubagent) with slug containing the ToolUseID.
	child, err := st.CreateSession(ctx, session.NewSession{
		ProjectDir: "/tmp/proj",
		Model:      model,
		ParentID:   parent.ID,
		Kind:       session.KindSubagent,
		Slug:       "general: find something [" + toolUseID + "]",
	})
	if err != nil {
		t.Fatalf("CreateSession child: %v", err)
	}

	// Append a message to the child session.
	if _, err := st.AppendMessage(ctx, child.ID, session.NewMessage{
		Role:  session.RoleAssistant,
		Parts: []session.Part{{Kind: session.PartText, Text: "I found it"}},
	}); err != nil {
		t.Fatalf("AppendMessage child: %v", err)
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
		SessionID:     parent.ID,
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
	_ = app.Init()

	// Find the task tool message.
	var taskMsg *uiMessage
	for i := range app.messages {
		if app.messages[i].Role == components.RoleTool && app.messages[i].ToolName == "task" {
			taskMsg = &app.messages[i]
			break
		}
	}
	if taskMsg == nil {
		t.Fatalf("expected a task UIMessage in messages: %+v", app.messages)
	}
	if taskMsg.SubagentID == "" {
		t.Fatalf("expected task message to have SubagentID set, got empty")
	}
	if taskMsg.SubagentID != child.ID {
		t.Errorf("SubagentID = %q, want %q", taskMsg.SubagentID, child.ID)
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
			{Kind: session.PartToolUse, ToolID: toolUseL1, ToolName: "task", ToolInput: []byte(`{}`)},
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
			{Kind: session.PartToolUse, ToolID: toolUseL2, ToolName: "task", ToolInput: []byte(`{}`)},
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
		Theme:         theme.ShellTheme(),
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
	app.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
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

	// Level-1 state should have a task tool message pointing at l2.
	var l1TaskMsg *uiMessage
	for i := range stateL1.Messages {
		if stateL1.Messages[i].Role == components.RoleTool && stateL1.Messages[i].ToolName == "task" {
			l1TaskMsg = &stateL1.Messages[i]
			break
		}
	}
	if l1TaskMsg == nil {
		t.Fatalf("expected task message in l1 messages: %+v", stateL1.Messages)
	}
	if l1TaskMsg.SubagentID != l2.ID {
		t.Errorf("l1 task SubagentID = %q, want %q", l1TaskMsg.SubagentID, l2.ID)
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
