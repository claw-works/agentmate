package skills

import (
	"strings"
	"testing"
	"time"
)

// ─── K4: the Skill knowledge contract ───
//
// Architecture §4's justification for this type is one sentence: a Skill that only says
// "find relevant knowledge" cannot be linted, authorised, budgeted or evaluated. These tests
// hold the parts that make the difference — that a malformed contract is refused rather than
// dropped, that platform ceilings cannot be widened from a Skill file, and that the contract
// takes part in identity only when discovery semantics actually change.

const contractSkill = `---
name: grounded-answer
description: Answer questions from knowledge bases.
triggers:
  - answer this
knowledge:
  mode: discover
  requirements:
    - id: primary-domain
      purpose: 找到与用户问题直接相关的领域知识
      required: true
      match:
        capabilities:
          - factual-reference
          - entity-lookup
        languages:
          - zh-CN
          - en-US
      retrieval:
        max_knowledge_bases: 3
        top_k_per_base: 8
        freshness: active
        citations: required
      fallback:
        on_no_match: ask_user
        on_ambiguous: search_multiple
---

# Grounded answer
`

// TestParseKnowledgeContractReadsNestedBlock: the frontmatter scanner is flat and its default
// branch skips a key with an empty value, so a nested block would have been dropped in
// silence. A contract that is present but invisible is worse than none, because the Skill then
// looks like one that needs no knowledge.
func TestParseKnowledgeContractReadsNestedBlock(t *testing.T) {
	contract, err := ParseKnowledgeContract(contractSkill)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if contract == nil {
		t.Fatalf("the contract was silently dropped")
	}
	if contract.Mode != ContractModeDiscover {
		t.Fatalf("mode: want discover, got %q", contract.Mode)
	}
	if len(contract.Requirements) != 1 {
		t.Fatalf("want one requirement, got %d", len(contract.Requirements))
	}
	requirement := contract.Requirements[0]
	if requirement.ID != "primary-domain" || !requirement.Required {
		t.Fatalf("requirement head wrong: %+v", requirement)
	}
	if len(requirement.Match.Capabilities) != 2 || requirement.Match.Capabilities[0] != "factual-reference" {
		t.Fatalf("capabilities lost: %+v", requirement.Match)
	}
	if len(requirement.Match.Languages) != 2 {
		t.Fatalf("languages lost: %+v", requirement.Match)
	}
	if requirement.Retrieval.MaxKnowledgeBases != 3 || requirement.Retrieval.TopKPerBase != 8 {
		t.Fatalf("retrieval budget lost: %+v", requirement.Retrieval)
	}
	if requirement.Fallback.OnNoMatch != FallbackAskUser || requirement.Fallback.OnAmbiguous != FallbackSearchMultiple {
		t.Fatalf("fallback lost: %+v", requirement.Fallback)
	}
	// The other frontmatter keys must survive: the block is lifted out, not consumed.
	metadata, err := parseSkillFrontmatter(contractSkill)
	if err != nil {
		t.Fatalf("frontmatter: %v", err)
	}
	if metadata.Name != "grounded-answer" || len(metadata.Triggers) != 1 {
		t.Fatalf("the contract block ate its neighbours: %+v", metadata)
	}
	if metadata.Knowledge == nil {
		t.Fatalf("frontmatter did not carry the contract through")
	}
}

// TestContractAbsentIsFineEmptyIsNot: most Skills consult no knowledge and must not be forced
// to declare an empty block. A block that is present with nothing under it is different: the
// author began a declaration and stopped, and reading that as "needs no knowledge" would hide
// unfinished work.
func TestContractAbsentIsFineEmptyIsNot(t *testing.T) {
	plain := "---\nname: plain\ndescription: No knowledge needed.\n---\n\n# Plain\n"
	contract, err := ParseKnowledgeContract(plain)
	if err != nil || contract != nil {
		t.Fatalf("a Skill with no contract must parse to nil without error, got %+v / %v", contract, err)
	}
	if err := ValidateKnowledgeContract(nil); err != nil {
		t.Fatalf("nil contract must validate: %v", err)
	}

	empty := "---\nname: half\ndescription: Started declaring.\nknowledge:\n---\n\n# Half\n"
	if _, err := ParseKnowledgeContract(empty); err == nil {
		t.Fatalf("an empty knowledge block must be rejected, not read as absent")
	}
}

func contractWith(t *testing.T, body string) *KnowledgeContract {
	t.Helper()
	contract, err := ParseKnowledgeContract("---\nname: x\ndescription: y\n" + body + "---\n\n# X\n")
	if err != nil {
		t.Fatalf("parse %q: %v", body, err)
	}
	return contract
}

// TestValidateRejectsUnexecutableContracts. Each case is a contract whose statements cannot
// all be true at once, or that asks for more than the platform will give. Refusal rather than
// clamping: a Skill silently given less than it declared leaves its author reasoning about a
// retrieval plan that never runs.
func TestValidateRejectsUnexecutableContracts(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "no requirements at all",
			body: "knowledge:\n  mode: discover\n  requirements: []\n",
			want: "no requirements",
		},
		{
			name: "unknown mode",
			body: "knowledge:\n  mode: whatever\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n",
			want: "not one of discover",
		},
		{
			name: "duplicate requirement id",
			body: "knowledge:\n  requirements:\n    - id: dup\n      match:\n        domains: [x]\n    - id: DUP\n      match:\n        domains: [y]\n",
			want: "more than once",
		},
		{
			name: "missing id",
			body: "knowledge:\n  requirements:\n    - purpose: nameless\n      match:\n        domains: [x]\n",
			want: "has no id",
		},
		{
			name: "discovery with nothing to match on",
			body: "knowledge:\n  requirements:\n    - id: a\n",
			want: "no match criteria",
		},
		{
			name: "asks for more bases than the platform allows",
			body: "knowledge:\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n      retrieval:\n        max_knowledge_bases: 40\n",
			want: "platform ceiling",
		},
		{
			name: "asks for more hits per base than allowed",
			body: "knowledge:\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n      retrieval:\n        top_k_per_base: 500\n",
			want: "platform ceiling",
		},
		{
			name: "required but proceeds without knowledge",
			body: "knowledge:\n  requirements:\n    - id: a\n      required: true\n      match:\n        domains: [x]\n      fallback:\n        on_no_match: proceed_without_knowledge\n",
			want: "required but falls back",
		},
		{
			name: "search_multiple when nothing matched",
			body: "knowledge:\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n      fallback:\n        on_no_match: search_multiple\n",
			want: "nothing to search",
		},
		{
			name: "proceed when several matched",
			body: "knowledge:\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n      fallback:\n        on_ambiguous: proceed_without_knowledge\n",
			want: "discards what was found",
		},
		{
			name: "unknown freshness",
			body: "knowledge:\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n      retrieval:\n        freshness: yesterday\n",
			want: "not active or any",
		},
		{
			name: "scope without scoped_discover",
			body: "knowledge:\n  mode: discover\n  scope:\n    tags: [approved]\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n",
			want: "scope applies to scoped_discover",
		},
		{
			name: "scoped_discover that narrows nothing",
			body: "knowledge:\n  mode: scoped_discover\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n",
			want: "requires a scope",
		},
		{
			name: "pinned without a base",
			body: "knowledge:\n  mode: pinned\n  requirements:\n    - id: a\n",
			want: "at least one knowledge base",
		},
		{
			name: "pinning outside pinned mode",
			body: "knowledge:\n  mode: discover\n  requirements:\n    - id: a\n      match:\n        domains: [x]\n      pinned:\n        - source_name: kb\n",
			want: "pinning belongs to mode pinned",
		},
		{
			name: "pinned entry naming nothing",
			body: "knowledge:\n  mode: pinned\n  requirements:\n    - id: a\n      pinned:\n        - build_id: b\n",
			want: "neither source_name nor source_id",
		},
	}
	for _, testCase := range cases {
		contract := contractWith(t, testCase.body)
		err := ValidateKnowledgeContract(contract)
		if err == nil {
			t.Errorf("%s: expected refusal, got a valid contract", testCase.name)
			continue
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Errorf("%s: error %q does not mention %q", testCase.name, err, testCase.want)
		}
	}
}

// TestContractDefaultsAreConservative: an omitted budget means the author did not think about
// breadth, and defaulting to the ceiling would spend the widest possible budget on their
// behalf.
func TestContractDefaultsAreConservative(t *testing.T) {
	contract := contractWith(t, "knowledge:\n  requirements:\n    - id: a\n      match:\n        domains: [platform]\n")
	if err := ValidateKnowledgeContract(contract); err != nil {
		t.Fatalf("a minimal contract should be valid: %v", err)
	}
	requirement := contract.Requirements[0]
	if requirement.Retrieval.MaxKnowledgeBases != defaultContractKnowledgeBases {
		t.Fatalf("default bases: want %d, got %d", defaultContractKnowledgeBases, requirement.Retrieval.MaxKnowledgeBases)
	}
	if requirement.Retrieval.MaxKnowledgeBases >= maxContractKnowledgeBases {
		t.Fatalf("the default must not be the ceiling")
	}
	if requirement.Retrieval.Freshness != FreshnessActive {
		t.Fatalf("default freshness must be active, got %q", requirement.Retrieval.Freshness)
	}
	if requirement.Retrieval.Citations != CitationsRequired {
		t.Fatalf("default citations must be required, got %q", requirement.Retrieval.Citations)
	}
	// An optional need carries on; a required one stops and says so. Defaulting a required
	// need to "proceed" would let a Skill answer from nothing while claiming it needs sources.
	if requirement.Fallback.OnNoMatch != FallbackProceed {
		t.Fatalf("optional need should default to proceeding, got %q", requirement.Fallback.OnNoMatch)
	}
	required := contractWith(t, "knowledge:\n  requirements:\n    - id: a\n      required: true\n      match:\n        domains: [platform]\n")
	if required.Requirements[0].Fallback.OnNoMatch != FallbackAskUser {
		t.Fatalf("required need should default to asking, got %q", required.Requirements[0].Fallback.OnNoMatch)
	}
	if contract.Mode != ContractModeDiscover {
		t.Fatalf("omitted mode should default to discover, got %q", contract.Mode)
	}
}

// TestContractIdentityTracksSemanticsNotWording. The contract belongs to Skill identity
// because a Skill that starts consulting three bases instead of one behaves differently.
// But identity must not move for a reworded purpose, or every typo fix mints a version whose
// behaviour is identical — and then a real behaviour change looks like more of the same.
func TestContractIdentityTracksSemanticsNotWording(t *testing.T) {
	base := contractWith(t, "knowledge:\n  requirements:\n    - id: a\n      purpose: original wording\n      match:\n        capabilities: [factual-reference]\n      retrieval:\n        max_knowledge_bases: 2\n")
	reworded := contractWith(t, "knowledge:\n  requirements:\n    - id: a\n      purpose: completely different prose here\n      match:\n        capabilities: [factual-reference]\n      retrieval:\n        max_knowledge_bases: 2\n")
	if ContractIdentity(base) != ContractIdentity(reworded) {
		t.Fatalf("rewording the purpose changed discovery identity")
	}

	wider := contractWith(t, "knowledge:\n  requirements:\n    - id: a\n      purpose: original wording\n      match:\n        capabilities: [factual-reference]\n      retrieval:\n        max_knowledge_bases: 5\n")
	if ContractIdentity(base) == ContractIdentity(wider) {
		t.Fatalf("widening the budget must change discovery identity")
	}

	// Reordering lists or requirements is not a semantic change.
	first := contractWith(t, "knowledge:\n  requirements:\n    - id: a\n      match:\n        capabilities: [x, y]\n    - id: b\n      match:\n        domains: [d]\n")
	second := contractWith(t, "knowledge:\n  requirements:\n    - id: b\n      match:\n        domains: [d]\n    - id: a\n      match:\n        capabilities: [y, x]\n")
	if ContractIdentity(first) != ContractIdentity(second) {
		t.Fatalf("reordering changed identity:\n%s\n%s", ContractIdentity(first), ContractIdentity(second))
	}

	if ContractIdentity(nil) != "knowledge=none" {
		t.Fatalf("a Skill with no contract needs a stable identity too")
	}
}

// TestCompileRefusesMalformedContract: the compile is where a bad contract has to stop.
// Discovery, budgets and authorisation all read this block, so shipping an unparseable one
// would produce a Skill that looks like it needs no knowledge and then answers from nothing.
func TestCompileRefusesMalformedContract(t *testing.T) {
	version := SkillVersion{
		ID: "11111111-1111-1111-1111-111111111111", SkillName: "grounded-answer", Version: "1.0.0",
		PackageHash: strings.Repeat("a", 64),
		Content:     "---\nname: grounded-answer\ndescription: d\nknowledge:\n  mode: discover\n  requirements:\n    - id: a\n      required: true\n      match:\n        domains: [platform]\n      fallback:\n        on_no_match: proceed_without_knowledge\n---\n\n# X\n",
	}
	if _, err := CompileSkillVersion(version, nil, time.Now().UTC()); err == nil {
		t.Fatalf("a self-contradicting contract must fail the compile")
	} else if !strings.Contains(err.Error(), "knowledge contract") {
		t.Fatalf("the error must name the contract: %v", err)
	}

	version.Content = contractSkill
	catalog, err := CompileSkillVersion(version, nil, time.Now().UTC())
	if err != nil {
		t.Fatalf("a valid contract must compile: %v", err)
	}
	if catalog.KnowledgeContract == nil {
		t.Fatalf("the compiled catalog must carry the contract; discovery reads it from here, not from the file")
	}
	if catalog.KnowledgeContractIdentity == "" {
		t.Fatalf("the compiled catalog must carry the contract identity")
	}
	if !strings.Contains(catalog.KnowledgeContractIdentity, "mode=discover") {
		t.Fatalf("identity looks wrong: %q", catalog.KnowledgeContractIdentity)
	}
}
