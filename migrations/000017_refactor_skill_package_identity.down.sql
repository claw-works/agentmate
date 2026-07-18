DROP TABLE IF EXISTS skill_source_revision_aliases;

ALTER TABLE skill_versions
  DROP CONSTRAINT IF EXISTS skill_versions_revision_pair_fkey;

ALTER TABLE skill_source_revisions
  DROP CONSTRAINT IF EXISTS skill_source_revisions_version_pair_fkey;

ALTER TABLE skill_versions
  DROP CONSTRAINT IF EXISTS skill_versions_id_revision_key;

ALTER TABLE skill_source_revisions
  DROP CONSTRAINT IF EXISTS skill_source_revisions_id_version_key;

DROP INDEX IF EXISTS idx_skill_versions_global_active;
DROP INDEX IF EXISTS idx_skill_versions_account_active;
DROP INDEX IF EXISTS idx_skill_versions_source_revision;
DROP INDEX IF EXISTS idx_skill_source_revisions_version;
CREATE INDEX idx_skill_source_revisions_version ON skill_source_revisions(skill_version_id);
DROP INDEX IF EXISTS idx_skill_versions_global_package;
DROP INDEX IF EXISTS idx_skill_versions_account_direct_package;
DROP INDEX IF EXISTS idx_skill_versions_account_source_package;
DROP INDEX IF EXISTS idx_skill_versions_account_content;

CREATE TEMP TABLE skill_version_rollback_map AS
WITH ranked_versions AS (
  SELECT
    id,
    FIRST_VALUE(id) OVER (
      PARTITION BY
        COALESCE(account_id::text, 'user:' || COALESCE(user_id::text, 'global')),
        skill_name,
        content_hash
      ORDER BY is_active DESC, published_at DESC, id DESC
    ) AS winner_id,
    ROW_NUMBER() OVER (
      PARTITION BY
        COALESCE(account_id::text, 'user:' || COALESCE(user_id::text, 'global')),
        skill_name,
        content_hash
      ORDER BY is_active DESC, published_at DESC, id DESC
    ) AS duplicate_rank
  FROM skill_versions
)
SELECT id AS duplicate_id, winner_id
FROM ranked_versions
WHERE duplicate_rank > 1;

UPDATE skill_source_revisions AS revision
SET skill_version_id = rollback.winner_id
FROM skill_version_rollback_map AS rollback
WHERE revision.skill_version_id = rollback.duplicate_id;

UPDATE skill_version_files AS file
SET version_id = rollback.winner_id
FROM skill_version_rollback_map AS rollback
WHERE file.version_id = rollback.duplicate_id;

DELETE FROM skill_versions AS version
USING skill_version_rollback_map AS rollback
WHERE version.id = rollback.duplicate_id;

DROP TABLE skill_version_rollback_map;

ALTER TABLE skill_versions
  DROP CONSTRAINT IF EXISTS skill_versions_source_revision_id_fkey,
  DROP CONSTRAINT IF EXISTS skill_versions_source_id_fkey,
  DROP COLUMN IF EXISTS source_revision_id,
  DROP COLUMN IF EXISTS source_id,
  DROP COLUMN IF EXISTS package_hash;

CREATE UNIQUE INDEX idx_skill_versions_user_hash
  ON skill_versions(user_id, skill_name, content_hash)
  WHERE user_id IS NOT NULL;

CREATE UNIQUE INDEX idx_skill_versions_global_hash
  ON skill_versions(skill_name, content_hash)
  WHERE user_id IS NULL;

CREATE UNIQUE INDEX idx_skill_versions_account_hash
  ON skill_versions(account_id, skill_name, content_hash)
  WHERE account_id IS NOT NULL;

CREATE INDEX idx_skill_versions_active
  ON skill_versions(skill_name, is_active)
  WHERE is_active = true;

DROP INDEX IF EXISTS idx_skill_source_revisions_key;

ALTER TABLE skill_source_revisions
  DROP COLUMN IF EXISTS revision_key;
