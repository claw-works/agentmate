-- The cancellations applied on the way up are not reversed: the builds they
-- closed were genuinely abandoned, and resurrecting them as queued would hand a
-- synchronous-era build to a queue that cannot finish it.

DROP INDEX IF EXISTS idx_knowledge_build_revisions_claimable;

ALTER TABLE knowledge_build_revisions
  DROP CONSTRAINT IF EXISTS knowledge_build_revisions_attempt_check;

ALTER TABLE knowledge_build_revisions
  DROP COLUMN IF EXISTS activate_on_success,
  DROP COLUMN IF EXISTS queued_at,
  DROP COLUMN IF EXISTS next_attempt_at,
  DROP COLUMN IF EXISTS max_attempts,
  DROP COLUMN IF EXISTS attempt,
  DROP COLUMN IF EXISTS heartbeat_at,
  DROP COLUMN IF EXISTS lease_expires_at,
  DROP COLUMN IF EXISTS lease_owner;
