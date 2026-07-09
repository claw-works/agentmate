DROP TABLE IF EXISTS skill_version_files;
DROP TABLE IF EXISTS skill_source_revisions;
DROP TABLE IF EXISTS skill_sources;

DROP INDEX IF EXISTS idx_skill_versions_global_hash;
DROP INDEX IF EXISTS idx_skill_versions_user_hash;

CREATE UNIQUE INDEX idx_skill_versions_hash ON skill_versions(skill_name, content_hash);
