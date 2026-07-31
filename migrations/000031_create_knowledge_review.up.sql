-- K3.8: review findings.
--
-- review answers one question per page: are its claims faithful to the sources it cites.
-- It never gates. check already decided whether the build may serve, and review's verdicts
-- are not reproducible — giving an irreproducible judgement blocking power would mean the
-- same build retried twice could land differently.
--
-- The findings live in their own table rather than in knowledge_build_events, because
-- review runs after the build is committed and its wiki/log.md page has been written. The
-- log is the compile transcript; appending review to it would require rewriting an
-- immutable page, and would blur when each thing was learned.

CREATE TABLE knowledge_review_findings (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  build_id UUID NOT NULL,

  page_path TEXT NOT NULL,
  -- kind names the failure mode, taken from what review is actually asked to look for:
  -- a claim the sources do not support, a hedge hardened into an absolute, a causal link
  -- the sources never draw, two sources' conclusions merged into one that neither states.
  kind VARCHAR(32) NOT NULL,
  -- The claim as it appears on the page. Quoted rather than referenced by offset: pages
  -- are immutable per build, but a finding that cannot be read on its own is a finding
  -- nobody acts on.
  claim TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  CONSTRAINT knowledge_review_findings_kind_check
    CHECK (kind IN ('unsupported', 'overstated', 'fabricated_causality', 'conflated')),
  FOREIGN KEY (build_id, account_id)
    REFERENCES knowledge_build_revisions(id, account_id) ON DELETE CASCADE
);

CREATE INDEX idx_knowledge_review_findings_build
  ON knowledge_review_findings(account_id, build_id, page_path);

-- Coverage has to be recorded, not inferred. Reviewing 20 of 143 pages and reporting
-- "clean" would claim far more than was checked, so both numbers are stored and the
-- status distinguishes the two cases.
ALTER TABLE knowledge_build_revisions
  ADD COLUMN review_pages_examined INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN review_pages_total INTEGER NOT NULL DEFAULT 0,
  -- Why review did not run, or did not finish: reviewer unconfigured, same model as the
  -- compiler, capped, provider failure. A status without a reason sends an operator to
  -- read the source.
  ADD COLUMN review_note TEXT NOT NULL DEFAULT '';

-- 'partial' is new: examined fewer pages than were eligible, and found nothing among
-- them. Calling that 'clean' would be a claim about pages nobody looked at.
ALTER TABLE knowledge_build_revisions
  DROP CONSTRAINT knowledge_build_revisions_review_status_check;
ALTER TABLE knowledge_build_revisions
  ADD CONSTRAINT knowledge_build_revisions_review_status_check
  CHECK (review_status IN ('skipped', 'clean', 'partial', 'flagged', 'failed'));
