-- The retrieval rows in the knowledge_wiki namespace are not deleted here. They live in
-- retrieval_documents and are rebuildable from knowledge_pages, so dropping the pointer
-- leaves them orphaned but harmless; deleting tenant retrieval rows from a schema
-- rollback would destroy work this migration did not create.

ALTER TABLE knowledge_sources
  DROP CONSTRAINT IF EXISTS knowledge_sources_indexed_build_fkey;

ALTER TABLE knowledge_sources
  DROP COLUMN IF EXISTS wiki_indexed_at,
  DROP COLUMN IF EXISTS indexed_build_id;
