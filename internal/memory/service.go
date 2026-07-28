package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/wellxie/agentmate/internal/ownership"
	"github.com/wellxie/agentmate/internal/retrieval"
)

var (
	ErrNotFound            = errors.New("memory entry not found")
	ErrInvalidInput        = errors.New("invalid memory input")
	ErrIdempotencyConflict = errors.New("idempotency key already used with different event content")
)

type Service struct {
	repo      *Repo
	retrieval *retrieval.Service
}

func NewService(repo *Repo, retrievalServices ...*retrieval.Service) *Service {
	service := &Service{repo: repo}
	if len(retrievalServices) > 0 {
		service.retrieval = retrievalServices[0]
	}
	return service
}

func (s *Service) RecordEvent(ctx context.Context, owner ownership.Owner, req RecordEventRequest) (*Event, bool, error) {
	if err := validateOwner(owner); err != nil {
		return nil, false, err
	}
	normalizeEventRequest(&req)
	if err := validateEventRequest(req); err != nil {
		return nil, false, err
	}
	// The account-scoped composite foreign key already makes cross-account
	// attribution impossible. This check exists to turn that constraint
	// violation into a readable error instead of a raw FK failure.
	if req.SkillVersionID != "" {
		exists, err := s.repo.SkillVersionExistsInAccount(ctx, owner.Account(), req.SkillVersionID)
		if err != nil {
			return nil, false, err
		}
		if !exists {
			return nil, false, invalidInputf("skill_version_id not found in this account")
		}
	}

	hash, err := hashEvent(req)
	if err != nil {
		return nil, false, err
	}
	occurredAt := time.Now().UTC()
	if req.OccurredAt != nil {
		occurredAt = req.OccurredAt.UTC()
	}

	event, created, err := s.repo.RecordEvent(ctx, owner, req, hash, occurredAt)
	if err != nil {
		return nil, false, err
	}
	if !created && event.ContentHash != hash {
		return nil, false, ErrIdempotencyConflict
	}
	return event, created, nil
}

func (s *Service) CreateEntry(ctx context.Context, owner ownership.Owner, req CreateEntryRequest) (*EntryDetail, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	normalizeEntryRequest(&req)
	now := time.Now().UTC()
	if err := validateEntryRequest(req, now); err != nil {
		return nil, err
	}

	if req.SourceEventID != nil && !hasEventEvidence(req.Evidence, *req.SourceEventID) {
		req.Evidence = append(req.Evidence, EvidenceInput{
			SourceType: "memory_event",
			SourceID:   *req.SourceEventID,
		})
	}
	if len(req.Evidence) == 0 {
		return nil, invalidInputf("at least one evidence item or source_event_id is required")
	}

	validFrom := now
	if req.ValidFrom != nil {
		validFrom = req.ValidFrom.UTC()
	}
	confidence := 0.5
	if req.Confidence != nil {
		confidence = *req.Confidence
	}
	importance := 0.5
	if req.Importance != nil {
		importance = *req.Importance
	}
	entry, err := s.repo.CreateEntry(ctx, owner, createEntryInput{
		CreateEntryRequest: req,
		ContentHash:        sha256Hex(req.Content),
		ValidFromValue:     validFrom,
		ConfidenceValue:    confidence,
		ImportanceValue:    importance,
		ExtractionMethod:   ExtractionExplicit,
	})
	if err != nil {
		return nil, err
	}
	entry.Indexing = s.indexEntry(ctx, owner, entry)
	return entry, nil
}

func (s *Service) GetEntry(ctx context.Context, accountID, id string) (*EntryDetail, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, invalidInputf("account id required")
	}
	if strings.TrimSpace(id) == "" {
		return nil, invalidInputf("memory id required")
	}
	entry, err := s.repo.GetEntry(ctx, accountID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return entry, err
}

func (s *Service) ListEntries(ctx context.Context, accountID string, params ListEntriesParams) ([]Entry, int, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, 0, invalidInputf("account id required")
	}
	normalizeListParams(&params)
	if err := validateListParams(params); err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountEntries(ctx, accountID, params)
	if err != nil {
		return nil, 0, err
	}
	items, err := s.repo.ListEntries(ctx, accountID, params)
	if err != nil {
		return nil, 0, err
	}
	return items, total, nil
}

func (s *Service) SearchEntries(ctx context.Context, owner ownership.Owner, req SearchEntriesRequest) (*SearchResponse, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	normalizeSearchRequest(&req)
	if err := validateSearchRequest(req); err != nil {
		return nil, err
	}

	filters := map[string]any{
		"source_type":     "memory_entry",
		"metadata.status": req.Status,
	}
	if req.ScopeType != "" {
		filters["metadata.scope_type"] = req.ScopeType
	}
	if req.ScopeKey != "" {
		filters["metadata.scope_key"] = req.ScopeKey
	}
	if req.MemoryType != "" {
		filters["metadata.memory_type"] = req.MemoryType
	}

	candidateLimit := req.TopK * 3
	if candidateLimit > 50 {
		candidateLimit = 50
	}
	results, err := s.retrieval.SearchHybrid(ctx, owner, retrieval.SearchRequest{
		Namespace: retrieval.NamespaceMemory,
		Query:     req.Query,
		TopK:      candidateLimit,
		Filters:   filters,
		Metadata: map[string]any{
			"feature":         "memory_search",
			"requested_top_k": req.TopK,
		},
	})
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	items := make([]SearchItem, 0, req.TopK)
	accessedIDs := make([]string, 0, req.TopK)
	for _, result := range results {
		if result.Document == nil || result.Document.SourceType != "memory_entry" {
			continue
		}
		entry, err := s.repo.GetEntry(ctx, owner.Account(), result.Document.SourceID)
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !entryMatchesSearch(entry.Entry, req, now) {
			continue
		}
		channels := searchChannels(result)
		items = append(items, SearchItem{
			Entry:     entry,
			Rank:      len(items) + 1,
			Score:     result.Score,
			Channels:  channels,
			HitReason: searchHitReason(entry.Entry, channels),
		})
		accessedIDs = append(accessedIDs, entry.ID)
		if len(items) == req.TopK {
			break
		}
	}
	_ = s.repo.IncrementAccess(ctx, owner.Account(), accessedIDs)
	return &SearchResponse{Items: items, Total: len(items)}, nil
}

func (s *Service) indexEntry(ctx context.Context, owner ownership.Owner, entry *EntryDetail) *IndexState {
	if s.retrieval == nil {
		return &IndexState{Status: "not_configured"}
	}
	metadata := map[string]any{
		"memory_id":         entry.ID,
		"scope_type":        entry.ScopeType,
		"scope_key":         entry.ScopeKey,
		"memory_type":       entry.MemoryType,
		"status":            entry.Status,
		"confidence":        entry.Confidence,
		"importance":        entry.Importance,
		"valid_from":        entry.ValidFrom.Format(time.RFC3339Nano),
		"extraction_method": entry.ExtractionMethod,
	}
	if entry.ValidTo != nil {
		metadata["valid_to"] = entry.ValidTo.Format(time.RFC3339Nano)
	}
	if entry.TTLAt != nil {
		metadata["ttl_at"] = entry.TTLAt.Format(time.RFC3339Nano)
	}
	if entry.SourceEventID != nil {
		metadata["source_event_id"] = *entry.SourceEventID
	}
	if len(entry.Metadata) > 0 {
		var attributes map[string]any
		if err := json.Unmarshal(entry.Metadata, &attributes); err == nil && len(attributes) > 0 {
			metadata["attributes"] = attributes
		}
	}

	document, err := s.retrieval.IndexDocument(ctx, owner, retrieval.UpsertDocumentInput{
		Namespace:  retrieval.NamespaceMemory,
		SourceType: "memory_entry",
		SourceID:   entry.ID,
		ChunkKey:   "current",
		Title:      entry.Title,
		Content:    memoryIndexContent(entry.Entry),
		Metadata:   metadata,
	})
	if err != nil {
		return &IndexState{Status: retrieval.StatusFailed, Error: err.Error()}
	}
	return &IndexState{Status: document.Status, DocumentID: document.ID}
}

func normalizeEventRequest(req *RecordEventRequest) {
	req.ScopeType = strings.ToLower(strings.TrimSpace(req.ScopeType))
	if req.ScopeType == "" {
		req.ScopeType = DefaultScopeType
	}
	req.ScopeKey = strings.TrimSpace(req.ScopeKey)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.EventType = strings.ToLower(strings.TrimSpace(req.EventType))
	req.SourceType = strings.ToLower(strings.TrimSpace(req.SourceType))
	req.SourceID = strings.TrimSpace(req.SourceID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	req.SkillVersionID = strings.TrimSpace(req.SkillVersionID)
	if req.OccurredAt != nil {
		value := req.OccurredAt.UTC()
		req.OccurredAt = &value
	}
	if req.Payload == nil {
		req.Payload = map[string]any{}
	}
}

func validateEventRequest(req RecordEventRequest) error {
	if !validScopeTypes[req.ScopeType] {
		return invalidInputf("invalid scope_type %q", req.ScopeType)
	}
	if req.ScopeType != DefaultScopeType && req.ScopeKey == "" {
		return invalidInputf("scope_key required for %s scope", req.ScopeType)
	}
	if !validEventTypes[req.EventType] {
		return invalidInputf("invalid event_type %q", req.EventType)
	}
	if req.IdempotencyKey == "" {
		return invalidInputf("idempotency_key required")
	}
	if len(req.IdempotencyKey) > 512 {
		return invalidInputf("idempotency_key must be at most 512 characters")
	}
	if req.SequenceNo != nil {
		if req.SessionID == "" {
			return invalidInputf("session_id required when sequence_no is set")
		}
		if *req.SequenceNo < 0 {
			return invalidInputf("sequence_no must not be negative")
		}
	}
	if (req.SourceType == "") != (req.SourceID == "") {
		return invalidInputf("source_type and source_id must be provided together")
	}
	return nil
}

func normalizeEntryRequest(req *CreateEntryRequest) {
	req.ScopeType = strings.ToLower(strings.TrimSpace(req.ScopeType))
	if req.ScopeType == "" {
		req.ScopeType = DefaultScopeType
	}
	req.ScopeKey = strings.TrimSpace(req.ScopeKey)
	req.MemoryType = strings.ToLower(strings.TrimSpace(req.MemoryType))
	req.Title = strings.TrimSpace(req.Title)
	req.Content = strings.TrimSpace(req.Content)
	req.Summary = strings.TrimSpace(req.Summary)
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	if req.Status == "" {
		req.Status = StatusActive
	}
	if req.SourceEventID != nil {
		value := strings.TrimSpace(*req.SourceEventID)
		req.SourceEventID = &value
	}
	for i := range req.Evidence {
		req.Evidence[i].SourceType = strings.ToLower(strings.TrimSpace(req.Evidence[i].SourceType))
		req.Evidence[i].SourceID = strings.TrimSpace(req.Evidence[i].SourceID)
		req.Evidence[i].Excerpt = strings.TrimSpace(req.Evidence[i].Excerpt)
	}
}

func validateEntryRequest(req CreateEntryRequest, now time.Time) error {
	if !validScopeTypes[req.ScopeType] {
		return invalidInputf("invalid scope_type %q", req.ScopeType)
	}
	if req.ScopeType != DefaultScopeType && req.ScopeKey == "" {
		return invalidInputf("scope_key required for %s scope", req.ScopeType)
	}
	if !validMemoryTypes[req.MemoryType] {
		return invalidInputf("invalid memory_type %q", req.MemoryType)
	}
	if req.Content == "" {
		return invalidInputf("content required")
	}
	if req.Confidence != nil && (*req.Confidence < 0 || *req.Confidence > 1) {
		return invalidInputf("confidence must be between 0 and 1")
	}
	if req.Importance != nil && (*req.Importance < 0 || *req.Importance > 1) {
		return invalidInputf("importance must be between 0 and 1")
	}
	if req.Status != StatusActive && req.Status != StatusPending {
		return invalidInputf("status must be active or pending when creating a memory")
	}
	if req.TTLAt != nil && !req.TTLAt.After(now) {
		return invalidInputf("ttl_at must be in the future")
	}
	effectiveValidFrom := now
	if req.ValidFrom != nil {
		effectiveValidFrom = *req.ValidFrom
	}
	if req.ValidTo != nil && req.ValidTo.Before(effectiveValidFrom) {
		return invalidInputf("valid_to must not be before valid_from")
	}
	if req.SourceEventID != nil && *req.SourceEventID == "" {
		return invalidInputf("source_event_id must not be empty")
	}
	for i, item := range req.Evidence {
		if item.SourceType == "" || item.SourceID == "" {
			return invalidInputf("evidence[%d] source_type and source_id are required", i)
		}
	}
	return nil
}

func normalizeListParams(params *ListEntriesParams) {
	params.ScopeType = strings.ToLower(strings.TrimSpace(params.ScopeType))
	params.ScopeKey = strings.TrimSpace(params.ScopeKey)
	params.MemoryType = strings.ToLower(strings.TrimSpace(params.MemoryType))
	params.Status = strings.ToLower(strings.TrimSpace(params.Status))
	if params.Limit <= 0 || params.Limit > MaxListLimit {
		params.Limit = DefaultListLimit
	}
	if params.Offset < 0 {
		params.Offset = 0
	}
}

func validateListParams(params ListEntriesParams) error {
	if params.ScopeType != "" && !validScopeTypes[params.ScopeType] {
		return invalidInputf("invalid scope_type %q", params.ScopeType)
	}
	if params.MemoryType != "" && !validMemoryTypes[params.MemoryType] {
		return invalidInputf("invalid memory_type %q", params.MemoryType)
	}
	if params.Status != "" && !validStatuses[params.Status] {
		return invalidInputf("invalid status %q", params.Status)
	}
	return nil
}

func hashEvent(req RecordEventRequest) (string, error) {
	canonical := struct {
		ScopeType  string         `json:"scope_type"`
		ScopeKey   string         `json:"scope_key"`
		SessionID  string         `json:"session_id"`
		SequenceNo *int64         `json:"sequence_no"`
		EventType  string         `json:"event_type"`
		Payload    map[string]any `json:"payload"`
		SourceType string         `json:"source_type"`
		SourceID   string         `json:"source_id"`
		OccurredAt *time.Time     `json:"occurred_at,omitempty"`
		// Attribution is part of the event's identity. Leaving it out of the
		// hash would let a replay that adds or changes skill_version_id return
		// the original unattributed row, so the caller would believe the
		// attribution landed when it silently did not.
		SkillVersionID string `json:"skill_version_id,omitempty"`
	}{
		ScopeType: req.ScopeType, ScopeKey: req.ScopeKey, SessionID: req.SessionID,
		SequenceNo: req.SequenceNo, EventType: req.EventType, Payload: req.Payload,
		SourceType: req.SourceType, SourceID: req.SourceID, OccurredAt: req.OccurredAt,
		SkillVersionID: req.SkillVersionID,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal event for hashing: %w", err)
	}
	return sha256Hex(string(encoded)), nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func hasEventEvidence(items []EvidenceInput, eventID string) bool {
	for _, item := range items {
		if item.SourceType == "memory_event" && item.SourceID == eventID {
			return true
		}
	}
	return false
}

func validateOwner(owner ownership.Owner) error {
	if strings.TrimSpace(owner.Account()) == "" {
		return invalidInputf("account id required")
	}
	if strings.TrimSpace(owner.UserID) == "" {
		return invalidInputf("user id required")
	}
	return nil
}

func normalizeSearchRequest(req *SearchEntriesRequest) {
	req.Query = strings.TrimSpace(req.Query)
	req.ScopeType = strings.ToLower(strings.TrimSpace(req.ScopeType))
	req.ScopeKey = strings.TrimSpace(req.ScopeKey)
	req.MemoryType = strings.ToLower(strings.TrimSpace(req.MemoryType))
	req.Status = strings.ToLower(strings.TrimSpace(req.Status))
	if req.Status == "" {
		req.Status = StatusActive
	}
	if req.TopK <= 0 || req.TopK > 20 {
		req.TopK = 8
	}
}

func validateSearchRequest(req SearchEntriesRequest) error {
	if req.Query == "" {
		return invalidInputf("query required")
	}
	if req.ScopeType != "" && !validScopeTypes[req.ScopeType] {
		return invalidInputf("invalid scope_type %q", req.ScopeType)
	}
	if req.ScopeKey != "" && req.ScopeType == "" {
		return invalidInputf("scope_type required when scope_key is set")
	}
	if req.MemoryType != "" && !validMemoryTypes[req.MemoryType] {
		return invalidInputf("invalid memory_type %q", req.MemoryType)
	}
	if !validStatuses[req.Status] {
		return invalidInputf("invalid status %q", req.Status)
	}
	return nil
}

func entryMatchesSearch(entry Entry, req SearchEntriesRequest, now time.Time) bool {
	if entry.Status != req.Status {
		return false
	}
	if req.ScopeType != "" && entry.ScopeType != req.ScopeType {
		return false
	}
	if req.ScopeKey != "" && entry.ScopeKey != req.ScopeKey {
		return false
	}
	if req.MemoryType != "" && entry.MemoryType != req.MemoryType {
		return false
	}
	if entry.ValidFrom.After(now) || (entry.ValidTo != nil && !entry.ValidTo.After(now)) {
		return false
	}
	return entry.TTLAt == nil || entry.TTLAt.After(now)
}

func searchChannels(result retrieval.SearchResult) []string {
	if raw, ok := result.Payload["channels"].([]string); ok {
		return raw
	}
	if raw, ok := result.Payload["channels"].([]any); ok {
		channels := make([]string, 0, len(raw))
		for _, value := range raw {
			if channel, ok := value.(string); ok {
				channels = append(channels, channel)
			}
		}
		return channels
	}
	if result.Stage == "" {
		return []string{}
	}
	return strings.Split(result.Stage, "+")
}

func searchHitReason(entry Entry, channels []string) string {
	reason := strings.Join(channels, "+") + " match"
	if entry.ScopeType != DefaultScopeType {
		reason += " in " + entry.ScopeType + ":" + entry.ScopeKey
	}
	return reason
}

func memoryIndexContent(entry Entry) string {
	parts := make([]string, 0, 3)
	if entry.Title != "" {
		parts = append(parts, entry.Title)
	}
	if entry.Summary != "" {
		parts = append(parts, entry.Summary)
	}
	parts = append(parts, entry.Content)
	return strings.Join(parts, "\n\n")
}

func invalidInputf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}

var validScopeTypes = map[string]bool{
	"global": true, "project": true, "repository": true, "agent": true, "session": true,
}

var validEventTypes = map[string]bool{
	"goal": true, "observation": true, "action": true, "decision": true, "issue": true,
	"attempt": true, "outcome": true, "correction": true, "checkpoint": true, "note": true,
}

var validMemoryTypes = map[string]bool{
	"semantic": true, "episodic": true, "procedural": true,
}

var validStatuses = map[string]bool{
	StatusPending: true, StatusActive: true, StatusSuperseded: true,
	StatusInvalidated: true, StatusArchived: true, StatusExpired: true,
}

// ─── M1: attribution ───

// SessionTimeline returns skill executions and memory events for one session or
// one skill version, time-ordered.
//
// Requiring at least one anchor is deliberate: an unfiltered timeline over a
// whole account is not attribution, it is a data dump, and it would grow
// unbounded with usage.
func (s *Service) SessionTimeline(ctx context.Context, owner ownership.Owner, params SessionTimelineParams) (*SessionTimelineResponse, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	params.SessionID = strings.TrimSpace(params.SessionID)
	params.SkillVersionID = strings.TrimSpace(params.SkillVersionID)
	if params.SessionID == "" && params.SkillVersionID == "" {
		return nil, invalidInputf("session_id or skill_version_id required")
	}
	if params.Limit < 0 {
		return nil, invalidInputf("limit must not be negative")
	}
	if params.Limit == 0 {
		params.Limit = 200
	}
	if params.Limit > 500 {
		return nil, invalidInputf("limit must be at most 500")
	}

	items, err := s.repo.SessionTimeline(ctx, owner.Account(), params)
	if err != nil {
		return nil, err
	}
	response := &SessionTimelineResponse{
		SessionID: params.SessionID,
		Items:     items,
		Total:     len(items),
		// A full page means later activity may exist beyond it. Reporting this
		// matters for attribution: a conclusion drawn from a truncated timeline
		// can be wrong, and the caller has no other way to know.
		Truncated: len(items) == params.Limit,
	}
	for _, item := range items {
		switch item.Kind {
		case TimelineKindSkillLog:
			response.SkillLogCount++
		case TimelineKindMemoryEvent:
			response.MemoryEventCount++
		}
		if !item.Attributed {
			response.UnattributedCount++
		}
	}
	return response, nil
}

// EntryAttribution resolves which skill execution produced a durable memory,
// and includes the surrounding session activity when a session is known.
func (s *Service) EntryAttribution(ctx context.Context, owner ownership.Owner, entryID string) (*EntryAttribution, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	entryID = strings.TrimSpace(entryID)
	if entryID == "" {
		return nil, invalidInputf("entry id required")
	}

	attribution, err := s.repo.GetEntryAttribution(ctx, owner.Account(), entryID)
	if err != nil {
		return nil, err
	}

	switch {
	case attribution.SkillVersionID != nil && *attribution.SkillVersionID != "":
		attribution.Resolution = "skill_version"
	case attribution.SessionID != "":
		// The event exists and names a session, but no skill version was
		// recorded. Session scope is as far as this chain goes.
		attribution.Resolution = "session_only"
	case attribution.SourceEventID != nil:
		attribution.Resolution = "event_only"
	default:
		// No source event at all: the memory was written directly rather than
		// derived from journaled activity.
		attribution.Resolution = "none"
	}

	if attribution.SessionID != "" {
		timeline, err := s.repo.SessionTimeline(ctx, owner.Account(), SessionTimelineParams{
			SessionID: attribution.SessionID,
			Limit:     50,
		})
		if err != nil {
			return nil, err
		}
		attribution.SessionTimeline = timeline
	}
	return attribution, nil
}
