package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
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

	doc, err := s.repo.UpsertDocument(ctx, owner, in)
	if err != nil {
		return nil, err
	}

	vectors, err := s.embedder.Embed(ctx, []string{in.Content})
	if err != nil {
		_ = s.repo.MarkDocumentFailed(ctx, owner.Account(), doc.ID, err.Error())
		return nil, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		err = fmt.Errorf("empty embedding")
		_ = s.repo.MarkDocumentFailed(ctx, owner.Account(), doc.ID, err.Error())
		return nil, err
	}
	in.EmbeddingDimension = len(vectors[0])

	if err := s.store.EnsureCollection(ctx, in.EmbeddingDimension); err != nil {
		_ = s.repo.MarkDocumentFailed(ctx, owner.Account(), doc.ID, err.Error())
		return nil, err
	}

	doc, err = s.repo.UpsertDocument(ctx, owner, in)
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

// DeleteDocumentsByMetadata removes account-scoped derived documents by
// metadata containment. See Repo.DeleteDocumentsByMetadata for the stale
// Qdrant point semantics.
func (s *Service) DeleteDocumentsByMetadata(ctx context.Context, owner ownership.Owner, namespace, sourceType string, match, exclude map[string]any) (int64, error) {
	if owner.Account() == "" {
		return 0, fmt.Errorf("account id required")
	}
	if namespace == "" {
		return 0, fmt.Errorf("namespace required")
	}
	if sourceType == "" {
		return 0, fmt.Errorf("source_type required")
	}
	if len(match) == 0 {
		return 0, fmt.Errorf("match metadata required")
	}
	return s.repo.DeleteDocumentsByMetadata(ctx, owner.Account(), namespace, sourceType, match, exclude)
}

// DeleteDocumentChunksOutsideKeys removes account-scoped derived rows of one
// source document whose chunk_key falls outside keepKeys. Stale Qdrant
// points become non-hydratable, matching DeleteDocumentsByMetadata.
func (s *Service) DeleteDocumentChunksOutsideKeys(ctx context.Context, owner ownership.Owner, namespace, sourceType, sourceID string, keepKeys map[string]struct{}) (int64, error) {
	if owner.Account() == "" {
		return 0, fmt.Errorf("account id required")
	}
	if namespace == "" {
		return 0, fmt.Errorf("namespace required")
	}
	if sourceType == "" {
		return 0, fmt.Errorf("source_type required")
	}
	if sourceID == "" {
		return 0, fmt.Errorf("source_id required")
	}
	keys := make([]string, 0, len(keepKeys))
	for key := range keepKeys {
		keys = append(keys, key)
	}
	return s.repo.DeleteSourceChunksOutsideKeys(ctx, owner.Account(), namespace, sourceType, sourceID, keys)
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

func (s *Service) SearchHybrid(ctx context.Context, owner ownership.Owner, req SearchRequest) ([]SearchResult, error) {
	if owner.Account() == "" {
		return nil, fmt.Errorf("account id required")
	}
	if req.Namespace == "" {
		return nil, fmt.Errorf("namespace required")
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("query required")
	}
	if req.TopK <= 0 || req.TopK > 50 {
		req.TopK = DefaultTopK
	}

	textFilters, err := textFiltersFromSearch(req.Filters)
	if err != nil {
		return nil, err
	}
	candidateLimit := req.TopK * 4
	if candidateLimit < 20 {
		candidateLimit = 20
	}
	if candidateLimit > 50 {
		candidateLimit = 50
	}

	start := time.Now()
	textResults, textErr := s.repo.SearchDocumentsTextFiltered(
		ctx, owner.Account(), req.Namespace, req.Query, candidateLimit, textFilters,
	)

	var vectorResults []VectorSearchResult
	var docsByPointID map[string]Document
	vectors, vectorErr := s.embedder.Embed(ctx, []string{req.Query})
	if vectorErr == nil {
		if len(vectors) != 1 || len(vectors[0]) == 0 {
			vectorErr = fmt.Errorf("empty query embedding")
		} else {
			filterValues := map[string]any{
				"account_id": owner.Account(),
				"namespace":  req.Namespace,
			}
			for key, value := range req.Filters {
				filterValues[key] = value
			}
			vectorResults, vectorErr = s.store.Search(ctx, VectorSearchRequest{
				Vector:      vectors[0],
				Limit:       candidateLimit,
				Filter:      PayloadMatchFilter(filterValues),
				WithPayload: true,
			})
		}
	}
	if vectorErr == nil {
		pointIDs := make([]string, 0, len(vectorResults))
		for _, item := range vectorResults {
			pointIDs = append(pointIDs, item.ID)
		}
		docsByPointID, vectorErr = s.repo.DocumentsByPointIDs(ctx, owner.Account(), s.store.Collection(), pointIDs)
	}
	if textErr != nil && vectorErr != nil {
		return nil, fmt.Errorf("hybrid search failed: lexical: %v; vector: %v", textErr, vectorErr)
	}

	results, queryResults := fuseSearchCandidates(vectorResults, docsByPointID, textResults, req.TopK)
	logMetadata := cloneMetadata(req.Metadata)
	logMetadata["retrieval_mode"] = "hybrid"
	if textErr != nil {
		logMetadata["lexical_error"] = textErr.Error()
	}
	if vectorErr != nil {
		logMetadata["vector_error"] = vectorErr.Error()
	}

	queryLog, err := s.repo.CreateQueryLog(ctx, owner, CreateQueryLogInput{
		Namespace:      req.Namespace,
		Query:          req.Query,
		QueryHash:      sha256Hex(req.Query),
		TopK:           req.TopK,
		CandidateCount: countUniqueCandidates(vectorResults, docsByPointID, textResults),
		SelectedCount:  len(results),
		EmbeddingModel: s.embedder.Model(),
		RerankModel:    "rrf",
		LatencyMs:      int(time.Since(start).Milliseconds()),
		Metadata:       logMetadata,
	})
	if err != nil {
		return nil, err
	}
	if err := s.repo.AddQueryResults(ctx, queryLog.ID, queryResults); err != nil {
		return nil, err
	}
	return results, nil
}

type fusedCandidate struct {
	document    Document
	pointID     string
	payload     map[string]any
	rrfScore    float64
	vectorScore float64
	textScore   float64
	vector      bool
	lexical     bool
}

func fuseSearchCandidates(
	vectorResults []VectorSearchResult,
	docsByPointID map[string]Document,
	textResults []TextSearchResult,
	topK int,
) ([]SearchResult, []QueryResultInput) {
	const rrfK = 60.0
	candidates := make(map[string]*fusedCandidate)

	for index, result := range vectorResults {
		document, ok := docsByPointID[result.ID]
		if !ok {
			continue
		}
		candidate := candidates[document.ID]
		if candidate == nil {
			candidate = &fusedCandidate{document: document, pointID: result.ID, payload: cloneMetadata(result.Payload)}
			candidates[document.ID] = candidate
		}
		candidate.rrfScore += 1 / (rrfK + float64(index+1))
		candidate.vectorScore = result.Score
		candidate.vector = true
	}
	for index, result := range textResults {
		candidate := candidates[result.Document.ID]
		if candidate == nil {
			candidate = &fusedCandidate{
				document: result.Document,
				payload:  map[string]any{},
			}
			candidates[result.Document.ID] = candidate
		}
		candidate.rrfScore += 1 / (rrfK + float64(index+1))
		candidate.textScore = result.Score
		candidate.lexical = true
	}

	ordered := make([]*fusedCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		ordered = append(ordered, candidate)
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].rrfScore == ordered[j].rrfScore {
			if ordered[i].document.UpdatedAt.Equal(ordered[j].document.UpdatedAt) {
				return ordered[i].document.ID < ordered[j].document.ID
			}
			return ordered[i].document.UpdatedAt.After(ordered[j].document.UpdatedAt)
		}
		return ordered[i].rrfScore > ordered[j].rrfScore
	})
	if topK > 0 && len(ordered) > topK {
		ordered = ordered[:topK]
	}

	maxRRFScore := 2.0 / (rrfK + 1)
	results := make([]SearchResult, 0, len(ordered))
	queryResults := make([]QueryResultInput, 0, len(ordered))
	for index, candidate := range ordered {
		channels := make([]string, 0, 2)
		if candidate.lexical {
			channels = append(channels, "lexical")
		}
		if candidate.vector {
			channels = append(channels, "vector")
		}
		stage := strings.Join(channels, "+")
		score := candidate.rrfScore / maxRRFScore
		payload := cloneMetadata(candidate.payload)
		payload["channels"] = channels
		payload["vector_score"] = candidate.vectorScore
		payload["text_score"] = candidate.textScore
		document := candidate.document
		results = append(results, SearchResult{
			Document: &document,
			PointID:  candidate.pointID,
			Rank:     index + 1,
			Score:    score,
			Stage:    stage,
			Payload:  payload,
		})
		documentID := document.ID
		queryResults = append(queryResults, QueryResultInput{
			DocumentID:    &documentID,
			QdrantPointID: candidate.pointID,
			Rank:          index + 1,
			Score:         score,
			Stage:         stage,
			Metadata: map[string]any{
				"channels":     channels,
				"vector_score": candidate.vectorScore,
				"text_score":   candidate.textScore,
			},
		})
	}
	return results, queryResults
}

func textFiltersFromSearch(filters map[string]any) (TextSearchFilters, error) {
	result := TextSearchFilters{Metadata: map[string]any{}}
	for key, value := range filters {
		switch {
		case key == "source_type":
			text, ok := value.(string)
			if !ok {
				return result, fmt.Errorf("source_type filter must be a string")
			}
			result.SourceType = text
		case key == "source_id":
			text, ok := value.(string)
			if !ok {
				return result, fmt.Errorf("source_id filter must be a string")
			}
			result.SourceID = text
		case strings.HasPrefix(key, "metadata.") && len(key) > len("metadata."):
			result.Metadata[strings.TrimPrefix(key, "metadata.")] = value
		default:
			return result, fmt.Errorf("unsupported hybrid search filter %q", key)
		}
	}
	return result, nil
}

func countUniqueCandidates(
	vectorResults []VectorSearchResult,
	docsByPointID map[string]Document,
	textResults []TextSearchResult,
) int {
	ids := make(map[string]struct{})
	for _, result := range vectorResults {
		if document, ok := docsByPointID[result.ID]; ok {
			ids[document.ID] = struct{}{}
		}
	}
	for _, result := range textResults {
		ids[result.Document.ID] = struct{}{}
	}
	return len(ids)
}

func cloneMetadata(metadata map[string]any) map[string]any {
	clone := make(map[string]any, len(metadata)+2)
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
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
