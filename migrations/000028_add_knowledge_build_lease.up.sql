-- K3.3: asynchronous wiki compilation with leases.
--
-- Synchronous compilation was measured at 200-400 seconds per package against a
-- reasoning model, which is beyond any sane HTTP client default timeout. Raising
-- the timeout is not a fix: the caller still has to hold a connection open across
-- a multi-minute model call, and when it gives up the work is lost.
--
-- The lease lives on the build row rather than in a separate jobs table. A jobs
-- table would create two places that answer "is this build running", and the two
-- can disagree — after a crash, precisely when the answer matters most.

ALTER TABLE knowledge_build_revisions
  -- lease_owner identifies the worker holding the build. It is opaque on purpose:
  -- a hostname plus a process-lifetime nonce, so a restarted process on the same
  -- host never looks like the same owner and cannot inherit its own stale lease.
  ADD COLUMN lease_owner VARCHAR(128) NOT NULL DEFAULT '',
  -- lease_expires_at is the only thing that makes a crashed worker recoverable.
  -- Reclaiming on expiry rather than on a liveness check keeps recovery working
  -- when the worker is not crashed but partitioned, which looks identical from
  -- here and has the same consequence: nobody is making progress.
  ADD COLUMN lease_expires_at TIMESTAMPTZ,
  ADD COLUMN heartbeat_at TIMESTAMPTZ,

  -- attempt counts claims, not failures, so a worker that dies silently still
  -- burns an attempt. Otherwise a build that kills every worker it touches would
  -- be retried forever.
  ADD COLUMN attempt INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN max_attempts INTEGER NOT NULL DEFAULT 3,
  -- next_attempt_at implements backoff. A provider that just dropped a four
  -- minute connection is usually still unhappy a second later.
  ADD COLUMN next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ADD COLUMN queued_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  -- The request's activation intent has to survive until a worker runs, because
  -- the caller is long gone by then. Storing it on the job keeps "what was asked
  -- for" next to "what was done" instead of in a worker's memory.
  ADD COLUMN activate_on_success BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE knowledge_build_revisions
  ADD CONSTRAINT knowledge_build_revisions_attempt_check
    CHECK (attempt >= 0 AND max_attempts > 0);

-- The claim query: oldest eligible first, either never leased or with an expired
-- lease. Partial index because only queued and running rows are ever claimable
-- and terminal builds accumulate without bound.
CREATE INDEX idx_knowledge_build_revisions_claimable
  ON knowledge_build_revisions(next_attempt_at, queued_at)
  WHERE status IN ('queued', 'running');

-- Existing rows predate the queue. Builds that were left mid-flight by the
-- synchronous implementation can never be resumed — no worker owns them and no
-- lease will expire — so they are closed out honestly rather than becoming
-- immortal queue entries the moment a worker starts polling.
UPDATE knowledge_build_revisions
   SET status = 'cancelled',
       error = CASE WHEN error = '' THEN 'abandoned by the synchronous compiler' ELSE error END,
       finished_at = COALESCE(finished_at, NOW()),
       updated_at = NOW()
 WHERE status IN ('queued', 'running');
