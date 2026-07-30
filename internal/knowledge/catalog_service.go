package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/retrieval"
)

const (
	knowledgeChunkSourceType = "knowledge_chunk"
	maxCatalogQueryRunes     = 500
	maxSearchQueryRunes      = 500
	maxSearchTopK            = 20
	defaultSearchTopK        = 5
	maxSearchSourceIDs       = 16
	maxNeighborsPerHit       = 16
	maxIndexErrorsPerSource  = 20
)

// ─── K0 catalog ───

func (s *Service) ListCatalog(ctx context.Context, accountID string, params KnowledgeCatalogListParams) (*KnowledgeCatalogListResponse, error) {
	if params.Limit == 0 {
		params.Limit = 20
	}
	if params.Limit < 1 || params.Limit > 100 {
		return nil, fmt.Errorf("limit must be between 1 and 100")
	}
	if params.Offset < 0 {
		return nil, fmt.Errorf("offset must be non-negative")
	}
	params.Query = strings.TrimSpace(params.Query)
	if utf8.RuneCountInString(params.Query) > maxCatalogQueryRunes {
		return nil, fmt.Errorf("query must be at most %d Unicode code points", maxCatalogQueryRunes)
	}
	params.Domain = strings.TrimSpace(params.Domain)

	records, err := s.repo.ListCatalog(ctx, accountID, params)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountCatalog(ctx, accountID, params.Query, params.Domain)
	if err != nil {
		return nil, err
	}
	domains, err := s.repo.ListCatalogDomains(ctx, accountID)
	if err != nil {
		return nil, err
	}
	items := make([]KnowledgeCatalogItem, 0, len(records))
	for _, record := range records {
		items = append(items, catalogItemFromRecord(record))
	}
	return &KnowledgeCatalogListResponse{
		Items:   items,
		Total:   total,
		Limit:   params.Limit,
		Offset:  params.Offset,
		Domains: domains,
	}, nil
}

func catalogItemFromRecord(record catalogRecord) KnowledgeCatalogItem {
	var manifest Manifest
	_ = json.Unmarshal(record.Manifest, &manifest)
	name := manifest.Name
	if name == "" {
		name = record.Name
	}
	return KnowledgeCatalogItem{
		SourceID:         record.SourceID,
		Name:             name,
		Domain:           record.Domain,
		Description:      manifest.Description,
		Profile:          manifest.Profile,
		Language:         manifest.Language,
		CitationPolicy:   manifest.CitationPolicy,
		Type:             record.Type,
		ActiveRevisionID: record.ActiveRevisionID,
		PackageHash:      record.PackageHash,
		DocumentCount:    record.DocumentCount,
		IndexedChunks:    record.IndexedChunks,
		FailedChunks:     record.FailedChunks,
		PendingChunks:    record.PendingChunks,
		IndexStatus:      catalogIndexStatus(record),
	}
}

func catalogIndexStatus(record catalogRecord) string {
	switch {
	case record.IndexedChunks > 0 && record.FailedChunks == 0 && record.PendingChunks == 0:
		return "indexed"
	case record.IndexedChunks > 0:
		return "partial"
	case record.FailedChunks > 0 || record.PendingChunks > 0:
		return "failed"
	default:
		return "not_indexed"
	}
}

// ─── Indexing ───

// IndexActiveRevisions chunk-indexes the indexable documents of every active
// revision (or one source when sourceID is set) into the account-scoped
// 'knowledge' retrieval namespace, and rebuilds the revision link graph.
// Storing chunk bodies in retrieval_documents is by design for K2 evidence
// retrieval — unlike the Skill L0-only rule — and stays account-scoped and
// rebuildable from knowledge_documents. Embedding or Qdrant failures are
// tolerated: the chunk row remains status=failed and keeps serving the
// PostgreSQL lexical fallback.
func (s *Service) IndexActiveRevisions(ctx context.Context, owner ownership.Owner, sourceID string) (*IndexKnowledgeResponse, error) {
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	sourceID = strings.TrimSpace(sourceID)
	sources, err := s.repo.ListIndexableSources(ctx, owner.Account(), sourceID)
	if err != nil {
		return nil, err
	}
	if sourceID != "" && len(sources) == 0 {
		return nil, fmt.Errorf("source not found or has no active revision")
	}

	response := &IndexKnowledgeResponse{
		Indexed: make([]IndexedKnowledgeSource, 0, len(sources)),
		Errors:  make([]KnowledgeIndexError, 0),
	}
	for _, source := range sources {
		indexed, indexErr := s.indexSource(ctx, owner, source)
		if indexErr != nil {
			response.Errors = append(response.Errors, KnowledgeIndexError{SourceID: source.ID, Error: indexErr.Error()})
			continue
		}
		response.Indexed = append(response.Indexed, *indexed)
	}
	return response, nil
}

func (s *Service) indexSource(ctx context.Context, owner ownership.Owner, source KnowledgeSource) (*IndexedKnowledgeSource, error) {
	if source.ActiveRevisionID == nil {
		return nil, fmt.Errorf("source has no active revision")
	}
	revisionID := *source.ActiveRevisionID
	revision, err := s.repo.GetRevision(ctx, owner.Account(), revisionID)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	_ = json.Unmarshal(revision.Manifest, &manifest)
	knowledgeBaseName := manifest.Name
	if knowledgeBaseName == "" {
		knowledgeBaseName = source.Name
	}

	documents, err := s.repo.ListRevisionIndexableDocuments(ctx, owner.Account(), revisionID)
	if err != nil {
		return nil, err
	}

	// Rebuild the revision link graph (idempotent for immutable revisions).
	documentIDsByPath := make(map[string]string, len(documents))
	for _, document := range documents {
		documentIDsByPath[document.Path] = document.ID
	}
	links := deriveDocumentLinks(documents)
	if err := s.repo.RebuildRevisionLinks(ctx, owner.Account(), revisionID, documentIDsByPath, links); err != nil {
		return nil, err
	}

	// Drop retrieval rows derived from older revisions of this source before
	// writing current chunks. Same-revision chunks upsert by stable chunk_key.
	staleDeleted, err := s.retrieval.DeleteDocumentsByMetadata(ctx, owner,
		retrieval.NamespaceKnowledge, knowledgeChunkSourceType,
		map[string]any{"source_id": source.ID},
		map[string]any{"revision_id": revisionID},
	)
	if err != nil {
		return nil, err
	}

	result := &IndexedKnowledgeSource{
		SourceID:     source.ID,
		Name:         knowledgeBaseName,
		RevisionID:   revisionID,
		Documents:    len(documents),
		LinksRebuilt: len(links),
		StaleDeleted: staleDeleted,
	}
	errorCount := 0
	aborted := false
	currentChunkKeys := make(map[string]map[string]struct{}, len(documents))
	for _, document := range documents {
		chunked := ChunkDocument(document.Path, document.MimeType, document.ContentSnapshot)
		if chunked.Truncated {
			result.TruncatedChunks++
		}
		keys := make(map[string]struct{}, len(chunked.Chunks))
		currentChunkKeys[document.ID] = keys
		for _, chunk := range chunked.Chunks {
			keys[chunk.Key] = struct{}{}
			title := document.Path
			if chunk.HeadingPath != "" {
				title = document.Path + " # " + chunk.HeadingPath
			}
			_, indexErr := s.retrieval.IndexDocument(ctx, owner, retrieval.UpsertDocumentInput{
				Namespace:  retrieval.NamespaceKnowledge,
				SourceType: knowledgeChunkSourceType,
				SourceID:   document.ID,
				ChunkKey:   chunk.Key,
				Title:      title,
				Content:    chunk.Content,
				Metadata: map[string]any{
					"source_id":      source.ID,
					"revision_id":    revisionID,
					"document_id":    document.ID,
					"path":           document.Path,
					"heading_path":   chunk.HeadingPath,
					"knowledge_base": knowledgeBaseName,
				},
			})
			if indexErr != nil {
				result.ChunksFailed++
				errorCount++
				if errorCount > maxIndexErrorsPerSource {
					aborted = true
				}
				continue
			}
			result.ChunksIndexed++
		}
		if aborted {
			break
		}
	}

	// Same-revision rows whose chunk_key is no longer produced (for example
	// after chunker limit changes shrink a document's chunk count) must not
	// keep serving stale content. Delete per document outside the current
	// key set. Skipped after an abort: the remaining documents were never
	// re-chunked, so their existing rows are still the freshest projection.
	if !aborted {
		for documentID, keys := range currentChunkKeys {
			deleted, staleErr := s.retrieval.DeleteDocumentChunksOutsideKeys(ctx, owner,
				retrieval.NamespaceKnowledge, knowledgeChunkSourceType, documentID, keys)
			if staleErr != nil {
				return nil, staleErr
			}
			result.StaleDeleted += deleted
		}
	}
	if aborted {
		return nil, fmt.Errorf("indexing aborted after %d chunk errors; %d chunks indexed, %d failed", errorCount, result.ChunksIndexed, result.ChunksFailed)
	}
	return result, nil
}

// deriveDocumentLinks extracts package-internal Markdown links from every
// indexable Markdown document. Deterministic: same documents, same links.
func deriveDocumentLinks(documents []KnowledgeDocument) []DocumentLinkInput {
	links := make([]DocumentLinkInput, 0)
	for _, document := range documents {
		if !document.Indexable || document.ContentSnapshot == "" {
			continue
		}
		if !isMarkdownDocument(document.Path, document.MimeType) {
			continue
		}
		for _, target := range ExtractMarkdownLinks(document.Path, document.ContentSnapshot) {
			links = append(links, DocumentLinkInput{SourcePath: document.Path, TargetPath: target})
		}
	}
	return links
}

// ─── Search ───

// Search runs account-scoped hybrid retrieval over the 'knowledge' namespace
// and enriches each chunk hit with 1-hop link neighbors (metadata only).
// Chunk bodies are returned only when include_content is set.
func (s *Service) Search(ctx context.Context, owner ownership.Owner, req SearchKnowledgeRequest) (*SearchKnowledgeResponse, error) {
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return nil, fmt.Errorf("query required")
	}
	if utf8.RuneCountInString(req.Query) > maxSearchQueryRunes {
		return nil, fmt.Errorf("query must be at most %d Unicode code points", maxSearchQueryRunes)
	}
	if req.TopK <= 0 || req.TopK > maxSearchTopK {
		req.TopK = defaultSearchTopK
	}
	if len(req.SourceIDs) > maxSearchSourceIDs {
		return nil, fmt.Errorf("source_ids must contain at most %d entries", maxSearchSourceIDs)
	}
	req.Domain = strings.TrimSpace(req.Domain)
	if req.Domain != "" {
		domainSourceIDs, err := s.repo.ListSourceIDsByDomain(ctx, owner.Account(), req.Domain)
		if err != nil {
			return nil, err
		}
		if len(domainSourceIDs) == 0 {
			return nil, fmt.Errorf("domain not found: %s", req.Domain)
		}
		if len(req.SourceIDs) == 0 {
			if len(domainSourceIDs) > maxSearchSourceIDs {
				return nil, fmt.Errorf("domain %s spans %d collections; narrow with source_ids (max %d)", req.Domain, len(domainSourceIDs), maxSearchSourceIDs)
			}
			req.SourceIDs = domainSourceIDs
		} else {
			// Both given: intersect, so domain can only narrow the request.
			allowed := make(map[string]struct{}, len(domainSourceIDs))
			for _, id := range domainSourceIDs {
				allowed[id] = struct{}{}
			}
			intersected := make([]string, 0, len(req.SourceIDs))
			for _, id := range req.SourceIDs {
				if _, ok := allowed[strings.TrimSpace(id)]; ok {
					intersected = append(intersected, strings.TrimSpace(id))
				}
			}
			if len(intersected) == 0 {
				return nil, fmt.Errorf("no requested source_ids belong to domain %s", req.Domain)
			}
			req.SourceIDs = intersected
		}
	}
	sourceFilter := make(map[string]struct{}, len(req.SourceIDs))
	for _, id := range req.SourceIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("source_ids must not contain empty entries")
		}
		// Ownership check: reject IDs the account cannot see instead of
		// silently returning nothing.
		if _, err := s.repo.GetSource(ctx, owner.Account(), id); err != nil {
			return nil, fmt.Errorf("source not found: %s", id)
		}
		sourceFilter[id] = struct{}{}
	}

	filters := map[string]any{"source_type": knowledgeChunkSourceType}
	// A single-value payload match can push down exactly one source filter;
	// multiple sources are post-filtered on hit metadata below.
	if len(sourceFilter) == 1 {
		for id := range sourceFilter {
			filters["metadata.source_id"] = id
		}
	}
	topK := req.TopK
	if len(sourceFilter) > 1 {
		// Widen the candidate pool since post-filtering discards hits.
		topK = req.TopK * len(sourceFilter)
		if topK > 50 {
			topK = 50
		}
	}

	results, err := s.retrieval.SearchHybrid(ctx, owner, retrieval.SearchRequest{
		Namespace: retrieval.NamespaceKnowledge,
		Query:     req.Query,
		TopK:      topK,
		Filters:   filters,
		Metadata:  map[string]any{"feature": "knowledge_search"},
	})
	if err != nil {
		return nil, err
	}

	hits := make([]KnowledgeSearchHit, 0, len(results))
	hitDocumentIDs := make([]string, 0, len(results))
	seenDocuments := make(map[string]struct{})
	for _, result := range results {
		if result.Document == nil {
			continue
		}
		meta := documentMetadata(result.Document.Metadata)
		hitSourceID := stringMeta(meta, "source_id")
		if len(sourceFilter) > 1 {
			if _, ok := sourceFilter[hitSourceID]; !ok {
				continue
			}
		}
		hit := KnowledgeSearchHit{
			DocumentID:  stringMeta(meta, "document_id"),
			SourceID:    hitSourceID,
			RevisionID:  stringMeta(meta, "revision_id"),
			Path:        stringMeta(meta, "path"),
			HeadingPath: stringMeta(meta, "heading_path"),
			ChunkKey:    result.Document.ChunkKey,
			Knowledge:   stringMeta(meta, "knowledge_base"),
			Score:       result.Score,
			Snippet:     truncateChunkRunes(result.Document.Content, snippetRunes),
			Neighbors:   []KnowledgeDocumentLinkItem{},
		}
		if req.IncludeContent {
			hit.Content = result.Document.Content
		}
		hits = append(hits, hit)
		if hit.DocumentID != "" {
			if _, ok := seenDocuments[hit.DocumentID]; !ok {
				seenDocuments[hit.DocumentID] = struct{}{}
				hitDocumentIDs = append(hitDocumentIDs, hit.DocumentID)
			}
		}
		if len(hits) >= req.TopK {
			break
		}
	}
	for index := range hits {
		hits[index].Rank = index + 1
	}

	neighborsByDocument, err := s.repo.ListLinksForDocuments(ctx, owner.Account(), hitDocumentIDs, maxNeighborsPerHit)
	if err != nil {
		return nil, err
	}
	for index := range hits {
		if neighbors, ok := neighborsByDocument[hits[index].DocumentID]; ok {
			hits[index].Neighbors = neighbors
		}
	}
	return &SearchKnowledgeResponse{Items: hits, Total: len(hits)}, nil
}

// ─── Document links ───

func (s *Service) ListDocumentLinks(ctx context.Context, accountID, documentID string, limit, offset int) (*DocumentLinksResponse, error) {
	document, err := s.repo.GetDocumentByID(ctx, accountID, documentID)
	if err != nil {
		return nil, err
	}
	total, err := s.repo.CountDocumentLinks(ctx, accountID, document.ID)
	if err != nil {
		return nil, err
	}
	items, err := s.repo.ListDocumentLinks(ctx, accountID, document.ID, limit, offset)
	if err != nil {
		return nil, err
	}
	return &DocumentLinksResponse{
		DocumentID: document.ID,
		RevisionID: document.RevisionID,
		Items:      items,
		Total:      total,
		Limit:      limit,
		Offset:     offset,
	}, nil
}

// ─── metadata helpers ───

func documentMetadata(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var metadata map[string]any
	if err := json.Unmarshal(raw, &metadata); err != nil || metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func stringMeta(metadata map[string]any, key string) string {
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
}
