-- 回退到 SET NULL。注意：回退不会恢复已被级联删除的行。
ALTER TABLE api_logs
  DROP CONSTRAINT IF EXISTS api_logs_account_id_fkey;
ALTER TABLE api_logs
  ADD CONSTRAINT api_logs_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE retrieval_queries
  DROP CONSTRAINT IF EXISTS retrieval_queries_account_id_fkey;
ALTER TABLE retrieval_queries
  ADD CONSTRAINT retrieval_queries_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE retrieval_feedback
  DROP CONSTRAINT IF EXISTS retrieval_feedback_account_id_fkey;
ALTER TABLE retrieval_feedback
  ADD CONSTRAINT retrieval_feedback_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE SET NULL;
