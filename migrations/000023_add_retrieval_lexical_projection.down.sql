DROP INDEX IF EXISTS idx_retrieval_documents_search;

ALTER TABLE retrieval_documents DROP COLUMN IF EXISTS lexical_text;

CREATE INDEX idx_retrieval_documents_search ON retrieval_documents USING GIN(
  to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))
);
