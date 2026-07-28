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
	// SkillVersionID attributes the event to the skill execution that produced
	// it. Absent when the event has no skill origin, for example a note the
	// user wrote directly.
	SkillVersionID *string   `json:"skill_version_id,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
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
	// SkillVersionID is optional. When set it must belong to the caller's
	// account; the server verifies this rather than trusting the client.
	SkillVersionID string `json:"skill_version_id"`
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
	Confidence    *float64        `json:"confidence"`
	Importance    *float64        `json:"importance"`
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
	Evidence []Evidence  `json:"evidence"`
	Indexing *IndexState `json:"indexing,omitempty"`
}

type IndexState struct {
	Status     string `json:"status"`
	DocumentID string `json:"document_id,omitempty"`
	Error      string `json:"error,omitempty"`
}

type ListEntriesParams struct {
	ScopeType  string
	ScopeKey   string
	MemoryType string
	Status     string
	Limit      int
	Offset     int
}

type SearchEntriesRequest struct {
	Query      string `json:"query"`
	TopK       int    `json:"top_k"`
	ScopeType  string `json:"scope_type"`
	ScopeKey   string `json:"scope_key"`
	MemoryType string `json:"memory_type"`
	Status     string `json:"status"`
}

type SearchItem struct {
	Entry     *EntryDetail `json:"entry"`
	Rank      int          `json:"rank"`
	Score     float64      `json:"score"`
	Channels  []string     `json:"channels"`
	HitReason string       `json:"hit_reason"`
}

type SearchResponse struct {
	Items []SearchItem `json:"items"`
	Total int          `json:"total"`
}

type createEntryInput struct {
	CreateEntryRequest
	ContentHash      string
	ValidFromValue   time.Time
	ConfidenceValue  float64
	ImportanceValue  float64
	ExtractionMethod string
	ExtractorVersion string
}

// ─── M1: attribution ───

// TimelineEntryKind distinguishes the two journals being merged. A timeline is
// deliberately not a single table: skill executions and memory events are
// written by different domains at different times, and forcing them into one
// table would couple their write paths.
const (
	TimelineKindSkillLog    = "skill_log"
	TimelineKindMemoryEvent = "memory_event"
)

// TimelineItem is one entry in a session timeline. Only the fields relevant to
// attribution are surfaced: the goal is to answer "what ran, in what order, and
// what did it record", not to duplicate either record in full.
type TimelineItem struct {
	Kind           string    `json:"kind"`
	ID             string    `json:"id"`
	OccurredAt     time.Time `json:"occurred_at"`
	SessionID      string    `json:"session_id,omitempty"`
	SkillVersionID *string   `json:"skill_version_id,omitempty"`
	SkillName      string    `json:"skill_name,omitempty"`
	SkillVersion   string    `json:"skill_version,omitempty"`
	// Skill log fields.
	Outcome       string `json:"outcome,omitempty"`
	WasTriggered  *bool  `json:"was_triggered,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
	DurationMs    *int   `json:"duration_ms,omitempty"`
	// Memory event fields.
	EventType string `json:"event_type,omitempty"`
	// Attributed reports whether this item can be tied to a specific skill
	// version. Unattributed items are still shown — hiding them would make a
	// timeline look complete when it is not — but they cannot support
	// attribution conclusions.
	Attributed bool `json:"attributed"`
}

type SessionTimelineParams struct {
	SessionID      string
	SkillVersionID string
	Limit          int
}

type SessionTimelineResponse struct {
	SessionID string         `json:"session_id"`
	Items     []TimelineItem `json:"items"`
	Total     int            `json:"total"`
	// Counters make the coverage of an attribution query explicit rather than
	// something the caller has to derive by scanning items.
	SkillLogCount     int  `json:"skill_log_count"`
	MemoryEventCount  int  `json:"memory_event_count"`
	UnattributedCount int  `json:"unattributed_count"`
	Truncated         bool `json:"truncated"`
}

// EntryAttribution answers the reverse question: which skill execution produced
// this durable memory. The chain is entry -> source event -> skill version, so
// it breaks at the first missing link, and the response says where it broke
// instead of returning an empty result.
type EntryAttribution struct {
	EntryID        string  `json:"entry_id"`
	SourceEventID  *string `json:"source_event_id,omitempty"`
	SessionID      string  `json:"session_id,omitempty"`
	SkillVersionID *string `json:"skill_version_id,omitempty"`
	SkillName      string  `json:"skill_name,omitempty"`
	SkillVersion   string  `json:"skill_version,omitempty"`
	// Resolution is one of: "skill_version", "session_only", "event_only",
	// "none". It tells the caller how far the chain resolved.
	Resolution string `json:"resolution"`
	// SessionTimeline is the surrounding session activity when a session is
	// known, capped and ordered by time.
	SessionTimeline []TimelineItem `json:"session_timeline,omitempty"`
}
