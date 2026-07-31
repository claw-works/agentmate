-- K4 (part 3): KnowledgeResolutionRun — runtime discovery frozen into execution evidence.
--
-- Architecture §3.3: the result of runtime discovery must be fixed, without preconfiguring
-- a Binding. A run records which knowledge bases an execution actually selected, what it
-- retrieved, and what it cited — the anchor for permission audit, reproduction, evaluation,
-- and the evolution loop's attribution. It is execution evidence, not mutable binding
-- configuration: rows are append-only and there is no update path.
--
-- What the server asserts versus what the client reports is drawn explicitly:
--   * contract_identity is filled by the server from the compiled contract, never accepted
--     from the client — a run claiming an identity the version never had is corrupt
--     evidence, and the server knows the real one.
--   * selected bases are verified against the account's sources at write time, because the
--     authorisation-audit value of this table depends on selections being real.
--   * candidates/retrieved/citations are bounded client-reported echoes of what the agent
--     saw and used. The discovery_fingerprint is what ties them back to a discovery the
--     server actually served; the catalog may have moved since, so they cannot be
--     re-verified against current state without destroying their meaning as history.

CREATE TABLE knowledge_resolution_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  skill_version_id UUID NOT NULL,
  -- Nullable session anchor, matching migration 000033's stance: a resolution outside any
  -- session is legitimate, and an invented anchor is worse than an absent one.
  session_id UUID,
  requirement_id TEXT NOT NULL,
  contract_identity TEXT NOT NULL,
  discovery_fingerprint VARCHAR(64) NOT NULL,
  discovery_status VARCHAR(32) NOT NULL,

  -- Candidate summaries the agent chose among, the bases it selected, and the evidence it
  -- pulled. selected may be empty: "discovery found nothing and the skill proceeded per
  -- its fallback" is exactly the kind of run this table exists to make visible.
  candidates JSONB NOT NULL DEFAULT '[]'::jsonb,
  selected JSONB NOT NULL DEFAULT '[]'::jsonb,
  retrieved JSONB NOT NULL DEFAULT '[]'::jsonb,
  citations JSONB NOT NULL DEFAULT '[]'::jsonb,
  selection_reason TEXT NOT NULL DEFAULT '',
  confidence DOUBLE PRECISION,

  -- Replay protection, mirroring memory_events: same key + same content returns the
  -- original row, same key + different content is a conflict. Evidence must not be
  -- double-counted by a retrying agent, nor silently replaced by a disagreeing one.
  idempotency_key TEXT,
  content_hash VARCHAR(64) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  -- Audit targets stay append-only (000019 doctrine): deleting a skill version referenced
  -- by a resolution run is rejected by the default NO ACTION. Account deletion cascades
  -- both sides in one statement, which NO ACTION permits.
  CONSTRAINT knowledge_resolution_runs_account_version_fkey
    FOREIGN KEY (account_id, skill_version_id)
    REFERENCES skill_versions(account_id, id),
  CONSTRAINT knowledge_resolution_runs_fingerprint_check
    CHECK (discovery_fingerprint ~ '^[0-9a-f]{64}$'),
  CONSTRAINT knowledge_resolution_runs_content_hash_check
    CHECK (content_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT knowledge_resolution_runs_status_check
    CHECK (discovery_status IN ('matched', 'ambiguous', 'no_metadata_match',
                                'no_authorized_knowledge', 'pinned_resolved', 'pinned_missing')),
  CONSTRAINT knowledge_resolution_runs_candidates_array_check
    CHECK (jsonb_typeof(candidates) = 'array'),
  CONSTRAINT knowledge_resolution_runs_selected_array_check
    CHECK (jsonb_typeof(selected) = 'array'),
  CONSTRAINT knowledge_resolution_runs_retrieved_array_check
    CHECK (jsonb_typeof(retrieved) = 'array'),
  CONSTRAINT knowledge_resolution_runs_citations_array_check
    CHECK (jsonb_typeof(citations) = 'array'),
  -- Evidence is references, never bodies; a run approaching this cap is storing content.
  CONSTRAINT knowledge_resolution_runs_evidence_size_check
    CHECK (octet_length(candidates::text) + octet_length(selected::text) +
           octet_length(retrieved::text) + octet_length(citations::text) <= 262144),
  CONSTRAINT knowledge_resolution_runs_reason_length_check
    CHECK (char_length(selection_reason) <= 2000),
  CONSTRAINT knowledge_resolution_runs_requirement_length_check
    CHECK (char_length(requirement_id) BETWEEN 1 AND 64),
  CONSTRAINT knowledge_resolution_runs_confidence_check
    CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))
);

CREATE INDEX idx_knowledge_resolution_runs_version
  ON knowledge_resolution_runs(account_id, skill_version_id, created_at DESC, id DESC);

CREATE INDEX idx_knowledge_resolution_runs_account
  ON knowledge_resolution_runs(account_id, created_at DESC, id DESC);

CREATE INDEX idx_knowledge_resolution_runs_session
  ON knowledge_resolution_runs(account_id, session_id, created_at DESC)
  WHERE session_id IS NOT NULL;

CREATE UNIQUE INDEX idx_knowledge_resolution_runs_idempotency
  ON knowledge_resolution_runs(account_id, idempotency_key)
  WHERE idempotency_key IS NOT NULL;
