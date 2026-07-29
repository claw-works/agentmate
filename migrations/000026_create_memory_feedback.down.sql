DROP TABLE IF EXISTS memory_feedback;

ALTER TABLE memory_entries DROP CONSTRAINT IF EXISTS memory_entries_id_account_key;
