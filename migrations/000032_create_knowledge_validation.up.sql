-- K3.9 (part 1): validation signals and attribution.
--
-- §7.3 defines validation as the implicit confirmation carried by behaviour: nobody fills in
-- a rating, the use itself is the verdict. That framing assumed a human at a UI. Here the
-- consumer is an agent calling MCP tools, and that changes what is observable.
--
-- An agent adopting an answer cannot be watched. It has to say so. Self-reported signals
-- therefore carry the reporting bias the design already warns about — an agent that never
-- reports looks exactly like one that had no problems. So the origin of every signal is
-- recorded: 'reported' came from a caller, 'derived' was computed from data the platform
-- already had. Only derived signals are free of that bias, and mixing the two without
-- saying which is which would make the whole series unreadable.

CREATE TABLE knowledge_validation_signals (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  source_id UUID NOT NULL,
  -- The build serving when the signal happened. Signals outlive builds, and a complaint
  -- about a wiki that has since been recompiled must not be read as a complaint about the
  -- current one.
  build_id UUID,
  -- Empty when the signal is about a source rather than a page, which is the case for
  -- never_retrieved.
  page_path TEXT NOT NULL DEFAULT '',
  -- The retrieval query this signal followed, when there was one. It is what makes
  -- attribution possible at all: the same complaint means different things depending on
  -- whether anything was returned.
  query_id UUID,

  signal VARCHAR(32) NOT NULL,
  direction VARCHAR(8) NOT NULL,
  origin VARCHAR(8) NOT NULL,

  -- Attribution is stored, not recomputed on read: it depends on the state of the raw and
  -- wiki layers at the time, and both move.
  cause VARCHAR(24) NOT NULL DEFAULT 'unattributed',
  -- Which rule produced the cause, in words. §7.4's warning is that a signal you cannot
  -- attribute leaves you with "this account seems unhappy", which has no action attached;
  -- an attribution nobody can audit is the same thing wearing a label.
  attribution_basis TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_validation_direction_check
    CHECK (direction IN ('positive', 'negative')),
  CONSTRAINT knowledge_validation_origin_check
    CHECK (origin IN ('reported', 'derived')),
  CONSTRAINT knowledge_validation_cause_check
    CHECK (cause IN ('unattributed', 'wiki_synthesis', 'retrieval_miss', 'source_gap', 'skill_approach')),
  FOREIGN KEY (source_id, account_id)
    REFERENCES knowledge_sources(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE SET NULL
);

CREATE INDEX idx_knowledge_validation_source
  ON knowledge_validation_signals(account_id, source_id, created_at DESC);
CREATE INDEX idx_knowledge_validation_page
  ON knowledge_validation_signals(account_id, source_id, page_path, direction)
  WHERE page_path <> '';
CREATE INDEX idx_knowledge_validation_cause
  ON knowledge_validation_signals(account_id, cause, created_at DESC)
  WHERE direction = 'negative';

-- One derived signal per source per day at most. never_retrieved is computed by sweeping,
-- and a sweep that runs hourly would otherwise manufacture a rising trend out of one
-- unchanged fact.
CREATE UNIQUE INDEX idx_knowledge_validation_derived_daily
  ON knowledge_validation_signals(
       account_id, source_id, signal, ((created_at AT TIME ZONE 'UTC')::date))
  WHERE origin = 'derived';
