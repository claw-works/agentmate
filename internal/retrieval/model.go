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
	AccountID          string          `json:"account_id"`
	UserID             string          `json:"user_id"`
	KeyID              *string         `json:"key_id,omitempty"`
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
	AccountID      *string         `json:"account_id,omitempty"`
	UserID         *string         `json:"user_id,omitempty"`
	KeyID          *string         `json:"key_id,omitempty"`
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

type TextSearchFilters struct {
	SourceType string
	SourceID   string
	Metadata   map[string]any
}

type TextSearchResult struct {
	Document Document
	Score    float64
}
