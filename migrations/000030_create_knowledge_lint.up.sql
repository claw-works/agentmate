-- K3.7: wiki lint.
--
-- lint is not a second check. check is a gate: deterministic invariants that block
-- activation, evaluated on a build nobody has seen yet. lint runs on a wiki that is
-- already serving, is read-only, and blocks nothing — it reports what is worth a human's
-- attention. Conflating the two would either turn advisory signals into activation
-- failures, or dilute the gate into a list of suggestions.
--
-- Every rule here is a PostgreSQL query, including the cascade, which needs a recursive
-- CTE. That is deliberate: architecture §14 rejected a graph database on the grounds that
-- KB lint is the only genuine graph workload and recursive CTEs are enough for it. This
-- table is where that claim is either honoured or refuted.

CREATE TABLE knowledge_lint_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  key_id UUID REFERENCES api_keys(id) ON DELETE SET NULL,

  source_id UUID NOT NULL,
  -- The build that was linted. Recorded rather than inferred from the source's current
  -- pointer, because the pointer moves: findings describe one specific wiki, and a run
  -- whose subject cannot be identified afterwards is not evidence of anything.
  build_id UUID NOT NULL,
  -- The source revision the build was compared against. Staleness is a relation between
  -- a build and the raw sources as they are *now*, so the same build linted before and
  -- after a sync legitimately yields different findings.
  revision_id UUID NOT NULL,

  pages_examined INTEGER NOT NULL DEFAULT 0,
  findings_total INTEGER NOT NULL DEFAULT 0,
  findings_warning INTEGER NOT NULL DEFAULT 0,
  findings_info INTEGER NOT NULL DEFAULT 0,

  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  finished_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  FOREIGN KEY (source_id, account_id)
    REFERENCES knowledge_sources(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE CASCADE,
  FOREIGN KEY (revision_id, account_id)
    REFERENCES knowledge_source_revisions(id, account_id) ON DELETE CASCADE,
  UNIQUE (id, account_id)
);

CREATE INDEX idx_knowledge_lint_runs_source
  ON knowledge_lint_runs(account_id, source_id, created_at DESC);

CREATE TABLE knowledge_lint_findings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  run_id UUID NOT NULL,

  rule VARCHAR(64) NOT NULL,
  -- warning is worth acting on; info is worth knowing. There is no error level, because
  -- lint cannot fail a wiki — a rule that could would belong in check instead.
  severity VARCHAR(16) NOT NULL DEFAULT 'warning',

  page_path TEXT NOT NULL DEFAULT '',
  -- related_path is the other end of a relation: the link target, the superseding page,
  -- the uncited document. Findings about graphs are about pairs, and a finding naming only
  -- one side is not actionable.
  related_path TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_lint_findings_severity_check
    CHECK (severity IN ('warning', 'info')),
  FOREIGN KEY (run_id, account_id)
    REFERENCES knowledge_lint_runs(id, account_id) ON DELETE CASCADE
);

CREATE INDEX idx_knowledge_lint_findings_run
  ON knowledge_lint_findings(account_id, run_id, rule, page_path);
