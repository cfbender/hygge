package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cfbender/hygge/internal/session"
	"github.com/cfbender/hygge/internal/store"
)

// newTestStore opens a fresh SQLite database in a per-test temp directory.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	s, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// Compile-time check: the concrete Store satisfies the session.Store interface.
var _ session.Store = (*store.Store)(nil)

func TestOpen_FreshDB_RunsMigrations(t *testing.T) {
	s := newTestStore(t)
	var version int
	if err := s.DB().QueryRowContext(context.Background(),
		"SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1",
	).Scan(&version); err != nil {
		t.Fatalf("scan schema_migrations: %v", err)
	}
	// Bumped to 2 when the 0002_subagent_kind migration landed.  Keep
	// in lock-step with the highest version under
	// internal/store/migrations/.
	if version != 2 {
		t.Fatalf("expected schema_migrations version 2, got %d", version)
	}
}

func TestOpen_Idempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")

	s1, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	s2, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer func() { _ = s2.Close() }()

	var count int
	if err := s2.DB().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM schema_migrations",
	).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	// Must equal the number of files under
	// internal/store/migrations/.  Bumped to 2 with
	// 0002_subagent_kind.sql.
	if count != 2 {
		t.Fatalf("expected 2 migration records after re-open, got %d", count)
	}
}

func TestOpen_EmptyPathRejected(t *testing.T) {
	if _, err := store.Open(t.Context(), ""); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestOpen_InMemory(t *testing.T) {
	s, err := store.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatalf("Open :memory:: %v", err)
	}
	defer func() { _ = s.Close() }()

	sess, err := s.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p",
		Model:      session.ModelRef{Provider: "anthropic", Name: "x"},
	})
	if err != nil {
		t.Fatalf("CreateSession on memory db: %v", err)
	}
	if sess.ID == "" {
		t.Fatal("expected non-empty id")
	}
}

func TestOpen_ReopenPreservesData(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	s1, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	sess, err := s1.CreateSession(t.Context(), session.NewSession{
		ProjectDir: "/p",
		Model:      session.ModelRef{Provider: "anthropic", Name: "x"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	s2, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() { _ = s2.Close() }()
	got, err := s2.GetSession(t.Context(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession after reopen: %v", err)
	}
	if got.ID != sess.ID {
		t.Errorf("session not preserved: %q vs %q", got.ID, sess.ID)
	}
}

func TestStore_PragmasAreSet(t *testing.T) {
	s := newTestStore(t)
	var mode string
	if err := s.DB().QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Fatalf("expected wal, got %q", mode)
	}
	var fk int
	if err := s.DB().QueryRowContext(context.Background(), "PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("expected foreign_keys=1, got %d", fk)
	}
}

func TestStore_CloseIdempotent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "hygge_test.db")
	s, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}
