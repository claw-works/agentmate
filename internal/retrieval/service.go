package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/wellxie/agentmate/internal/ownership"
)

type Service struct {
	repo        *Repo
	store       VectorStore
	embedder    Embedder
	rerankModel string
}

func NewService(repo *Repo, store VectorStore, embedder Embedder) *Service {
	return &Service{
		repo:     repo,
		store:    store,
		embedder: embedder,
	}
}

func (s *Service) IndexDocument(ctx context.Context, owner ownership.Owner, in UpsertDocumentInput) (*Document, error) {
	if owner.Account() == "" {
		return nil, fmt.Errorf("account id required")
	}
	if in.Namespace == "" {
		return nil, fmt.Errorf("namespace required")
	}
	if in.SourceType == "" {
		return nil, fmt.Errorf("source_type required")
	}
	if in.SourceID == "" {
		return nil, fmt.Errorf("source_id required")
	}
	if in.Content == "" {
		return nil, fmt.Errorf("content required")
	}

	if in.ContentHash == "" {
		in.ContentHash = sha256Hex(in.Content)
	}
	if in.EmbeddingModel == "" {
		in.EmbeddingModel = s.embedder.Model()
	}
	if in.QdrantCollection == "" {
		in.QdrantCollection = s.store.Collection()
	}
	if in.VectorName == "" {
		in.VectorName = s.store.VectorName()
	}

	vectors, err := s.embedder.Embed(ctx, []string{in.Content})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("empty embedding")
	}
	in.EmbeddingDimension = len(vectors[0])

	if err := s.store.EnsureCollection(ctx, in.EmbeddingDimension); err != nil {
		return nil, err
	}

	doc, err := s.repo.UpsertDocument(ctx, owner, in)
	if err != nil {
		return nil, err
	}

	err = s.store.Upsert(ctx, []VectorPoint{{
		ID:      doc.QdrantPointID,
		Vector:  vectors[0],
		Payload: payloadForDocument(doc),
	}})
	if err != nil {
		_ = s.repo.MarkDocumentFailed(ctx, owner.Account(), doc.ID, err.Error())
		return nil, err
	}

	if err := s.repo.MarkDocumentIndexed(ctx, owner.Account(), doc.ID); err != nil {
		return nil, err
	}
	return s.repo.GetDocument(ctx, owner.Account(), doc.ID)
}

func (s *Service) Search(ctx context.Context, owner ownership.Owner, req SearchRequest) ([]SearchResult, error) {
	if owner.Account() == "" {
		return nil, fmt.Errorf("account id required")
	}
	if req.Namespace == "" {
		return nil, fmt.Errorf("namespace required")
	}
	if req.Query == "" {
		return nil, fmt.Errorf("query required")
	}
	if req.TopK <= 0 || req.TopK > 50 {
		req.TopK = DefaultTopK
	}

	start := time.Now()
	vectors, err := s.embedder.Embed(ctx, []string{req.Query})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return nil, fmt.Errorf("empty query embedding")
	}

	filterValues := map[string]any{
		"account_id": owner.Account(),
		"namespace":  req.Namespace,
	}
	for k, v := range req.Filters {
		filterValues[k] = v
	}

	vectorResults, err := s.store.Search(ctx, VectorSearchRequest{
		Vector:      vectors[0],
		Limit:       req.TopK,
		Filter:      PayloadMatchFilter(filterValues),
		WithPayload: true,
	})
	if err != nil {
		return nil, err
	}

	pointIDs := make([]string, 0, len(vectorResults))
	for _, item := range vectorResults {
		pointIDs = append(pointIDs, item.ID)
	}
	docsByPointID, err := s.repo.DocumentsByPointIDs(ctx, owner.Account(), s.store.Collection(), pointIDs)
	if err != nil {
		return nil, err
	}

	results := make([]SearchResult, 0, len(vectorResults))
	queryResultInputs := make([]QueryResultInput, 0, len(vectorResults))
	for i, item := range vectorResults {
		var docPtr *Document
		var docID *string
		if doc, ok := docsByPointID[item.ID]; ok {
			docCopy := doc
			docPtr = &docCopy
			docID = &docCopy.ID
		}
		results = append(results, SearchResult{
			Document: docPtr,
			PointID:  item.ID,
			Rank:     i + 1,
			Score:    item.Score,
			Stage:    "vector",
			Payload:  item.Payload,
		})
		queryResultInputs = append(queryResultInputs, QueryResultInput{
			DocumentID:    docID,
			QdrantPointID: item.ID,
			Rank:          i + 1,
			Score:         item.Score,
			Stage:         "vector",
		})
	}

	queryLog, err := s.repo.CreateQueryLog(ctx, owner, CreateQueryLogInput{
		Namespace:      req.Namespace,
		Query:          req.Query,
		QueryHash:      sha256Hex(req.Query),
		TopK:           req.TopK,
		CandidateCount: len(vectorResults),
		SelectedCount:  len(results),
		EmbeddingModel: s.embedder.Model(),
		RerankModel:    s.rerankModel,
		LatencyMs:      int(time.Since(start).Milliseconds()),
		Metadata:       req.Metadata,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.AddQueryResults(ctx, queryLog.ID, queryResultInputs); err != nil {
		return nil, err
	}
	return results, nil
}

func payloadForDocument(doc *Document) map[string]any {
	payload := map[string]any{
		"document_id":  doc.ID,
		"account_id":   doc.AccountID,
		"user_id":      doc.UserID,
		"namespace":    doc.Namespace,
		"source_type":  doc.SourceType,
		"source_id":    doc.SourceID,
		"chunk_key":    doc.ChunkKey,
		"title":        doc.Title,
		"content_hash": doc.ContentHash,
	}
	if doc.KeyID != nil {
		payload["key_id"] = *doc.KeyID
	}
	if len(doc.Metadata) > 0 {
		var metadata map[string]any
		if err := json.Unmarshal(doc.Metadata, &metadata); err == nil && metadata != nil {
			payload["metadata"] = metadata
		}
	}
	return payload
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
