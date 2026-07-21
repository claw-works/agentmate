DROP TABLE IF EXISTS skill_compiled_catalogs;

ALTER TABLE skill_versions
  DROP CONSTRAINT IF EXISTS skill_versions_account_id_id_key;
