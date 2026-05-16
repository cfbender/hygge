-- 0005_session_parent_tool_use.sql
--
-- Stores the parent task tool_use_id for subagent sessions explicitly.  Older
-- rows encoded this in the human slug as a trailing [toolUseID], which made
-- hydration depend on buildSlug's display format.

ALTER TABLE sessions
  ADD COLUMN parent_tool_use_id TEXT NULL;

CREATE INDEX IF NOT EXISTS idx_sessions_parent_tool_use_id
  ON sessions(parent_tool_use_id)
  WHERE parent_tool_use_id IS NOT NULL;
