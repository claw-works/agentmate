package knowledge

import (
	"encoding/json"
	"time"
)

type KnowledgeSource struct {
	ID               string          `json:"id"`
	AccountID        string          `json:"account_id"`
	UserID           *string         `json:"user_id,omitempty"`
	KeyID            *string         `json:"key_id,omitempty"`
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	RepositoryURL    string          `json:"repository_url"`
	PackagePath      string          `json:"package_path"`
	DefaultRef       string          `json:"default_ref"`
	SyncMode         string          `json:"sync_mode"`
	Status           string          `json:"status"`
	ActiveRevisionID *string         `json:"active_revision_id,omitempty"`
	Metadata         json.RawMessage `json:"metadata,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type KnowledgeSourceRevision struct {
	ID              string          `json:"id"`
	AccountID       string          `json:"account_id"`
	SourceID        string          `json:"source_id"`
	RevisionKey     string          `json:"revision_key"`
	CommitSHA       string          `json:"commit_sha"`
	LocalSnapshotID string          `json:"local_snapshot_id"`
	TreeHash        string          `json:"tree_hash"`
	PackageHash     string          `json:"package_hash"`
	Manifest        json.RawMessage `json:"manifest,omitempty"`
	Status          string          `json:"status"`
	Error           string          `json:"error,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}

type KnowledgeDocument struct {
	ID              string    `json:"id"`
	AccountID       string    `json:"account_id"`
	SourceID        string    `json:"source_id"`
	RevisionID      string    `json:"revision_id"`
	Path            string    `json:"path"`
	SHA256          string    `json:"sha256"`
	SizeBytes       int64     `json:"size_bytes"`
	MimeType        string    `json:"mime_type"`
	Indexable       bool      `json:"indexable"`
	ContentSnapshot string    `json:"content_snapshot,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

// KnowledgeDocumentSummary is the paginated list projection: document
// provenance and behavior metadata without the content body.
type KnowledgeDocumentSummary struct {
	ID         string    `json:"id"`
	SourceID   string    `json:"source_id"`
	RevisionID string    `json:"revision_id"`
	Path       string    `json:"path"`
	SHA256     string    `json:"sha256"`
	SizeBytes  int64     `json:"size_bytes"`
	MimeType   string    `json:"mime_type"`
	Indexable  bool      `json:"indexable"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateKnowledgeSourceRequest struct {
	Name          string          `json:"name"`
	Type          string          `json:"type" binding:"required"`
	RepositoryURL string          `json:"repository_url" binding:"required"`
	PackagePath   string          `json:"package_path"`
	DefaultRef    string          `json:"default_ref"`
	SyncMode      string          `json:"sync_mode"`
	Status        string          `json:"status"`
	Metadata      json.RawMessage `json:"metadata"`
}

type KnowledgeSourceListParams struct {
	Type   string
	Status string
	Limit  int
	Offset int
}

// SnapshotFile is one client-pushed local snapshot file. Text files send
// content (sha256/size may be omitted and are derived server-side); binary
// or opaque files send sha256 and size_bytes only.
type SnapshotFile struct {
	Path      string `json:"path" binding:"required"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	MimeType  string `json:"mime_type"`
	Content   string `json:"content"`
}

type SubmitSnapshotRequest struct {
	SnapshotID  string         `json:"snapshot_id"`
	TreeHash    string         `json:"tree_hash"`
	PackageHash string         `json:"package_hash"`
	Files       []SnapshotFile `json:"files" binding:"required"`
}

type SubmitSnapshotResponse struct {
	Source    *KnowledgeSource           `json:"source"`
	Revision  *KnowledgeSourceRevision   `json:"revision"`
	Manifest  Manifest                   `json:"manifest"`
	Documents []KnowledgeDocumentSummary `json:"documents"`
}

type SyncGitSourceRequest struct {
	Ref string `json:"ref"`
}

type SyncGitSourceResponse struct {
	Source    *KnowledgeSource           `json:"source"`
	Provider  string                     `json:"provider"`
	Ref       string                     `json:"ref"`
	CommitSHA string                     `json:"commit_sha"`
	Revision  *KnowledgeSourceRevision   `json:"revision"`
	Manifest  Manifest                   `json:"manifest"`
	Documents []KnowledgeDocumentSummary `json:"documents"`
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

type DocumentListParams struct {
	Limit  int
	Offset int
}

type DocumentListResponse struct {
	RevisionID string                     `json:"revision_id"`
	Items      []KnowledgeDocumentSummary `json:"items"`
	Total      int                        `json:"total"`
	Limit      int                        `json:"limit"`
	Offset     int                        `json:"offset"`
}
