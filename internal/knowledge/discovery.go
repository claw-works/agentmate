package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/skills"
)

// ─── K4: contract-driven knowledge discovery ───
//
// Architecture §7 step 5: knowledge_discover returns bounded K0 candidates inside the
// account's authorisation; the agent chooses among them and only then searches. This file is
// the platform half of the Skill's knowledge contract — the contract says what a Skill
// needs, discovery says what this account actually has that fits, and neither side can
// widen the other: the contract cannot see past the account scope, and no knowledge base can
// force itself into a Skill that did not ask for its shape.
//
// Discovery failures are classified, not flattened (§7): "you have no knowledge bases at
// all", "you have twelve but none declares what this Skill needs", and "seven matched, which
// is more than the contract budgeted" call for three different reactions, and reporting all
// of them as an empty list would push agents into answering from nothing without knowing
// which problem to fix.

// Requirement discovery statuses. Each names the problem precisely enough that the caller
// knows which side to fix: the account's catalog, the manifests' declarations, or the
// contract's budget.
const (
	// DiscoveryStatusMatched: between one and max_knowledge_bases candidates fit.
	DiscoveryStatusMatched = "matched"
	// DiscoveryStatusAmbiguous: more candidates fit than the contract budgeted for.
	// The contract's on_ambiguous fallback says what to do; the platform does not
	// silently pick, because a silent pick is a decision nobody can audit.
	DiscoveryStatusAmbiguous = "ambiguous"
	// DiscoveryStatusNoMetadataMatch: authorised knowledge exists but none of it
	// declares the shape this requirement asks for.
	DiscoveryStatusNoMetadataMatch = "no_metadata_match"
	// DiscoveryStatusNoAuthorizedKnowledge: the account has no knowledge bases with an
	// active revision at all.
	DiscoveryStatusNoAuthorizedKnowledge = "no_authorized_knowledge"
	// DiscoveryStatusPinnedResolved / PinnedMissing: pinned mode resolves references
	// instead of matching shapes, and a pin that no longer resolves is its own failure
	// class — falling back to discovery would silently unpin a compliance decision.
	DiscoveryStatusPinnedResolved = "pinned_resolved"
	DiscoveryStatusPinnedMissing  = "pinned_missing"
)

// maxDiscoveryCatalogCards bounds how much catalog one discovery reads. Matching is
// in-memory over K0 cards; an account with more collections than this gets a truncation
// note rather than an unbounded scan.
const maxDiscoveryCatalogCards = 200

// ErrScopedDiscoveryUnsupported: scoped_discover narrows by workspaces, tags or approved
// state, none of which exist in the knowledge domain yet. Refusing is deliberate — treating
// the scope as empty would run the Skill against a wider candidate set than its author
// declared, which is exactly the authorisation drift the contract exists to prevent.
var ErrScopedDiscoveryUnsupported = errors.New("mode scoped_discover is not executable yet: workspaces, tags and approved state do not exist in the knowledge domain; use discover or pinned")

// SkillContractSource supplies the compiled knowledge contract governing a skill version.
// Implemented by skills.Service; an interface here so discovery is testable without a
// skills database.
type SkillContractSource interface {
	CompiledContract(ctx context.Context, accountID, versionID string) (*skills.CompiledContractResult, error)
}

// WithSkillContracts wires the skills domain in. Optional, mirroring WithLLM: every other
// knowledge path works without it, and Discover reports the missing wiring explicitly.
func (s *Service) WithSkillContracts(source SkillContractSource) {
	s.skillContracts = source
}

type DiscoverKnowledgeRequest struct {
	SkillVersionID string `json:"skill_version_id"`
	// RequirementID narrows discovery to one requirement, for the agent that already
	// satisfied the others and is re-discovering mid-execution (§7 step 8).
	RequirementID string `json:"requirement_id,omitempty"`
}

// KnowledgeDiscoveryCandidate is one K0 card plus the reason it matched. The matched-*
// fields are the explanation: a candidate without them would force the agent to re-derive
// the ranking to trust it.
type KnowledgeDiscoveryCandidate struct {
	Card                KnowledgeCatalogItem `json:"card"`
	MatchedCapabilities []string             `json:"matched_capabilities,omitempty"`
	MatchedLanguages    []string             `json:"matched_languages,omitempty"`
	MatchedDomain       string               `json:"matched_domain,omitempty"`
	// Score counts matched criteria. Deterministic and dumb on purpose: discovery ranks
	// by declared metadata, not by semantic similarity — the semantic judgement happens
	// at search time against actual content, where it can be grounded in evidence.
	Score int `json:"score"`
	Rank  int `json:"rank"`
}

type RequirementDiscovery struct {
	RequirementID string                        `json:"requirement_id"`
	Purpose       string                        `json:"purpose,omitempty"`
	Required      bool                          `json:"required"`
	Status        string                        `json:"status"`
	Candidates    []KnowledgeDiscoveryCandidate `json:"candidates"`
	// TotalMatched can exceed len(Candidates) when the status is ambiguous: the caller
	// needs to know how much was cut to judge whether widening the budget is worth it.
	TotalMatched int `json:"total_matched"`
	// Retrieval echoes the contract's budgets so the agent can build the follow-up
	// search calls without re-reading SKILL.md.
	Retrieval skills.ContractRetrieval `json:"retrieval"`
	// Fallback is the contract's own instruction for the branch that fired: on_no_match
	// guidance when nothing usable was found, on_ambiguous guidance when too much was.
	// Empty on success.
	Fallback string `json:"fallback,omitempty"`
	Note     string `json:"note,omitempty"`
}

type DiscoverKnowledgeResponse struct {
	SkillVersionID   string                 `json:"skill_version_id"`
	SkillName        string                 `json:"skill_name"`
	SkillVersion     string                 `json:"skill_version"`
	HasContract      bool                   `json:"has_contract"`
	Mode             string                 `json:"mode,omitempty"`
	ContractIdentity string                 `json:"contract_identity,omitempty"`
	CatalogSize      int                    `json:"catalog_size"`
	Requirements     []RequirementDiscovery `json:"requirements"`
	// Fingerprint hashes the discovery inputs — contract identity, requirement filter,
	// and the catalog state that was matched against. Two discoveries with one
	// fingerprint saw the same question and the same world; it is the anchor a future
	// KnowledgeResolutionRun records so a resolution can be checked against what
	// discovery actually offered (§3.3).
	Fingerprint string `json:"fingerprint"`
	Note        string `json:"note,omitempty"`
}

// DiscoverForSkill resolves a skill version's knowledge contract against the account's K0
// catalog. Read-only: it reports candidates and classified failures, and selection stays
// with the agent — the platform bounds the candidate set, it does not choose from it.
func (s *Service) DiscoverForSkill(ctx context.Context, owner ownership.Owner, req DiscoverKnowledgeRequest) (*DiscoverKnowledgeResponse, error) {
	if s.skillContracts == nil {
		return nil, fmt.Errorf("skill contract source is not configured")
	}
	req.SkillVersionID = strings.TrimSpace(req.SkillVersionID)
	if req.SkillVersionID == "" {
		return nil, fmt.Errorf("skill_version_id required")
	}
	req.RequirementID = strings.TrimSpace(req.RequirementID)

	contract, err := s.skillContracts.CompiledContract(ctx, owner.Account(), req.SkillVersionID)
	if err != nil {
		return nil, err
	}
	response := &DiscoverKnowledgeResponse{
		SkillVersionID: contract.SkillVersionID,
		SkillName:      contract.SkillName,
		SkillVersion:   contract.Version,
		Requirements:   []RequirementDiscovery{},
	}
	if contract.Contract == nil {
		// Not an error: the agent may call discover unconditionally before executing
		// any Skill. "This Skill needs nothing" is an answer, not a failure.
		response.Note = "this skill version declares no knowledge contract; there is nothing to discover"
		response.Fingerprint = discoveryFingerprint("knowledge=none", req.RequirementID, nil)
		return response, nil
	}
	if contract.Contract.Mode == skills.ContractModeScopedDiscover {
		return nil, ErrScopedDiscoveryUnsupported
	}
	response.HasContract = true
	response.Mode = contract.Contract.Mode
	response.ContractIdentity = contract.ContractIdentity

	requirements := contract.Contract.Requirements
	if req.RequirementID != "" {
		filtered := requirements[:0:0]
		for _, requirement := range requirements {
			if strings.EqualFold(requirement.ID, req.RequirementID) {
				filtered = append(filtered, requirement)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf("requirement not found in contract: %s", req.RequirementID)
		}
		requirements = filtered
	}

	records, err := s.repo.ListCatalog(ctx, owner.Account(), KnowledgeCatalogListParams{Limit: maxDiscoveryCatalogCards})
	if err != nil {
		return nil, err
	}
	cards := make([]KnowledgeCatalogItem, 0, len(records))
	for _, record := range records {
		cards = append(cards, catalogItemFromRecord(record))
	}
	total, err := s.repo.CountCatalog(ctx, owner.Account(), "", "")
	if err != nil {
		return nil, err
	}
	response.CatalogSize = total
	if total > maxDiscoveryCatalogCards {
		response.Note = fmt.Sprintf("catalog truncated: matching considered %d of %d collections", maxDiscoveryCatalogCards, total)
	}

	for _, requirement := range requirements {
		response.Requirements = append(response.Requirements,
			discoverRequirement(contract.Contract.Mode, requirement, cards))
	}
	response.Fingerprint = discoveryFingerprint(contract.ContractIdentity, req.RequirementID, cards)
	return response, nil
}

// discoverRequirement classifies one requirement against the catalog. Pure: same
// requirement and cards, same answer.
func discoverRequirement(mode string, requirement skills.KnowledgeRequirement, cards []KnowledgeCatalogItem) RequirementDiscovery {
	result := RequirementDiscovery{
		RequirementID: requirement.ID,
		Purpose:       requirement.Purpose,
		Required:      requirement.Required,
		Retrieval:     requirement.Retrieval,
		Candidates:    []KnowledgeDiscoveryCandidate{},
	}

	if mode == skills.ContractModePinned {
		candidates, missing := resolvePinnedCandidates(requirement, cards)
		result.Candidates = candidates
		result.TotalMatched = len(candidates)
		if len(missing) > 0 {
			result.Status = DiscoveryStatusPinnedMissing
			result.Fallback = requirement.Fallback.OnNoMatch
			result.Note = "pinned knowledge bases not found in this account's catalog: " + strings.Join(missing, ", ")
			return result
		}
		result.Status = DiscoveryStatusPinnedResolved
		result.Note = "pinned build_ids are verified at search time; discovery resolves sources only"
		return result
	}

	if len(cards) == 0 {
		result.Status = DiscoveryStatusNoAuthorizedKnowledge
		result.Fallback = requirement.Fallback.OnNoMatch
		result.Note = "the account has no knowledge bases with an active revision"
		return result
	}

	matched := matchRequirementCandidates(requirement, cards)
	result.TotalMatched = len(matched)
	if len(matched) == 0 {
		result.Status = DiscoveryStatusNoMetadataMatch
		result.Fallback = requirement.Fallback.OnNoMatch
		result.Note = noMatchNote(requirement, cards)
		return result
	}
	if len(matched) > requirement.Retrieval.MaxKnowledgeBases {
		result.Status = DiscoveryStatusAmbiguous
		result.Fallback = requirement.Fallback.OnAmbiguous
		result.Candidates = matched[:requirement.Retrieval.MaxKnowledgeBases]
		result.Note = fmt.Sprintf("%d collections matched but the contract budgets max_knowledge_bases=%d; top candidates returned",
			len(matched), requirement.Retrieval.MaxKnowledgeBases)
		return result
	}
	result.Status = DiscoveryStatusMatched
	result.Candidates = matched
	return result
}

// matchRequirementCandidates ranks catalog cards against one requirement's match criteria.
//
// A card must overlap every criterion group the requirement declares (capabilities AND
// languages AND domains), by at least one entry each: a group the author wrote down is a
// constraint, not a preference. Within a group one overlap suffices — a base offering
// factual-reference but not entity-lookup is still partially useful, and demanding the full
// set would make any two-entry list nearly unmatchable. Score counts total overlaps, so a
// base matching more of what was asked ranks above one scraping by.
func matchRequirementCandidates(requirement skills.KnowledgeRequirement, cards []KnowledgeCatalogItem) []KnowledgeDiscoveryCandidate {
	matched := make([]KnowledgeDiscoveryCandidate, 0)
	for _, card := range cards {
		candidate := KnowledgeDiscoveryCandidate{Card: card}
		score := 0

		if len(requirement.Match.Capabilities) > 0 {
			overlap := listOverlap(requirement.Match.Capabilities, card.Capabilities)
			if len(overlap) == 0 {
				continue
			}
			candidate.MatchedCapabilities = overlap
			score += len(overlap)
		}
		if len(requirement.Match.Languages) > 0 {
			overlap := listOverlap(requirement.Match.Languages, card.Languages)
			if len(overlap) == 0 {
				continue
			}
			candidate.MatchedLanguages = overlap
			score += len(overlap)
		}
		if len(requirement.Match.Domains) > 0 {
			domain := domainMatch(requirement.Match.Domains, card.Domain)
			if domain == "" {
				continue
			}
			candidate.MatchedDomain = domain
			score++
		}
		candidate.Score = score
		matched = append(matched, candidate)
	}
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Score != matched[j].Score {
			return matched[i].Score > matched[j].Score
		}
		left, right := strings.ToLower(matched[i].Card.Name), strings.ToLower(matched[j].Card.Name)
		if left != right {
			return left < right
		}
		return matched[i].Card.SourceID < matched[j].Card.SourceID
	})
	for index := range matched {
		matched[index].Rank = index + 1
	}
	return matched
}

// resolvePinnedCandidates resolves pinned references by identity instead of by shape.
// Every pinned entry must resolve; a partial pin set is reported as missing rather than
// served, because the author pinned a specific combination, not a best effort.
func resolvePinnedCandidates(requirement skills.KnowledgeRequirement, cards []KnowledgeCatalogItem) ([]KnowledgeDiscoveryCandidate, []string) {
	candidates := make([]KnowledgeDiscoveryCandidate, 0, len(requirement.Pinned))
	missing := make([]string, 0)
	for _, pinned := range requirement.Pinned {
		var found *KnowledgeCatalogItem
		for index := range cards {
			if pinned.SourceID != "" && cards[index].SourceID == pinned.SourceID {
				found = &cards[index]
				break
			}
			if pinned.SourceID == "" && strings.EqualFold(cards[index].Name, pinned.SourceName) {
				found = &cards[index]
				break
			}
		}
		if found == nil {
			reference := pinned.SourceID
			if reference == "" {
				reference = pinned.SourceName
			}
			missing = append(missing, reference)
			continue
		}
		candidates = append(candidates, KnowledgeDiscoveryCandidate{Card: *found, Rank: len(candidates) + 1})
	}
	return candidates, missing
}

// noMatchNote distinguishes "nothing fits" from "nothing declares anything to fit against".
// The second is the common state right after this feature ships — existing manifests
// predate the capabilities field — and telling the operator to fix manifests is more useful
// than implying the knowledge itself is wrong.
func noMatchNote(requirement skills.KnowledgeRequirement, cards []KnowledgeCatalogItem) string {
	if len(requirement.Match.Capabilities) > 0 {
		declared := 0
		for _, card := range cards {
			if len(card.Capabilities) > 0 {
				declared++
			}
		}
		if declared == 0 {
			return fmt.Sprintf("none of the %d collections declare capabilities in KNOWLEDGE.yaml; the contract cannot match against undeclared metadata", len(cards))
		}
	}
	return "no collection declares the capabilities, languages or domain this requirement asks for"
}

func listOverlap(wanted, declared []string) []string {
	if len(wanted) == 0 || len(declared) == 0 {
		return nil
	}
	declaredSet := make(map[string]struct{}, len(declared))
	for _, value := range declared {
		declaredSet[strings.ToLower(strings.TrimSpace(value))] = struct{}{}
	}
	overlap := make([]string, 0)
	for _, value := range wanted {
		if _, ok := declaredSet[strings.ToLower(strings.TrimSpace(value))]; ok {
			overlap = append(overlap, value)
		}
	}
	return overlap
}

func domainMatch(wanted []string, domain string) string {
	if domain == "" {
		return ""
	}
	for _, value := range wanted {
		if strings.EqualFold(strings.TrimSpace(value), domain) {
			return value
		}
	}
	return ""
}

// discoveryFingerprint hashes what discovery was asked and what it saw: the contract
// identity, the requirement filter, and each catalog card's identity (source and package
// hash). It deliberately excludes match results — they are derived, and hashing inputs keeps
// the fingerprint checkable by recomputation.
func discoveryFingerprint(contractIdentity, requirementFilter string, cards []KnowledgeCatalogItem) string {
	entries := make([]string, 0, len(cards))
	for _, card := range cards {
		entries = append(entries, card.SourceID+"@"+card.PackageHash)
	}
	sort.Strings(entries)
	canonical := "v1|" + contractIdentity + "|filter=" + strings.ToLower(requirementFilter) + "|catalog=" + strings.Join(entries, ",")
	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
