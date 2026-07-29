-- Usefulness signals for durable memories.
--
-- memory_entries already had useful_count and harmful_count, but nothing ever
-- wrote them: the counters were a data model with no behaviour. Counters alone
-- are also not enough for the evolution loop — a bare number cannot be
-- attributed, audited or recomputed. This table is the durable signal log; the
-- counters on memory_entries become a denormalised projection of it, kept
-- because ranking cannot afford an aggregate query per search.
--
-- Attribution anchors mirror M1: session_id plus an optional skill_version_id.
-- Without them a signal only supports coarse trend statistics, since a negative
-- signal cannot be traced to the execution that produced it.
--
-- This is the memory-side counterpart of the KnowledgeValidationSignal design in
-- docs/knowledge-wiki-compiler-k3-v0.1.md; both feed proposals, never mutations.

-- Composite key so the feedback foreign key can be account-scoped, the same
-- pattern 000021 used for knowledge_documents. memory_entries.account_id is
-- already NOT NULL, so the composite reference is always checked.
ALTER TABLE memory_entries
  ADD CONSTRAINT memory_entries_id_account_key UNIQUE (id, account_id);

CREATE TABLE memory_feedback (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  memory_id UUID NOT NULL,
  signal VARCHAR(16) NOT NULL,
  reason TEXT NOT NULL DEFAULT '',

  -- Attribution. session_id is NOT NULL with a '' default rather than nullable
  -- so it can take part in the uniqueness rule below without tri-state logic.
  session_id TEXT NOT NULL DEFAULT '',
  skill_version_id UUID,

  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  -- When the signal happened, which is not necessarily when it was reported.
  observed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT memory_feedback_signal_check CHECK (signal IN ('useful', 'harmful')),
  CONSTRAINT memory_feedback_reason_check CHECK (char_length(reason) <= 2000),

  -- Account-scoped composite keys: a signal can never cross accounts.
  FOREIGN KEY (memory_id, account_id)
    REFERENCES memory_entries(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (skill_version_id, account_id)
    REFERENCES skill_versions(id, account_id) ON DELETE SET NULL,

  -- One signal of each kind per memory per session. A retrying agent must not be
  -- able to inflate a memory's standing by repeating the call; a genuinely new
  -- judgement belongs to a new session.
  UNIQUE (account_id, memory_id, session_id, signal)
);

CREATE INDEX idx_memory_feedback_memory ON memory_feedback(account_id, memory_id, observed_at DESC);
CREATE INDEX idx_memory_feedback_skill_version
  ON memory_feedback(account_id, skill_version_id, observed_at DESC)
  WHERE skill_version_id IS NOT NULL;
