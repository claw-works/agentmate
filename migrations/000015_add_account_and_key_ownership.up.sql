CREATE TABLE accounts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO accounts (id, name, created_at, updated_at)
SELECT id, email, created_at, created_at
FROM users
ON CONFLICT (id) DO NOTHING;

ALTER TABLE users ADD COLUMN account_id UUID;
UPDATE users SET account_id = id WHERE account_id IS NULL;
ALTER TABLE users ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE users
  ADD CONSTRAINT users_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_users_account_id ON users(account_id);

ALTER TABLE api_keys ADD COLUMN account_id UUID;
UPDATE api_keys ak
SET account_id = u.account_id
FROM users u
WHERE ak.user_id = u.id;
ALTER TABLE api_keys ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE api_keys
  ADD CONSTRAINT api_keys_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_api_keys_account_id ON api_keys(account_id, created_at DESC);

ALTER TABLE api_logs ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
UPDATE api_logs l
SET account_id = ak.account_id
FROM api_keys ak
WHERE l.key_id = ak.id;
UPDATE api_logs l
SET account_id = u.account_id
FROM users u
WHERE l.account_id IS NULL AND l.user_id = u.id;
CREATE INDEX idx_api_logs_account_id ON api_logs(account_id, created_at DESC);

ALTER TABLE todos ADD COLUMN account_id UUID;
ALTER TABLE todos ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE todos t
SET account_id = u.account_id
FROM users u
WHERE t.user_id = u.id;
ALTER TABLE todos ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE todos
  ADD CONSTRAINT todos_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_todos_account_id ON todos(account_id, created_at DESC);
CREATE INDEX idx_todos_key_id ON todos(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE notes ADD COLUMN account_id UUID;
ALTER TABLE notes ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE notes n
SET account_id = u.account_id
FROM users u
WHERE n.user_id = u.id;
ALTER TABLE notes ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE notes
  ADD CONSTRAINT notes_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_notes_account_id ON notes(account_id, created_at DESC);
CREATE INDEX idx_notes_key_id ON notes(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE reports ADD COLUMN account_id UUID;
ALTER TABLE reports ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE reports r
SET account_id = u.account_id,
    key_id = r.source_key_id
FROM users u
WHERE r.user_id = u.id;
ALTER TABLE reports ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE reports
  ADD CONSTRAINT reports_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_reports_account_id ON reports(account_id, created_at DESC);
CREATE INDEX idx_reports_key_id ON reports(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE bookmarks ADD COLUMN account_id UUID;
ALTER TABLE bookmarks ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE bookmarks b
SET account_id = u.account_id
FROM users u
WHERE b.user_id = u.id;
ALTER TABLE bookmarks ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE bookmarks
  ADD CONSTRAINT bookmarks_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_bookmarks_account_id ON bookmarks(account_id, created_at DESC);
CREATE INDEX idx_bookmarks_key_id ON bookmarks(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE expenses ADD COLUMN account_id UUID;
ALTER TABLE expenses ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE expenses e
SET account_id = u.account_id
FROM users u
WHERE e.user_id = u.id;
ALTER TABLE expenses ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE expenses
  ADD CONSTRAINT expenses_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_expenses_account_id ON expenses(account_id, happened_at DESC);
CREATE INDEX idx_expenses_key_id ON expenses(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE skill_logs ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE skill_logs ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE skill_logs l
SET account_id = u.account_id
FROM users u
WHERE l.user_id = u.id;
CREATE INDEX idx_skill_logs_account_id ON skill_logs(account_id, created_at DESC);
CREATE INDEX idx_skill_logs_key_id ON skill_logs(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE skill_versions ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE skill_versions ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE skill_versions v
SET account_id = u.account_id
FROM users u
WHERE v.user_id = u.id;
CREATE INDEX idx_skill_versions_account_name ON skill_versions(account_id, skill_name, published_at DESC);
CREATE UNIQUE INDEX idx_skill_versions_account_hash
  ON skill_versions(account_id, skill_name, content_hash)
  WHERE account_id IS NOT NULL;
CREATE INDEX idx_skill_versions_key_id ON skill_versions(key_id, published_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE skill_sources ADD COLUMN account_id UUID;
ALTER TABLE skill_sources ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE skill_sources s
SET account_id = u.account_id
FROM users u
WHERE s.user_id = u.id;
ALTER TABLE skill_sources ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE skill_sources
  ADD CONSTRAINT skill_sources_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX idx_skill_sources_account_unique
  ON skill_sources(account_id, type, repository_url, package_path);
CREATE INDEX idx_skill_sources_account_type ON skill_sources(account_id, type, status);
CREATE INDEX idx_skill_sources_key_id ON skill_sources(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE skill_source_revisions ADD COLUMN account_id UUID;
ALTER TABLE skill_source_revisions ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE skill_source_revisions r
SET account_id = u.account_id
FROM users u
WHERE r.user_id = u.id;
ALTER TABLE skill_source_revisions ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE skill_source_revisions
  ADD CONSTRAINT skill_source_revisions_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_skill_source_revisions_account_id ON skill_source_revisions(account_id, created_at DESC);
CREATE INDEX idx_skill_source_revisions_key_id ON skill_source_revisions(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE skill_version_files ADD COLUMN account_id UUID;
ALTER TABLE skill_version_files ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE skill_version_files f
SET account_id = u.account_id
FROM users u
WHERE f.user_id = u.id;
ALTER TABLE skill_version_files ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE skill_version_files
  ADD CONSTRAINT skill_version_files_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_skill_version_files_account_path ON skill_version_files(account_id, path);
CREATE INDEX idx_skill_version_files_key_id ON skill_version_files(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE retrieval_documents ADD COLUMN account_id UUID;
ALTER TABLE retrieval_documents ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE retrieval_documents d
SET account_id = u.account_id
FROM users u
WHERE d.user_id = u.id;
ALTER TABLE retrieval_documents ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE retrieval_documents
  ADD CONSTRAINT retrieval_documents_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX idx_retrieval_documents_account_source
  ON retrieval_documents(account_id, namespace, source_type, source_id, chunk_key);
CREATE INDEX idx_retrieval_documents_account_namespace ON retrieval_documents(account_id, namespace, status);
CREATE INDEX idx_retrieval_documents_key_id ON retrieval_documents(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE retrieval_index_jobs ADD COLUMN account_id UUID;
ALTER TABLE retrieval_index_jobs ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE retrieval_index_jobs j
SET account_id = u.account_id
FROM users u
WHERE j.user_id = u.id;
ALTER TABLE retrieval_index_jobs ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE retrieval_index_jobs
  ADD CONSTRAINT retrieval_index_jobs_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_retrieval_index_jobs_account_source ON retrieval_index_jobs(account_id, namespace, source_type, source_id);
CREATE INDEX idx_retrieval_index_jobs_key_id ON retrieval_index_jobs(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE retrieval_queries ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE retrieval_queries ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE retrieval_queries q
SET account_id = u.account_id
FROM users u
WHERE q.user_id = u.id;
CREATE INDEX idx_retrieval_queries_account_namespace ON retrieval_queries(account_id, namespace, created_at DESC);
CREATE INDEX idx_retrieval_queries_key_id ON retrieval_queries(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE retrieval_feedback ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE retrieval_feedback ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE retrieval_feedback f
SET account_id = u.account_id
FROM users u
WHERE f.user_id = u.id;
CREATE INDEX idx_retrieval_feedback_account ON retrieval_feedback(account_id, created_at DESC);
CREATE INDEX idx_retrieval_feedback_key_id ON retrieval_feedback(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE memory_entries ADD COLUMN account_id UUID;
ALTER TABLE memory_entries ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE memory_entries m
SET account_id = u.account_id
FROM users u
WHERE m.user_id = u.id;
ALTER TABLE memory_entries ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE memory_entries
  ADD CONSTRAINT memory_entries_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
CREATE INDEX idx_memory_entries_account_scope ON memory_entries(account_id, scope_type, scope_key, status);
CREATE INDEX idx_memory_entries_key_id ON memory_entries(key_id, created_at DESC) WHERE key_id IS NOT NULL;

ALTER TABLE memory_evidence ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE memory_evidence ADD COLUMN key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL;
UPDATE memory_evidence e
SET account_id = m.account_id,
    key_id = m.key_id
FROM memory_entries m
WHERE e.memory_id = m.id;
CREATE INDEX idx_memory_evidence_account ON memory_evidence(account_id, created_at DESC);
CREATE INDEX idx_memory_evidence_key_id ON memory_evidence(key_id, created_at DESC) WHERE key_id IS NOT NULL;
