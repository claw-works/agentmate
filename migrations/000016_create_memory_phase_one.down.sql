DROP TABLE IF EXISTS memory_relations;

ALTER TABLE memory_evidence
  DROP CONSTRAINT IF EXISTS memory_evidence_account_memory_fkey,
  DROP CONSTRAINT IF EXISTS memory_evidence_account_id_fkey,
  ALTER COLUMN account_id DROP NOT NULL,
  ADD CONSTRAINT memory_evidence_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL,
  DROP COLUMN IF EXISTS user_id;

ALTER TABLE memory_entries
  DROP CONSTRAINT IF EXISTS memory_entries_account_source_event_fkey,
  DROP COLUMN IF EXISTS harmful_count,
  DROP COLUMN IF EXISTS useful_count,
  DROP COLUMN IF EXISTS access_count,
  DROP COLUMN IF EXISTS extractor_version,
  DROP COLUMN IF EXISTS extraction_method,
  DROP COLUMN IF EXISTS source_event_id,
  DROP COLUMN IF EXISTS superseded_by,
  DROP COLUMN IF EXISTS valid_to,
  DROP COLUMN IF EXISTS valid_from;

DROP INDEX IF EXISTS idx_memory_entries_account_id_unique;
DROP TABLE IF EXISTS memory_events;
