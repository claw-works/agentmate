CREATE TABLE memory_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  scope_type VARCHAR(40) NOT NULL DEFAULT 'global',
  scope_key TEXT NOT NULL DEFAULT '',
  session_id TEXT NOT NULL DEFAULT '',
  sequence_no BIGINT,
  event_type VARCHAR(40) NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,

  source_type VARCHAR(80) NOT NULL DEFAULT '',
  source_id TEXT NOT NULL DEFAULT '',
  occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  idempotency_key TEXT NOT NULL,
  content_hash VARCHAR(64) NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT memory_events_idempotency_unique UNIQUE (account_id, idempotency_key),
  CONSTRAINT memory_events_idempotency_key_not_empty CHECK (btrim(idempotency_key) <> ''),
  CONSTRAINT memory_events_sequence_no_valid CHECK (sequence_no IS NULL OR sequence_no >= 0),
  CONSTRAINT memory_events_type_valid CHECK (
    event_type IN (
      'goal', 'observation', 'action', 'decision', 'issue',
      'attempt', 'outcome', 'correction', 'checkpoint', 'note'
    )
  )
);

CREATE UNIQUE INDEX idx_memory_events_session_sequence
  ON memory_events(account_id, session_id, sequence_no)
  WHERE session_id <> '' AND sequence_no IS NOT NULL;
CREATE INDEX idx_memory_events_account_scope
  ON memory_events(account_id, scope_type, scope_key, occurred_at DESC);
CREATE INDEX idx_memory_events_account_session
  ON memory_events(account_id, session_id, occurred_at DESC)
  WHERE session_id <> '';
CREATE INDEX idx_memory_events_source
  ON memory_events(account_id, source_type, source_id)
  WHERE source_type <> '' AND source_id <> '';
CREATE INDEX idx_memory_events_payload ON memory_events USING GIN(payload);
CREATE UNIQUE INDEX idx_memory_events_account_id_unique ON memory_events(account_id, id);

ALTER TABLE memory_entries
  ADD COLUMN valid_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ADD COLUMN valid_to TIMESTAMPTZ,
  ADD COLUMN superseded_by UUID,
  ADD COLUMN source_event_id UUID,
  ADD COLUMN extraction_method VARCHAR(30) NOT NULL DEFAULT 'explicit',
  ADD COLUMN extractor_version TEXT NOT NULL DEFAULT '',
  ADD COLUMN access_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN useful_count BIGINT NOT NULL DEFAULT 0,
  ADD COLUMN harmful_count BIGINT NOT NULL DEFAULT 0,
  ADD CONSTRAINT memory_entries_validity_valid CHECK (valid_to IS NULL OR valid_to >= valid_from),
  ADD CONSTRAINT memory_entries_extraction_method_valid CHECK (
    extraction_method IN ('explicit', 'rule', 'llm', 'dream')
  ),
  ADD CONSTRAINT memory_entries_counters_valid CHECK (
    access_count >= 0 AND useful_count >= 0 AND harmful_count >= 0
  ),
  ADD CONSTRAINT memory_entries_account_source_event_fkey
    FOREIGN KEY (account_id, source_event_id)
    REFERENCES memory_events(account_id, id) ON DELETE SET NULL (source_event_id);

CREATE UNIQUE INDEX idx_memory_entries_account_id_unique ON memory_entries(account_id, id);
ALTER TABLE memory_entries
  ADD CONSTRAINT memory_entries_account_superseded_by_fkey
  FOREIGN KEY (account_id, superseded_by)
  REFERENCES memory_entries(account_id, id) ON DELETE SET NULL (superseded_by);
CREATE INDEX idx_memory_entries_account_validity
  ON memory_entries(account_id, status, valid_from DESC, valid_to);
CREATE INDEX idx_memory_entries_superseded_by
  ON memory_entries(superseded_by)
  WHERE superseded_by IS NOT NULL;
CREATE INDEX idx_memory_entries_source_event
  ON memory_entries(source_event_id)
  WHERE source_event_id IS NOT NULL;

ALTER TABLE memory_evidence ADD COLUMN user_id UUID REFERENCES users(id) ON DELETE CASCADE;
UPDATE memory_evidence e
SET user_id = m.user_id
FROM memory_entries m
WHERE e.memory_id = m.id;
ALTER TABLE memory_evidence
  DROP CONSTRAINT memory_evidence_account_id_fkey,
  ADD CONSTRAINT memory_evidence_account_id_fkey
    FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE memory_evidence ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE memory_evidence ALTER COLUMN account_id SET NOT NULL;
ALTER TABLE memory_evidence
  ADD CONSTRAINT memory_evidence_account_memory_fkey
  FOREIGN KEY (account_id, memory_id)
  REFERENCES memory_entries(account_id, id) ON DELETE CASCADE;
CREATE INDEX idx_memory_evidence_user ON memory_evidence(user_id, created_at DESC);

CREATE TABLE memory_relations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,
  from_memory_id UUID NOT NULL,
  to_memory_id UUID NOT NULL,
  relation_type VARCHAR(40) NOT NULL,
  weight DOUBLE PRECISION NOT NULL DEFAULT 1,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT memory_relations_unique
    UNIQUE (account_id, from_memory_id, to_memory_id, relation_type),
  CONSTRAINT memory_relations_account_from_fkey
    FOREIGN KEY (account_id, from_memory_id)
    REFERENCES memory_entries(account_id, id) ON DELETE CASCADE,
  CONSTRAINT memory_relations_account_to_fkey
    FOREIGN KEY (account_id, to_memory_id)
    REFERENCES memory_entries(account_id, id) ON DELETE CASCADE,
  CONSTRAINT memory_relations_not_self CHECK (from_memory_id <> to_memory_id),
  CONSTRAINT memory_relations_weight_valid CHECK (weight >= 0 AND weight <= 1),
  CONSTRAINT memory_relations_type_valid CHECK (
    relation_type IN (
      'related_to', 'depends_on', 'caused_by', 'contradicts',
      'supersedes', 'supports', 'derived_from'
    )
  )
);

CREATE INDEX idx_memory_relations_from
  ON memory_relations(account_id, from_memory_id, relation_type);
CREATE INDEX idx_memory_relations_to
  ON memory_relations(account_id, to_memory_id, relation_type);
CREATE INDEX idx_memory_relations_metadata ON memory_relations USING GIN(metadata);
