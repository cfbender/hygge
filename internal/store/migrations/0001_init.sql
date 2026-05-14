CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
  id                        TEXT PRIMARY KEY,
  parent_id                 TEXT NULL REFERENCES sessions(id) ON DELETE SET NULL,
  fork_message_id           TEXT NULL,
  slug                      TEXT NULL,
  project_dir               TEXT NOT NULL,
  model_provider            TEXT NOT NULL,
  model_name                TEXT NOT NULL,
  total_input_tokens        INTEGER NOT NULL DEFAULT 0,
  total_output_tokens       INTEGER NOT NULL DEFAULT 0,
  total_cache_read_tokens   INTEGER NOT NULL DEFAULT 0,
  total_cache_write_tokens  INTEGER NOT NULL DEFAULT 0,
  total_cost_usd            REAL NOT NULL DEFAULT 0.0,
  created_at                INTEGER NOT NULL,
  updated_at                INTEGER NOT NULL,
  deleted_at                INTEGER NULL,
  metadata                  TEXT NOT NULL DEFAULT '{}'
);

CREATE INDEX IF NOT EXISTS idx_sessions_parent_id   ON sessions(parent_id) WHERE parent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_sessions_project_dir ON sessions(project_dir, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_sessions_not_deleted ON sessions(created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS messages (
  id                  TEXT PRIMARY KEY,
  session_id          TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  role                TEXT NOT NULL CHECK (role IN ('user','assistant','tool','system')),
  parts               TEXT NOT NULL CHECK (json_valid(parts)),
  input_tokens        INTEGER NULL,
  output_tokens       INTEGER NULL,
  cache_read_tokens   INTEGER NULL,
  cache_write_tokens  INTEGER NULL,
  cost_usd            REAL NULL,
  duration_ms         INTEGER NULL,
  created_at          INTEGER NOT NULL,
  deleted_at          INTEGER NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id, created_at) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS compaction_markers (
  id                  TEXT PRIMARY KEY,
  session_id          TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
  before_message_id   TEXT NOT NULL,
  summary             TEXT NOT NULL,
  input_tokens_saved  INTEGER NOT NULL DEFAULT 0,
  created_at          INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_compaction_session ON compaction_markers(session_id, created_at DESC);
