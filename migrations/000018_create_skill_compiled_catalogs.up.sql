ALTER TABLE skill_versions
  ADD CONSTRAINT skill_versions_account_id_id_key UNIQUE (account_id, id);

CREATE TABLE skill_compiled_catalogs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  skill_version_id UUID NOT NULL REFERENCES skill_versions(id) ON DELETE CASCADE,
  skill_name TEXT NOT NULL,
  compiler_name TEXT NOT NULL,
  compiler_version TEXT NOT NULL,
  input_package_hash VARCHAR(64) NOT NULL,
  description TEXT NOT NULL DEFAULT '',
  triggers JSONB NOT NULL DEFAULT '[]'::jsonb,
  capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
  constraints JSONB NOT NULL DEFAULT '[]'::jsonb,
  dependencies JSONB NOT NULL DEFAULT '[]'::jsonb,
  resource_manifest JSONB NOT NULL DEFAULT '[]'::jsonb,
  compiled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  CONSTRAINT skill_compiled_catalogs_account_version_key UNIQUE (account_id, skill_version_id),
  CONSTRAINT skill_compiled_catalogs_account_version_fkey
    FOREIGN KEY (account_id, skill_version_id)
    REFERENCES skill_versions(account_id, id)
    ON DELETE CASCADE,
  CONSTRAINT skill_compiled_catalogs_package_hash_check CHECK (input_package_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT skill_compiled_catalogs_triggers_array_check CHECK (jsonb_typeof(triggers) = 'array'),
  CONSTRAINT skill_compiled_catalogs_capabilities_array_check CHECK (jsonb_typeof(capabilities) = 'array'),
  CONSTRAINT skill_compiled_catalogs_constraints_array_check CHECK (jsonb_typeof(constraints) = 'array'),
  CONSTRAINT skill_compiled_catalogs_dependencies_array_check CHECK (jsonb_typeof(dependencies) = 'array'),
  CONSTRAINT skill_compiled_catalogs_manifest_array_check CHECK (jsonb_typeof(resource_manifest) = 'array'),
  CONSTRAINT skill_compiled_catalogs_skill_name_length_check CHECK (char_length(skill_name) BETWEEN 1 AND 100),
  CONSTRAINT skill_compiled_catalogs_description_length_check CHECK (char_length(description) <= 2000),
  CONSTRAINT skill_compiled_catalogs_routing_metadata_size_check CHECK (
    octet_length(triggers::text) + octet_length(capabilities::text) +
    octet_length(constraints::text) + octet_length(dependencies::text) <= 262144
  ),
  CONSTRAINT skill_compiled_catalogs_manifest_size_check CHECK (octet_length(resource_manifest::text) <= 1048576)
);

CREATE INDEX idx_skill_compiled_catalogs_skill_name
  ON skill_compiled_catalogs(account_id, skill_name);

-- Phase 3 indexes only compiled L0 cards. Existing skill retrieval documents may
-- contain the complete SKILL.md from the pre-compiler indexer. Replace that
-- derived content with a bounded basic card and mark the document for reindex.
-- status=failed keeps the safe PostgreSQL lexical fallback available while
-- excluding the stale Qdrant point from vector result hydration.
WITH safe_skill_documents AS (
  SELECT
    id,
    concat_ws(
      E'\n',
      'Skill: ' || COALESCE(NULLIF(metadata->>'skill_name', ''), source_id),
      CASE
        WHEN COALESCE(metadata->>'version', '') <> '' THEN 'Version: ' || (metadata->>'version')
        ELSE NULL
      END,
      CASE
        WHEN COALESCE(metadata->>'description', '') <> '' THEN 'Description: ' || left((metadata->>'description'), 2000)
        ELSE NULL
      END
    ) AS safe_content
  FROM retrieval_documents
  WHERE namespace = 'skills'
    AND source_type = 'skill_version'
)
UPDATE retrieval_documents AS document
SET
  content = safe.safe_content,
  content_hash = encode(sha256(convert_to(safe.safe_content, 'UTF8')), 'hex'),
  metadata = document.metadata || jsonb_build_object(
    'catalog_reindex_required', true,
    'description', left(COALESCE(document.metadata->>'description', ''), 2000),
    'change_summary', left(COALESCE(document.metadata->>'change_summary', ''), 2000)
  ),
  status = 'failed',
  error = 'skill catalog reindex required after compiler upgrade',
  indexed_at = NULL,
  updated_at = NOW()
FROM safe_skill_documents AS safe
WHERE document.id = safe.id;
