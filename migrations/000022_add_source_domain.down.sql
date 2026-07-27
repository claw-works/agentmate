DROP INDEX IF EXISTS idx_knowledge_sources_domain;
DROP INDEX IF EXISTS idx_skill_sources_domain;

ALTER TABLE knowledge_sources DROP COLUMN IF EXISTS domain;
ALTER TABLE skill_sources DROP COLUMN IF EXISTS domain;
