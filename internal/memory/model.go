package memory

import (
	"encoding/json"
	"time"
)

const (
	DefaultScopeType = "global"
	DefaultListLimit = 20
	MaxListLimit     = 100

	StatusPending      = "pending"
	StatusActive       = "active"
	StatusSuperseded   = "superseded"
	StatusInvalidated  = "invalidated"
	StatusArchived     = "archived"
	StatusExpired      = "expired"
	ExtractionExplicit = "explicit"
)

type Event struct {
	ID             string          `json:"id"`
	AccountID      string          `json:"account_id"`
	UserID         string          `json:"user_id"`
	KeyID          *string         `json:"key_id,omitempty"`
	ScopeType      string          `json:"scope_type"`
	ScopeKey       string          `json:"scope_key"`
	SessionID      string          `json:"session_id"`
	SequenceNo     *int64          `json:"sequence_no,omitempty"`
	EventType      string          `json:"event_type"`
	Payload        json.RawMessage `json:"payload"`
	SourceType     string          `json:"source_type"`
	SourceID       string          `json:"source_id"`
	OccurredAt     time.Time       `json:"occurred_at"`
	IdempotencyKey string          `json:"idempotency_key"`
	ContentHash    string          `json:"content_hash"`
	CreatedAt      time.Time       `json:"created_at"`
}

type RecordEventRequest struct {
	ScopeType      string         `json:"scope_type"`
	ScopeKey       string         `json:"scope_key"`
	SessionID      string         `json:"session_id"`
	SequenceNo     *int64         `json:"sequence_no"`
	EventType      string         `json:"event_type"`
	Payload        map[string]any `json:"payload"`
	SourceType     string         `json:"source_type"`
	SourceID       string         `json:"source_id"`
	OccurredAt     *time.Time     `json:"occurred_at"`
	IdempotencyKey string         `json:"idempotency_key"`
}

type Entry struct {
	ID               string          `json:"id"`
	AccountID        string          `json:"account_id"`
	UserID           string          `json:"user_id"`
	KeyID            *string         `json:"key_id,omitempty"`
	ScopeType        string          `json:"scope_type"`
	ScopeKey         string          `json:"scope_key"`
	MemoryType       string          `json:"memory_type"`
	Title            string          `json:"title"`
	Content          string          `json:"content"`
	Summary          string          `json:"summary"`
	ContentHash      string          `json:"content_hash"`
	Confidence       float64         `json:"confidence"`
	Importance       float64         `json:"importance"`
	Status           string          `json:"status"`
	Metadata         json.RawMessage `json:"metadata"`
	TTLAt            *time.Time      `json:"ttl_at,omitempty"`
	LastAccessedAt   *time.Time      `json:"last_accessed_at,omitempty"`
	ValidFrom        time.Time       `json:"valid_from"`
	ValidTo          *time.Time      `json:"valid_to,omitempty"`
	SupersededBy     *string         `json:"superseded_by,omitempty"`
	SourceEventID    *string         `json:"source_event_id,omitempty"`
	ExtractionMethod string          `json:"extraction_method"`
	ExtractorVersion string          `json:"extractor_version"`
	AccessCount      int64           `json:"access_count"`
	UsefulCount      int64           `json:"useful_count"`
	HarmfulCount     int64           `json:"harmful_count"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type Evidence struct {
	ID         string          `json:"id"`
	AccountID  string          `json:"account_id"`
	UserID     string          `json:"user_id"`
	KeyID      *string         `json:"key_id,omitempty"`
	MemoryID   string          `json:"memory_id"`
	SourceType string          `json:"source_type"`
	SourceID   string          `json:"source_id"`
	Excerpt    string          `json:"excerpt"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}

type EvidenceInput struct {
	SourceType string         `json:"source_type"`
	SourceID   string         `json:"source_id"`
	Excerpt    string         `json:"excerpt"`
	Metadata   map[string]any `json:"metadata"`
}

type CreateEntryRequest struct {
	ScopeType     string          `json:"scope_type"`
	ScopeKey      string          `json:"scope_key"`
	MemoryType    string          `json:"memory_type"`
	Title         string          `json:"title"`
	Content       string          `json:"content"`
	Summary       string          `json:"summary"`
	Confidence    float64         `json:"confidence"`
	Importance    float64         `json:"importance"`
	Status        string          `json:"status"`
	Metadata      map[string]any  `json:"metadata"`
	TTLAt         *time.Time      `json:"ttl_at"`
	ValidFrom     *time.Time      `json:"valid_from"`
	ValidTo       *time.Time      `json:"valid_to"`
	SourceEventID *string         `json:"source_event_id"`
	Evidence      []EvidenceInput `json:"evidence"`
}

type EntryDetail struct {
	Entry
	Evidence []Evidence `json:"evidence"`
}

type ListEntriesParams struct {
	ScopeType  string
	ScopeKey   string
	MemoryType string
	Status     string
	Limit      int
	Offset     int
}

type createEntryInput struct {
	CreateEntryRequest
	ContentHash      string
	ValidFromValue   time.Time
	ExtractionMethod string
	ExtractorVersion string
}
