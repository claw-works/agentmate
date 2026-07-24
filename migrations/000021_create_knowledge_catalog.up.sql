-- Knowledge Registry K2: document link graph (rebuildable derivation).
--
-- Links are parsed from Markdown package-internal relative references inside
-- indexable text documents of one immutable revision. They can always be
-- rebuilt from knowledge_documents, so this table stores no content bodies.
-- Chunks are NOT stored here: K2 chunks live as retrieval_documents rows in
-- the 'knowledge' namespace and are likewise rebuildable derivations.

-- Composite key on knowledge_documents so links can carry account-scoped FKs.
-- (id is already the primary key; the pair makes the account binding explicit.)
ALTER TABLE knowledge_documents
  ADD CONSTRAINT knowledge_documents_id_account_key UNIQUE (id, account_id);

CREATE TABLE knowledge_document_links (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  revision_id UUID NOT NULL,
  source_document_id UUID NOT NULL,
  -- Resolved same-revision target by path; NULL when the target path does
  -- not exist in the revision (dangling link keeps target_path only).
  target_document_id UUID,
  target_path TEXT NOT NULL,

  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_document_links_target_path_check
    CHECK (char_length(target_path) BETWEEN 1 AND 1024),

  -- Account-scoped composite FKs: a link can never point across accounts.
  FOREIGN KEY (revision_id, account_id)
    REFERENCES knowledge_source_revisions(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (source_document_id, account_id)
    REFERENCES knowledge_documents(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (target_document_id, account_id)
    REFERENCES knowledge_documents(id, account_id) ON DELETE CASCADE,

  UNIQUE (revision_id, source_document_id, target_path)
);

-- Both traversal directions: outgoing links of a document and incoming links
-- to a document.
CREATE INDEX idx_knowledge_document_links_out
  ON knowledge_document_links(account_id, source_document_id, target_path);
CREATE INDEX idx_knowledge_document_links_in
  ON knowledge_document_links(account_id, target_document_id)
  WHERE target_document_id IS NOT NULL;
CREATE INDEX idx_knowledge_document_links_revision
  ON knowledge_document_links(revision_id);
