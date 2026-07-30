package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/retrieval"
	"github.com/jackc/pgx/v5"
)

// memoryEntrySourceType is the retrieval source_type for durable memories.
const memoryEntrySourceType = "memory_entry"

var (
	ErrNotFound            = errors.New("memory entry not found")
	ErrInvalidInput        = errors.New("invalid memory input")
	ErrIdempotencyConflict = errors.New("idempotency key already used with different event content")
	// ErrSupersedeConflict covers the two ways a supersede request contradicts
	// existing state: the target is already replaced by a different entry, or the
	// pair would close a cycle. Both are the caller's problem, not a server
	// fault, so they must not surface as 500.
	ErrSupersedeConflict = errors.New("supersede conflicts with existing state")
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
		"source_type":     memoryEntrySourceType,
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
	accessedIDs := make([]string, 0, candidateLimit)
	for _, result := range results {
		if result.Document == nil || result.Document.SourceType != memoryEntrySourceType {
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
		adjustment := feedbackAdjustment(entry.Entry)
		items = append(items, SearchItem{
			Entry:              entry,
			Rank:               len(items) + 1,
			Score:              clampScore(result.Score + adjustment),
			RetrievalScore:     result.Score,
			FeedbackAdjustment: adjustment,
			Channels:           channels,
			HitReason:          searchHitReason(entry.Entry, channels),
		})
		accessedIDs = append(accessedIDs, entry.ID)
		// Take more candidates than requested: the feedback adjustment can
		// reorder them, so truncating to top_k before adjusting would discard a
		// memory that should have ranked higher.
		if len(items) == candidateLimit {
			break
		}
	}

	// Re-sort by adjusted score, then trim. Ranks are assigned after sorting so
	// they describe the delivered order rather than the retrieval order.
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	if len(items) > req.TopK {
		items = items[:req.TopK]
	}
	for index := range items {
		items[index].Rank = index + 1
	}

	accessed := make([]string, 0, len(items))
	for _, item := range items {
		accessed = append(accessed, item.Entry.ID)
	}
	_ = s.repo.IncrementAccess(ctx, owner.Account(), accessed)
	return &SearchResponse{Items: items, Total: len(items)}, nil
}

// clampScore keeps the adjusted score inside the normalised 0..1 range so a
// boosted entry cannot report a score the fusion scale does not define.
func clampScore(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1 {
		return 1
	}
	return value
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
		SourceType: memoryEntrySourceType,
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

// ─── M3: supersede ───

// SupersedeEntry records that one durable memory replaces another.
//
// Marking the old entry superseded is not enough on its own: search draws
// candidates from the retrieval projection and only then filters by status, so a
// replaced entry would keep consuming top-k slots and crowd out the replacement.
// Its projection is therefore removed as well. The projection is derived data,
// rebuildable from memory_entries, so deleting it loses nothing.
func (s *Service) SupersedeEntry(ctx context.Context, owner ownership.Owner, req SupersedeRequest) (*SupersedeResponse, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	req.SupersedingID = strings.TrimSpace(req.SupersedingID)
	req.SupersededID = strings.TrimSpace(req.SupersededID)
	if req.SupersedingID == "" || req.SupersededID == "" {
		return nil, invalidInputf("superseding_id and superseded_id are required")
	}
	if req.SupersedingID == req.SupersededID {
		return nil, invalidInputf("a memory entry cannot supersede itself")
	}

	superseding, superseded, err := s.repo.SupersedeEntry(ctx, owner, req.SupersedingID, req.SupersededID, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	response := &SupersedeResponse{Superseding: superseding, Superseded: superseded}
	if s.retrieval != nil {
		removed, deleteErr := s.retrieval.DeleteDocumentsByMetadata(ctx, owner,
			retrieval.NamespaceMemory, memoryEntrySourceType,
			map[string]any{"memory_id": superseded.ID}, nil)
		if deleteErr != nil {
			// The supersede itself is committed and correct. Report the stale
			// projection instead of failing: a caller that sees this can re-run
			// indexing, whereas an error would suggest the supersede did not
			// happen.
			response.Warning = "superseded entry remains in the retrieval projection: " + deleteErr.Error()
		} else {
			response.ProjectionRemoved = removed
		}
	}
	return response, nil
}

// ─── M3: feedback ───

var validSignals = map[string]bool{SignalUseful: true, SignalHarmful: true}

// RecordFeedback stores a usefulness signal for a durable memory.
//
// This is the memory side of validation: the platform learns which remembered
// experience actually helped from how it was used, not from asking. Signals feed
// ranking and, later, promotion proposals — never automatic rewrites of the
// memory itself.
func (s *Service) RecordFeedback(ctx context.Context, owner ownership.Owner, req FeedbackRequest) (*FeedbackResponse, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	req.MemoryID = strings.TrimSpace(req.MemoryID)
	req.Signal = strings.ToLower(strings.TrimSpace(req.Signal))
	req.Reason = strings.TrimSpace(req.Reason)
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.SkillVersionID = strings.TrimSpace(req.SkillVersionID)

	if req.MemoryID == "" {
		return nil, invalidInputf("memory_id required")
	}
	if !validSignals[req.Signal] {
		return nil, invalidInputf("signal must be %s or %s", SignalUseful, SignalHarmful)
	}
	if utf8.RuneCountInString(req.Reason) > 2000 {
		return nil, invalidInputf("reason must be at most 2000 characters")
	}
	if req.SkillVersionID != "" {
		exists, err := s.repo.SkillVersionExistsInAccount(ctx, owner.Account(), req.SkillVersionID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, invalidInputf("skill_version_id not found in this account")
		}
	}

	feedback, created, err := s.repo.RecordFeedback(ctx, owner, req, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return &FeedbackResponse{Feedback: feedback, Created: created}, nil
}

// ListFeedback returns the signal log for one entry. The log is the durable
// record; the counters on the entry are a projection of it.
func (s *Service) ListFeedback(ctx context.Context, owner ownership.Owner, memoryID string, limit int) ([]Feedback, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	memoryID = strings.TrimSpace(memoryID)
	if memoryID == "" {
		return nil, invalidInputf("memory id required")
	}
	// Ownership check first: a missing entry must not look like an empty log.
	if _, err := s.repo.GetEntry(ctx, owner.Account(), memoryID); err != nil {
		return nil, err
	}
	return s.repo.ListFeedback(ctx, owner.Account(), memoryID, limit)
}

// feedbackAdjustment nudges a search score by the entry's recorded usefulness.
//
// The adjustment is deliberately small and bounded. Feedback is a weak, biased
// signal — only some sessions report it, and negative experience is reported
// more readily than positive — so letting it dominate would turn a few clicks
// into a permanent ranking verdict. It breaks ties and demotes memories that
// were repeatedly harmful; it does not reorder semantically better matches
// below worse ones.
func feedbackAdjustment(entry Entry) float64 {
	total := entry.UsefulCount + entry.HarmfulCount
	if total == 0 {
		return 0
	}
	ratio := float64(entry.UsefulCount-entry.HarmfulCount) / float64(total)
	// Confidence grows with evidence: one signal moves the score far less than
	// ten do, so a single stray click cannot bury an otherwise good memory.
	weight := float64(total) / float64(total+3)
	return maxFeedbackAdjustment * ratio * weight
}

// maxFeedbackAdjustment caps the influence of feedback on the fused score, whose
// normalised range is 0..1.
const maxFeedbackAdjustment = 0.15

// ─── M3: checkpoint ───

const checkpointEventType = "checkpoint"

// SaveCheckpoint appends a resumable snapshot of session intent to the journal.
//
// It reuses RecordEvent rather than writing the event directly, so a checkpoint
// inherits the journal's guarantees for free: immutability, ordering, idempotency
// and skill attribution. The default idempotency key is derived from the content,
// which makes saving unchanged state a no-op instead of appending near-duplicate
// snapshots.
func (s *Service) SaveCheckpoint(ctx context.Context, owner ownership.Owner, req SaveCheckpointRequest) (*SaveCheckpointResponse, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	req.SessionID = strings.TrimSpace(req.SessionID)
	req.Goal = strings.TrimSpace(req.Goal)
	req.Label = strings.TrimSpace(req.Label)
	req.Notes = strings.TrimSpace(req.Notes)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)

	if req.SessionID == "" {
		return nil, invalidInputf("session_id required")
	}
	if req.Goal == "" {
		// A checkpoint without a goal cannot be resumed from: the lists alone do
		// not say what the session was trying to achieve.
		return nil, invalidInputf("goal required")
	}
	req.Done = trimList(req.Done)
	req.Next = trimList(req.Next)
	req.Open = trimList(req.Open)

	payload := map[string]any{"goal": req.Goal}
	if req.Label != "" {
		payload["label"] = req.Label
	}
	if len(req.Done) > 0 {
		payload["done"] = req.Done
	}
	if len(req.Next) > 0 {
		payload["next"] = req.Next
	}
	if len(req.Open) > 0 {
		payload["open"] = req.Open
	}
	if req.Notes != "" {
		payload["notes"] = req.Notes
	}

	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("marshal checkpoint payload: %w", err)
		}
		idempotencyKey = "checkpoint:" + req.SessionID + ":" + sha256Hex(string(encoded))
	}

	event, created, err := s.RecordEvent(ctx, owner, RecordEventRequest{
		ScopeType:      req.ScopeType,
		ScopeKey:       req.ScopeKey,
		SessionID:      req.SessionID,
		EventType:      checkpointEventType,
		Payload:        payload,
		SkillVersionID: req.SkillVersionID,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	checkpoint, err := checkpointFromEvent(event)
	if err != nil {
		return nil, err
	}
	return &SaveCheckpointResponse{Checkpoint: checkpoint, Created: created}, nil
}

// Resume returns the latest checkpoint plus whatever happened after it.
//
// Returning the snapshot alone would be wrong: a session is interrupted *after*
// its last checkpoint, so the activity in between is exactly the state the agent
// is missing. Resolution distinguishes a session that was never checkpointed from
// one that never started.
func (s *Service) Resume(ctx context.Context, owner ownership.Owner, sessionID string) (*ResumeResponse, error) {
	if err := validateOwner(owner); err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, invalidInputf("session_id required")
	}

	event, err := s.repo.LatestCheckpoint(ctx, owner.Account(), sessionID)
	if err != nil {
		return nil, err
	}

	response := &ResumeResponse{SessionID: sessionID, SinceCheckpoint: []TimelineItem{}}
	if event == nil {
		timeline, err := s.repo.SessionTimeline(ctx, owner.Account(), SessionTimelineParams{
			SessionID: sessionID,
			Limit:     50,
		})
		if err != nil {
			return nil, err
		}
		response.SinceCheckpoint = timeline
		if len(timeline) == 0 {
			response.Resolution = "empty"
		} else {
			response.Resolution = "journal_only"
		}
		return response, nil
	}

	checkpoint, err := checkpointFromEvent(event)
	if err != nil {
		return nil, err
	}
	since, err := s.repo.TimelineSince(ctx, owner.Account(), sessionID, event.OccurredAt, 200)
	if err != nil {
		return nil, err
	}
	response.Checkpoint = checkpoint
	response.SinceCheckpoint = since
	response.Resolution = "checkpoint"
	return response, nil
}

func checkpointFromEvent(event *Event) (*Checkpoint, error) {
	var payload struct {
		Label string   `json:"label"`
		Goal  string   `json:"goal"`
		Done  []string `json:"done"`
		Next  []string `json:"next"`
		Open  []string `json:"open"`
		Notes string   `json:"notes"`
	}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode checkpoint payload: %w", err)
		}
	}
	return &Checkpoint{
		EventID:        event.ID,
		SessionID:      event.SessionID,
		ScopeType:      event.ScopeType,
		ScopeKey:       event.ScopeKey,
		Label:          payload.Label,
		Goal:           payload.Goal,
		Done:           payload.Done,
		Next:           payload.Next,
		Open:           payload.Open,
		Notes:          payload.Notes,
		SkillVersionID: event.SkillVersionID,
		OccurredAt:     event.OccurredAt,
	}, nil
}

func trimList(values []string) []string {
	trimmed := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			trimmed = append(trimmed, value)
		}
	}
	return trimmed
}
