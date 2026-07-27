-- CJK-capable lexical projection for hybrid retrieval.
--
-- The 'simple' text search configuration does not segment CJK script, so a
-- whole Chinese sentence became one token: query tokens never matched indexed
-- tokens and the lexical leg of hybrid search returned nothing for CJK text.
-- Hybrid retrieval silently degraded to semantic-only, which also made the
-- documented "fall back to lexical when embedding fails" behaviour untrue for
-- CJK corpora.
--
-- lexical_text holds an overlapping-character-bigram projection of title and
-- content (CJK runs become bigrams, ASCII runs stay whole lowercased words).
-- It is a derived column, always recomputable from title/content by
-- retrieval.LexicalProjection in Go, which is also what the query side uses.
-- Keeping one implementation in Go is deliberate: an SQL reimplementation
-- would drift from the query side and break matching silently.
--
-- Existing rows are backfilled to '' and repaired by the lexical rebuild
-- endpoint, which recomputes the projection without re-embedding. Rows with an
-- empty projection simply do not match lexically, exactly as before.

ALTER TABLE retrieval_documents
  ADD COLUMN lexical_text TEXT NOT NULL DEFAULT '';

DROP INDEX IF EXISTS idx_retrieval_documents_search;

CREATE INDEX idx_retrieval_documents_search ON retrieval_documents USING GIN(
  to_tsvector('simple', lexical_text)
);
