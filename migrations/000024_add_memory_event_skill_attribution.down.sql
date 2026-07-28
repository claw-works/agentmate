DROP INDEX IF EXISTS idx_memory_events_session;
DROP INDEX IF EXISTS idx_memory_events_skill_version;

ALTER TABLE memory_events DROP CONSTRAINT IF EXISTS memory_events_skill_version_account_fkey;
ALTER TABLE memory_events DROP COLUMN IF EXISTS skill_version_id;

ALTER TABLE skill_versions DROP CONSTRAINT IF EXISTS skill_versions_id_account_key;
