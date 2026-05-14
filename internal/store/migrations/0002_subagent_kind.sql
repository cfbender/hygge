-- 0002_subagent_kind.sql
--
-- Adds a `kind` column to `sessions` so we can distinguish primary
-- sessions ("primary") from sub-agent sessions ("subagent") at the
-- schema level.  We took Approach A from the Stage A design: an
-- explicit kind column rather than overloading fork_message_id.  The
-- payoff is that `hygge sessions list` can grow a filter without
-- having to peek at message rows to figure out which sessions were
-- spawned by a `task` tool call.
--
-- Note: SQLite does not enforce CHECK constraints retroactively, so
-- we phrase the constraint as a CHECK that admits both legacy rows
-- (DEFAULT 'primary' fills them in) and new sub-agent rows.
--
-- The previous constraint -- "parent_id requires fork_message_id" --
-- lived only inside Store.CreateSession (Go code), not in the SQL
-- schema, so no DB-level loosening is required.  CreateSession is
-- updated alongside this migration so subagent rows can have
-- parent_id without fork_message_id.

ALTER TABLE sessions
  ADD COLUMN kind TEXT NOT NULL DEFAULT 'primary'
  CHECK (kind IN ('primary', 'subagent'));

CREATE INDEX IF NOT EXISTS idx_sessions_kind ON sessions(kind);
