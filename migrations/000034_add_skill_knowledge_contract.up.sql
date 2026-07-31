-- K4: persist the Skill knowledge contract on the compiled artifact.
--
-- The contract is compiled out of SKILL.md frontmatter and carried on
-- CompiledSkillCatalog so runtime discovery reads what was compiled into the
-- version, never what the file says now. Without these columns the contract
-- survives compilation only in memory: an artifact loaded back from this table
-- would report "no contract", and discovery driven by it would quietly treat a
-- knowledge-consulting Skill as one that needs nothing.
--
-- knowledge_contract is NULL for the common case — a Skill that consults no
-- knowledge — mirroring the nil pointer in Go. The identity string is the
-- ordering-normalised projection that answers "did discovery semantics change";
-- '' means no contract, matching ContractIdentity's "knowledge=none" only in
-- spirit: the column stores the identity of a present contract and stays empty
-- otherwise so a partial index on it stays small.

ALTER TABLE skill_compiled_catalogs
  ADD COLUMN knowledge_contract JSONB,
  ADD COLUMN knowledge_contract_identity TEXT NOT NULL DEFAULT '',
  ADD CONSTRAINT skill_compiled_catalogs_contract_object_check
    CHECK (knowledge_contract IS NULL OR jsonb_typeof(knowledge_contract) = 'object'),
  -- A contract is bounded at parse time (8 requirements, 16-item lists); 64 KiB
  -- of headroom keeps a corrupted writer from bloating the catalog row.
  ADD CONSTRAINT skill_compiled_catalogs_contract_size_check
    CHECK (knowledge_contract IS NULL OR octet_length(knowledge_contract::text) <= 65536),
  -- The two columns describe one fact and must move together: a contract with
  -- no identity cannot participate in version comparison, and an identity with
  -- no contract asserts semantics that do not exist.
  ADD CONSTRAINT skill_compiled_catalogs_contract_identity_pairing_check
    CHECK ((knowledge_contract IS NULL) = (knowledge_contract_identity = ''));

-- Artifacts compiled before this migration lose nothing real: the contract is
-- derived deterministically from the immutable version content, so
-- POST /api/skills/compile rebuilds it, and the read path falls back to parsing
-- the stored content when the artifact predates this column.
