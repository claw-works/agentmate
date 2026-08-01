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

// EventDetail 是回读一个事件时的返回形态：事件本身，加上服务端对它的判断。
//
// Warning 存在的理由：正文为空的事件回读出来只是一个 {}，调用方无从判断这是"本来
// 就没内容"还是"内容在写入时丢了"。真实接入里有五条事件正是这样丢掉正文而无人发现，
// 直到有人去看才发现 checkpoint 里只剩 "Goal: "。
type EventDetail struct {
	Event
	Warning string `json:"warning,omitempty"`
}

// ListEventsParams 收窄事件回读。全部可选：都为空即本账号最近的事件。
type ListEventsParams struct {
	SessionID string
	ScopeType string
	ScopeKey  string
	EventType string
	Limit     int
	Offset    int
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
	Entry *EntryDetail `json:"entry"`
	Rank  int          `json:"rank"`
	// Score is the retrieval score adjusted by recorded usefulness.
	Score float64 `json:"score"`
	// RetrievalScore and FeedbackAdjustment are reported separately so a
	// surprising order can be explained: without them a caller cannot tell a
	// weak semantic match from a demotion by negative feedback.
	RetrievalScore     float64  `json:"retrieval_score"`
	FeedbackAdjustment float64  `json:"feedback_adjustment"`
	Channels           []string `json:"channels"`
	HitReason          string   `json:"hit_reason"`
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

// ─── M3: supersede ───

type SupersedeRequest struct {
	// SupersedingID is the entry that takes over; SupersededID is the one being
	// replaced. Both are required and must differ.
	SupersedingID string `json:"superseding_id"`
	SupersededID  string `json:"superseded_id"`
}

type SupersedeResponse struct {
	Superseding *Entry `json:"superseding"`
	Superseded  *Entry `json:"superseded"`
	// ProjectionRemoved counts retrieval rows dropped for the replaced entry.
	// Reported because a replaced memory that stays in the projection keeps
	// consuming top-k slots even though search filters it out afterwards.
	ProjectionRemoved int64 `json:"projection_removed"`
	// Warning is set when the supersede committed but the projection could not
	// be cleaned up, so the caller knows to re-run indexing rather than assuming
	// the supersede failed.
	Warning string `json:"warning,omitempty"`
}

// ─── M3: feedback ───

const (
	SignalUseful  = "useful"
	SignalHarmful = "harmful"
)

type Feedback struct {
	ID        string  `json:"id"`
	AccountID string  `json:"account_id"`
	UserID    *string `json:"user_id,omitempty"`
	KeyID     *string `json:"key_id,omitempty"`
	MemoryID  string  `json:"memory_id"`
	Signal    string  `json:"signal"`
	Reason    string  `json:"reason,omitempty"`
	// SessionID and SkillVersionID are the attribution anchors, mirroring M1.
	// Without them a signal only supports coarse trend statistics.
	SessionID      string          `json:"session_id,omitempty"`
	SkillVersionID *string         `json:"skill_version_id,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	ObservedAt     time.Time       `json:"observed_at"`
	CreatedAt      time.Time       `json:"created_at"`
	// Entry carries the updated counters when this call created the signal.
	Entry *Entry `json:"entry,omitempty"`
}

type FeedbackRequest struct {
	MemoryID       string         `json:"memory_id"`
	Signal         string         `json:"signal"`
	Reason         string         `json:"reason"`
	SessionID      string         `json:"session_id"`
	SkillVersionID string         `json:"skill_version_id"`
	Metadata       map[string]any `json:"metadata"`
}

type FeedbackResponse struct {
	Feedback *Feedback `json:"feedback"`
	// Created is false when this signal was already recorded for the same
	// (memory, session, signal) triple, so counters were not moved again.
	Created bool `json:"created"`
}

// ─── M3: checkpoint ───

// A checkpoint is a resumable snapshot of session intent: the goal, what has been
// done, what is next, and open questions.
//
// It is stored as a memory event of type "checkpoint" rather than in its own
// table. The journal is already immutable, ordered and idempotent per session,
// and a checkpoint is exactly a journal entry that happens to carry structured
// state. A separate table would duplicate those guarantees and split the session
// timeline across two stores.
type Checkpoint struct {
	EventID        string    `json:"event_id"`
	SessionID      string    `json:"session_id"`
	ScopeType      string    `json:"scope_type"`
	ScopeKey       string    `json:"scope_key,omitempty"`
	Label          string    `json:"label,omitempty"`
	Goal           string    `json:"goal"`
	Done           []string  `json:"done,omitempty"`
	Next           []string  `json:"next,omitempty"`
	Open           []string  `json:"open,omitempty"`
	Notes          string    `json:"notes,omitempty"`
	SkillVersionID *string   `json:"skill_version_id,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
	// Warning 报告读取这个 checkpoint 时的降级：类型写错但被尽力读出的字段，以及
	// 被忽略的未知键。空表示 payload 完全符合预期。
	Warning string `json:"warning,omitempty"`
}

type SaveCheckpointRequest struct {
	SessionID string   `json:"session_id"`
	ScopeType string   `json:"scope_type"`
	ScopeKey  string   `json:"scope_key"`
	Label     string   `json:"label"`
	Goal      string   `json:"goal"`
	Done      []string `json:"done"`
	Next      []string `json:"next"`
	Open      []string `json:"open"`
	Notes     string   `json:"notes"`
	// SkillVersionID attributes the checkpoint to the execution that saved it.
	SkillVersionID string `json:"skill_version_id"`
	// IdempotencyKey defaults to a hash of the content, so saving the same state
	// twice does not append a duplicate checkpoint to the journal.
	IdempotencyKey string `json:"idempotency_key"`
}

type SaveCheckpointResponse struct {
	Checkpoint *Checkpoint `json:"checkpoint"`
	Created    bool        `json:"created"`
}

type ResumeResponse struct {
	SessionID string `json:"session_id"`
	// Checkpoint is the latest saved snapshot, or nil when the session has none.
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
	// SinceCheckpoint lists activity recorded after the checkpoint was saved.
	// Resuming from the snapshot alone would silently discard whatever happened
	// afterwards, which is precisely the state an interrupted session is in.
	SinceCheckpoint []TimelineItem `json:"since_checkpoint"`
	// Resolution is "checkpoint", "journal_only" (activity but no checkpoint) or
	// "empty", so a caller can tell a fresh session from an unsaved one.
	Resolution string `json:"resolution"`
}
