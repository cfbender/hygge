package store

import (
	"context"
	"database/sql"
	"fmt"
)

// applyPragmas configures the SQLite connection for hygge:
//
//   - journal_mode = WAL   (concurrent reads, single writer; the workload fit)
//   - synchronous  = NORMAL (durable enough for a local agent store; faster)
//   - foreign_keys = ON     (must be set per-connection; default is OFF)
//   - busy_timeout = 5000ms (avoid SQLITE_BUSY under brief writer contention)
//
// journal_mode is the one PRAGMA that returns the new mode as a row; we
// scan and assert it equals "wal", otherwise we fail rather than silently
// running with the default rollback journal.  In-memory databases
// (":memory:" or file::memory:) report "memory" instead — that is the only
// mode they support — and we accept it.
func applyPragmas(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode = WAL").Scan(&mode); err != nil {
		return fmt.Errorf("store: enable WAL: %w", err)
	}
	if mode != "wal" && mode != "memory" {
		return fmt.Errorf("store: expected journal_mode=wal, got %q", mode)
	}

	for _, stmt := range []string{
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: %s: %w", stmt, err)
		}
	}

	// Sanity-check foreign_keys actually took.  It is silently ignored if
	// the SQLite build does not support it, so verify rather than trust.
	var fk int
	if err := db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&fk); err != nil {
		return fmt.Errorf("store: read foreign_keys: %w", err)
	}
	if fk != 1 {
		return fmt.Errorf("store: foreign_keys did not enable (got %d)", fk)
	}
	return nil
}
