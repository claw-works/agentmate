package retrieval

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/ownership"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) UpsertDocument(ctx context.Context, owner ownership.Owner, in UpsertDocumentInput) (*Document, error) {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	setDocumentDefaults(&in)

	var d Document
	err = r.pool.QueryRow(ctx,
		`INSERT INTO retrieval_documents
		 (account_id, user_id, key_id, namespace, source_type, source_id, chunk_key, title, content, content_hash, metadata,
		  qdrant_collection, vector_name, embedding_model, embedding_dimension, status, error, indexed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, 'pending', '', NULL)
		 ON CONFLICT (account_id, namespace, source_type, source_id, chunk_key)
		 DO UPDATE SET
		   user_id = EXCLUDED.user_id,
		   key_id = EXCLUDED.key_id,
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
		 RETURNING id, account_id, user_id, key_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at`,
		owner.Account(), owner.UserID, owner.KeyID, in.Namespace, in.SourceType, in.SourceID, in.ChunkKey, in.Title, in.Content, in.ContentHash,
		metadata, in.QdrantCollection, in.VectorName, in.EmbeddingModel, in.EmbeddingDimension,
	).Scan(scanDocument(&d)...)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repo) MarkDocumentIndexed(ctx context.Context, accountID, id string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE retrieval_documents
		 SET status = 'indexed', error = '', indexed_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND account_id = $2`,
		id, accountID,
	)
	return err
}

func (r *Repo) MarkDocumentFailed(ctx context.Context, accountID, id, message string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE retrieval_documents
		 SET status = 'failed', error = $3, updated_at = NOW()
		 WHERE id = $1 AND account_id = $2`,
		id, accountID, message,
	)
	return err
}

func (r *Repo) GetDocument(ctx context.Context, accountID, id string) (*Document, error) {
	var d Document
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at
		 FROM retrieval_documents WHERE id = $1 AND account_id = $2`,
		id, accountID,
	).Scan(scanDocument(&d)...)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *Repo) DocumentsByPointIDs(ctx context.Context, accountID, collection string, pointIDs []string) (map[string]Document, error) {
	result := make(map[string]Document, len(pointIDs))
	if len(pointIDs) == 0 {
		return result, nil
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at
		 FROM retrieval_documents
		 WHERE account_id = $1
		   AND qdrant_collection = $2
		   AND status = 'indexed'
		   AND qdrant_point_id::text = ANY($3)`,
		accountID, collection, pointIDs,
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

func (r *Repo) SearchDocumentsText(ctx context.Context, accountID, namespace, query string, limit int) ([]Document, error) {
	results, err := r.SearchDocumentsTextFiltered(ctx, accountID, namespace, query, limit, TextSearchFilters{})
	if err != nil {
		return nil, err
	}
	items := make([]Document, 0, len(results))
	for _, result := range results {
		items = append(items, result.Document)
	}
	return items, nil
}

func (r *Repo) SearchDocumentsTextFiltered(
	ctx context.Context,
	accountID, namespace, query string,
	limit int,
	filters TextSearchFilters,
) ([]TextSearchResult, error) {
	if limit <= 0 || limit > 50 {
		limit = DefaultTopK
	}
	metadata, err := marshalMetadata(filters.Metadata)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, namespace, source_type, source_id, chunk_key, title, content, content_hash,
		   metadata, qdrant_collection, qdrant_point_id, vector_name, embedding_model, embedding_dimension,
		   status, error, indexed_at, created_at, updated_at,
		   ts_rank_cd(
		     to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')),
		     plainto_tsquery('simple', $3)
		   ) AS text_score
		 FROM retrieval_documents
		 WHERE account_id = $1
		   AND namespace = $2
		   AND status IN ('indexed', 'failed')
		   AND to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', $3)
		   AND ($4 = '' OR source_type = $4)
		   AND ($5 = '' OR source_id = $5)
		   AND metadata @> $6::jsonb
		 ORDER BY text_score DESC, updated_at DESC
		 LIMIT $7`,
		accountID, namespace, query, filters.SourceType, filters.SourceID, metadata, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]TextSearchResult, 0)
	for rows.Next() {
		var d Document
		var score float64
		destinations := append(scanDocument(&d), &score)
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		items = append(items, TextSearchResult{Document: d, Score: score})
	}
	return items, rows.Err()
}

// DeleteDocumentsByMetadata removes account-scoped documents in one
// namespace/source_type whose metadata contains `match` and does not contain
// `exclude`. An empty exclude keeps every match eligible for deletion. Stale
// Qdrant points are intentionally left behind: vector hits are re-verified
// against PostgreSQL during hydration, so orphaned points become
// non-hydratable (same safety model as the 000018 skill reindex migration).
func (r *Repo) DeleteDocumentsByMetadata(ctx context.Context, accountID, namespace, sourceType string, match, exclude map[string]any) (int64, error) {
	matchJSON, err := marshalMetadata(match)
	if err != nil {
		return 0, err
	}
	excludeJSON, err := marshalMetadata(exclude)
	if err != nil {
		return 0, err
	}
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM retrieval_documents
		 WHERE account_id = $1
		   AND namespace = $2
		   AND source_type = $3
		   AND metadata @> $4::jsonb
		   AND ($5::jsonb = '{}'::jsonb OR NOT metadata @> $5::jsonb)`,
		accountID, namespace, sourceType, matchJSON, excludeJSON,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DeleteSourceChunksOutsideKeys removes rows of one source document whose
// chunk_key is not in keepKeys (an empty set deletes every chunk of the
// document). Orphaned Qdrant points stay non-hydratable, matching
// DeleteDocumentsByMetadata semantics.
func (r *Repo) DeleteSourceChunksOutsideKeys(ctx context.Context, accountID, namespace, sourceType, sourceID string, keepKeys []string) (int64, error) {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM retrieval_documents
		 WHERE account_id = $1
		   AND namespace = $2
		   AND source_type = $3
		   AND source_id = $4
		   AND NOT (chunk_key = ANY($5::text[]))`,
		accountID, namespace, sourceType, sourceID, keepKeys,
	)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (r *Repo) CreateIndexJob(ctx context.Context, owner ownership.Owner, namespace, sourceType, sourceID string) (string, error) {
	var id string
	err := r.pool.QueryRow(ctx,
		`INSERT INTO retrieval_index_jobs (account_id, user_id, key_id, namespace, source_type, source_id)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		owner.Account(), owner.UserID, owner.KeyID, namespace, sourceType, sourceID,
	).Scan(&id)
	return id, err
}

func (r *Repo) CreateQueryLog(ctx context.Context, owner ownership.Owner, in CreateQueryLogInput) (*QueryLog, error) {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return nil, err
	}
	var q QueryLog
	err = r.pool.QueryRow(ctx,
		`INSERT INTO retrieval_queries
		 (account_id, user_id, key_id, namespace, query, query_hash, top_k, candidate_count, selected_count, embedding_model, rerank_model, latency_ms, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		 RETURNING id, account_id, user_id, key_id, namespace, query, query_hash, top_k, candidate_count, selected_count,
		   embedding_model, rerank_model, latency_ms, metadata, created_at`,
		nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, in.Namespace, in.Query, in.QueryHash, in.TopK, in.CandidateCount, in.SelectedCount,
		in.EmbeddingModel, in.RerankModel, in.LatencyMs, metadata,
	).Scan(&q.ID, &q.AccountID, &q.UserID, &q.KeyID, &q.Namespace, &q.Query, &q.QueryHash, &q.TopK, &q.CandidateCount,
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

func (r *Repo) AddFeedback(ctx context.Context, owner ownership.Owner, in FeedbackInput) error {
	metadata, err := marshalMetadata(in.Metadata)
	if err != nil {
		return err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO retrieval_feedback (query_id, document_id, account_id, user_id, key_id, signal, reason, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		in.QueryID, in.DocumentID, nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, in.Signal, in.Reason, metadata,
	)
	return err
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
		&d.ID, &d.AccountID, &d.UserID, &d.KeyID, &d.Namespace, &d.SourceType, &d.SourceID, &d.ChunkKey, &d.Title, &d.Content,
		&d.ContentHash, &d.Metadata, &d.QdrantCollection, &d.QdrantPointID, &d.VectorName,
		&d.EmbeddingModel, &d.EmbeddingDimension, &d.Status, &d.Error, &d.IndexedAt, &d.CreatedAt, &d.UpdatedAt,
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

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
