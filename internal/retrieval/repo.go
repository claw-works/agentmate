package retrieval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) UpsertDocument(ctx context.Context, userID string, in UpsertDocumentInput) (*Document, error) {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	setDocumentDefaults(&in)

	var d Document
	err = r.pool.QueryRow(ctx,
		`INSERT INTO retrieval_documents
		 (user_id, namespace, source_type, source_id, chunk_key, title, content, content_hash, metadata,
		  qdrant_collection, vector_name, embedding_model, embedding_dimension, status, error, indexed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, 'pending', '', NULL)
		 ON CONFLICT (user_id, namespace, source_type, source_id, chunk_key)
		 DO UPDATE SET
		   title = EXCLUDED.title,
		   content = EXCLUDED.content,
		   content_hash = EXCLUDED.content_hash,
		   metadata = EXCLUDED.metadata,
		   qdrant_collection = EXCLUDED.qdrant_collection,
		   vector_name = EXCLUDED.vector_name,
		   embedding_model = EXCLUDED.embedding_model,
		   embedding_dimension = EXCLUDED.embedding_dimension,
		   status = 'pending',
		   error = '',
		   indexed_at = NULL,
		   updated_at = NOW()
		 RETURNING id, user_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at`,
		userID, in.Namespace, in.SourceType, in.SourceID, in.ChunkKey, in.Title, in.Content, in.ContentHash,
		metadata, in.QdrantCollection, in.VectorName, in.EmbeddingModel, in.EmbeddingDimension,
	).Scan(scanDocument(&d)...)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repo) MarkDocumentIndexed(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE retrieval_documents
		 SET status = 'indexed', error = '', indexed_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND user_id = $2`,
		id, userID,
	)
	return err
}

func (r *Repo) MarkDocumentFailed(ctx context.Context, userID, id, message string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE retrieval_documents
		 SET status = 'failed', error = $3, updated_at = NOW()
		 WHERE id = $1 AND user_id = $2`,
		id, userID, message,
	)
	return err
}

func (r *Repo) GetDocument(ctx context.Context, userID, id string) (*Document, error) {
	var d Document
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at
		 FROM retrieval_documents WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(scanDocument(&d)...)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repo) DocumentsByPointIDs(ctx context.Context, userID, collection string, pointIDs []string) (map[string]Document, error) {
	result := make(map[string]Document, len(pointIDs))
	if len(pointIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at
		 FROM retrieval_documents
		 WHERE user_id = $1 AND qdrant_collection = $2 AND qdrant_point_id::text = ANY($3)`,
		userID, collection, pointIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d Document
		if err := rows.Scan(scanDocument(&d)...); err != nil {
			return nil, err
		}
		result[d.QdrantPointID] = d
	}
	return result, rows.Err()
}

func (r *Repo) SearchDocumentsText(ctx context.Context, userID, namespace, query string, limit int) ([]Document, error) {
	if limit <= 0 || limit > 50 {
		limit = DefaultTopK
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at
		 FROM retrieval_documents
		 WHERE user_id = $1
		   AND namespace = $2
		   AND status = 'indexed'
		   AND to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', $3)
		 ORDER BY ts_rank(to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')), plainto_tsquery('simple', $3)) DESC
		 LIMIT $4`,
		userID, namespace, query, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Document, 0)
	for rows.Next() {
		var d Document
		if err := rows.Scan(scanDocument(&d)...); err != nil {
			return nil, err
		}
		items = append(items, d)
	}
	return items, rows.Err()
}

func (r *Repo) CreateIndexJob(ctx context.Context, userID, namespace, sourceType, sourceID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO retrieval_index_jobs (user_id, namespace, source_type, source_id)
		 VALUES ($1, $2, $3, $4) RETURNING id`,
		userID, namespace, sourceType, sourceID,
	).Scan(&id)
	return id, err
}

func (r *Repo) CreateQueryLog(ctx context.Context, userID string, in CreateQueryLogInput) (*QueryLog, error) {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	var userArg any
	if userID != "" {
		userArg = userID
	}
	var q QueryLog
	err = r.pool.QueryRow(ctx,
		`INSERT INTO retrieval_queries
		 (user_id, namespace, query, query_hash, top_k, candidate_count, selected_count, embedding_model, rerank_model, latency_ms, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id, user_id, namespace, query, query_hash, top_k, candidate_count, selected_count,
		   embedding_model, rerank_model, latency_ms, metadata, created_at`,
		userArg, in.Namespace, in.Query, in.QueryHash, in.TopK, in.CandidateCount, in.SelectedCount,
		in.EmbeddingModel, in.RerankModel, in.LatencyMs, metadata,
	).Scan(&q.ID, &q.UserID, &q.Namespace, &q.Query, &q.QueryHash, &q.TopK, &q.CandidateCount,
		&q.SelectedCount, &q.EmbeddingModel, &q.RerankModel, &q.LatencyMs, &q.Metadata, &q.CreatedAt)
	return &q, err
}

func (r *Repo) AddQueryResults(ctx context.Context, queryID string, results []QueryResultInput) error {
	for _, item := range results {
		metadata, err := marshalMetadata(item.Metadata)
		if err != nil {
			return err
		}
		stage := item.Stage
		if stage == "" {
			stage = "vector"
		}
		_, err = r.pool.Exec(ctx,
			`INSERT INTO retrieval_query_results
			 (query_id, document_id, qdrant_point_id, rank, score, stage, metadata)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			queryID, item.DocumentID, nullableString(item.QdrantPointID), item.Rank, item.Score, stage, metadata,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Repo) AddFeedback(ctx context.Context, userID string, in FeedbackInput) error {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return err
	}
	var userArg any
	if userID != "" {
		userArg = userID
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO retrieval_feedback (query_id, document_id, user_id, signal, reason, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		in.QueryID, in.DocumentID, userArg, in.Signal, in.Reason, metadata,
	)
	return err
}

func (r *Repo) CreateMemoryEntry(ctx context.Context, userID string, in CreateMemoryEntryInput) (*MemoryEntry, error) {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	setMemoryDefaults(&in)

	var m MemoryEntry
	err = r.pool.QueryRow(ctx,
		`INSERT INTO memory_entries
		 (user_id, scope_type, scope_key, memory_type, title, content, summary, content_hash,
		  confidence, importance, status, metadata, ttl_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id, user_id, scope_type, scope_key, memory_type, title, content, summary, content_hash,
		   confidence, importance, status, metadata, ttl_at, last_accessed_at, created_at, updated_at`,
		userID, in.ScopeType, in.ScopeKey, in.MemoryType, in.Title, in.Content, in.Summary, in.ContentHash,
		in.Confidence, in.Importance, in.Status, metadata, in.TTLAt,
	).Scan(scanMemoryEntry(&m)...)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *Repo) AddMemoryEvidence(ctx context.Context, in AddMemoryEvidenceInput) (*MemoryEvidence, error) {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	var e MemoryEvidence
	err = r.pool.QueryRow(ctx,
		`INSERT INTO memory_evidence (memory_id, source_type, source_id, excerpt, metadata)
		 VALUES ($1, $2, $3, $4, $5)
		 RETURNING id, memory_id, source_type, source_id, excerpt, metadata, created_at`,
		in.MemoryID, in.SourceType, in.SourceID, in.Excerpt, metadata,
	).Scan(&e.ID, &e.MemoryID, &e.SourceType, &e.SourceID, &e.Excerpt, &e.Metadata, &e.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *Repo) ListMemoryEntries(ctx context.Context, userID, scopeType, scopeKey, memoryType, status string, limit, offset int) ([]MemoryEntry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, scope_type, scope_key, memory_type, title, content, summary, content_hash,
		   confidence, importance, status, metadata, ttl_at, last_accessed_at, created_at, updated_at
		 FROM memory_entries
		 WHERE user_id = $1
		   AND ($2 = '' OR scope_type = $2)
		   AND ($3 = '' OR scope_key = $3)
		   AND ($4 = '' OR memory_type = $4)
		   AND ($5 = '' OR status = $5)
		 ORDER BY importance DESC, updated_at DESC
		 LIMIT $6 OFFSET $7`,
		userID, scopeType, scopeKey, memoryType, status, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]MemoryEntry, 0)
	for rows.Next() {
		var m MemoryEntry
		if err := rows.Scan(scanMemoryEntry(&m)...); err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return items, rows.Err()
}

func marshalMetadata(metadata map[string]any) ([]byte, error) {
	if metadata == nil {
		return []byte("{}"), nil
	}
	b, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	return b, nil
}

func scanDocument(d *Document) []any {
	return []any{
		&d.ID, &d.UserID, &d.Namespace, &d.SourceType, &d.SourceID, &d.ChunkKey, &d.Title, &d.Content,
		&d.ContentHash, &d.Metadata, &d.QdrantCollection, &d.QdrantPointID, &d.VectorName,
		&d.EmbeddingModel, &d.EmbeddingDimension, &d.Status, &d.Error, &d.IndexedAt, &d.CreatedAt, &d.UpdatedAt,
	}
}

func scanMemoryEntry(m *MemoryEntry) []any {
	return []any{
		&m.ID, &m.UserID, &m.ScopeType, &m.ScopeKey, &m.MemoryType, &m.Title, &m.Content, &m.Summary,
		&m.ContentHash, &m.Confidence, &m.Importance, &m.Status, &m.Metadata, &m.TTLAt,
		&m.LastAccessedAt, &m.CreatedAt, &m.UpdatedAt,
	}
}

func setDocumentDefaults(in *UpsertDocumentInput) {
	if in.ChunkKey == "" {
		in.ChunkKey = "default"
	}
	if in.QdrantCollection == "" {
		in.QdrantCollection = DefaultCollection
	}
	if in.VectorName == "" {
		in.VectorName = DefaultVectorName
	}
}

func setMemoryDefaults(in *CreateMemoryEntryInput) {
	if in.ScopeType == "" {
		in.ScopeType = "global"
	}
	if in.Status == "" {
		in.Status = StatusPending
	}
	if in.Confidence <= 0 {
		in.Confidence = 0.5
	}
	if in.Importance <= 0 {
		in.Importance = 0.5
	}
	if in.ContentHash == "" {
		in.ContentHash = sha256Hex(in.Content)
	}
}

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
