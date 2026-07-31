package skills

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml"
)

// ─── K4: the Skill knowledge contract ───
//
// Architecture §4 opens with the reason this type exists: "只写'自行寻找相关知识'无法 lint、
// 授权、预算或评测" — a sentence of prose telling an agent to find relevant knowledge cannot be
// linted, authorised, budgeted or evaluated. The contract is the machine-readable half of that
// instruction, and everything the platform enforces hangs off it.
//
// It describes what a Skill needs and how to look, never which knowledge base to use. That
// distinction is what keeps Skills and knowledge bases many-to-many: a general
// grounded-answer Skill discovers whichever domain bases fit, and one product base serves
// answer, compare and incident-analysis alike. Naming a base here would collapse that into
// one Skill per base.

const (
	// ContractModeDiscover selects dynamically within whatever the account authorises.
	ContractModeDiscover = "discover"
	// ContractModeScopedDiscover narrows by workspace, tags or approved state first.
	ContractModeScopedDiscover = "scoped_discover"
	// ContractModePinned fixes specific bases or builds. Reserved for compliance and strict
	// reproduction, and deliberately not a premise of the initial model: a contract that
	// pins is a contract that stops discovering, and most Skills that pin do it out of
	// habit rather than need.
	ContractModePinned = "pinned"
)

const (
	FallbackAskUser        = "ask_user"
	FallbackSearchMultiple = "search_multiple"
	FallbackProceed        = "proceed_without_knowledge"
	FallbackFail           = "fail"
)

const (
	FreshnessActive = "active"
	FreshnessAny    = "any"

	CitationsRequired = "required"
	CitationsOptional = "optional"
)

// Platform ceilings. Architecture §4 is explicit that Skill instructions cannot widen
// permissions: ACLs, candidate caps, cost budgets, active build and write permission are
// enforced by the platform in the discovery and search APIs. A contract asking for more than
// these is not silently clamped — it fails to compile, because a Skill whose declared
// retrieval plan differs from what it will actually get is a Skill whose author is
// reasoning about a system that does not exist.
const (
	maxContractRequirements     = 8
	maxContractKnowledgeBases   = 10
	maxContractTopKPerBase      = 50
	maxRequirementIDRunes       = 64
	maxRequirementPurposeRunes  = 500
	maxContractMatchListItems   = 16
	maxContractMatchValueRunes  = 100
	maxContractScopeListItems   = 16
	maxContractPinnedReferences = 10
)

// KnowledgeContract is the parsed `knowledge:` block of SKILL.md frontmatter.
//
// It is part of Skill package identity: changing discovery semantics produces a new Skill
// version. That is not bookkeeping — a Skill that quietly starts consulting three bases
// instead of one behaves differently, and attributing the new behaviour to the old version
// would make every validation signal about it unreadable.
type KnowledgeContract struct {
	Mode         string                 `json:"mode" yaml:"mode"`
	Requirements []KnowledgeRequirement `json:"requirements,omitempty" yaml:"requirements"`
	// Scope applies to scoped_discover: workspaces, tags or an approved-state filter that
	// narrows the candidate set before ranking.
	Scope *ContractScope `json:"scope,omitempty" yaml:"scope"`
}

type KnowledgeRequirement struct {
	ID      string `json:"id" yaml:"id"`
	Purpose string `json:"purpose,omitempty" yaml:"purpose"`
	// Required distinguishes "this Skill cannot work without knowledge of this kind" from
	// "use it if it is there". Without the distinction, fallback has nothing to branch on.
	Required  bool                 `json:"required" yaml:"required"`
	Match     ContractMatch        `json:"match,omitempty" yaml:"match"`
	Retrieval ContractRetrieval    `json:"retrieval,omitempty" yaml:"retrieval"`
	Fallback  ContractFallback     `json:"fallback,omitempty" yaml:"fallback"`
	Pinned    []ContractPinnedBase `json:"pinned,omitempty" yaml:"pinned"`
}

// ContractMatch describes the shape of knowledge wanted, not its identity.
type ContractMatch struct {
	Capabilities []string `json:"capabilities,omitempty" yaml:"capabilities"`
	Languages    []string `json:"languages,omitempty" yaml:"languages"`
	Domains      []string `json:"domains,omitempty" yaml:"domains"`
}

type ContractRetrieval struct {
	MaxKnowledgeBases int    `json:"max_knowledge_bases" yaml:"max_knowledge_bases"`
	TopKPerBase       int    `json:"top_k_per_base" yaml:"top_k_per_base"`
	Freshness         string `json:"freshness,omitempty" yaml:"freshness"`
	Citations         string `json:"citations,omitempty" yaml:"citations"`
}

type ContractFallback struct {
	OnNoMatch   string `json:"on_no_match,omitempty" yaml:"on_no_match"`
	OnAmbiguous string `json:"on_ambiguous,omitempty" yaml:"on_ambiguous"`
}

type ContractScope struct {
	Workspaces   []string `json:"workspaces,omitempty" yaml:"workspaces"`
	Tags         []string `json:"tags,omitempty" yaml:"tags"`
	ApprovedOnly bool     `json:"approved_only,omitempty" yaml:"approved_only"`
}

// ContractPinnedBase names a base, and optionally an exact build.
//
// BuildID is what makes pinning meaningful for reproduction: pinning a source while its wiki
// keeps recompiling reproduces nothing.
type ContractPinnedBase struct {
	SourceName string `json:"source_name,omitempty" yaml:"source_name"`
	SourceID   string `json:"source_id,omitempty" yaml:"source_id"`
	BuildID    string `json:"build_id,omitempty" yaml:"build_id"`
}

// contractDefaults are applied to omitted retrieval fields.
//
// They are conservative on purpose. An omitted max_knowledge_bases means the author did not
// think about breadth, and defaulting to the platform ceiling would spend the widest possible
// budget on their behalf.
const (
	defaultContractKnowledgeBases = 3
	defaultContractTopKPerBase    = 8
)

// extractKnowledgeContractBlock pulls the `knowledge:` block out of frontmatter as raw YAML.
//
// The existing frontmatter reader is a flat key/list scanner: it cannot express nested maps,
// and its default branch treats a key with an empty value as an unknown list to skip — so a
// `knowledge:` block would be silently discarded rather than rejected. Rather than teach that
// scanner nested YAML by hand, the block is lifted by indentation and handed to the YAML
// parser the manifest already uses. Hand-rolled nested YAML is how a parser comes to disagree
// with what the author wrote, and a contract that cannot be trusted to mean what it says
// cannot be linted, which was the whole reason for having one.
func extractKnowledgeContractBlock(content string) (string, bool) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return "", false
	}
	start := -1
	baseIndent := 0
	for index := 1; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		if trimmed == "---" {
			break
		}
		if start >= 0 {
			// Inside the block: it ends at the first line indented no further than the
			// `knowledge:` key itself.
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			if leadingWhitespace(lines[index]) <= baseIndent {
				return strings.Join(lines[start:index], "\n"), true
			}
			continue
		}
		key, _, ok := strings.Cut(trimmed, ":")
		if !ok || strings.ToLower(strings.TrimSpace(key)) != "knowledge" {
			continue
		}
		start = index
		baseIndent = leadingWhitespace(lines[index])
	}
	if start < 0 {
		return "", false
	}
	// The block ran to the end of the frontmatter.
	for index := start; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			return strings.Join(lines[start:index], "\n"), true
		}
	}
	return strings.Join(lines[start:], "\n"), true
}

// ParseKnowledgeContract reads the contract from SKILL.md content.
//
// A Skill without a `knowledge:` block returns (nil, nil): most Skills do not consult
// knowledge, and requiring an empty block from all of them would be ceremony. What is not
// tolerated is a block that is present and wrong — see ValidateKnowledgeContract.
func ParseKnowledgeContract(content string) (*KnowledgeContract, error) {
	block, found := extractKnowledgeContractBlock(content)
	if !found {
		return nil, nil
	}
	var wrapper struct {
		Knowledge *KnowledgeContract `yaml:"knowledge"`
	}
	if err := yaml.Unmarshal([]byte(dedentBlock(block)), &wrapper); err != nil {
		return nil, fmt.Errorf("knowledge contract is not valid YAML: %w", err)
	}
	if wrapper.Knowledge == nil {
		// `knowledge:` with nothing under it. Not the same as absent: the author started
		// declaring a contract and stopped, and silently treating it as "no knowledge
		// needed" would hide an unfinished declaration.
		return nil, fmt.Errorf("knowledge contract is empty")
	}
	contract := wrapper.Knowledge
	normaliseContract(contract)
	return contract, nil
}

// dedentBlock removes the common leading indentation so the block parses as a document.
func dedentBlock(block string) string {
	lines := strings.Split(block, "\n")
	indent := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		width := leadingWhitespace(line)
		if indent < 0 || width < indent {
			indent = width
		}
	}
	if indent <= 0 {
		return block
	}
	for i, line := range lines {
		if len(line) >= indent {
			lines[i] = line[indent:]
		} else {
			lines[i] = strings.TrimLeft(line, " \t")
		}
	}
	return strings.Join(lines, "\n")
}

func normaliseContract(contract *KnowledgeContract) {
	contract.Mode = strings.ToLower(strings.TrimSpace(contract.Mode))
	if contract.Mode == "" {
		// Architecture §4: start from discover. An omitted mode is the common case and
		// discover is the least restrictive thing that is still bounded by the platform.
		contract.Mode = ContractModeDiscover
	}
	for index := range contract.Requirements {
		requirement := &contract.Requirements[index]
		requirement.ID = strings.TrimSpace(requirement.ID)
		requirement.Purpose = strings.TrimSpace(requirement.Purpose)
		requirement.Match.Capabilities = normaliseContractList(requirement.Match.Capabilities)
		requirement.Match.Languages = normaliseContractList(requirement.Match.Languages)
		requirement.Match.Domains = normaliseContractList(requirement.Match.Domains)

		if requirement.Retrieval.MaxKnowledgeBases == 0 {
			requirement.Retrieval.MaxKnowledgeBases = defaultContractKnowledgeBases
		}
		if requirement.Retrieval.TopKPerBase == 0 {
			requirement.Retrieval.TopKPerBase = defaultContractTopKPerBase
		}
		requirement.Retrieval.Freshness = strings.ToLower(strings.TrimSpace(requirement.Retrieval.Freshness))
		if requirement.Retrieval.Freshness == "" {
			// Only the active build by default. Searching superseded builds returns text
			// that no longer serves anywhere else, which reads as a contradiction rather
			// than as history.
			requirement.Retrieval.Freshness = FreshnessActive
		}
		requirement.Retrieval.Citations = strings.ToLower(strings.TrimSpace(requirement.Retrieval.Citations))
		if requirement.Retrieval.Citations == "" {
			requirement.Retrieval.Citations = CitationsRequired
		}
		requirement.Fallback.OnNoMatch = strings.ToLower(strings.TrimSpace(requirement.Fallback.OnNoMatch))
		requirement.Fallback.OnAmbiguous = strings.ToLower(strings.TrimSpace(requirement.Fallback.OnAmbiguous))
		if requirement.Fallback.OnNoMatch == "" {
			// A required need with nothing found stops and says so; an optional one carries
			// on. Defaulting a required need to "proceed" would let a Skill answer from
			// nothing while its contract claims it needs sources.
			if requirement.Required {
				requirement.Fallback.OnNoMatch = FallbackAskUser
			} else {
				requirement.Fallback.OnNoMatch = FallbackProceed
			}
		}
		if requirement.Fallback.OnAmbiguous == "" {
			requirement.Fallback.OnAmbiguous = FallbackSearchMultiple
		}
	}
	if contract.Scope != nil {
		contract.Scope.Workspaces = normaliseContractList(contract.Scope.Workspaces)
		contract.Scope.Tags = normaliseContractList(contract.Scope.Tags)
	}
}

func normaliseContractList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		lowered := strings.ToLower(trimmed)
		if _, done := seen[lowered]; done {
			continue
		}
		seen[lowered] = struct{}{}
		cleaned = append(cleaned, trimmed)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

// ValidateKnowledgeContract rejects a contract that cannot be executed as written.
//
// This is compile-time refusal, not clamping. A contract asking for twenty knowledge bases
// when the platform allows ten could be silently reduced, but then the author is reasoning
// about a retrieval plan that will never run, and the difference surfaces later as
// unexplained results. Refusing makes the ceiling visible at the moment it is exceeded.
func ValidateKnowledgeContract(contract *KnowledgeContract) error {
	if contract == nil {
		return nil
	}
	switch contract.Mode {
	case ContractModeDiscover, ContractModeScopedDiscover, ContractModePinned:
	default:
		return fmt.Errorf("knowledge contract mode %q is not one of discover, scoped_discover, pinned", contract.Mode)
	}
	if len(contract.Requirements) == 0 {
		// A contract with no requirements declares an intention to use knowledge and says
		// nothing about what kind — exactly the unlintable prose the contract replaces.
		return fmt.Errorf("knowledge contract has no requirements; a contract that names no need cannot be linted, authorised or budgeted")
	}
	if len(contract.Requirements) > maxContractRequirements {
		return fmt.Errorf("knowledge contract has %d requirements, more than the %d allowed",
			len(contract.Requirements), maxContractRequirements)
	}
	if contract.Mode != ContractModeScopedDiscover && contract.Scope != nil {
		return fmt.Errorf("knowledge contract declares a scope but mode is %q; scope applies to scoped_discover", contract.Mode)
	}
	if contract.Mode == ContractModeScopedDiscover {
		if contract.Scope == nil ||
			(len(contract.Scope.Workspaces) == 0 && len(contract.Scope.Tags) == 0 && !contract.Scope.ApprovedOnly) {
			return fmt.Errorf("mode scoped_discover requires a scope that narrows something")
		}
		if len(contract.Scope.Workspaces) > maxContractScopeListItems || len(contract.Scope.Tags) > maxContractScopeListItems {
			return fmt.Errorf("knowledge contract scope lists more than %d entries", maxContractScopeListItems)
		}
	}

	seen := make(map[string]struct{}, len(contract.Requirements))
	for index := range contract.Requirements {
		requirement := contract.Requirements[index]
		position := index + 1
		if requirement.ID == "" {
			return fmt.Errorf("requirement %d has no id; ids are how a resolution run says which need it satisfied", position)
		}
		if utf8.RuneCountInString(requirement.ID) > maxRequirementIDRunes {
			return fmt.Errorf("requirement %q id exceeds %d Unicode code points", requirement.ID, maxRequirementIDRunes)
		}
		key := strings.ToLower(requirement.ID)
		if _, duplicate := seen[key]; duplicate {
			// Two needs with one id make a resolution run ambiguous about which was met.
			return fmt.Errorf("requirement id %q appears more than once", requirement.ID)
		}
		seen[key] = struct{}{}

		if utf8.RuneCountInString(requirement.Purpose) > maxRequirementPurposeRunes {
			return fmt.Errorf("requirement %q purpose exceeds %d Unicode code points", requirement.ID, maxRequirementPurposeRunes)
		}
		for name, values := range map[string][]string{
			"capabilities": requirement.Match.Capabilities,
			"languages":    requirement.Match.Languages,
			"domains":      requirement.Match.Domains,
		} {
			if len(values) > maxContractMatchListItems {
				return fmt.Errorf("requirement %q match.%s lists more than %d entries", requirement.ID, name, maxContractMatchListItems)
			}
			for _, value := range values {
				if utf8.RuneCountInString(value) > maxContractMatchValueRunes {
					return fmt.Errorf("requirement %q match.%s entry %q exceeds %d Unicode code points",
						requirement.ID, name, value, maxContractMatchValueRunes)
				}
			}
		}

		if requirement.Retrieval.MaxKnowledgeBases < 1 || requirement.Retrieval.MaxKnowledgeBases > maxContractKnowledgeBases {
			return fmt.Errorf("requirement %q max_knowledge_bases must be between 1 and %d (the platform ceiling), got %d",
				requirement.ID, maxContractKnowledgeBases, requirement.Retrieval.MaxKnowledgeBases)
		}
		if requirement.Retrieval.TopKPerBase < 1 || requirement.Retrieval.TopKPerBase > maxContractTopKPerBase {
			return fmt.Errorf("requirement %q top_k_per_base must be between 1 and %d (the platform ceiling), got %d",
				requirement.ID, maxContractTopKPerBase, requirement.Retrieval.TopKPerBase)
		}
		switch requirement.Retrieval.Freshness {
		case FreshnessActive, FreshnessAny:
		default:
			return fmt.Errorf("requirement %q freshness %q is not active or any", requirement.ID, requirement.Retrieval.Freshness)
		}
		switch requirement.Retrieval.Citations {
		case CitationsRequired, CitationsOptional:
		default:
			return fmt.Errorf("requirement %q citations %q is not required or optional", requirement.ID, requirement.Retrieval.Citations)
		}
		switch requirement.Fallback.OnNoMatch {
		case FallbackAskUser, FallbackProceed, FallbackFail:
		case FallbackSearchMultiple:
			return fmt.Errorf("requirement %q on_no_match cannot be search_multiple; there is nothing to search when nothing matched", requirement.ID)
		default:
			return fmt.Errorf("requirement %q on_no_match %q is not ask_user, proceed_without_knowledge or fail", requirement.ID, requirement.Fallback.OnNoMatch)
		}
		switch requirement.Fallback.OnAmbiguous {
		case FallbackSearchMultiple, FallbackAskUser, FallbackFail:
		case FallbackProceed:
			return fmt.Errorf("requirement %q on_ambiguous cannot be proceed_without_knowledge; several bases matched, so proceeding without any discards what was found", requirement.ID)
		default:
			return fmt.Errorf("requirement %q on_ambiguous %q is not search_multiple, ask_user or fail", requirement.ID, requirement.Fallback.OnAmbiguous)
		}
		if requirement.Required && requirement.Fallback.OnNoMatch == FallbackProceed {
			// The two statements contradict each other, and the contract is the thing that
			// is supposed to make such a contradiction visible.
			return fmt.Errorf("requirement %q is required but falls back to proceeding without knowledge", requirement.ID)
		}

		if contract.Mode == ContractModePinned {
			if len(requirement.Pinned) == 0 {
				return fmt.Errorf("mode pinned requires requirement %q to name at least one knowledge base", requirement.ID)
			}
			if len(requirement.Pinned) > maxContractPinnedReferences {
				return fmt.Errorf("requirement %q pins more than %d bases", requirement.ID, maxContractPinnedReferences)
			}
			for pinIndex, pinned := range requirement.Pinned {
				if strings.TrimSpace(pinned.SourceName) == "" && strings.TrimSpace(pinned.SourceID) == "" {
					return fmt.Errorf("requirement %q pinned entry %d names neither source_name nor source_id", requirement.ID, pinIndex+1)
				}
			}
			continue
		}
		if len(requirement.Pinned) > 0 {
			return fmt.Errorf("requirement %q pins knowledge bases but mode is %q; pinning belongs to mode pinned", requirement.ID, contract.Mode)
		}
		if len(requirement.Match.Capabilities) == 0 && len(requirement.Match.Languages) == 0 && len(requirement.Match.Domains) == 0 {
			// Discovery with no match criteria ranks every authorised base against nothing.
			return fmt.Errorf("requirement %q has no match criteria; discovery needs something to match on", requirement.ID)
		}
	}
	return nil
}

// ContractIdentity renders the contract into the canonical string that takes part in Skill
// identity.
//
// Architecture §4: the contract belongs to Skill package identity, and changing discovery
// semantics produces a new Skill version. Ordering is normalised so a reordered but
// semantically identical contract does not manufacture a new version — while any change to
// what would actually be discovered does.
func ContractIdentity(contract *KnowledgeContract) string {
	if contract == nil {
		return "knowledge=none"
	}
	var builder strings.Builder
	builder.WriteString("mode=" + contract.Mode)
	if contract.Scope != nil {
		workspaces := append([]string(nil), contract.Scope.Workspaces...)
		tags := append([]string(nil), contract.Scope.Tags...)
		sort.Strings(workspaces)
		sort.Strings(tags)
		builder.WriteString(fmt.Sprintf(";scope(workspaces=%s,tags=%s,approved=%t)",
			strings.Join(workspaces, ","), strings.Join(tags, ","), contract.Scope.ApprovedOnly))
	}
	requirements := append([]KnowledgeRequirement(nil), contract.Requirements...)
	sort.Slice(requirements, func(i, j int) bool {
		return strings.ToLower(requirements[i].ID) < strings.ToLower(requirements[j].ID)
	})
	for _, requirement := range requirements {
		capabilities := append([]string(nil), requirement.Match.Capabilities...)
		languages := append([]string(nil), requirement.Match.Languages...)
		domains := append([]string(nil), requirement.Match.Domains...)
		sort.Strings(capabilities)
		sort.Strings(languages)
		sort.Strings(domains)
		pinned := make([]string, 0, len(requirement.Pinned))
		for _, entry := range requirement.Pinned {
			pinned = append(pinned, entry.SourceID+"/"+entry.SourceName+"@"+entry.BuildID)
		}
		sort.Strings(pinned)
		// Purpose is excluded: it is documentation for a human and changing its wording
		// alters nothing about what gets discovered. Including it would mint a new Skill
		// version for a typo fix.
		builder.WriteString(fmt.Sprintf(";req(%s,required=%t,cap=%s,lang=%s,dom=%s,max=%d,topk=%d,fresh=%s,cite=%s,nomatch=%s,ambig=%s,pin=%s)",
			strings.ToLower(requirement.ID), requirement.Required,
			strings.Join(capabilities, ","), strings.Join(languages, ","), strings.Join(domains, ","),
			requirement.Retrieval.MaxKnowledgeBases, requirement.Retrieval.TopKPerBase,
			requirement.Retrieval.Freshness, requirement.Retrieval.Citations,
			requirement.Fallback.OnNoMatch, requirement.Fallback.OnAmbiguous,
			strings.Join(pinned, "|")))
	}
	return builder.String()
}

// ─── K4: contract lint ───
//
// The split between this and ValidateKnowledgeContract is the same one K3.7 drew between
// lint and check. Validation is a gate: it refuses contracts that cannot be executed as
// written, and a Skill carrying one does not compile. Lint runs on a contract that already
// compiles and reports what is worth a second look without blocking anything. Every rule
// below is therefore something validation deliberately permits.
//
// What is *not* here: whether any knowledge base in the account actually offers the
// capabilities a requirement asks for. That question needs to know what exists, and this
// package's quality engine is a pure function whose own checks assert repeatability and
// order invariance. Giving it database access to answer one rule would break the property
// its other rules verify. Discovery reports that mismatch instead, where the answer is both
// knowable and current.

// ContractLintFinding is one observation about a compiling contract.
type ContractLintFinding struct {
	Rule          string `json:"rule"`
	RequirementID string `json:"requirement_id,omitempty"`
	Detail        string `json:"detail"`
}

// Contract lint rule IDs, also used as quality check IDs.
const (
	// lintContractCitationsOnRequired: a requirement the Skill cannot work without, that
	// accepts uncited answers. Validation allows it — the two fields are independent — but
	// together they say the Skill needs knowledge it will not be able to attribute.
	lintContractCitationsOnRequired = "knowledge_contract_required_needs_citations"
	// lintContractFreshness: freshness "any" searches superseded builds. Valid, and
	// occasionally wanted for history, but a superseded page is text that serves nowhere
	// else, so an answer drawn from it reads as a contradiction rather than as the past.
	lintContractFreshness = "knowledge_contract_prefers_active_builds"
	// lintContractBudgetHeadroom: a requirement sitting exactly at a platform ceiling. It
	// runs, at the highest cost the platform permits, with no room to widen later.
	lintContractBudgetHeadroom = "knowledge_contract_budget_within_headroom"
	// lintContractMatchDiscriminates: matching on language alone selects nearly every base
	// written in that language. Validation only requires that some criterion be present.
	lintContractMatchDiscriminates = "knowledge_contract_match_discriminates"
	// lintContractPinNamesBuild: pinning a source but not a build. Validation accepts it
	// because the source is named, but pinning exists for reproduction, and a source whose
	// wiki keeps recompiling reproduces nothing.
	lintContractPinNamesBuild = "knowledge_contract_pin_names_build"
	// lintContractPurposeDocumented: a requirement with no purpose. Harmless to execute and
	// impossible to evaluate — nobody reviewing a resolution can say whether the base that
	// was chosen was the right kind of thing.
	lintContractPurposeDocumented = "knowledge_contract_purpose_documented"
)

// LintKnowledgeContract returns findings for a contract that already validates.
//
// A nil contract yields nothing: most Skills consult no knowledge and there is nothing to
// advise about.
func LintKnowledgeContract(contract *KnowledgeContract) []ContractLintFinding {
	findings := make([]ContractLintFinding, 0)
	if contract == nil {
		return findings
	}
	for _, requirement := range contract.Requirements {
		if requirement.Required && requirement.Retrieval.Citations == CitationsOptional {
			findings = append(findings, ContractLintFinding{
				Rule: lintContractCitationsOnRequired, RequirementID: requirement.ID,
				Detail: "this requirement is required but accepts uncited results, so the Skill will depend on knowledge it cannot attribute",
			})
		}
		if requirement.Retrieval.Freshness == FreshnessAny {
			findings = append(findings, ContractLintFinding{
				Rule: lintContractFreshness, RequirementID: requirement.ID,
				Detail: "freshness \"any\" includes superseded builds, whose pages serve nowhere else; an answer drawn from one reads as a contradiction rather than as history",
			})
		}
		if requirement.Retrieval.MaxKnowledgeBases >= maxContractKnowledgeBases ||
			requirement.Retrieval.TopKPerBase >= maxContractTopKPerBase {
			findings = append(findings, ContractLintFinding{
				Rule: lintContractBudgetHeadroom, RequirementID: requirement.ID,
				Detail: fmt.Sprintf("retrieval sits at a platform ceiling (max_knowledge_bases %d of %d, top_k_per_base %d of %d): highest permitted cost per call, and no room to widen later",
					requirement.Retrieval.MaxKnowledgeBases, maxContractKnowledgeBases,
					requirement.Retrieval.TopKPerBase, maxContractTopKPerBase),
			})
		}
		if requirement.Purpose == "" {
			findings = append(findings, ContractLintFinding{
				Rule: lintContractPurposeDocumented, RequirementID: requirement.ID,
				Detail: "no purpose recorded; a resolution against this requirement cannot be judged for whether it found the right kind of knowledge",
			})
		}
		if contract.Mode == ContractModePinned {
			for _, pinned := range requirement.Pinned {
				if strings.TrimSpace(pinned.BuildID) == "" {
					findings = append(findings, ContractLintFinding{
						Rule: lintContractPinNamesBuild, RequirementID: requirement.ID,
						Detail: "pins a source without a build_id; pinning is for reproduction, and a source whose wiki keeps recompiling reproduces nothing",
					})
					break
				}
			}
			continue
		}
		if len(requirement.Match.Capabilities) == 0 && len(requirement.Match.Domains) == 0 &&
			len(requirement.Match.Languages) > 0 {
			findings = append(findings, ContractLintFinding{
				Rule: lintContractMatchDiscriminates, RequirementID: requirement.ID,
				Detail: "matches on language alone, which selects nearly every knowledge base written in that language; add a capability or domain to discriminate",
			})
		}
	}
	return findings
}
