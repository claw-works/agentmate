package memory

import (
	"testing"
	"time"
)

func TestEventHashIsStableAcrossPayloadOrder(t *testing.T) {
	occurredAt := time.Unix(100, 0)
	first := RecordEventRequest{
		ScopeType:  "project",
		ScopeKey:   "agentmate",
		EventType:  "decision",
		Payload:    map[string]any{"port": float64(26001), "mode": "dev"},
		OccurredAt: &occurredAt,
	}
	second := RecordEventRequest{
		ScopeType:  "project",
		ScopeKey:   "agentmate",
		EventType:  "decision",
		Payload:    map[string]any{"mode": "dev", "port": float64(26001)},
		OccurredAt: &occurredAt,
	}

	firstHash, err := hashEvent(first)
	if err != nil {
		t.Fatalf("hashEvent(first): %v", err)
	}
	secondHash, err := hashEvent(second)
	if err != nil {
		t.Fatalf("hashEvent(second): %v", err)
	}
	if firstHash != secondHash {
		t.Fatalf("hashes differ: %q != %q", firstHash, secondHash)
	}
}

func TestEventHashIncludesExplicitOccurredAt(t *testing.T) {
	first := RecordEventRequest{EventType: "note", OccurredAt: timePointer(time.Unix(100, 0))}
	second := RecordEventRequest{EventType: "note", OccurredAt: timePointer(time.Unix(200, 0))}
	firstHash, err := hashEvent(first)
	if err != nil {
		t.Fatalf("hashEvent(first): %v", err)
	}
	secondHash, err := hashEvent(second)
	if err != nil {
		t.Fatalf("hashEvent(second): %v", err)
	}
	if firstHash == secondHash {
		t.Fatal("explicit occurred_at must affect the event hash")
	}
}

func TestNormalizeAndValidateEventRequest(t *testing.T) {
	sequence := int64(3)
	req := RecordEventRequest{
		ScopeType:      " Repository ",
		ScopeKey:       " claw-works/agentmate ",
		SessionID:      " session-1 ",
		SequenceNo:     &sequence,
		EventType:      " Decision ",
		IdempotencyKey: " event-3 ",
	}
	normalizeEventRequest(&req)
	if err := validateEventRequest(req); err != nil {
		t.Fatalf("validateEventRequest: %v", err)
	}
	if req.ScopeType != "repository" || req.EventType != "decision" {
		t.Fatalf("request was not normalized: %#v", req)
	}
	if req.Payload == nil {
		t.Fatal("payload should default to an empty object")
	}
}

func TestValidateEventRequiresSessionForSequence(t *testing.T) {
	sequence := int64(1)
	req := RecordEventRequest{
		ScopeType:      DefaultScopeType,
		EventType:      "note",
		SequenceNo:     &sequence,
		IdempotencyKey: "event-1",
		Payload:        map[string]any{},
	}
	if err := validateEventRequest(req); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateEntryRejectsNegativeConfidence(t *testing.T) {
	confidence := -0.1
	req := CreateEntryRequest{
		MemoryType: "semantic",
		Content:    "Production port is 26001.",
		Confidence: &confidence,
	}
	normalizeEntryRequest(&req)
	if err := validateEntryRequest(req, time.Now()); err == nil {
		t.Fatal("expected negative confidence validation error")
	}
}

func TestNormalizeEntryAddsDefaults(t *testing.T) {
	req := CreateEntryRequest{
		MemoryType: " SEMANTIC ",
		Content:    " Production port is 26001. ",
	}
	normalizeEntryRequest(&req)
	if req.ScopeType != DefaultScopeType || req.Status != StatusActive {
		t.Fatalf("unexpected defaults: scope=%q status=%q", req.ScopeType, req.Status)
	}
	if req.Confidence != nil || req.Importance != nil {
		t.Fatalf("scores should remain unset: confidence=%v importance=%v", req.Confidence, req.Importance)
	}
	if req.Content != "Production port is 26001." {
		t.Fatalf("content = %q", req.Content)
	}
}

func TestEntryMatchesSearchEnforcesValidityAndTTL(t *testing.T) {
	now := time.Now()
	req := SearchEntriesRequest{Status: StatusActive}
	entry := Entry{
		Status:    StatusActive,
		ValidFrom: now.Add(-time.Hour),
	}
	if !entryMatchesSearch(entry, req, now) {
		t.Fatal("active valid entry should match")
	}

	expired := now.Add(-time.Minute)
	entry.TTLAt = &expired
	if entryMatchesSearch(entry, req, now) {
		t.Fatal("expired entry should not match")
	}

	entry.TTLAt = nil
	future := now.Add(time.Hour)
	entry.ValidFrom = future
	if entryMatchesSearch(entry, req, now) {
		t.Fatal("not-yet-valid entry should not match")
	}
}

func TestNormalizeSearchRequestDefaults(t *testing.T) {
	req := SearchEntriesRequest{Query: " deployment failure "}
	normalizeSearchRequest(&req)
	if req.Query != "deployment failure" || req.TopK != 8 || req.Status != StatusActive {
		t.Fatalf("unexpected normalized search request: %#v", req)
	}
}

func timePointer(value time.Time) *time.Time {
	return &value
}
