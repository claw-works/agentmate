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
	SkillVersionID *string         `json:"skill_version_id,omitempty"`
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
	ID               string    `json:"id"`
	AccountID        *string   `json:"account_id,omitempty"`
	UserID           *string   `json:"user_id,omitempty"`
	KeyID            *string   `json:"key_id,omitempty"`
	SourceID         *string   `json:"source_id,omitempty"`
	SourceRevisionID *string   `json:"source_revision_id,omitempty"`
	SkillName        string    `json:"skill_name"`
	Version          string    `json:"version"`
	Content          string    `json:"content"`
	ContentHash      string    `json:"content_hash"`
	PackageHash      string    `json:"package_hash"`
	AgentID          string    `json:"agent_id"`
	ChangeSummary    string    `json:"change_summary"`
	EvalPassRate     *float64  `json:"eval_pass_rate,omitempty"`
	IsActive         bool      `json:"is_active"`
	PublishedAt      time.Time `json:"published_at"`
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
	RevisionKey     string    `json:"revision_key"`
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
	SkillVersionID string          `json:"skill_version_id"`
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

type GitSourceSyncState struct {
	Status      string    `json:"status"`
	Provider    string    `json:"provider,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	CommitSHA   string    `json:"commit_sha,omitempty"`
	PackageHash string    `json:"package_hash,omitempty"`
	Error       string    `json:"error,omitempty"`
	SyncedAt    time.Time `json:"synced_at"`
}

type SyncGitSourceRequest struct {
	Ref      string `json:"ref"`
	Activate *bool  `json:"activate"`
	Index    *bool  `json:"index"`
}

type SyncGitSourceResponse struct {
	Source    *SkillSource         `json:"source"`
	Provider  string               `json:"provider"`
	Ref       string               `json:"ref"`
	CommitSHA string               `json:"commit_sha"`
	Revision  *SkillSourceRevision `json:"revision"`
	Version   *SkillVersion        `json:"version"`
	Files     []SkillVersionFile   `json:"files"`
	Index     *IndexSkillsResponse `json:"index,omitempty"`
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
	SkillName       string    `json:"skill_name"`
	Version         string    `json:"version"`
	VersionID       string    `json:"version_id"`
	SourceID        *string   `json:"source_id,omitempty"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	Triggers        []string  `json:"triggers"`
	Capabilities    []string  `json:"capabilities"`
	Constraints     []string  `json:"constraints"`
	Dependencies    []string  `json:"dependencies"`
	CompilerName    string    `json:"compiler_name"`
	CompilerVersion string    `json:"compiler_version"`
	PackageHash     string    `json:"package_hash"`
	ResourceCount   int       `json:"resource_count"`
	ResourceKinds   []string  `json:"resource_kinds"`
	Score           float64   `json:"score"`
	Rank            int       `json:"rank"`
	DocumentID      string    `json:"document_id"`
	Content         string    `json:"content,omitempty"`
	PublishedAt     time.Time `json:"published_at,omitempty"`
	CompiledAt      time.Time `json:"compiled_at,omitempty"`
	ChangeSummary   string    `json:"change_summary,omitempty"`
}

type SkillResourceManifestItem struct {
	FileID        string `json:"file_id"`
	Path          string `json:"path"`
	Kind          string `json:"kind"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"size_bytes"`
	MimeType      string `json:"mime_type"`
	Indexable     bool   `json:"indexable"`
	TextAvailable bool   `json:"text_available"`
}

type CompiledSkillCatalog struct {
	ID               string                      `json:"-"`
	AccountID        string                      `json:"-"`
	SkillVersionID   string                      `json:"version_id"`
	SourceID         *string                     `json:"-"`
	SkillName        string                      `json:"skill_name"`
	Version          string                      `json:"version"`
	CompilerName     string                      `json:"compiler_name"`
	CompilerVersion  string                      `json:"compiler_version"`
	InputPackageHash string                      `json:"input_package_hash"`
	Description      string                      `json:"description"`
	Triggers         []string                    `json:"triggers"`
	Capabilities     []string                    `json:"capabilities"`
	Constraints      []string                    `json:"constraints"`
	Dependencies     []string                    `json:"dependencies"`
	ResourceManifest []SkillResourceManifestItem `json:"resources"`
	CompiledAt       time.Time                   `json:"compiled_at"`
	PublishedAt      time.Time                   `json:"published_at"`
}

type SkillCatalogItem struct {
	SkillVersionID    string    `json:"version_id"`
	SkillName         string    `json:"skill_name"`
	Version           string    `json:"version"`
	SourceID          *string   `json:"source_id,omitempty"`
	Description       string    `json:"description"`
	Triggers          []string  `json:"triggers"`
	Capabilities      []string  `json:"capabilities"`
	Constraints       []string  `json:"constraints"`
	Dependencies      []string  `json:"dependencies"`
	CompilerName      string    `json:"compiler_name"`
	CompilerVersion   string    `json:"compiler_version"`
	PackageHash       string    `json:"package_hash"`
	ResourceCount     int       `json:"resource_count"`
	ResourceKinds     []string  `json:"resource_kinds"`
	CompiledAt        time.Time `json:"compiled_at,omitempty"`
	PublishedAt       time.Time `json:"published_at"`
	ArtifactAvailable bool      `json:"artifact_available"`
}

type SkillCatalogListParams struct {
	Query  string
	Limit  int
	Offset int
}

type SkillCatalogListResponse struct {
	Items  []SkillCatalogItem `json:"items"`
	Total  int                `json:"total"`
	Limit  int                `json:"limit"`
	Offset int                `json:"offset"`
}

type CompileSkillsRequest struct {
	VersionID string `json:"version_id"`
}

type CompileSkillsResponse struct {
	Items  []SkillCatalogItem `json:"items"`
	Errors []IndexError       `json:"errors"`
}

type SkillInstructionsResponse struct {
	VersionID    string    `json:"version_id"`
	SkillName    string    `json:"skill_name"`
	Version      string    `json:"version"`
	Instructions string    `json:"instructions"`
	ContentHash  string    `json:"content_hash"`
	PublishedAt  time.Time `json:"published_at"`
}

type SkillResourceListParams struct {
	Limit  int
	Offset int
}

type SkillResourcesResponse struct {
	VersionID string                      `json:"version_id"`
	SkillName string                      `json:"skill_name"`
	Version   string                      `json:"version"`
	Items     []SkillResourceManifestItem `json:"items"`
	Total     int                         `json:"total"`
	Limit     int                         `json:"limit"`
	Offset    int                         `json:"offset"`
}

type SkillResourceResponse struct {
	VersionID string `json:"version_id"`
	FileID    string `json:"file_id"`
	Path      string `json:"path"`
	Kind      string `json:"kind"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	MimeType  string `json:"mime_type"`
	Content   string `json:"content"`
}
