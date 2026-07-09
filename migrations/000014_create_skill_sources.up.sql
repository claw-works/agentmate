DROP INDEX IF EXISTS idx_skill_versions_hash;

CREATE UNIQUE INDEX idx_skill_versions_user_hash
  ON skill_versions(user_id, skill_name, content_hash)
  WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX idx_skill_versions_global_hash
  ON skill_versions(skill_name, content_hash)
  WHERE user_id IS NULL;

CREATE TABLE skill_sources (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  name VARCHAR(160) NOT NULL,
  type VARCHAR(20) NOT NULL CHECK (type IN ('git', 'local')),
  repository_url TEXT NOT NULL,
  package_path TEXT NOT NULL DEFAULT '',
  default_ref TEXT NOT NULL DEFAULT '',
  sync_mode VARCHAR(30) NOT NULL CHECK (sync_mode IN ('server_pull', 'client_push')),
  visibility VARCHAR(20) NOT NULL DEFAULT 'private' CHECK (visibility IN ('private', 'shared', 'public')),
  status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled', 'error')),
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (user_id, type, repository_url, package_path)
);

CREATE INDEX idx_skill_sources_user_type ON skill_sources(user_id, type, status);
CREATE INDEX idx_skill_sources_metadata ON skill_sources USING GIN(metadata);

CREATE TABLE skill_source_revisions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_id UUID NOT NULL REFERENCES skill_sources(id) ON DELETE CASCADE,
  skill_version_id UUID REFERENCES skill_versions(id) ON DELETE SET NULL,

  commit_sha VARCHAR(80) NOT NULL DEFAULT '',
  local_snapshot_id TEXT NOT NULL DEFAULT '',
  tree_hash VARCHAR(64) NOT NULL DEFAULT '',
  package_hash VARCHAR(64) NOT NULL DEFAULT '',
  status VARCHAR(20) NOT NULL DEFAULT 'ingested' CHECK (status IN ('queued', 'ingesting', 'ingested', 'failed')),
  error TEXT NOT NULL DEFAULT '',

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_skill_source_revisions_source ON skill_source_revisions(source_id, created_at DESC);
CREATE INDEX idx_skill_source_revisions_version ON skill_source_revisions(skill_version_id);
CREATE UNIQUE INDEX idx_skill_source_revisions_commit
  ON skill_source_revisions(source_id, commit_sha)
  WHERE commit_sha <> '';
CREATE UNIQUE INDEX idx_skill_source_revisions_snapshot
  ON skill_source_revisions(source_id, local_snapshot_id)
  WHERE local_snapshot_id <> '';

CREATE TABLE skill_version_files (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  source_revision_id UUID NOT NULL REFERENCES skill_source_revisions(id) ON DELETE CASCADE,
  version_id UUID REFERENCES skill_versions(id) ON DELETE CASCADE,

  path TEXT NOT NULL,
  kind VARCHAR(40) NOT NULL DEFAULT 'file',
  sha256 VARCHAR(64) NOT NULL,
  size_bytes BIGINT NOT NULL DEFAULT 0,
  mime_type TEXT NOT NULL DEFAULT '',
  indexable BOOLEAN NOT NULL DEFAULT false,
  content_snapshot TEXT NOT NULL DEFAULT '',

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (source_revision_id, path)
);

CREATE INDEX idx_skill_version_files_version ON skill_version_files(version_id);
CREATE INDEX idx_skill_version_files_user_path ON skill_version_files(user_id, path);
