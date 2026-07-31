DROP TABLE IF EXISTS knowledge_review_findings;
ALTER TABLE knowledge_build_revisions
  DROP COLUMN IF EXISTS review_pages_examined,
  DROP COLUMN IF EXISTS review_pages_total,
  DROP COLUMN IF EXISTS review_note;
-- 'partial' cannot be expressed once the old constraint is back, and PostgreSQL validates
-- existing rows when a CHECK is added: without this, a single partial review makes the
-- rollback fail. They become 'skipped' rather than 'clean', because after the downgrade
-- there is no way to say "examined some pages" and claiming clean would be a statement
-- about pages nobody read.
UPDATE knowledge_build_revisions SET review_status = 'skipped' WHERE review_status = 'partial';
ALTER TABLE knowledge_build_revisions
  DROP CONSTRAINT IF EXISTS knowledge_build_revisions_review_status_check;
ALTER TABLE knowledge_build_revisions
  ADD CONSTRAINT knowledge_build_revisions_review_status_check
  CHECK (review_status IN ('skipped', 'clean', 'flagged', 'failed'));
