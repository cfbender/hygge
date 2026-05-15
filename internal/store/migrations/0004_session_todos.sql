CREATE TABLE IF NOT EXISTS session_todos (
  session_id  TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
  items       TEXT NOT NULL CHECK (json_valid(items)),
  updated_at  INTEGER NOT NULL
);
