CREATE TABLE IF NOT EXISTS session_memories (
  id          TEXT PRIMARY KEY,
  session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  content     TEXT NOT NULL,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL,
  deleted_at  INTEGER NULL
);

CREATE INDEX IF NOT EXISTS idx_session_memories_active
  ON session_memories(session_id, created_at, id)
  WHERE deleted_at IS NULL;
