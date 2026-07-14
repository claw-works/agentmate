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
)

var (
	ErrNotFound            = errors.New("memory entry not found")
	ErrIdempotencyConflict = errors.New("idempotency key already used with different event content")
)

type Service struct {
	repo *Repo
}

func NewService(repo *Repo) *Service {
	return &Service{repo: repo}
}

func (s *Service) RecordEvent(ctx context.Context, owner ownership.Owner, req RecordEventRequest) (*Event, bool, error) {
	if err := validateOwner(owner); err != nil {
		return nil, false, err
	}
	normalizeEventRequest(&req)
	if err := validateEventRequest(req); err != nil {
		return nil, false, err
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
		return nil, fmt.Errorf("at least one evidence item or source_event_id is required")
	}

	validFrom := now
	if req.ValidFrom != nil {
		validFrom = req.ValidFrom.UTC()
	}
	return s.repo.CreateEntry(ctx, owner, createEntryInput{
		CreateEntryRequest: req,
		ContentHash:        sha256Hex(req.Content),
		ValidFromValue:     validFrom,
		ExtractionMethod:   ExtractionExplicit,
	})
}

func (s *Service) GetEntry(ctx context.Context, accountID, id string) (*EntryDetail, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, fmt.Errorf("account id required")
	}
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("memory id required")
	}
	entry, err := s.repo.GetEntry(ctx, accountID, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return entry, err
}

func (s *Service) ListEntries(ctx context.Context, accountID string, params ListEntriesParams) ([]Entry, int, error) {
	if strings.TrimSpace(accountID) == "" {
		return nil, 0, fmt.Errorf("account id required")
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
		return fmt.Errorf("invalid scope_type %q", req.ScopeType)
	}
	if req.ScopeType != DefaultScopeType && req.ScopeKey == "" {
		return fmt.Errorf("scope_key required for %s scope", req.ScopeType)
	}
	if !validEventTypes[req.EventType] {
		return fmt.Errorf("invalid event_type %q", req.EventType)
	}
	if req.IdempotencyKey == "" {
		return fmt.Errorf("idempotency_key required")
	}
	if len(req.IdempotencyKey) > 512 {
		return fmt.Errorf("idempotency_key must be at most 512 characters")
	}
	if req.SequenceNo != nil {
		if req.SessionID == "" {
			return fmt.Errorf("session_id required when sequence_no is set")
		}
		if *req.SequenceNo < 0 {
			return fmt.Errorf("sequence_no must not be negative")
		}
	}
	if (req.SourceType == "") != (req.SourceID == "") {
		return fmt.Errorf("source_type and source_id must be provided together")
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
	if req.Confidence == 0 {
		req.Confidence = 0.5
	}
	if req.Importance == 0 {
		req.Importance = 0.5
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
		return fmt.Errorf("invalid scope_type %q", req.ScopeType)
	}
	if req.ScopeType != DefaultScopeType && req.ScopeKey == "" {
		return fmt.Errorf("scope_key required for %s scope", req.ScopeType)
	}
	if !validMemoryTypes[req.MemoryType] {
		return fmt.Errorf("invalid memory_type %q", req.MemoryType)
	}
	if req.Content == "" {
		return fmt.Errorf("content required")
	}
	if req.Confidence < 0 || req.Confidence > 1 {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if req.Importance < 0 || req.Importance > 1 {
		return fmt.Errorf("importance must be between 0 and 1")
	}
	if req.Status != StatusActive && req.Status != StatusPending {
		return fmt.Errorf("status must be active or pending when creating a memory")
	}
	if req.TTLAt != nil && !req.TTLAt.After(now) {
		return fmt.Errorf("ttl_at must be in the future")
	}
	effectiveValidFrom := now
	if req.ValidFrom != nil {
		effectiveValidFrom = *req.ValidFrom
	}
	if req.ValidTo != nil && req.ValidTo.Before(effectiveValidFrom) {
		return fmt.Errorf("valid_to must not be before valid_from")
	}
	if req.SourceEventID != nil && *req.SourceEventID == "" {
		return fmt.Errorf("source_event_id must not be empty")
	}
	for i, item := range req.Evidence {
		if item.SourceType == "" || item.SourceID == "" {
			return fmt.Errorf("evidence[%d] source_type and source_id are required", i)
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
		return fmt.Errorf("invalid scope_type %q", params.ScopeType)
	}
	if params.MemoryType != "" && !validMemoryTypes[params.MemoryType] {
		return fmt.Errorf("invalid memory_type %q", params.MemoryType)
	}
	if params.Status != "" && !validStatuses[params.Status] {
		return fmt.Errorf("invalid status %q", params.Status)
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
	}{
		ScopeType: req.ScopeType, ScopeKey: req.ScopeKey, SessionID: req.SessionID,
		SequenceNo: req.SequenceNo, EventType: req.EventType, Payload: req.Payload,
		SourceType: req.SourceType, SourceID: req.SourceID, OccurredAt: req.OccurredAt,
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
		return fmt.Errorf("account id required")
	}
	if strings.TrimSpace(owner.UserID) == "" {
		return fmt.Errorf("user id required")
	}
	return nil
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
