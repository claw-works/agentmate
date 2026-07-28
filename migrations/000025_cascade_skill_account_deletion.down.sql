ALTER TABLE skill_logs
  DROP CONSTRAINT skill_logs_account_id_fkey;
ALTER TABLE skill_logs
  ADD CONSTRAINT skill_logs_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE skill_versions
  DROP CONSTRAINT skill_versions_account_id_fkey;
ALTER TABLE skill_versions
  ADD CONSTRAINT skill_versions_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;
