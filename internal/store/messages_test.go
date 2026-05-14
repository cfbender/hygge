package store_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
)

func appendUserText(t *testing.T, s *store.Store, sessionID, text string) *session.Message {
	t.Helper()
	m, err := s.AppendMessage(t.Context(), sessionID, session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: text}},
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	return m
}

func TestAppendMessage_PopulatesFields(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m := appendUserText(t, s, sess.ID, "hello")

	if m.ID == "" || len(m.ID) != 26 {
		t.Errorf("expected 26-char ULID id, got %q", m.ID)
	}
	if m.CreatedAt.IsZero() {
		t.Errorf("expected CreatedAt set")
	}
	if m.Role != session.RoleUser {
		t.Errorf("role: got %q", m.Role)
	}

	// Round-trip through GetMessage.
	got, err := s.GetMessage(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.ID != m.ID || got.SessionID != sess.ID || len(got.Parts) != 1 {
		t.Errorf("GetMessage mismatch: %+v", got)
	}
}

func TestAppendMessage_RejectsInvalidPartsViaCHECK(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	// Bypass MarshalParts and write a deliberately invalid parts blob.
	_, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO messages (id, session_id, role, parts, created_at)
		VALUES (?, ?, 'user', ?, 0)`,
		session.NewMessageID(), sess.ID, "not json")
	if err == nil {
		t.Fatal("expected CHECK constraint to reject invalid parts JSON")
	}
}

func TestAppendMessage_RejectsInvalidRoleViaCHECK(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	_, err := s.DB().ExecContext(context.Background(), `
		INSERT INTO messages (id, session_id, role, parts, created_at)
		VALUES (?, ?, 'bogus_role', '[]', 0)`,
		session.NewMessageID(), sess.ID)
	if err == nil {
		t.Fatal("expected CHECK constraint to reject unknown role")
	}
}

func TestAppendMessage_NoSuchSessionFails(t *testing.T) {
	s := newTestStore(t)
	_, err := s.AppendMessage(t.Context(), "01ZZZZZZZZZZZZZZZZZZZZZZZZ", session.NewMessage{
		Role:  session.RoleUser,
		Parts: []session.Part{{Kind: session.PartText, Text: "x"}},
	})
	if err == nil {
		t.Fatal("expected FK violation for non-existent session_id")
	}
}

func TestAppendMessage_RequiresFields(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.AppendMessage(t.Context(), "", session.NewMessage{Role: session.RoleUser}); err == nil {
		t.Error("expected error for empty session id")
	}
	sess := mustCreateSession(t, s, "/p")
	if _, err := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{}); err == nil {
		t.Error("expected error for empty role")
	}
}

func TestAppendMessage_EmptyPartsRoundTrip(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m, err := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{Role: session.RoleSystem})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	got, err := s.GetMessage(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if len(got.Parts) != 0 {
		t.Errorf("expected empty parts, got %d", len(got.Parts))
	}
	if got.Role != session.RoleSystem {
		t.Errorf("role: got %q want system", got.Role)
	}
}

func TestAppendMessage_PersistsUsageColumns(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m, err := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
		Role:             session.RoleAssistant,
		Parts:            []session.Part{{Kind: session.PartText, Text: "ok"}},
		InputTokens:      11,
		OutputTokens:     22,
		CacheReadTokens:  33,
		CacheWriteTokens: 44,
		CostUSD:          0.05,
		DurationMs:       120,
	})
	if err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	got, err := s.GetMessage(t.Context(), m.ID)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.InputTokens != 11 || got.OutputTokens != 22 || got.CacheReadTokens != 33 || got.CacheWriteTokens != 44 {
		t.Errorf("token columns mismatch: %+v", got)
	}
	if got.CostUSD != 0.05 || got.DurationMs != 120 {
		t.Errorf("cost/duration mismatch: %+v", got)
	}
}

func TestGetMessage_NotFound(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetMessage(t.Context(), "01ZZZZZZZZZZZZZZZZZZZZZZZZ"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMessagesForSession_EmptySessionID(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.MessagesForSession(t.Context(), ""); err == nil {
		t.Error("expected error for empty session id")
	}
}

func TestMessagesSinceLatestMarker_EmptySessionID(t *testing.T) {
	s := newTestStore(t)
	if _, _, err := s.MessagesSinceLatestMarker(t.Context(), ""); err == nil {
		t.Error("expected error for empty session id")
	}
}

func TestUpdateSessionTotals_BumpsUpdatedAt(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	originalUpdated := sess.UpdatedAt
	// Sleep so the millisecond-resolution updated_at column actually
	// advances.
	time.Sleep(2 * time.Millisecond)
	if err := s.UpdateSessionTotals(t.Context(), sess.ID, session.Totals{InputTokens: 1}); err != nil {
		t.Fatalf("UpdateSessionTotals: %v", err)
	}
	got, err := s.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !got.UpdatedAt.After(originalUpdated) {
		t.Errorf("expected updated_at to advance: %v vs %v", got.UpdatedAt, originalUpdated)
	}
}

// TestMessagesForSession_NoFork: a simple linear conversation.
func TestMessagesForSession_NoFork(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	var ids []string
	for i := range 4 {
		m := appendUserText(t, s, sess.ID, "msg")
		ids = append(ids, m.ID)
		_ = i
	}
	msgs, err := s.MessagesForSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
	for i, m := range msgs {
		if m.ID != ids[i] {
			t.Errorf("msgs[%d].ID: got %q want %q", i, m.ID, ids[i])
		}
	}
}

// TestMessagesForSession_SingleLevelFork: create 5 messages, fork at #3,
// add 2 more to the fork.  Reading the fork session returns
// [m1, m2, m3, m4_fork, m5_fork] in order — message 3 IS included because
// the filter is `m.id <= fork_message_id` (inclusive).
func TestMessagesForSession_SingleLevelFork(t *testing.T) {
	s := newTestStore(t)
	root := mustCreateSession(t, s, "/p")
	var rootIDs []string
	for range 5 {
		rootIDs = append(rootIDs, appendUserText(t, s, root.ID, "r").ID)
	}

	// Fork at the third message.
	forkAt := rootIDs[2]
	child, err := s.ForkSession(t.Context(), root.ID, forkAt, sampleModel(), "branch")
	if err != nil {
		t.Fatalf("ForkSession: %v", err)
	}

	var forkIDs []string
	for range 2 {
		forkIDs = append(forkIDs, appendUserText(t, s, child.ID, "f").ID)
	}

	msgs, err := s.MessagesForSession(t.Context(), child.ID)
	if err != nil {
		t.Fatalf("MessagesForSession: %v", err)
	}

	want := []string{rootIDs[0], rootIDs[1], rootIDs[2], forkIDs[0], forkIDs[1]}
	if len(msgs) != len(want) {
		t.Fatalf("len: got %d want %d (got=%v want=%v)", len(msgs), len(want), msgIDs(msgs), want)
	}
	for i, id := range want {
		if msgs[i].ID != id {
			t.Errorf("msgs[%d]: got %q want %q", i, msgs[i].ID, id)
		}
	}

	// The root session itself should still see all 5 of its messages and
	// none of the fork's.
	rootMsgs, err := s.MessagesForSession(t.Context(), root.ID)
	if err != nil {
		t.Fatalf("MessagesForSession root: %v", err)
	}
	if len(rootMsgs) != 5 {
		t.Errorf("root sees %d messages, want 5", len(rootMsgs))
	}
}

// TestMessagesForSession_TwoLevelFork builds root -> fork1 (at root.msg2)
// -> fork2 (at fork1.msg2) and asserts the leaf sees the interleaved history.
func TestMessagesForSession_TwoLevelFork(t *testing.T) {
	s := newTestStore(t)
	root := mustCreateSession(t, s, "/p")
	rootMsgs := []string{
		appendUserText(t, s, root.ID, "r1").ID,
		appendUserText(t, s, root.ID, "r2").ID,
		appendUserText(t, s, root.ID, "r3").ID,
	}

	fork1, err := s.ForkSession(t.Context(), root.ID, rootMsgs[1], sampleModel(), "f1")
	if err != nil {
		t.Fatalf("ForkSession root->fork1: %v", err)
	}
	fork1Msgs := []string{
		appendUserText(t, s, fork1.ID, "f1.1").ID,
		appendUserText(t, s, fork1.ID, "f1.2").ID,
	}

	fork2, err := s.ForkSession(t.Context(), fork1.ID, fork1Msgs[1], sampleModel(), "f2")
	if err != nil {
		t.Fatalf("ForkSession fork1->fork2: %v", err)
	}
	fork2Msgs := []string{
		appendUserText(t, s, fork2.ID, "f2.1").ID,
	}

	got, err := s.MessagesForSession(t.Context(), fork2.ID)
	if err != nil {
		t.Fatalf("MessagesForSession fork2: %v", err)
	}

	// Expect: root r1, root r2 (fork point on root); fork1 1, fork1 2
	// (fork point on fork1); fork2 1.
	want := []string{
		rootMsgs[0], rootMsgs[1],
		fork1Msgs[0], fork1Msgs[1],
		fork2Msgs[0],
	}
	if len(got) != len(want) {
		t.Fatalf("len: got %d want %d (got=%v want=%v)",
			len(got), len(want), msgIDs(got), want)
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Errorf("msgs[%d]: got %q want %q", i, got[i].ID, id)
		}
	}
}

// TestMessagesForSession_DepthCap builds a chain 10 levels deep and asserts
// a query against the deepest leaf surfaces ErrForkChainTooDeep.
func TestMessagesForSession_DepthCap(t *testing.T) {
	s := newTestStore(t)
	cur := mustCreateSession(t, s, "/p")
	firstMsg := appendUserText(t, s, cur.ID, "seed")
	prevMsg := firstMsg
	// Build 10 levels of forks on top.
	for range 10 {
		child, err := s.ForkSession(t.Context(), cur.ID, prevMsg.ID, sampleModel(), "")
		if err != nil {
			t.Fatalf("ForkSession: %v", err)
		}
		newMsg := appendUserText(t, s, child.ID, "x")
		cur = child
		prevMsg = newMsg
	}

	_, err := s.MessagesForSession(t.Context(), cur.ID)
	if !errors.Is(err, store.ErrForkChainTooDeep) {
		t.Fatalf("expected ErrForkChainTooDeep, got %v", err)
	}
}

func TestMessagesSinceLatestMarker_NoMarker(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	for range 3 {
		appendUserText(t, s, sess.ID, "m")
	}
	msgs, marker, err := s.MessagesSinceLatestMarker(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("MessagesSinceLatestMarker: %v", err)
	}
	if marker != nil {
		t.Errorf("expected nil marker, got %+v", marker)
	}
	if len(msgs) != 3 {
		t.Errorf("expected 3 messages, got %d", len(msgs))
	}
}

func TestMessagesSinceLatestMarker_AfterMarker(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	var allIDs []string
	for range 5 {
		allIDs = append(allIDs, appendUserText(t, s, sess.ID, "m").ID)
	}

	// Compact at message 2 (index 1).  Messages 3..5 should remain visible.
	marker, err := s.AddCompactionMarker(t.Context(), sess.ID, allIDs[1], "summary", 12345)
	if err != nil {
		t.Fatalf("AddCompactionMarker: %v", err)
	}

	msgs, gotMarker, err := s.MessagesSinceLatestMarker(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("MessagesSinceLatestMarker: %v", err)
	}
	if gotMarker == nil || gotMarker.ID != marker.ID {
		t.Fatalf("marker mismatch: got %+v want %+v", gotMarker, marker)
	}
	want := allIDs[2:]
	if len(msgs) != len(want) {
		t.Fatalf("len: got %d want %d (got=%v want=%v)", len(msgs), len(want), msgIDs(msgs), want)
	}
	for i, id := range want {
		if msgs[i].ID != id {
			t.Errorf("msgs[%d]: got %q want %q", i, msgs[i].ID, id)
		}
	}
}

func TestForeignKey_CASCADEOnSessionHardDelete(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")
	m := appendUserText(t, s, sess.ID, "hi")
	_, err := s.AddCompactionMarker(t.Context(), sess.ID, m.ID, "s", 1)
	if err != nil {
		t.Fatalf("AddCompactionMarker: %v", err)
	}

	if _, err := s.DB().ExecContext(context.Background(),
		"DELETE FROM sessions WHERE id = ?", sess.ID,
	); err != nil {
		t.Fatalf("hard delete: %v", err)
	}

	var msgs, markers int
	_ = s.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM messages WHERE session_id = ?", sess.ID,
	).Scan(&msgs)
	_ = s.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM compaction_markers WHERE session_id = ?", sess.ID,
	).Scan(&markers)
	if msgs != 0 || markers != 0 {
		t.Errorf("expected cascade: msgs=%d markers=%d", msgs, markers)
	}
}

// TestConcurrent_ReadsAndOneWriter spawns 4 readers and 1 writer that
// appends 100 messages.  No errors, no races; all writes land.
func TestConcurrent_ReadsAndOneWriter(t *testing.T) {
	s := newTestStore(t)
	sess := mustCreateSession(t, s, "/p")

	var (
		wg        sync.WaitGroup
		writeErrs atomic.Uint64
		readErrs  atomic.Uint64
		stop      atomic.Bool
	)

	// 4 readers.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if _, err := s.MessagesForSession(t.Context(), sess.ID); err != nil {
					readErrs.Add(1)
				}
			}
		}()
	}

	// 1 writer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stop.Store(true)
		for range 100 {
			if _, err := s.AppendMessage(t.Context(), sess.ID, session.NewMessage{
				Role:  session.RoleUser,
				Parts: []session.Part{{Kind: session.PartText, Text: "x"}},
			}); err != nil {
				writeErrs.Add(1)
			}
		}
	}()

	wg.Wait()
	if writeErrs.Load() != 0 {
		t.Errorf("write errors: %d", writeErrs.Load())
	}
	if readErrs.Load() != 0 {
		t.Errorf("read errors: %d", readErrs.Load())
	}

	var count int
	if err := s.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM messages WHERE session_id = ?", sess.ID,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 100 {
		t.Errorf("expected 100 messages, got %d", count)
	}
}

func msgIDs(msgs []*session.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ID
	}
	return out
}
