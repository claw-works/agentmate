WITH historical_file_packages AS (
  SELECT
    source_revision_id,
    encode(
      sha256(
        string_agg(
          convert_to(path, 'UTF8') || decode('00', 'hex') ||
          convert_to(sha256, 'UTF8') || decode('00', 'hex') ||
          convert_to(size_bytes::text, 'UTF8'),
          decode('0a', 'hex')
          ORDER BY path
        )
      ),
      'hex'
    ) AS package_hash
  FROM skill_version_files
  GROUP BY source_revision_id
)
UPDATE skill_source_revisions AS revision
SET package_hash = historical.package_hash
FROM historical_file_packages AS historical
WHERE revision.id = historical.source_revision_id
  AND revision.package_hash IS DISTINCT FROM historical.package_hash;

UPDATE skill_source_revisions AS revision
SET package_hash = encode(
  sha256(
    convert_to('SKILL.md', 'UTF8') || decode('00', 'hex') ||
    convert_to(version.content_hash, 'UTF8') || decode('00', 'hex') ||
    convert_to(octet_length(version.content)::text, 'UTF8')
  ),
  'hex'
)
FROM skill_versions AS version
WHERE revision.skill_version_id = version.id
  AND NOT EXISTS (
    SELECT 1
    FROM skill_version_files AS file
    WHERE file.source_revision_id = revision.id
  );

CREATE TABLE skill_source_revision_aliases (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
  source_id UUID NOT NULL REFERENCES skill_sources(id) ON DELETE CASCADE,
  revision_id UUID NOT NULL REFERENCES skill_source_revisions(id) ON DELETE CASCADE,
  local_snapshot_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (source_id, local_snapshot_id)
);

CREATE INDEX idx_skill_source_revision_aliases_revision
  ON skill_source_revision_aliases(revision_id);

WITH ranked_packages AS (
  SELECT
    revision.*,
    FIRST_VALUE(revision.id) OVER (
      PARTITION BY revision.source_id, revision.package_hash
      ORDER BY revision.created_at DESC, revision.id DESC
    ) AS winner_id
  FROM skill_source_revisions AS revision
  WHERE revision.package_hash <> ''
)
INSERT INTO skill_source_revision_aliases (
  account_id,
  user_id,
  key_id,
  source_id,
  revision_id,
  local_snapshot_id,
  created_at
)
SELECT
  account_id,
  user_id,
  key_id,
  source_id,
  winner_id,
  local_snapshot_id,
  created_at
FROM ranked_packages
WHERE local_snapshot_id <> '';

WITH duplicate_packages AS (
  SELECT
    revision.id,
    ROW_NUMBER() OVER (
      PARTITION BY revision.source_id,
        COALESCE(NULLIF(revision.package_hash, ''), version.content_hash)
      ORDER BY revision.created_at DESC, revision.id DESC
    ) AS duplicate_rank
  FROM skill_source_revisions AS revision
  LEFT JOIN skill_versions AS version ON version.id = revision.skill_version_id
  WHERE COALESCE(NULLIF(revision.package_hash, ''), version.content_hash, '') <> ''
)
DELETE FROM skill_source_revisions AS revision
USING duplicate_packages
WHERE revision.id = duplicate_packages.id
  AND duplicate_packages.duplicate_rank > 1;

UPDATE skill_source_revisions AS revision
SET package_hash = version.content_hash
FROM skill_versions AS version
WHERE revision.skill_version_id = version.id
  AND revision.package_hash = '';

UPDATE skill_source_revisions
SET tree_hash = package_hash
WHERE package_hash <> ''
  AND tree_hash IS DISTINCT FROM package_hash;

ALTER TABLE skill_source_revisions
  ADD COLUMN revision_key TEXT;

WITH revision_keys AS (
  SELECT
    revision.id,
    CASE
      WHEN revision.package_hash <> '' THEN 'package:' || revision.package_hash
      WHEN revision.commit_sha <> '' THEN 'commit:' || revision.commit_sha
      WHEN revision.local_snapshot_id <> '' THEN 'snapshot:' || revision.local_snapshot_id
      ELSE 'legacy:' || revision.id::text
    END AS base_key,
    ROW_NUMBER() OVER (
      PARTITION BY revision.source_id,
        CASE
          WHEN revision.package_hash <> '' THEN 'package:' || revision.package_hash
          WHEN revision.commit_sha <> '' THEN 'commit:' || revision.commit_sha
          WHEN revision.local_snapshot_id <> '' THEN 'snapshot:' || revision.local_snapshot_id
          ELSE 'legacy:' || revision.id::text
        END
      ORDER BY revision.created_at DESC, revision.id DESC
    ) AS duplicate_rank
  FROM skill_source_revisions AS revision
)
UPDATE skill_source_revisions AS revision
SET revision_key = CASE
  WHEN revision_keys.duplicate_rank = 1 THEN revision_keys.base_key
  ELSE revision_keys.base_key || ':legacy:' || revision.id::text
END
FROM revision_keys
WHERE revision.id = revision_keys.id;

ALTER TABLE skill_source_revisions
  ALTER COLUMN revision_key SET NOT NULL;

CREATE UNIQUE INDEX idx_skill_source_revisions_key
  ON skill_source_revisions(source_id, revision_key);

ALTER TABLE skill_versions
  ADD COLUMN source_id UUID,
  ADD COLUMN source_revision_id UUID,
  ADD COLUMN package_hash VARCHAR(64);

UPDATE skill_versions
SET package_hash = content_hash;

WITH latest_revision AS (
  SELECT DISTINCT ON (skill_version_id)
    skill_version_id,
    id AS source_revision_id,
    source_id,
    package_hash
  FROM skill_source_revisions
  WHERE skill_version_id IS NOT NULL
  ORDER BY skill_version_id, created_at DESC, id DESC
)
UPDATE skill_versions AS version
SET
  source_id = latest_revision.source_id,
  source_revision_id = latest_revision.source_revision_id,
  package_hash = COALESCE(NULLIF(latest_revision.package_hash, ''), version.content_hash)
FROM latest_revision
WHERE version.id = latest_revision.skill_version_id;

DROP INDEX IF EXISTS idx_skill_versions_user_hash;
DROP INDEX IF EXISTS idx_skill_versions_global_hash;
DROP INDEX IF EXISTS idx_skill_versions_account_hash;

INSERT INTO skill_versions (
  account_id,
  user_id,
  key_id,
  source_id,
  source_revision_id,
  skill_name,
  version,
  content,
  content_hash,
  package_hash,
  agent_id,
  change_summary,
  eval_pass_rate,
  is_active,
  published_at
)
SELECT
  original.account_id,
  original.user_id,
  original.key_id,
  revision.source_id,
  revision.id,
  original.skill_name,
  original.version,
  original.content,
  original.content_hash,
  COALESCE(NULLIF(revision.package_hash, ''), original.content_hash),
  original.agent_id,
  original.change_summary,
  original.eval_pass_rate,
  false,
  revision.created_at
FROM skill_source_revisions AS revision
JOIN skill_versions AS original ON original.id = revision.skill_version_id
WHERE original.source_revision_id IS DISTINCT FROM revision.id;

UPDATE skill_source_revisions AS revision
SET skill_version_id = version.id
FROM skill_versions AS version
WHERE version.source_revision_id = revision.id
  AND revision.skill_version_id IS DISTINCT FROM version.id;

UPDATE skill_version_files AS file
SET version_id = version.id
FROM skill_versions AS version
WHERE version.source_revision_id = file.source_revision_id
  AND file.version_id IS DISTINCT FROM version.id;

ALTER TABLE skill_versions
  ALTER COLUMN package_hash SET NOT NULL,
  ADD CONSTRAINT skill_versions_source_id_fkey
    FOREIGN KEY (source_id) REFERENCES skill_sources(id) ON DELETE CASCADE,
  ADD CONSTRAINT skill_versions_source_revision_id_fkey
    FOREIGN KEY (source_revision_id) REFERENCES skill_source_revisions(id) ON DELETE RESTRICT;

ALTER TABLE skill_source_revisions
  ADD CONSTRAINT skill_source_revisions_id_version_key UNIQUE (id, skill_version_id);

ALTER TABLE skill_versions
  ADD CONSTRAINT skill_versions_id_revision_key UNIQUE (id, source_revision_id),
  ADD CONSTRAINT skill_versions_revision_pair_fkey
    FOREIGN KEY (source_revision_id, id)
    REFERENCES skill_source_revisions(id, skill_version_id)
    DEFERRABLE INITIALLY DEFERRED;

ALTER TABLE skill_source_revisions
  ADD CONSTRAINT skill_source_revisions_version_pair_fkey
    FOREIGN KEY (skill_version_id, id)
    REFERENCES skill_versions(id, source_revision_id)
    DEFERRABLE INITIALLY DEFERRED;

DROP INDEX IF EXISTS idx_skill_versions_active;

CREATE INDEX idx_skill_versions_account_content
  ON skill_versions(account_id, skill_name, content_hash);

CREATE UNIQUE INDEX idx_skill_versions_account_source_package
  ON skill_versions(account_id, source_id, skill_name, package_hash)
  WHERE account_id IS NOT NULL AND source_id IS NOT NULL;

CREATE UNIQUE INDEX idx_skill_versions_account_direct_package
  ON skill_versions(account_id, skill_name, package_hash)
  WHERE account_id IS NOT NULL AND source_id IS NULL;

CREATE UNIQUE INDEX idx_skill_versions_global_package
  ON skill_versions(skill_name, package_hash)
  WHERE account_id IS NULL;

CREATE UNIQUE INDEX idx_skill_versions_source_revision
  ON skill_versions(source_revision_id)
  WHERE source_revision_id IS NOT NULL;

DROP INDEX IF EXISTS idx_skill_source_revisions_version;

CREATE UNIQUE INDEX idx_skill_source_revisions_version
  ON skill_source_revisions(skill_version_id)
  WHERE skill_version_id IS NOT NULL;

WITH ranked_active AS (
  SELECT
    id,
    ROW_NUMBER() OVER (
      PARTITION BY account_id, skill_name
      ORDER BY published_at DESC, id DESC
    ) AS active_rank
  FROM skill_versions
  WHERE is_active = true
)
UPDATE skill_versions AS version
SET is_active = false
FROM ranked_active
WHERE version.id = ranked_active.id
  AND ranked_active.active_rank > 1;

CREATE UNIQUE INDEX idx_skill_versions_account_active
  ON skill_versions(account_id, skill_name)
  WHERE account_id IS NOT NULL AND is_active = true;

CREATE UNIQUE INDEX idx_skill_versions_global_active
  ON skill_versions(skill_name)
  WHERE account_id IS NULL AND is_active = true;
