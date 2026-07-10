package skills

import (
	"encoding/json"
	"time"
)

type SkillLog struct {
	ID             string          `json:"id"`
	AccountID      *string         `json:"account_id,omitempty"`
	UserID         *string         `json:"user_id,omitempty"`
	KeyID          *string         `json:"key_id,omitempty"`
	SkillName      string          `json:"skill_name"`
	SkillVersion   string          `json:"skill_version"`
	AgentID        string          `json:"agent_id"`
	SessionID      string          `json:"session_id"`
	TriggerText    string          `json:"trigger_text"`
	WasTriggered   bool            `json:"was_triggered"`
	Outcome        string          `json:"outcome"`
	FailureReason  string          `json:"failure_reason,omitempty"`
	UserCorrection string          `json:"user_correction,omitempty"`
	ToolCalls      json.RawMessage `json:"tool_calls,omitempty"`
	DurationMs     *int            `json:"duration_ms,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type SkillVersion struct {
	ID            string    `json:"id"`
	AccountID     *string   `json:"account_id,omitempty"`
	UserID        *string   `json:"user_id,omitempty"`
	KeyID         *string   `json:"key_id,omitempty"`
	SkillName     string    `json:"skill_name"`
	Version       string    `json:"version"`
	Content       string    `json:"content"`
	ContentHash   string    `json:"content_hash"`
	AgentID       string    `json:"agent_id"`
	ChangeSummary string    `json:"change_summary"`
	EvalPassRate  *float64  `json:"eval_pass_rate,omitempty"`
	IsActive      bool      `json:"is_active"`
	PublishedAt   time.Time `json:"published_at"`
}

type SkillSource struct {
	ID            string          `json:"id"`
	AccountID     *string         `json:"account_id,omitempty"`
	UserID        *string         `json:"user_id,omitempty"`
	KeyID         *string         `json:"key_id,omitempty"`
	Name          string          `json:"name"`
	Type          string          `json:"type"`
	RepositoryURL string          `json:"repository_url"`
	PackagePath   string          `json:"package_path"`
	DefaultRef    string          `json:"default_ref"`
	SyncMode      string          `json:"sync_mode"`
	Visibility    string          `json:"visibility"`
	Status        string          `json:"status"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type SkillSourceRevision struct {
	ID              string    `json:"id"`
	AccountID       *string   `json:"account_id,omitempty"`
	UserID          *string   `json:"user_id,omitempty"`
	KeyID           *string   `json:"key_id,omitempty"`
	SourceID        string    `json:"source_id"`
	SkillVersionID  *string   `json:"skill_version_id,omitempty"`
	CommitSHA       string    `json:"commit_sha"`
	LocalSnapshotID string    `json:"local_snapshot_id"`
	TreeHash        string    `json:"tree_hash"`
	PackageHash     string    `json:"package_hash"`
	Status          string    `json:"status"`
	Error           string    `json:"error,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type SkillVersionFile struct {
	ID               string    `json:"id"`
	AccountID        *string   `json:"account_id,omitempty"`
	UserID           *string   `json:"user_id,omitempty"`
	KeyID            *string   `json:"key_id,omitempty"`
	SourceRevisionID string    `json:"source_revision_id"`
	VersionID        *string   `json:"version_id,omitempty"`
	Path             string    `json:"path"`
	Kind             string    `json:"kind"`
	SHA256           string    `json:"sha256"`
	SizeBytes        int64     `json:"size_bytes"`
	MimeType         string    `json:"mime_type"`
	Indexable        bool      `json:"indexable"`
	ContentSnapshot  string    `json:"content_snapshot,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

type CreateLogRequest struct {
	SkillName      string          `json:"skill_name" binding:"required"`
	SkillVersion   string          `json:"skill_version"`
	AgentID        string          `json:"agent_id" binding:"required"`
	SessionID      string          `json:"session_id"`
	TriggerText    string          `json:"trigger_text"`
	WasTriggered   *bool           `json:"was_triggered"`
	Outcome        string          `json:"outcome" binding:"required,oneof=success failure partial user_corrected"`
	FailureReason  string          `json:"failure_reason"`
	UserCorrection string          `json:"user_correction"`
	ToolCalls      json.RawMessage `json:"tool_calls"`
	DurationMs     *int            `json:"duration_ms"`
}

type CreateVersionRequest struct {
	SkillName     string   `json:"skill_name" binding:"required"`
	Version       string   `json:"version" binding:"required"`
	Content       string   `json:"content" binding:"required"`
	AgentID       string   `json:"agent_id"`
	ChangeSummary string   `json:"change_summary"`
	EvalPassRate  *float64 `json:"eval_pass_rate"`
	Activate      bool     `json:"activate"`
}

type CreateSkillSourceRequest struct {
	Name          string          `json:"name"`
	Type          string          `json:"type" binding:"required"`
	RepositoryURL string          `json:"repository_url" binding:"required"`
	PackagePath   string          `json:"package_path"`
	DefaultRef    string          `json:"default_ref"`
	SyncMode      string          `json:"sync_mode"`
	Visibility    string          `json:"visibility"`
	Status        string          `json:"status"`
	Metadata      json.RawMessage `json:"metadata"`
}

type SnapshotFile struct {
	Path      string `json:"path" binding:"required"`
	Kind      string `json:"kind"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Size      int64  `json:"size"`
	MimeType  string `json:"mime_type"`
	Indexable bool   `json:"indexable"`
	Content   string `json:"content"`
}

type SubmitLocalSnapshotRequest struct {
	SnapshotID    string         `json:"snapshot_id"`
	TreeHash      string         `json:"tree_hash"`
	PackageHash   string         `json:"package_hash"`
	SkillName     string         `json:"skill_name"`
	Version       string         `json:"version"`
	AgentID       string         `json:"agent_id"`
	ChangeSummary string         `json:"change_summary"`
	Activate      *bool          `json:"activate"`
	Index         *bool          `json:"index"`
	Files         []SnapshotFile `json:"files" binding:"required"`
}

type SubmitLocalSnapshotResponse struct {
	Source   *SkillSource         `json:"source"`
	Revision *SkillSourceRevision `json:"revision"`
	Version  *SkillVersion        `json:"version"`
	Files    []SkillVersionFile   `json:"files"`
	Index    *IndexSkillsResponse `json:"index,omitempty"`
}

type LogListParams struct {
	SkillName string
	AgentID   string
	Outcome   string
	Limit     int
	Offset    int
}

type VersionListParams struct {
	SkillName string
	Limit     int
	Offset    int
}

type SkillSourceListParams struct {
	Type   string
	Status string
	Limit  int
	Offset int
}

type IndexSkillsRequest struct {
	SkillName string `json:"skill_name"`
}

type IndexSkillsResponse struct {
	Indexed []IndexedSkill `json:"indexed"`
	Errors  []IndexError   `json:"errors"`
}

type IndexedSkill struct {
	SkillName  string `json:"skill_name"`
	Version    string `json:"version"`
	VersionID  string `json:"version_id"`
	DocumentID string `json:"document_id"`
}

type IndexError struct {
	SkillName string `json:"skill_name"`
	Error     string `json:"error"`
}

type SearchSkillsRequest struct {
	Query          string `json:"query" binding:"required"`
	TopK           int    `json:"top_k"`
	IncludeContent bool   `json:"include_content"`
}

type SearchSkillsResponse struct {
	Items []SkillSearchItem `json:"items"`
	Total int               `json:"total"`
}

type SkillSearchItem struct {
	SkillName     string    `json:"skill_name"`
	Version       string    `json:"version"`
	VersionID     string    `json:"version_id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	Score         float64   `json:"score"`
	Rank          int       `json:"rank"`
	DocumentID    string    `json:"document_id"`
	Content       string    `json:"content,omitempty"`
	PublishedAt   time.Time `json:"published_at,omitempty"`
	ChangeSummary string    `json:"change_summary,omitempty"`
}
