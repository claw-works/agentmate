-- Rollback Knowledge Registry K2 link graph. Chunk rows in
-- retrieval_documents (namespace 'knowledge') are rebuildable derivations;
-- remove them so no K2 projection outlives its schema.
DELETE FROM retrieval_documents WHERE namespace = 'knowledge';

DROP TABLE IF EXISTS knowledge_document_links;

ALTER TABLE knowledge_documents
  DROP CONSTRAINT IF EXISTS knowledge_documents_id_account_key;
