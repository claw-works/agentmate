package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/skills"
)

// fakeContractSource returns a fixed compiled contract, standing in for the skills domain.
type fakeContractSource struct {
	result *skills.CompiledContractResult
}

func (f fakeContractSource) CompiledContract(_ context.Context, _, _ string) (*skills.CompiledContractResult, error) {
	return f.result, nil
}

func discoveryContract(mode string) *skills.KnowledgeContract {
	return &skills.KnowledgeContract{
		Mode: mode,
		Requirements: []skills.KnowledgeRequirement{{
			ID:       "primary-domain",
			Required: true,
			Match:    skills.ContractMatch{Capabilities: []string{"factual-reference"}, Languages: []string{"zh-CN"}},
			Retrieval: skills.ContractRetrieval{
				MaxKnowledgeBases: 2, TopKPerBase: 4,
				Freshness: skills.FreshnessActive, Citations: skills.CitationsRequired,
			},
			Fallback: skills.ContractFallback{
				OnNoMatch: skills.FallbackAskUser, OnAmbiguous: skills.FallbackSearchMultiple,
			},
		}},
	}
}

// End to end against a real catalog: the manifest's declared capabilities/languages are what
// the contract matches, an undeclared collection stays invisible to discovery, and the K0
// card in the candidate carries the declarations so the ranking is explainable.
func TestDiscoverForSkillIntegration(t *testing.T) {
	ctx := context.Background()
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "discovery")
	defer cleanup()

	service := NewService(NewRepo(pool))
	declared := createIntegrationSource(t, ctx, service, owner, "discovery-declared-kb")
	undeclared := createIntegrationSource(t, ctx, service, owner, "discovery-undeclared-kb")

	if _, err := service.SubmitSnapshot(ctx, owner, declared.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: discovery-declared-kb\ndescription: declared fixture\ncapabilities:\n  - factual-reference\n  - entity-lookup\nlanguages:\n  - zh-CN\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/guide.md", Content: "# Declared\n"},
	}}); err != nil {
		t.Fatalf("ingest declared: %v", err)
	}
	if _, err := service.SubmitSnapshot(ctx, owner, undeclared.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: discovery-undeclared-kb\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/guide.md", Content: "# Undeclared\n"},
	}}); err != nil {
		t.Fatalf("ingest undeclared: %v", err)
	}

	contract := discoveryContract(skills.ContractModeDiscover)
	service.WithSkillContracts(fakeContractSource{result: &skills.CompiledContractResult{
		SkillVersionID:   "version-1",
		SkillName:        "grounded-answer",
		Version:          "v1",
		Contract:         contract,
		ContractIdentity: skills.ContractIdentity(contract),
	}})

	response, err := service.DiscoverForSkill(ctx, owner, DiscoverKnowledgeRequest{SkillVersionID: "version-1"})
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !response.HasContract || response.Mode != skills.ContractModeDiscover {
		t.Fatalf("response head = %+v", response)
	}
	if len(response.Requirements) != 1 {
		t.Fatalf("requirements = %d, want 1", len(response.Requirements))
	}
	requirement := response.Requirements[0]
	if requirement.Status != DiscoveryStatusMatched || len(requirement.Candidates) != 1 {
		t.Fatalf("requirement = %+v", requirement)
	}
	candidate := requirement.Candidates[0]
	if candidate.Card.Name != "discovery-declared-kb" {
		t.Fatalf("wrong candidate: %+v", candidate.Card)
	}
	if len(candidate.MatchedCapabilities) != 1 || candidate.MatchedCapabilities[0] != "factual-reference" {
		t.Fatalf("match explanation = %+v", candidate)
	}
	if len(candidate.Card.Capabilities) != 2 || len(candidate.Card.Languages) != 1 {
		t.Fatalf("K0 card lost its declarations: %+v", candidate.Card)
	}
	if response.Fingerprint == "" || response.CatalogSize != 2 {
		t.Fatalf("fingerprint/catalog = %q / %d", response.Fingerprint, response.CatalogSize)
	}

	// Same question, same world: the fingerprint must repeat.
	again, err := service.DiscoverForSkill(ctx, owner, DiscoverKnowledgeRequest{SkillVersionID: "version-1"})
	if err != nil {
		t.Fatalf("re-discover: %v", err)
	}
	if again.Fingerprint != response.Fingerprint {
		t.Fatal("fingerprint is not reproducible against an unchanged catalog")
	}

	// Unknown requirement filter is a caller error, not an empty result.
	if _, err := service.DiscoverForSkill(ctx, owner, DiscoverKnowledgeRequest{
		SkillVersionID: "version-1", RequirementID: "no-such-need",
	}); err == nil || !strings.Contains(err.Error(), "requirement not found") {
		t.Fatalf("unknown requirement error = %v", err)
	}

	// scoped_discover is refused, not silently widened.
	scoped := discoveryContract(skills.ContractModeScopedDiscover)
	scoped.Scope = &skills.ContractScope{ApprovedOnly: true}
	service.WithSkillContracts(fakeContractSource{result: &skills.CompiledContractResult{
		SkillVersionID: "version-2", SkillName: "grounded-answer", Version: "v2",
		Contract: scoped, ContractIdentity: skills.ContractIdentity(scoped),
	}})
	if _, err := service.DiscoverForSkill(ctx, owner, DiscoverKnowledgeRequest{SkillVersionID: "version-2"}); err != ErrScopedDiscoveryUnsupported {
		t.Fatalf("scoped_discover error = %v, want ErrScopedDiscoveryUnsupported", err)
	}

	// A contract-less Skill is an answer, not a failure.
	service.WithSkillContracts(fakeContractSource{result: &skills.CompiledContractResult{
		SkillVersionID: "version-3", SkillName: "plain-skill", Version: "v1",
	}})
	plain, err := service.DiscoverForSkill(ctx, owner, DiscoverKnowledgeRequest{SkillVersionID: "version-3"})
	if err != nil {
		t.Fatalf("contract-less discover: %v", err)
	}
	if plain.HasContract || len(plain.Requirements) != 0 || plain.Note == "" {
		t.Fatalf("contract-less response = %+v", plain)
	}
}
