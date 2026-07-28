-- Deleting an account must remove its skill content, not orphan it.
--
-- skill_versions.account_id and skill_logs.account_id were ON DELETE SET NULL.
-- Two consequences, both real:
--
-- 1. Account deletion could fail outright. idx_skill_versions_global_active is
--    UNIQUE(skill_name) WHERE account_id IS NULL AND is_active, so as soon as a
--    second account owning an active skill of the same name was deleted, the
--    SET NULL collided with the first account's orphaned row and the delete was
--    rejected. Discovered by the M1 attribution tests, which create skills with
--    recurring names across scratch accounts.
-- 2. Customer content survived the account. skill_versions stores the full
--    SKILL.md body and skill_logs stores trigger_text and user_correction, so
--    orphaned rows kept user-authored text with no owner to attribute or delete
--    it by.
--
-- No read path treats account_id IS NULL as globally visible (every skill query
-- filters account_id = $1), so this was never cross-account exposure — only
-- retention and a blocked delete.
--
-- Existing orphaned rows are intentionally left untouched: they predate this
-- migration, cannot be attributed to an account any more, and silently deleting
-- unattributable customer data is a worse default than reporting it. Query
-- `SELECT count(*) FROM skill_versions WHERE account_id IS NULL` to find them.

ALTER TABLE skill_versions
  DROP CONSTRAINT skill_versions_account_id_fkey;
ALTER TABLE skill_versions
  ADD CONSTRAINT skill_versions_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE skill_logs
  DROP CONSTRAINT skill_logs_account_id_fkey;
ALTER TABLE skill_logs
  ADD CONSTRAINT skill_logs_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
