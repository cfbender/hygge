// Package store is the SQLite-backed implementation of session.Store.
//
// # Driver and dependencies
//
// Uses modernc.org/sqlite, a pure-Go transpilation of SQLite.  No CGo is
// required.  ULIDs come from github.com/oklog/ulid/v2 (via internal/session).
//
// # Connection setup
//
// Every database opened by Open runs the following PRAGMAs before any other
// query:
//
//   - journal_mode = WAL
//   - synchronous  = NORMAL
//   - foreign_keys = ON
//   - busy_timeout = 5000
//
// See pragmas.go for the implementation and the assertion that WAL actually
// engaged.
//
// # Migrations
//
// Migrations are .sql files embedded from internal/store/migrations/.  Each
// is applied inside its own transaction.  Already-applied versions are
// tracked in schema_migrations and skipped on subsequent opens.
//
// # Fork-chain reads
//
// MessagesForSession walks the fork chain by recursive CTE, returning the
// inherited prefix from each ancestor up to (and including) its fork point,
// concatenated with the local session's messages.  See messages.go for the
// query.  The recursion depth is capped at 8 to defend against cycles or
// pathological nesting; exceeding the cap returns ErrForkChainTooDeep.
//
// # Bus events
//
// This package does NOT publish bus events.  The agent layer (Task 11)
// observes store operations and publishes the corresponding events.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/cfbender/hygge/internal/session"

	// Registers the "sqlite" driver name.
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a lookup misses (no row with the given id).
var ErrNotFound = errors.New("store: not found")

// ErrForkChainTooDeep is returned when MessagesForSession's recursive CTE
// hits the depth cap (currently 8 ancestors).
var ErrForkChainTooDeep = errors.New("store: fork chain exceeds maximum depth")

// MaxForkDepth is the maximum number of ancestor sessions walked when
// resolving fork history.  Anything deeper aborts with ErrForkChainTooDeep.
const MaxForkDepth = 8

// Store is a SQLite-backed session.Store.
type Store struct {
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Compile-time check: *Store satisfies session.Store.
var _ session.Store = (*Store)(nil)

// Open opens (or creates) a SQLite database at path, applies the connection
// PRAGMAs, and runs any pending migrations.  Pass ":memory:" for an
// in-memory database.
//
// The returned *Store is safe for concurrent use.
func Open(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("store: empty path")
	}

	dsn := path
	// For in-memory DBs, force a single shared connection so all
	// statements see the same schema.  Without this, sql.DB's pool can
	// spin up separate :memory: databases per connection.
	isMemory := path == ":memory:"
	if isMemory {
		dsn = "file::memory:?cache=shared"
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %q: %w", path, err)
	}
	if isMemory {
		// Single connection prevents the shared-cache memory DB from
		// being reaped if all conns are returned to the pool.
		db.SetMaxOpenConns(1)
	}

	if err := applyPragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database.  Safe to call multiple times.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

// DB exposes the underlying *sql.DB for tests that need to issue raw SQL.
// Not part of session.Store; do not use outside of test or maintenance code.
func (s *Store) DB() *sql.DB { return s.db }
