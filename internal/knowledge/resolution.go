package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/claw-works/agentmate/internal/ownership"
)

// ─── K4: KnowledgeResolutionRun ───
//
// Architecture §3.3: runtime discovery's outcome must be frozen, without preconfiguring a
// binding. A resolution run is the record of one requirement being satisfied (or explicitly
// not satisfied) during one execution: which discovery it followed, which bases were chosen,
// what was retrieved, what was cited. It is execution evidence — append-only, never updated,
// the anchor that lets a validation signal or an audit question reach the exact knowledge an
// answer rested on.
//
// The trust boundary is drawn per field. The server asserts what it knows authoritatively:
// contract identity comes from the compiled contract, the requirement must exist in it, and
// every selected base is verified against the account's sources — the authorisation-audit
// value of the table depends on selections being real. The client reports what only it saw:
// candidates, retrieved references, citations, its reason and confidence. Those are bounded
// echoes tied back to a served discovery by the fingerprint; the catalog may have moved
// since, so re-verifying them against current state would destroy their meaning as history.

const (
	maxResolutionCandidates    = 20
	maxResolutionSelectedBases = 10 // the platform's max_knowledge_bases ceiling
	maxResolutionRetrievedRefs = 200
	maxResolutionCitations     = 100
	maxResolutionReasonRunes   = 2000
	maxResolutionIDRunes       = 64
	maxResolutionNameRunes     = 160
	maxResolutionPathRunes     = 1024
)

// ErrResolutionConflict: an idempotency key was replayed with different content. The
// original run is evidence; silently replacing it or double-recording it would both corrupt
// attribution, so the disagreement is surfaced.
var ErrResolutionConflict = errors.New("idempotency key was already used for a different resolution run")

var resolutionStatuses = map[string]struct{}{
	DiscoveryStatusMatched:               {},
	DiscoveryStatusAmbiguous:             {},
	DiscoveryStatusNoMetadataMatch:       {},
	DiscoveryStatusNoAuthorizedKnowledge: {},
	DiscoveryStatusPinnedResolved:        {},
	DiscoveryStatusPinnedMissing:         {},
}

// ResolutionCandidateSummary is the client's echo of one discovery candidate.
type ResolutionCandidateSummary struct {
	SourceID string `json:"source_id"`
	Name     string `json:"name,omitempty"`
	Score    int    `json:"score,omitempty"`
	Rank     int    `json:"rank,omitempty"`
}

// ResolutionSelectedBase names a base the execution actually used. SourceID is verified;
// RevisionID and BuildID, when present, are verified to belong to that source, because a
// selection pointing at another tenant's build is precisely what the audit must catch.
type ResolutionSelectedBase struct {
	SourceID   string `json:"source_id"`
	RevisionID string `json:"revision_id,omitempty"`
	BuildID    string `json:"build_id,omitempty"`
}

// ResolutionRetrievedRef is one retrieved unit, by reference only — a document chunk from
// raw search or a wiki page from wiki search. Bodies never enter a resolution run.
type ResolutionRetrievedRef struct {
	SourceID   string `json:"source_id,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	PagePath   string `json:"page_path,omitempty"`
	ChunkKey   string `json:"chunk_key,omitempty"`
}

// ResolutionCitation is one citation the produced answer carries.
type ResolutionCitation struct {
	SourceID   string `json:"source_id,omitempty"`
	DocumentID string `json:"document_id,omitempty"`
	Path       string `json:"path,omitempty"`
}

type RecordResolutionRequest struct {
	SkillVersionID       string                       `json:"skill_version_id"`
	SessionID            string                       `json:"session_id,omitempty"`
	RequirementID        string                       `json:"requirement_id"`
	DiscoveryFingerprint string                       `json:"discovery_fingerprint"`
	DiscoveryStatus      string                       `json:"discovery_status"`
	Candidates           []ResolutionCandidateSummary `json:"candidates,omitempty"`
	Selected             []ResolutionSelectedBase     `json:"selected,omitempty"`
	Retrieved            []ResolutionRetrievedRef     `json:"retrieved,omitempty"`
	Citations            []ResolutionCitation         `json:"citations,omitempty"`
	SelectionReason      string                       `json:"selection_reason,omitempty"`
	Confidence           *float64                     `json:"confidence,omitempty"`
	IdempotencyKey       string                       `json:"idempotency_key,omitempty"`
}

type KnowledgeResolutionRun struct {
	ID                   string                       `json:"id"`
	AccountID            string                       `json:"account_id"`
	SkillVersionID       string                       `json:"skill_version_id"`
	SessionID            string                       `json:"session_id,omitempty"`
	RequirementID        string                       `json:"requirement_id"`
	ContractIdentity     string                       `json:"contract_identity"`
	DiscoveryFingerprint string                       `json:"discovery_fingerprint"`
	DiscoveryStatus      string                       `json:"discovery_status"`
	Candidates           []ResolutionCandidateSummary `json:"candidates"`
	Selected             []ResolutionSelectedBase     `json:"selected"`
	Retrieved            []ResolutionRetrievedRef     `json:"retrieved"`
	Citations            []ResolutionCitation         `json:"citations"`
	SelectionReason      string                       `json:"selection_reason,omitempty"`
	Confidence           *float64                     `json:"confidence,omitempty"`
	IdempotencyKey       string                       `json:"idempotency_key,omitempty"`
	ContentHash          string                       `json:"content_hash"`
	CreatedAt            time.Time                    `json:"created_at"`
}

// KnowledgeResolutionRunSummary is the list projection: counts instead of the evidence
// arrays, so a page of history does not ship every reference it contains.
type KnowledgeResolutionRunSummary struct {
	ID                   string    `json:"id"`
	SkillVersionID       string    `json:"skill_version_id"`
	SessionID            string    `json:"session_id,omitempty"`
	RequirementID        string    `json:"requirement_id"`
	DiscoveryFingerprint string    `json:"discovery_fingerprint"`
	DiscoveryStatus      string    `json:"discovery_status"`
	SelectedCount        int       `json:"selected_count"`
	RetrievedCount       int       `json:"retrieved_count"`
	CitationCount        int       `json:"citation_count"`
	CreatedAt            time.Time `json:"created_at"`
}

type ResolutionListParams struct {
	SkillVersionID string
	SessionID      string
	// SourceID filters to runs whose selected set contains this base: "which executions
	// rested on this knowledge base" is the audit question this table answers.
	SourceID string
	Limit    int
	Offset   int
}

type ResolutionListResponse struct {
	Items  []KnowledgeResolutionRunSummary `json:"items"`
	Total  int                             `json:"total"`
	Limit  int                             `json:"limit"`
	Offset int                             `json:"offset"`
}

type RecordResolutionResponse struct {
	Run *KnowledgeResolutionRun `json:"run"`
	// Created is false when an idempotent replay returned the original row.
	Created bool `json:"created"`
}

// RecordResolution validates and freezes one resolution run.
func (s *Service) RecordResolution(ctx context.Context, owner ownership.Owner, req RecordResolutionRequest) (*RecordResolutionResponse, error) {
	if s.skillContracts == nil {
		return nil, fmt.Errorf("skill contract source is not configured")
	}
	normalizeResolutionRequest(&req)
	if err := validateResolutionRequest(req); err != nil {
		return nil, err
	}

	// The contract is the server's, not the client's. A run recorded against a
	// requirement the compiled contract never declared would be evidence about a
	// contract that does not exist.
	contract, err := s.skillContracts.CompiledContract(ctx, owner.Account(), req.SkillVersionID)
	if err != nil {
		return nil, err
	}
	if contract.Contract == nil {
		return nil, fmt.Errorf("skill version declares no knowledge contract; a resolution run records the satisfaction of a contract requirement")
	}
	known := false
	for _, requirement := range contract.Contract.Requirements {
		if strings.EqualFold(requirement.ID, req.RequirementID) {
			req.RequirementID = requirement.ID
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("requirement not found in contract: %s", req.RequirementID)
	}

	// Selected bases must be real and owned. This is the strict half of the trust
	// boundary: an unverifiable selection would defeat the table's audit purpose.
	for index, selected := range req.Selected {
		source, sourceErr := s.repo.GetSource(ctx, owner.Account(), selected.SourceID)
		if sourceErr != nil {
			return nil, fmt.Errorf("selected[%d]: source not found: %s", index, selected.SourceID)
		}
		if selected.RevisionID != "" {
			revision, revisionErr := s.repo.GetRevision(ctx, owner.Account(), selected.RevisionID)
			if revisionErr != nil || revision.SourceID != source.ID {
				return nil, fmt.Errorf("selected[%d]: revision %s does not belong to source %s", index, selected.RevisionID, source.ID)
			}
		}
		if selected.BuildID != "" {
			build, buildErr := s.repo.GetBuild(ctx, owner.Account(), selected.BuildID)
			if buildErr != nil || build.SourceID != source.ID {
				return nil, fmt.Errorf("selected[%d]: build %s does not belong to source %s", index, selected.BuildID, source.ID)
			}
		}
	}

	contentHash, err := resolutionContentHash(req)
	if err != nil {
		return nil, err
	}
	run, created, err := s.repo.InsertResolutionRun(ctx, owner, req, contract.ContractIdentity, contentHash)
	if err != nil {
		return nil, err
	}
	if !created && run.ContentHash != contentHash {
		// Same key, different content: the retry contract requires byte-identical
		// replays, exactly as memory events do.
		return nil, ErrResolutionConflict
	}
	return &RecordResolutionResponse{Run: run, Created: created}, nil
}

func (s *Service) GetResolution(ctx context.Context, accountID, runID string) (*KnowledgeResolutionRun, error) {
	return s.repo.GetResolutionRun(ctx, accountID, strings.TrimSpace(runID))
}

func (s *Service) ListResolutions(ctx context.Context, accountID string, params ResolutionListParams) (*ResolutionListResponse, error) {
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	if params.Offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}
	params.SkillVersionID = strings.TrimSpace(params.SkillVersionID)
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.SourceID = strings.TrimSpace(params.SourceID)
	items, total, err := s.repo.ListResolutionRuns(ctx, accountID, params)
	if err != nil {
		return nil, err
	}
	return &ResolutionListResponse{Items: items, Total: total, Limit: params.Limit, Offset: params.Offset}, nil
}

func normalizeResolutionRequest(req *RecordResolutionRequest) {
	req.SkillVersionID = strings.TrimSpace(req.SkillVersionID)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.RequirementID = strings.TrimSpace(req.RequirementID)
	req.DiscoveryFingerprint = strings.ToLower(strings.TrimSpace(req.DiscoveryFingerprint))
	req.DiscoveryStatus = strings.ToLower(strings.TrimSpace(req.DiscoveryStatus))
	req.SelectionReason = strings.TrimSpace(req.SelectionReason)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	for index := range req.Candidates {
		req.Candidates[index].SourceID = strings.TrimSpace(req.Candidates[index].SourceID)
		req.Candidates[index].Name = strings.TrimSpace(req.Candidates[index].Name)
	}
	for index := range req.Selected {
		req.Selected[index].SourceID = strings.TrimSpace(req.Selected[index].SourceID)
		req.Selected[index].RevisionID = strings.TrimSpace(req.Selected[index].RevisionID)
		req.Selected[index].BuildID = strings.TrimSpace(req.Selected[index].BuildID)
	}
	for index := range req.Retrieved {
		req.Retrieved[index].SourceID = strings.TrimSpace(req.Retrieved[index].SourceID)
		req.Retrieved[index].DocumentID = strings.TrimSpace(req.Retrieved[index].DocumentID)
		req.Retrieved[index].PagePath = strings.TrimSpace(req.Retrieved[index].PagePath)
		req.Retrieved[index].ChunkKey = strings.TrimSpace(req.Retrieved[index].ChunkKey)
	}
	for index := range req.Citations {
		req.Citations[index].SourceID = strings.TrimSpace(req.Citations[index].SourceID)
		req.Citations[index].DocumentID = strings.TrimSpace(req.Citations[index].DocumentID)
		req.Citations[index].Path = strings.TrimSpace(req.Citations[index].Path)
	}
}

func validateResolutionRequest(req RecordResolutionRequest) error {
	if req.SkillVersionID == "" {
		return fmt.Errorf("skill_version_id required")
	}
	if req.RequirementID == "" {
		return fmt.Errorf("requirement_id required")
	}
	if utf8.RuneCountInString(req.RequirementID) > maxResolutionIDRunes {
		return fmt.Errorf("requirement_id exceeds %d Unicode code points", maxResolutionIDRunes)
	}
	if !isHex64(req.DiscoveryFingerprint) {
		return fmt.Errorf("discovery_fingerprint must be the 64-character hex fingerprint returned by discovery")
	}
	if _, ok := resolutionStatuses[req.DiscoveryStatus]; !ok {
		return fmt.Errorf("discovery_status %q is not a discovery status", req.DiscoveryStatus)
	}
	if len(req.Candidates) > maxResolutionCandidates {
		return fmt.Errorf("candidates lists more than %d entries", maxResolutionCandidates)
	}
	if len(req.Selected) > maxResolutionSelectedBases {
		return fmt.Errorf("selected lists more than %d entries (the platform ceiling)", maxResolutionSelectedBases)
	}
	if len(req.Retrieved) > maxResolutionRetrievedRefs {
		return fmt.Errorf("retrieved lists more than %d entries", maxResolutionRetrievedRefs)
	}
	if len(req.Citations) > maxResolutionCitations {
		return fmt.Errorf("citations lists more than %d entries", maxResolutionCitations)
	}
	if utf8.RuneCountInString(req.SelectionReason) > maxResolutionReasonRunes {
		return fmt.Errorf("selection_reason exceeds %d Unicode code points", maxResolutionReasonRunes)
	}
	if req.Confidence != nil && (*req.Confidence < 0 || *req.Confidence > 1) {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if utf8.RuneCountInString(req.IdempotencyKey) > 128 {
		return fmt.Errorf("idempotency_key exceeds 128 Unicode code points")
	}
	for index, candidate := range req.Candidates {
		if candidate.SourceID == "" {
			return fmt.Errorf("candidates[%d]: source_id required", index)
		}
		if utf8.RuneCountInString(candidate.Name) > maxResolutionNameRunes {
			return fmt.Errorf("candidates[%d]: name exceeds %d Unicode code points", index, maxResolutionNameRunes)
		}
	}
	for index, selected := range req.Selected {
		if selected.SourceID == "" {
			return fmt.Errorf("selected[%d]: source_id required", index)
		}
	}
	for index, retrieved := range req.Retrieved {
		if retrieved.DocumentID == "" && retrieved.PagePath == "" {
			return fmt.Errorf("retrieved[%d]: document_id or page_path required", index)
		}
		if utf8.RuneCountInString(retrieved.PagePath) > maxResolutionPathRunes ||
			utf8.RuneCountInString(retrieved.ChunkKey) > maxResolutionPathRunes {
			return fmt.Errorf("retrieved[%d]: reference exceeds %d Unicode code points", index, maxResolutionPathRunes)
		}
	}
	for index, citation := range req.Citations {
		if citation.DocumentID == "" && citation.Path == "" {
			return fmt.Errorf("citations[%d]: document_id or path required", index)
		}
		if utf8.RuneCountInString(citation.Path) > maxResolutionPathRunes {
			return fmt.Errorf("citations[%d]: path exceeds %d Unicode code points", index, maxResolutionPathRunes)
		}
	}
	return nil
}

// resolutionContentHash hashes the normalized request minus the idempotency key: the key
// names the attempt, the hash names the content, and the replay rule compares the two
// separately.
func resolutionContentHash(req RecordResolutionRequest) (string, error) {
	req.IdempotencyKey = ""
	canonical, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func isHex64(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
