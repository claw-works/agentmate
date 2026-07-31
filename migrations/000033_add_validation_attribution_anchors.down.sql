DROP INDEX IF EXISTS idx_knowledge_validation_session;
DROP INDEX IF EXISTS idx_knowledge_validation_skill;
ALTER TABLE knowledge_validation_signals
  DROP COLUMN IF EXISTS session_id,
  DROP COLUMN IF EXISTS skill_version_id;
