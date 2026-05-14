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
	entries := uiEntriesFromStoreMessage(m, map[string]session.Part{}, map[string]struct{}{})
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
// with thinking + text + tool_use produces 3 entries in order: thinking, text,
// tool — with the result from the split tool row.
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

	// Expected: thinking, assistant-text, tool
	if got := len(app.messages); got != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", got, app.messages)
	}
	if app.messages[0].Role != components.RoleThinking {
		t.Errorf("[0].Role = %q, want thinking", app.messages[0].Role)
	}
	if app.messages[0].Raw != "planning…" {
		t.Errorf("[0].Raw = %q", app.messages[0].Raw)
	}
	if app.messages[1].Role != components.RoleAssistant {
		t.Errorf("[1].Role = %q, want assistant", app.messages[1].Role)
	}
	if app.messages[1].Raw != "I'll grep for it." {
		t.Errorf("[1].Raw = %q", app.messages[1].Raw)
	}
	if app.messages[2].Role != components.RoleTool {
		t.Errorf("[2].Role = %q, want tool", app.messages[2].Role)
	}
	if app.messages[2].Raw != "found it" {
		t.Errorf("[2].Raw = %q", app.messages[2].Raw)
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
// The task tool row must be present with ToolUseID set, so subagent
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
					ToolName:  "task",
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
	if toolMsg.ToolName != "task" {
		t.Errorf("[2].ToolName = %q, want task", toolMsg.ToolName)
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
