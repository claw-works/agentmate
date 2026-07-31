package skills

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

const contractIntegrationSkill = `---
name: contract-skill
description: Contract persistence integration
triggers: [grounded question]
capabilities: [grounded answers]
knowledge:
  mode: discover
  requirements:
    - id: primary-domain
      purpose: Ground answers in domain knowledge
      required: true
      match:
        capabilities: [factual-reference]
        languages: [zh-CN]
      retrieval:
        max_knowledge_bases: 2
        top_k_per_base: 4
---

# Contract instructions
`

// The contract must survive the round trip through skill_compiled_catalogs: an artifact
// read back with a nil contract would make a knowledge-consulting Skill look like one that
// needs nothing, which is the exact failure the compile-time refusal exists to prevent.
// And when the artifact is missing entirely — or predates migration 000034 — the immutable
// version content is the fallback authority, yielding the same contract.
func TestKnowledgeContractPersistenceIntegration(t *testing.T) {
	databaseURL := os.Getenv("AGENTMATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("AGENTMATE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	defer pool.Close()

	owner := createSkillIntegrationOwner(t, ctx, pool, "contract-owner")
	defer deleteSkillIntegrationAccount(t, ctx, pool, owner.Account())

	repo := NewRepo(pool)
	service := NewService(repo)
	source, err := service.CreateSource(ctx, owner, CreateSkillSourceRequest{
		Name:          "contract-source",
		Type:          "local",
		RepositoryURL: "file:///contract-source",
		PackagePath:   "contract-skill",
	})
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	activate := true
	index := false
	snapshot, err := service.SubmitLocalSnapshot(ctx, owner, source.ID, SubmitLocalSnapshotRequest{
		SnapshotID: "contract-v1",
		Activate:   &activate,
		Index:      &index,
		Files:      []SnapshotFile{{Path: "SKILL.md", Content: contractIntegrationSkill}},
	})
	if err != nil {
		t.Fatalf("submit snapshot: %v", err)
	}
	versionID := snapshot.Version.ID
	if _, err := service.Compile(ctx, owner.Account(), versionID); err != nil {
		t.Fatalf("compile: %v", err)
	}

	assertContract := func(label string, result *CompiledContractResult) {
		t.Helper()
		if result.Contract == nil {
			t.Fatalf("%s: contract lost", label)
		}
		if result.Contract.Mode != ContractModeDiscover || len(result.Contract.Requirements) != 1 {
			t.Fatalf("%s: contract shape = %+v", label, result.Contract)
		}
		requirement := result.Contract.Requirements[0]
		if requirement.ID != "primary-domain" || requirement.Retrieval.MaxKnowledgeBases != 2 {
			t.Fatalf("%s: requirement = %+v", label, requirement)
		}
		if result.ContractIdentity != ContractIdentity(result.Contract) {
			t.Fatalf("%s: stored identity diverges from recomputation", label)
		}
	}

	// Round trip through the artifact table.
	artifact, err := repo.GetCompiledCatalog(ctx, owner.Account(), versionID)
	if err != nil {
		t.Fatalf("get artifact: %v", err)
	}
	if artifact.KnowledgeContract == nil || artifact.KnowledgeContractIdentity == "" {
		t.Fatalf("artifact did not persist the contract: %+v", artifact)
	}
	fromArtifact, err := service.CompiledContract(ctx, owner.Account(), versionID)
	if err != nil {
		t.Fatalf("compiled contract via artifact: %v", err)
	}
	assertContract("artifact path", fromArtifact)

	// Simulate a pre-000034 artifact: contract columns empty, artifact still present.
	// The read must fall back to the immutable content, not report "no contract".
	if _, err := pool.Exec(ctx,
		`UPDATE skill_compiled_catalogs SET knowledge_contract = NULL, knowledge_contract_identity = ''
		 WHERE account_id = $1 AND skill_version_id = $2`,
		owner.Account(), versionID,
	); err != nil {
		t.Fatalf("blank contract columns: %v", err)
	}
	fromFallback, err := service.CompiledContract(ctx, owner.Account(), versionID)
	if err != nil {
		t.Fatalf("compiled contract via fallback: %v", err)
	}
	assertContract("pre-migration fallback", fromFallback)
	if fromFallback.ContractIdentity != fromArtifact.ContractIdentity {
		t.Fatalf("fallback identity %q diverges from artifact identity %q",
			fromFallback.ContractIdentity, fromArtifact.ContractIdentity)
	}

	// Missing artifact entirely: same fallback, same answer.
	if _, err := pool.Exec(ctx,
		`DELETE FROM skill_compiled_catalogs WHERE account_id = $1 AND skill_version_id = $2`,
		owner.Account(), versionID,
	); err != nil {
		t.Fatalf("delete artifact: %v", err)
	}
	fromMissing, err := service.CompiledContract(ctx, owner.Account(), versionID)
	if err != nil {
		t.Fatalf("compiled contract with missing artifact: %v", err)
	}
	assertContract("missing artifact fallback", fromMissing)
}
