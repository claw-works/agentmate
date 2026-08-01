-- 决策（#6）：api_logs / retrieval_queries / retrieval_feedback 的 account_id
-- 从 SET NULL 改为 CASCADE。
--
-- 这不是技术偏好，是"客户要求删除数据时我们实际删了什么"。三张表都存客户内容：
-- retrieval_queries 存查询原文（用户问了什么），retrieval_feedback 存对结果的判断，
-- api_logs 存请求元数据。SET NULL 之后这些行留在库里、失去归属，于是"账户已删除"
-- 与"这个人问过什么"同时成立——删除承诺没有兑现，而且再也无法按账户找回来删掉。
--
-- 先例已经在 migration 000025 立好了：skill_versions 与 skill_logs 当初也是
-- SET NULL，同样理由改成了 CASCADE（孤立行留着用户写的正文，没有归属可追溯、
-- 也没有归属可删除）。这三张表是当时漏掉的同一类。
--
-- 反方论点是"保留数据做聚合分析"。它不成立：聚合不需要可识别的行。要指标就预聚合
-- 成不带 account_id 的计数，而不是留一堆无主的明细行等着以后"也许有用"。
--
-- 与既有行的处理：已经存在的孤立行（account_id IS NULL）不动。它们诞生在这次改动
-- 之前，已经无法归属到任何账户，静默删掉无法归属的客户数据比留着更糟——留着至少
-- 是可清点的。用下面的查询清点：
--   SELECT count(*) FROM retrieval_queries WHERE account_id IS NULL;

ALTER TABLE api_logs
  DROP CONSTRAINT IF EXISTS api_logs_account_id_fkey;
ALTER TABLE api_logs
  ADD CONSTRAINT api_logs_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE retrieval_queries
  DROP CONSTRAINT IF EXISTS retrieval_queries_account_id_fkey;
ALTER TABLE retrieval_queries
  ADD CONSTRAINT retrieval_queries_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;

ALTER TABLE retrieval_feedback
  DROP CONSTRAINT IF EXISTS retrieval_feedback_account_id_fkey;
ALTER TABLE retrieval_feedback
  ADD CONSTRAINT retrieval_feedback_account_id_fkey
  FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE;
