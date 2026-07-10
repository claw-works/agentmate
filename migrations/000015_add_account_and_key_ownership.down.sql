ALTER TABLE memory_evidence DROP COLUMN IF EXISTS key_id;
ALTER TABLE memory_evidence DROP COLUMN IF EXISTS account_id;

ALTER TABLE memory_entries DROP COLUMN IF EXISTS key_id;
ALTER TABLE memory_entries DROP COLUMN IF EXISTS account_id;

ALTER TABLE retrieval_feedback DROP COLUMN IF EXISTS key_id;
ALTER TABLE retrieval_feedback DROP COLUMN IF EXISTS account_id;

ALTER TABLE retrieval_queries DROP COLUMN IF EXISTS key_id;
ALTER TABLE retrieval_queries DROP COLUMN IF EXISTS account_id;

ALTER TABLE retrieval_index_jobs DROP COLUMN IF EXISTS key_id;
ALTER TABLE retrieval_index_jobs DROP COLUMN IF EXISTS account_id;

ALTER TABLE retrieval_documents DROP COLUMN IF EXISTS key_id;
ALTER TABLE retrieval_documents DROP COLUMN IF EXISTS account_id;

ALTER TABLE skill_version_files DROP COLUMN IF EXISTS key_id;
ALTER TABLE skill_version_files DROP COLUMN IF EXISTS account_id;

ALTER TABLE skill_source_revisions DROP COLUMN IF EXISTS key_id;
ALTER TABLE skill_source_revisions DROP COLUMN IF EXISTS account_id;

ALTER TABLE skill_sources DROP COLUMN IF EXISTS key_id;
ALTER TABLE skill_sources DROP COLUMN IF EXISTS account_id;

ALTER TABLE skill_versions DROP COLUMN IF EXISTS key_id;
ALTER TABLE skill_versions DROP COLUMN IF EXISTS account_id;

ALTER TABLE skill_logs DROP COLUMN IF EXISTS key_id;
ALTER TABLE skill_logs DROP COLUMN IF EXISTS account_id;

ALTER TABLE expenses DROP COLUMN IF EXISTS key_id;
ALTER TABLE expenses DROP COLUMN IF EXISTS account_id;

ALTER TABLE bookmarks DROP COLUMN IF EXISTS key_id;
ALTER TABLE bookmarks DROP COLUMN IF EXISTS account_id;

ALTER TABLE reports DROP COLUMN IF EXISTS key_id;
ALTER TABLE reports DROP COLUMN IF EXISTS account_id;

ALTER TABLE notes DROP COLUMN IF EXISTS key_id;
ALTER TABLE notes DROP COLUMN IF EXISTS account_id;

ALTER TABLE todos DROP COLUMN IF EXISTS key_id;
ALTER TABLE todos DROP COLUMN IF EXISTS account_id;

ALTER TABLE api_logs DROP COLUMN IF EXISTS account_id;

ALTER TABLE api_keys DROP COLUMN IF EXISTS account_id;

ALTER TABLE users DROP COLUMN IF EXISTS account_id;

DROP TABLE IF EXISTS accounts;
