package retrieval

import (
	"encoding/json"
	"time"
)

const (
	NamespaceSkills = "skills"
	NamespaceMemory = "memory"

	StatusPending = "pending"
	StatusIndexed = "indexed"
	StatusFailed  = "failed"

	DefaultCollection = "agentmate_retrieval"
	DefaultVectorName = "semantic"
	DefaultDistance   = "Cosine"
	DefaultTopK       = 8
)

type Document struct {
	ID                 string          `json:"id"`
	UserID             string          `json:"user_id"`
	Namespace          string          `json:"namespace"`
	SourceType         string          `json:"source_type"`
	SourceID           string          `json:"source_id"`
	ChunkKey           string          `json:"chunk_key"`
	Title              string          `json:"title"`
	Content            string          `json:"content,omitempty"`
	ContentHash        string          `json:"content_hash"`
	Metadata           json.RawMessage `json:"metadata,omitempty"`
	QdrantCollection   string          `json:"qdrant_collection"`
	QdrantPointID      string          `json:"qdrant_point_id"`
	VectorName         string          `json:"vector_name"`
	EmbeddingModel     string          `json:"embedding_model"`
	EmbeddingDimension int             `json:"embedding_dimension"`
	Status             string          `json:"status"`
	Error              string          `json:"error,omitempty"`
	IndexedAt          *time.Time      `json:"indexed_at,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
	UpdatedAt          time.Time       `json:"updated_at"`
}

type UpsertDocumentInput struct {
	Namespace          string         `json:"namespace"`
	SourceType         string         `json:"source_type"`
	SourceID           string         `json:"source_id"`
	ChunkKey           string         `json:"chunk_key"`
	Title              string         `json:"title"`
	Content            string         `json:"content"`
	ContentHash        string         `json:"content_hash"`
	Metadata           map[string]any `json:"metadata"`
	QdrantCollection   string         `json:"qdrant_collection"`
	VectorName         string         `json:"vector_name"`
	EmbeddingModel     string         `json:"embedding_model"`
	EmbeddingDimension int            `json:"embedding_dimension"`
}

type QueryLog struct {
	ID             string          `json:"id"`
	UserID         *string         `json:"user_id,omitempty"`
	Namespace      string          `json:"namespace"`
	Query          string          `json:"query"`
	QueryHash      string          `json:"query_hash"`
	TopK           int             `json:"top_k"`
	CandidateCount int             `json:"candidate_count"`
	SelectedCount  int             `json:"selected_count"`
	EmbeddingModel string          `json:"embedding_model"`
	RerankModel    string          `json:"rerank_model"`
	LatencyMs      int             `json:"latency_ms"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

type CreateQueryLogInput struct {
	Namespace      string
	Query          string
	QueryHash      string
	TopK           int
	CandidateCount int
	SelectedCount  int
	EmbeddingModel string
	RerankModel    string
	LatencyMs      int
	Metadata       map[string]any
}

type QueryResultInput struct {
	DocumentID    *string
	QdrantPointID string
	Rank          int
	Score         float64
	Stage         string
	Metadata      map[string]any
}

type FeedbackInput struct {
	QueryID    *string
	DocumentID *string
	Signal     string
	Reason     string
	Metadata   map[string]any
}

type SearchRequest struct {
	Namespace string         `json:"namespace"`
	Query     string         `json:"query"`
	TopK      int            `json:"top_k"`
	Filters   map[string]any `json:"filters"`
	Metadata  map[string]any `json:"metadata"`
}

type SearchResult struct {
	Document *Document      `json:"document,omitempty"`
	PointID  string         `json:"point_id"`
	Rank     int            `json:"rank"`
	Score    float64        `json:"score"`
	Stage    string         `json:"stage"`
	Payload  map[string]any `json:"payload,omitempty"`
}

type MemoryEntry struct {
	ID             string          `json:"id"`
	UserID         string          `json:"user_id"`
	ScopeType      string          `json:"scope_type"`
	ScopeKey       string          `json:"scope_key"`
	MemoryType     string          `json:"memory_type"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Summary        string          `json:"summary"`
	ContentHash    string          `json:"content_hash"`
	Confidence     float64         `json:"confidence"`
	Importance     float64         `json:"importance"`
	Status         string          `json:"status"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	TTLAt          *time.Time      `json:"ttl_at,omitempty"`
	LastAccessedAt *time.Time      `json:"last_accessed_at,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type CreateMemoryEntryInput struct {
	ScopeType   string
	ScopeKey    string
	MemoryType  string
	Title       string
	Content     string
	Summary     string
	ContentHash string
	Confidence  float64
	Importance  float64
	Status      string
	Metadata    map[string]any
	TTLAt       *time.Time
}

type MemoryEvidence struct {
	ID         string          `json:"id"`
	MemoryID   string          `json:"memory_id"`
	SourceType string          `json:"source_type"`
	SourceID   string          `json:"source_id"`
	Excerpt    string          `json:"excerpt"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
	CreatedAt  time.Time       `json:"created_at"`
}

type AddMemoryEvidenceInput struct {
	MemoryID   string
	SourceType string
	SourceID   string
	Excerpt    string
	Metadata   map[string]any
}
