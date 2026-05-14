-- 0003_session_first_message.sql
--
-- Adds a `first_message_preview` column to `sessions` to support fast
-- substring filtering in session listings without a per-query JOIN to
-- the messages table.  The column caches the first user-message text
-- (up to 256 chars) on the session row; it is populated by
-- Store.AppendMessage the first time a user-role message is written.
--
-- Rationale for column vs. JOIN:
--   A JOIN requires a correlated sub-query or CTE per row in ListSessions.
--   With potentially dozens of sessions in a picker, the extra round-trips
--   are noticeable.  The preview column is a single-column scan that
--   pays a one-time write cost (one extra UPDATE on the first user
--   message per session) for O(1) read cost.  The 256-char cap keeps
--   the row size bounded and the preview useful for display too.
--
-- Backfill:
--   The UPDATE below walks every existing session that has at least one
--   user-role message, picks the chronologically first one (lowest
--   created_at), and writes the first 256 chars as the preview.  The
--   json_extract expression pulls the text from the first element of the
--   JSON parts array whose kind is 'text'.  Sessions without a user
--   message get NULL (the default).

ALTER TABLE sessions
  ADD COLUMN first_message_preview TEXT NULL DEFAULT NULL;

-- Backfill: for each session, find the first user message's text part
-- and write up to 256 chars into first_message_preview.
-- We use a subquery that returns the parts JSON of the earliest
-- user-role message for each session, then extract the 'text' field
-- from the first array element whose 'kind' == 'text'.
UPDATE sessions
SET first_message_preview = (
  SELECT SUBSTR(
    json_extract(m.parts, '$[0].text'),
    1, 256
  )
  FROM messages m
  WHERE m.session_id = sessions.id
    AND m.role = 'user'
    AND m.deleted_at IS NULL
  ORDER BY m.created_at ASC
  LIMIT 1
)
WHERE EXISTS (
  SELECT 1 FROM messages m2
  WHERE m2.session_id = sessions.id
    AND m2.role = 'user'
    AND m2.deleted_at IS NULL
);
