DROP TABLE IF EXISTS skill_quality_runs;

DROP INDEX IF EXISTS idx_skill_logs_account_version_cutoff;
ALTER TABLE skill_logs
  DROP CONSTRAINT IF EXISTS skill_logs_account_version_fkey;
ALTER TABLE skill_logs
  DROP COLUMN IF EXISTS skill_version_id;
