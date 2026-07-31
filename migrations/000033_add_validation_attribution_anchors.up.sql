-- K3.9: the two attribution anchors validation signals were missing.
--
-- The architecture is explicit that ResolutionRun's association with skill_version_id and
-- session_id is not merely audit capability but a necessary condition for the evolution
-- loop: without them a validation signal cannot be located to a specific build, page or
-- citation, and all that remains is "this account seems unhappy". M1 (migration 000024)
-- already carries both on skill_logs, memory_events and memory_feedback. The knowledge
-- validation signals were introduced without them, which left one cause — skill_approach —
-- permanently unreachable and cut these signals off from the correlation M1 provides.
--
-- Nullable on purpose. A signal reported outside any skill execution is legitimate: a person
-- querying the wiki directly has no skill version. Requiring the anchors would push callers
-- into inventing values, and an invented anchor is worse than an absent one because it looks
-- like evidence.

ALTER TABLE knowledge_validation_signals
  ADD COLUMN session_id UUID,
  ADD COLUMN skill_version_id UUID;

-- The index that makes the aggregate question answerable: does one skill version accumulate
-- negatives across several different pages. One page failing points at the page; the same
-- skill failing across many pages points at the skill.
CREATE INDEX idx_knowledge_validation_skill
  ON knowledge_validation_signals(account_id, skill_version_id, direction, created_at DESC)
  WHERE skill_version_id IS NOT NULL;

CREATE INDEX idx_knowledge_validation_session
  ON knowledge_validation_signals(account_id, session_id, created_at DESC)
  WHERE session_id IS NOT NULL;
