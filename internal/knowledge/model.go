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
	Domain           string          `json:"domain,omitempty"`
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
	Name          string `json:"name"`
	Type          string `json:"type" binding:"required"`
	RepositoryURL string `json:"repository_url" binding:"required"`
	PackagePath   string `json:"package_path"`
	// Domain is derived from PackagePath, never accepted from the client, so a
	// request cannot declare a domain that contradicts its package location.
	Domain     string          `json:"-"`
	DefaultRef string          `json:"default_ref"`
	SyncMode   string          `json:"sync_mode"`
	Status     string          `json:"status"`
	Metadata   json.RawMessage `json:"metadata"`
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

// ─── K2: link graph ───

// DocumentLinkInput is one parsed Markdown package-internal link, expressed
// as source/target paths before path→document ID resolution.
type DocumentLinkInput struct {
	SourcePath string
	TargetPath string
}

// KnowledgeDocumentLinkItem is one direction-tagged link neighbor. For "out"
// links Path is the target path (DocumentID nil when the target does not
// exist in the revision); for "in" links Path is the linking source
// document's path.
type KnowledgeDocumentLinkItem struct {
	Direction  string  `json:"direction"`
	DocumentID *string `json:"document_id,omitempty"`
	Path       string  `json:"path"`
}

type DocumentLinksResponse struct {
	DocumentID string                      `json:"document_id"`
	RevisionID string                      `json:"revision_id"`
	Items      []KnowledgeDocumentLinkItem `json:"items"`
	Total      int                         `json:"total"`
	Limit      int                         `json:"limit"`
	Offset     int                         `json:"offset"`
}

// ─── K2: K0 catalog ───

type KnowledgeCatalogItem struct {
	SourceID         string `json:"source_id"`
	Name             string `json:"name"`
	Domain           string `json:"domain,omitempty"`
	Description      string `json:"description,omitempty"`
	Profile          string `json:"profile,omitempty"`
	Language         string `json:"language,omitempty"`
	CitationPolicy   string `json:"citation_policy,omitempty"`
	Type             string `json:"type"`
	ActiveRevisionID string `json:"active_revision_id"`
	PackageHash      string `json:"package_hash"`
	DocumentCount    int    `json:"document_count"`
	IndexedChunks    int    `json:"indexed_chunks"`
	FailedChunks     int    `json:"failed_chunks"`
	PendingChunks    int    `json:"pending_chunks"`
	IndexStatus      string `json:"index_status"`
}

type KnowledgeCatalogListParams struct {
	Query  string
	Domain string
	Limit  int
	Offset int
}

type KnowledgeCatalogListResponse struct {
	Items  []KnowledgeCatalogItem `json:"items"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
	// Domains lists every domain present in the account's catalog with its
	// collection count, so an agent can narrow to a domain before reading
	// individual collection cards.
	Domains []KnowledgeDomainCount `json:"domains,omitempty"`
}

type KnowledgeDomainCount struct {
	Domain          string `json:"domain"`
	CollectionCount int    `json:"collection_count"`
}

// ─── K2: indexing ───

type IndexKnowledgeRequest struct {
	SourceID string `json:"source_id"`
}

type IndexedKnowledgeSource struct {
	SourceID        string `json:"source_id"`
	Name            string `json:"name"`
	RevisionID      string `json:"revision_id"`
	Documents       int    `json:"documents"`
	ChunksIndexed   int    `json:"chunks_indexed"`
	ChunksFailed    int    `json:"chunks_failed"`
	LinksRebuilt    int    `json:"links_rebuilt"`
	StaleDeleted    int64  `json:"stale_deleted"`
	TruncatedChunks int    `json:"truncated_documents"`
}

type KnowledgeIndexError struct {
	SourceID string `json:"source_id"`
	Error    string `json:"error"`
}

type IndexKnowledgeResponse struct {
	Indexed []IndexedKnowledgeSource `json:"indexed"`
	Errors  []KnowledgeIndexError    `json:"errors"`
}

// ─── K2: retrieval ───

type SearchKnowledgeRequest struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
	// Domain narrows the search to collections owned by one domain. It is
	// resolved to the domain's source IDs, so it composes with SourceIDs by
	// intersection rather than widening the search.
	Domain         string   `json:"domain"`
	SourceIDs      []string `json:"source_ids"`
	IncludeContent bool     `json:"include_content"`
}

type KnowledgeSearchHit struct {
	DocumentID  string                      `json:"document_id"`
	SourceID    string                      `json:"source_id"`
	RevisionID  string                      `json:"revision_id"`
	Path        string                      `json:"path"`
	HeadingPath string                      `json:"heading_path,omitempty"`
	ChunkKey    string                      `json:"chunk_key"`
	Knowledge   string                      `json:"knowledge_base,omitempty"`
	Score       float64                     `json:"score"`
	Rank        int                         `json:"rank"`
	Snippet     string                      `json:"snippet"`
	Content     string                      `json:"content,omitempty"`
	Neighbors   []KnowledgeDocumentLinkItem `json:"neighbors"`
}

type SearchKnowledgeResponse struct {
	Items []KnowledgeSearchHit `json:"items"`
	Total int                  `json:"total"`
}
