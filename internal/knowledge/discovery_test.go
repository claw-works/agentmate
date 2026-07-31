package knowledge

import (
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/skills"
)

// ─── K4: contract-driven discovery ───
//
// These tests hold the properties agents depend on: a declared criterion group is a
// constraint (AND across groups), one overlap inside a group suffices (OR within), ranking
// is deterministic, every failure keeps its own classification instead of flattening into
// an empty list, and the fingerprint identifies "same question, same world" regardless of
// catalog ordering.

func discoveryCard(name, domain string, capabilities, languages []string) KnowledgeCatalogItem {
	return KnowledgeCatalogItem{
		SourceID:     "src-" + strings.ToLower(name),
		Name:         name,
		Domain:       domain,
		Capabilities: capabilities,
		Languages:    languages,
		PackageHash:  strings.Repeat("a", 60) + "beef",
	}
}

func discoveryRequirement(capabilities, languages, domains []string, maxBases int) skills.KnowledgeRequirement {
	return skills.KnowledgeRequirement{
		ID:       "primary-domain",
		Required: true,
		Match:    skills.ContractMatch{Capabilities: capabilities, Languages: languages, Domains: domains},
		Retrieval: skills.ContractRetrieval{
			MaxKnowledgeBases: maxBases,
			TopKPerBase:       8,
			Freshness:         skills.FreshnessActive,
			Citations:         skills.CitationsRequired,
		},
		Fallback: skills.ContractFallback{
			OnNoMatch:   skills.FallbackAskUser,
			OnAmbiguous: skills.FallbackSearchMultiple,
		},
	}
}

// Every declared group must overlap; within a group one entry is enough. A base missing a
// whole declared group is out, however well it matches the others — the author wrote the
// group down as a constraint, and discovery quietly waiving it would hand the Skill
// knowledge of a shape it did not ask for.
func TestMatchRequirementCandidatesGroupsAreConstraints(t *testing.T) {
	cards := []KnowledgeCatalogItem{
		discoveryCard("full-match", "product", []string{"factual-reference", "entity-lookup"}, []string{"zh-CN"}),
		discoveryCard("partial-capability", "product", []string{"factual-reference"}, []string{"zh-CN"}),
		discoveryCard("wrong-language", "product", []string{"factual-reference", "entity-lookup"}, []string{"ja-JP"}),
		discoveryCard("no-declarations", "product", nil, nil),
	}
	requirement := discoveryRequirement(
		[]string{"factual-reference", "entity-lookup"}, []string{"zh-CN", "en-US"}, nil, 3)

	matched := matchRequirementCandidates(requirement, cards)
	if len(matched) != 2 {
		t.Fatalf("matched = %d candidates, want 2: %#v", len(matched), matched)
	}
	// Two capability overlaps + one language beats one capability + one language.
	if matched[0].Card.Name != "full-match" || matched[0].Score != 3 || matched[0].Rank != 1 {
		t.Fatalf("top candidate = %+v", matched[0])
	}
	if matched[1].Card.Name != "partial-capability" || matched[1].Score != 2 {
		t.Fatalf("second candidate = %+v", matched[1])
	}
	if len(matched[0].MatchedCapabilities) != 2 || matched[0].MatchedLanguages[0] != "zh-CN" {
		t.Fatalf("match explanation lost: %+v", matched[0])
	}
}

func TestMatchRequirementCandidatesDomainAndCaseInsensitivity(t *testing.T) {
	cards := []KnowledgeCatalogItem{
		discoveryCard("in-domain", "platform", []string{"Factual-Reference"}, nil),
		discoveryCard("out-of-domain", "product", []string{"factual-reference"}, nil),
	}
	requirement := discoveryRequirement([]string{"factual-reference"}, nil, []string{"Platform"}, 3)

	matched := matchRequirementCandidates(requirement, cards)
	if len(matched) != 1 || matched[0].Card.Name != "in-domain" {
		t.Fatalf("domain constraint failed: %#v", matched)
	}
	if matched[0].MatchedDomain != "Platform" || len(matched[0].MatchedCapabilities) != 1 {
		t.Fatalf("case-insensitive match lost its explanation: %+v", matched[0])
	}
}

// Ranking must not depend on catalog order: two discoveries against the same world have to
// return the same list, or the fingerprint's promise is broken.
func TestMatchRequirementCandidatesDeterministicOrder(t *testing.T) {
	cards := []KnowledgeCatalogItem{
		discoveryCard("bravo", "", []string{"factual-reference"}, nil),
		discoveryCard("alpha", "", []string{"factual-reference"}, nil),
	}
	requirement := discoveryRequirement([]string{"factual-reference"}, nil, nil, 5)

	forward := matchRequirementCandidates(requirement, cards)
	reversed := matchRequirementCandidates(requirement, []KnowledgeCatalogItem{cards[1], cards[0]})
	if forward[0].Card.Name != "alpha" || reversed[0].Card.Name != "alpha" {
		t.Fatalf("tie-break is order dependent: %s vs %s", forward[0].Card.Name, reversed[0].Card.Name)
	}
}

// The failure classes must stay distinct: each one names a different thing to fix.
func TestDiscoverRequirementClassification(t *testing.T) {
	requirement := discoveryRequirement([]string{"factual-reference"}, nil, nil, 1)
	declared := discoveryCard("declared", "", []string{"factual-reference"}, nil)
	declaredToo := discoveryCard("declared-too", "", []string{"factual-reference"}, nil)
	undeclared := discoveryCard("undeclared", "", nil, nil)

	for _, testCase := range []struct {
		name         string
		cards        []KnowledgeCatalogItem
		wantStatus   string
		wantFallback string
		wantTotal    int
	}{
		{"empty catalog", nil, DiscoveryStatusNoAuthorizedKnowledge, skills.FallbackAskUser, 0},
		{"nothing declares", []KnowledgeCatalogItem{undeclared}, DiscoveryStatusNoMetadataMatch, skills.FallbackAskUser, 0},
		{"single match", []KnowledgeCatalogItem{declared, undeclared}, DiscoveryStatusMatched, "", 1},
		{"over budget", []KnowledgeCatalogItem{declared, declaredToo}, DiscoveryStatusAmbiguous, skills.FallbackSearchMultiple, 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			result := discoverRequirement(skills.ContractModeDiscover, requirement, testCase.cards)
			if result.Status != testCase.wantStatus {
				t.Fatalf("status = %q, want %q", result.Status, testCase.wantStatus)
			}
			if result.Fallback != testCase.wantFallback {
				t.Fatalf("fallback = %q, want %q", result.Fallback, testCase.wantFallback)
			}
			if result.TotalMatched != testCase.wantTotal {
				t.Fatalf("total matched = %d, want %d", result.TotalMatched, testCase.wantTotal)
			}
			if len(result.Candidates) > requirement.Retrieval.MaxKnowledgeBases {
				t.Fatalf("candidates exceed the contract budget: %d", len(result.Candidates))
			}
		})
	}
}

// "Nothing declares capabilities" and "declarations exist but none fit" push the operator
// at different fixes; the note must tell them apart.
func TestDiscoverRequirementNoMatchNoteNamesUndeclaredCatalog(t *testing.T) {
	requirement := discoveryRequirement([]string{"factual-reference"}, nil, nil, 3)
	result := discoverRequirement(skills.ContractModeDiscover, requirement,
		[]KnowledgeCatalogItem{discoveryCard("undeclared", "", nil, nil)})
	if !strings.Contains(result.Note, "declare capabilities") {
		t.Fatalf("note does not point at undeclared manifests: %q", result.Note)
	}
}

// A pin that no longer resolves must not fall back to shape matching: the author pinned a
// specific combination, and serving a partial set silently unpins a compliance decision.
func TestDiscoverRequirementPinnedResolution(t *testing.T) {
	cards := []KnowledgeCatalogItem{discoveryCard("pinned-base", "", nil, nil)}
	requirement := discoveryRequirement(nil, nil, nil, 3)
	requirement.Pinned = []skills.ContractPinnedBase{{SourceName: "Pinned-Base"}}

	resolved := discoverRequirement(skills.ContractModePinned, requirement, cards)
	if resolved.Status != DiscoveryStatusPinnedResolved || len(resolved.Candidates) != 1 {
		t.Fatalf("pinned resolution = %+v", resolved)
	}

	requirement.Pinned = append(requirement.Pinned, skills.ContractPinnedBase{SourceName: "gone-base"})
	missing := discoverRequirement(skills.ContractModePinned, requirement, cards)
	if missing.Status != DiscoveryStatusPinnedMissing {
		t.Fatalf("status = %q, want pinned_missing", missing.Status)
	}
	if !strings.Contains(missing.Note, "gone-base") {
		t.Fatalf("note does not name the missing pin: %q", missing.Note)
	}
}

// The fingerprint identifies the question and the world it was asked in. Catalog order is
// presentation, so it must not move the fingerprint; package identity is the world, so it
// must.
func TestDiscoveryFingerprintProperties(t *testing.T) {
	first := discoveryCard("alpha", "", nil, nil)
	second := discoveryCard("bravo", "", nil, nil)

	base := discoveryFingerprint("mode=discover;req(a)", "", []KnowledgeCatalogItem{first, second})
	reordered := discoveryFingerprint("mode=discover;req(a)", "", []KnowledgeCatalogItem{second, first})
	if base != reordered {
		t.Fatal("fingerprint depends on catalog order")
	}
	if base == discoveryFingerprint("mode=discover;req(b)", "", []KnowledgeCatalogItem{first, second}) {
		t.Fatal("fingerprint ignores contract identity")
	}
	if base == discoveryFingerprint("mode=discover;req(a)", "primary", []KnowledgeCatalogItem{first, second}) {
		t.Fatal("fingerprint ignores the requirement filter")
	}
	changed := first
	changed.PackageHash = strings.Repeat("b", 64)
	if base == discoveryFingerprint("mode=discover;req(a)", "", []KnowledgeCatalogItem{changed, second}) {
		t.Fatal("fingerprint ignores package identity")
	}
}
