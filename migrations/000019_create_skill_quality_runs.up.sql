ALTER TABLE skill_logs
  ADD COLUMN skill_version_id UUID;

ALTER TABLE skill_logs
  ADD CONSTRAINT skill_logs_account_version_fkey
  FOREIGN KEY (account_id, skill_version_id)
  REFERENCES skill_versions(account_id, id);

CREATE INDEX idx_skill_logs_account_version_cutoff
  ON skill_logs(account_id, skill_version_id, created_at DESC, id DESC)
  WHERE skill_version_id IS NOT NULL;

CREATE TABLE skill_quality_runs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  account_id UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  skill_version_id UUID NOT NULL,
  baseline_version_id UUID,
  engine_version TEXT NOT NULL,
  checkset_version TEXT NOT NULL,
  input_package_hash VARCHAR(64) NOT NULL,
  baseline_package_hash VARCHAR(64),
  telemetry_cutoff TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL,
  report JSONB NOT NULL,
  failure_message TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  completed_at TIMESTAMPTZ,
  CONSTRAINT skill_quality_runs_account_version_fkey
    FOREIGN KEY (account_id, skill_version_id)
    REFERENCES skill_versions(account_id, id),
  CONSTRAINT skill_quality_runs_account_baseline_fkey
    FOREIGN KEY (account_id, baseline_version_id)
    REFERENCES skill_versions(account_id, id)
    ON DELETE SET NULL (baseline_version_id),
  CONSTRAINT skill_quality_runs_status_check
    CHECK (status IN ('running', 'completed', 'failed')),
  CONSTRAINT skill_quality_runs_input_hash_check
    CHECK (input_package_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT skill_quality_runs_baseline_hash_check
    CHECK (baseline_package_hash IS NULL OR baseline_package_hash ~ '^[0-9a-f]{64}$'),
  CONSTRAINT skill_quality_runs_report_object_check
    CHECK (jsonb_typeof(report) = 'object'),
  CONSTRAINT skill_quality_runs_report_size_check
    CHECK (octet_length(report::text) <= 1048576),
  CONSTRAINT skill_quality_runs_completion_check
    CHECK (
      (status = 'running' AND completed_at IS NULL) OR
      (status IN ('completed', 'failed') AND completed_at IS NOT NULL)
    )
);

CREATE INDEX idx_skill_quality_runs_version_page
  ON skill_quality_runs(account_id, skill_version_id, created_at DESC, id DESC);

CREATE INDEX idx_skill_quality_runs_account_page
  ON skill_quality_runs(account_id, created_at DESC, id DESC);
