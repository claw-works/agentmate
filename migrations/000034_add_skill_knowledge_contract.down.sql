ALTER TABLE skill_compiled_catalogs
  DROP CONSTRAINT IF EXISTS skill_compiled_catalogs_contract_identity_pairing_check,
  DROP CONSTRAINT IF EXISTS skill_compiled_catalogs_contract_size_check,
  DROP CONSTRAINT IF EXISTS skill_compiled_catalogs_contract_object_check,
  DROP COLUMN IF EXISTS knowledge_contract_identity,
  DROP COLUMN IF EXISTS knowledge_contract;
