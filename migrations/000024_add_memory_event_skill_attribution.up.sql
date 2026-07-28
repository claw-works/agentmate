-- M1: attribute memory events to the skill execution that produced them.
--
-- memory_events already carries session_id, which is enough to correlate a
-- session's activity. It is NOT enough to attribute: a session commonly runs
-- several skills, so session-level correlation cannot tell which execution
-- produced a given event. Attribution needs the skill version itself.
--
-- The column is nullable on purpose. Not every memory event comes from a skill
-- execution — a user jotting down a note produces an event with no skill at
-- all. Requiring a value would force callers to invent one, which is worse than
-- recording honestly that the origin is unknown.

-- Composite key so the foreign key below can be account-scoped, mirroring what
-- 000021 did for knowledge_documents. skill_versions.account_id is nullable for
-- historical reasons, and a composite FK uses MATCH SIMPLE: a NULL in either
-- referencing column skips the check entirely. That is acceptable here because
-- the referencing side (memory_events.account_id) is NOT NULL, so the only way
-- to skip the check is a NULL skill_version_id, which means "no attribution"
-- and is exactly what we want to allow.
ALTER TABLE skill_versions
  ADD CONSTRAINT skill_versions_id_account_key UNIQUE (id, account_id);

ALTER TABLE memory_events
  ADD COLUMN skill_version_id UUID;

ALTER TABLE memory_events
  ADD CONSTRAINT memory_events_skill_version_account_fkey
  FOREIGN KEY (skill_version_id, account_id)
  REFERENCES skill_versions(id, account_id) ON DELETE SET NULL;

-- Attribution queries always start from one of these two anchors, both
-- account-scoped. Partial indexes keep them small: in a note-taking workload
-- most events carry neither anchor.
CREATE INDEX idx_memory_events_skill_version
  ON memory_events(account_id, skill_version_id, occurred_at DESC)
  WHERE skill_version_id IS NOT NULL;
CREATE INDEX idx_memory_events_session
  ON memory_events(account_id, session_id, occurred_at)
  WHERE session_id <> '';
