-- Knowledge Registry K1: knowledge sources, immutable source revisions, and
-- document snapshots. Registry facts only — no index/chunk/K0 derivations.

CREATE TABLE knowledge_sources (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  name VARCHAR(160) NOT NULL,
  type VARCHAR(20) NOT NULL CHECK (type IN ('git', 'local')),
  repository_url TEXT NOT NULL,
  package_path TEXT NOT NULL DEFAULT '',
  default_ref TEXT NOT NULL DEFAULT '',
  sync_mode VARCHAR(30) NOT NULL CHECK (sync_mode IN ('server_pull', 'client_push')),
  status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'error')),
  active_revision_id UUID,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (account_id, name),
  -- Composite key so child rows can carry account-scoped foreign keys.
  UNIQUE (id, account_id)
);

CREATE INDEX idx_knowledge_sources_account_type ON knowledge_sources(account_id, type, status);
CREATE INDEX idx_knowledge_sources_metadata ON knowledge_sources USING GIN(metadata);

-- Immutable ingest facts. Rows are append-only: no update path exists in the
-- application, and identity columns are hash-checked.
CREATE TABLE knowledge_source_revisions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  source_id UUID NOT NULL,

  revision_key TEXT NOT NULL,
  commit_sha VARCHAR(80) NOT NULL DEFAULT ''
    CHECK (commit_sha = '' OR commit_sha ~ '^[0-9a-f]{7,80}$'),
  local_snapshot_id TEXT NOT NULL DEFAULT '',
  tree_hash VARCHAR(64) NOT NULL DEFAULT ''
    CHECK (tree_hash = '' OR tree_hash ~ '^[0-9a-f]{64}$'),
  package_hash VARCHAR(64) NOT NULL CHECK (package_hash ~ '^[0-9a-f]{64}$'),
  manifest JSONB NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(manifest) = 'object'),
  status VARCHAR(20) NOT NULL DEFAULT 'ingested' CHECK (status IN ('ingested', 'failed')),
  error TEXT NOT NULL DEFAULT '',

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (id, account_id),
  UNIQUE (source_id, revision_key),
  UNIQUE (source_id, package_hash),
  -- Account-scoped composite FK: a revision can never point at a source in a
  -- different account.
  FOREIGN KEY (source_id, account_id)
    REFERENCES knowledge_sources(id, account_id) ON DELETE CASCADE
);

CREATE INDEX idx_knowledge_source_revisions_source
  ON knowledge_source_revisions(source_id, created_at DESC);

-- Active pointer is a mutable reference onto immutable revisions. Default
-- NO ACTION: deleting a revision that is still referenced as active is
-- rejected until the pointer is cleared first. Account deletion cascades
-- through accounts -> sources/revisions without violating this constraint.
ALTER TABLE knowledge_sources
  ADD CONSTRAINT knowledge_sources_active_revision_fkey
  FOREIGN KEY (active_revision_id, account_id)
  REFERENCES knowledge_source_revisions(id, account_id);

CREATE TABLE knowledge_documents (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  source_id UUID NOT NULL,
  revision_id UUID NOT NULL,

  path TEXT NOT NULL,
  sha256 VARCHAR(64) NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  size_bytes BIGINT NOT NULL DEFAULT 0 CHECK (size_bytes >= 0),
  mime_type TEXT NOT NULL DEFAULT '',
  indexable BOOLEAN NOT NULL DEFAULT false,
  content_snapshot TEXT NOT NULL DEFAULT '',

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (revision_id, path),
  FOREIGN KEY (source_id, account_id)
    REFERENCES knowledge_sources(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (revision_id, account_id)
    REFERENCES knowledge_source_revisions(id, account_id) ON DELETE CASCADE
);

CREATE INDEX idx_knowledge_documents_account_revision
  ON knowledge_documents(account_id, revision_id, path);
CREATE INDEX idx_knowledge_documents_source ON knowledge_documents(source_id);
