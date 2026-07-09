CREATE TABLE retrieval_documents (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,

  namespace VARCHAR(50) NOT NULL,
  source_type VARCHAR(80) NOT NULL,
  source_id TEXT NOT NULL,
  chunk_key TEXT NOT NULL,

  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  content_hash VARCHAR(64) NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,

  qdrant_collection VARCHAR(120) NOT NULL,
  qdrant_point_id UUID NOT NULL DEFAULT gen_random_uuid(),
  vector_name VARCHAR(80) NOT NULL DEFAULT 'semantic',
  embedding_model VARCHAR(200) NOT NULL,
  embedding_dimension INT NOT NULL,

  status VARCHAR(20) NOT NULL DEFAULT 'pending',
  error TEXT NOT NULL DEFAULT '',
  indexed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  UNIQUE (user_id, namespace, source_type, source_id, chunk_key),
  UNIQUE (qdrant_collection, qdrant_point_id)
);

CREATE INDEX idx_retrieval_documents_user_namespace ON retrieval_documents(user_id, namespace, status);
CREATE INDEX idx_retrieval_documents_source ON retrieval_documents(user_id, source_type, source_id);
CREATE INDEX idx_retrieval_documents_metadata ON retrieval_documents USING GIN(metadata);
CREATE INDEX idx_retrieval_documents_search ON retrieval_documents USING GIN(
  to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))
);

CREATE TABLE retrieval_index_jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  namespace VARCHAR(50) NOT NULL,
  source_type VARCHAR(80) NOT NULL,
  source_id TEXT NOT NULL,
  status VARCHAR(20) NOT NULL DEFAULT 'queued',
  attempts INT NOT NULL DEFAULT 0,
  last_error TEXT NOT NULL DEFAULT '',
  scheduled_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  locked_at TIMESTAMPTZ,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_retrieval_index_jobs_ready ON retrieval_index_jobs(status, scheduled_at);
CREATE INDEX idx_retrieval_index_jobs_source ON retrieval_index_jobs(user_id, namespace, source_type, source_id);

CREATE TABLE retrieval_queries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  namespace VARCHAR(50) NOT NULL,
  query TEXT NOT NULL,
  query_hash VARCHAR(64) NOT NULL,
  top_k INT NOT NULL,
  candidate_count INT NOT NULL DEFAULT 0,
  selected_count INT NOT NULL DEFAULT 0,
  embedding_model VARCHAR(200) NOT NULL DEFAULT '',
  rerank_model VARCHAR(200) NOT NULL DEFAULT '',
  latency_ms INT NOT NULL DEFAULT 0,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_retrieval_queries_user_namespace ON retrieval_queries(user_id, namespace, created_at DESC);
CREATE INDEX idx_retrieval_queries_hash ON retrieval_queries(query_hash, created_at DESC);

CREATE TABLE retrieval_query_results (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  query_id UUID NOT NULL REFERENCES retrieval_queries(id) ON DELETE CASCADE,
  document_id UUID REFERENCES retrieval_documents(id) ON DELETE SET NULL,
  qdrant_point_id UUID,
  rank INT NOT NULL,
  score DOUBLE PRECISION NOT NULL DEFAULT 0,
  stage VARCHAR(30) NOT NULL DEFAULT 'vector',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_retrieval_query_results_query ON retrieval_query_results(query_id, rank);

CREATE TABLE retrieval_feedback (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  query_id UUID REFERENCES retrieval_queries(id) ON DELETE SET NULL,
  document_id UUID REFERENCES retrieval_documents(id) ON DELETE SET NULL,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  signal VARCHAR(30) NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_retrieval_feedback_document ON retrieval_feedback(document_id, signal, created_at DESC);
CREATE INDEX idx_retrieval_feedback_user ON retrieval_feedback(user_id, created_at DESC);

CREATE TABLE memory_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  scope_type VARCHAR(40) NOT NULL DEFAULT 'global',
  scope_key TEXT NOT NULL DEFAULT '',
  memory_type VARCHAR(40) NOT NULL,
  title TEXT NOT NULL DEFAULT '',
  content TEXT NOT NULL,
  summary TEXT NOT NULL DEFAULT '',
  content_hash VARCHAR(64) NOT NULL,
  confidence DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  importance DOUBLE PRECISION NOT NULL DEFAULT 0.5,
  status VARCHAR(20) NOT NULL DEFAULT 'pending',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  ttl_at TIMESTAMPTZ,
  last_accessed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_memory_entries_scope ON memory_entries(user_id, scope_type, scope_key, status);
CREATE INDEX idx_memory_entries_type ON memory_entries(user_id, memory_type, status);
CREATE INDEX idx_memory_entries_metadata ON memory_entries USING GIN(metadata);
CREATE INDEX idx_memory_entries_search ON memory_entries USING GIN(
  to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(summary, '') || ' ' || coalesce(content, ''))
);

CREATE TABLE memory_evidence (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  memory_id UUID NOT NULL REFERENCES memory_entries(id) ON DELETE CASCADE,
  source_type VARCHAR(80) NOT NULL,
  source_id TEXT NOT NULL,
  excerpt TEXT NOT NULL DEFAULT '',
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_memory_evidence_memory ON memory_evidence(memory_id, created_at DESC);
CREATE INDEX idx_memory_evidence_source ON memory_evidence(source_type, source_id);
