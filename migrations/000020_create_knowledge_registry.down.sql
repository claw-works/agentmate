-- Full rollback of Knowledge Registry K1.
ALTER TABLE knowledge_sources
  DROP CONSTRAINT IF EXISTS knowledge_sources_active_revision_fkey;

DROP TABLE IF EXISTS knowledge_documents;
DROP TABLE IF EXISTS knowledge_source_revisions;
DROP TABLE IF EXISTS knowledge_sources;
