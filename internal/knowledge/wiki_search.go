package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/retrieval"
)

// ─── K3.6: wiki pages in retrieval ───
//
// Until now retrieval only reached raw chunks, so the wiki was compiled and then never
// found — the acceleration layer sat on the first layer instead of the second. Wiki pages
// get their own namespace rather than joining the raw one, because a synthesis and the
// documents it was synthesised from should not compete in a single ranking: the synthesis
// usually wins and the evidence it rests on disappears from the results.
//
// Only the active build is searchable, and search filters on the active build id rather
// than trusting the index to be current. Builds are immutable and retained, so an index
// covering all of them would serve pages from wikis that were rolled back while the read
// API served the restored one. Filtering means a stale index returns fewer hits instead of
// wrong ones — the cheap failure rather than the silent one.

const wikiPageSourceType = "knowledge_wiki_page"

// IndexActiveWikiBuilds indexes the pages of each source's active build.
//
// Explicit rather than triggered by activation, matching the raw indexing path: embedding
// round trips take seconds per chunk and cannot run inside a pointer move without either
// making activation fail on a provider hiccup or leaving the index quietly behind. The
// gap is recorded instead — indexed_build_id against active_build_id — so it is visible
// rather than assumed away.
func (s *Service) IndexActiveWikiBuilds(ctx context.Context, owner ownership.Owner, sourceID string) (*IndexWikiResponse, error) {
	if s.retrieval == nil {
		return nil, fmt.Errorf("retrieval service is not configured")
	}
	sourceID = strings.TrimSpace(sourceID)
	sources, err := s.repo.ListSourcesWithActiveBuild(ctx, owner.Account(), sourceID)
	if err != nil {
		return nil, err
	}
	if sourceID != "" && len(sources) == 0 {
		return nil, fmt.Errorf("source not found or has no active wiki build")
	}

	response := &IndexWikiResponse{
		Indexed: make([]IndexedWikiBuild, 0, len(sources)),
		Errors:  make([]KnowledgeIndexError, 0),
	}
	for _, source := range sources {
		indexed, indexErr := s.indexWikiBuild(ctx, owner, source)
		if indexErr != nil {
			response.Errors = append(response.Errors, KnowledgeIndexError{SourceID: source.ID, Error: indexErr.Error()})
			continue
		}
		response.Indexed = append(response.Indexed, *indexed)
	}
	return response, nil
}

func (s *Service) indexWikiBuild(ctx context.Context, owner ownership.Owner, source KnowledgeSource) (*IndexedWikiBuild, error) {
	if source.ActiveBuildID == nil {
		return nil, fmt.Errorf("source has no active wiki build")
	}
	buildID := *source.ActiveBuildID
	pages, err := s.repo.ListPages(ctx, owner.Account(), buildID, true)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("active build %s has no pages", shortID(buildID))
	}

	// Rows from earlier builds of this source are removed before writing. Without this
	// the index would accumulate every build ever activated, and the build filter at
	// query time would be doing the work of a garbage collector.
	staleDeleted, err := s.retrieval.DeleteDocumentsByMetadata(ctx, owner,
		retrieval.NamespaceKnowledgeWiki, wikiPageSourceType,
		map[string]any{"source_id": source.ID},
		map[string]any{"build_id": buildID},
	)
	if err != nil {
		return nil, err
	}

	result := &IndexedWikiBuild{
		SourceID: source.ID, Name: source.Name, BuildID: buildID,
		Pages: len(pages), StaleDeleted: staleDeleted,
	}
	errorCount := 0
	currentKeys := make(map[string]map[string]struct{}, len(pages))
	for _, page := range pages {
		// The log page is a build transcript, not knowledge. Indexing it would put
		// "page_written wiki/x.md" into the same ranking as the wiki's actual claims and
		// let an agent cite the build process as a fact about the domain.
		if page.Kind == PageKindLog {
			result.PagesSkipped++
			continue
		}
		chunked := ChunkDocument(page.Path, "text/markdown", page.Content)
		if chunked.Truncated {
			result.TruncatedChunks++
		}
		keys := make(map[string]struct{}, len(chunked.Chunks))
		currentKeys[page.ID] = keys
		for _, chunk := range chunked.Chunks {
			keys[chunk.Key] = struct{}{}
			title := page.Path
			if page.Title != "" {
				title = page.Title + " (" + page.Path + ")"
			}
			if chunk.HeadingPath != "" {
				title += " # " + chunk.HeadingPath
			}
			_, indexErr := s.retrieval.IndexDocument(ctx, owner, retrieval.UpsertDocumentInput{
				Namespace:  retrieval.NamespaceKnowledgeWiki,
				SourceType: wikiPageSourceType,
				SourceID:   page.ID,
				ChunkKey:   chunk.Key,
				Title:      title,
				Content:    chunk.Content,
				Metadata: map[string]any{
					"source_id":      source.ID,
					"build_id":       buildID,
					"page_id":        page.ID,
					"path":           page.Path,
					"kind":           page.Kind,
					"page_title":     page.Title,
					"heading_path":   chunk.HeadingPath,
					"knowledge_base": source.Name,
					// Recorded so a hit can be traced to the model run that wrote it.
					// A wiki page is generated text; which generation produced it is
					// part of reading it.
					"derived_from_build_id": derivedFrom(page),
				},
			})
			if indexErr != nil {
				result.ChunksFailed++
				errorCount++
				if errorCount > maxIndexErrorsPerSource {
					return nil, fmt.Errorf("wiki indexing aborted after %d chunk errors; %d indexed, %d failed",
						errorCount, result.ChunksIndexed, result.ChunksFailed)
				}
				continue
			}
			result.ChunksIndexed++
		}
	}

	// Same-build rows whose chunk key is no longer produced must not keep serving text
	// the page no longer contains.
	for pageID, keys := range currentKeys {
		deleted, staleErr := s.retrieval.DeleteDocumentChunksOutsideKeys(ctx, owner,
			retrieval.NamespaceKnowledgeWiki, wikiPageSourceType, pageID, keys)
		if staleErr != nil {
			return nil, staleErr
		}
		result.StaleDeleted += deleted
	}

	if err := s.repo.MarkWikiIndexed(ctx, owner.Account(), source.ID, buildID); err != nil {
		return nil, err
	}
	return result, nil
}

func derivedFrom(page WikiPage) string {
	if page.DerivedFromBuildID == nil {
		return ""
	}
	return *page.DerivedFromBuildID
}

// SearchWiki runs hybrid retrieval over compiled wiki pages.
//
// This is the first level of the two-level query: find the synthesised page, then follow
// its citations down to the raw documents for evidence. Each hit therefore carries the
// page's citations and typed links — without them an agent would have a plausible
// paragraph and no way to check it, which is the failure mode a generated wiki invites.
func (s *Service) SearchWiki(ctx context.Context, owner ownership.Owner, req SearchWikiRequest) (*SearchWikiResponse, error) {
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
		return nil, fmt.Errorf("at most %d source ids may be given", maxSearchSourceIDs)
	}

	// The set of builds a hit is allowed to come from, resolved from the active pointer
	// rather than from whatever the index happens to hold. An index left behind by a
	// rollback then yields nothing for that source instead of the wiki that was rolled
	// back.
	sources, err := s.repo.ListSourcesWithActiveBuild(ctx, owner.Account(), "")
	if err != nil {
		return nil, err
	}
	activeBuilds := make(map[string]string, len(sources))
	requested := pagePathSet(req.SourceIDs)
	for _, source := range sources {
		if len(req.SourceIDs) > 0 {
			if _, wanted := requested[source.ID]; !wanted {
				continue
			}
		}
		if req.Domain != "" && !strings.EqualFold(source.Domain, req.Domain) {
			continue
		}
		activeBuilds[*source.ActiveBuildID] = source.ID
	}
	response := &SearchWikiResponse{Query: req.Query, Items: make([]WikiSearchHit, 0), TopK: req.TopK}
	if len(activeBuilds) == 0 {
		// No active wiki matched the filters. Reported as an empty result with a reason
		// rather than as an error: "nothing compiled yet" is a normal state for a new
		// knowledge base, and an error would push callers into treating it as a fault.
		response.Note = "no source has an active wiki build matching these filters; compile one first"
		return response, nil
	}

	// Over-fetch, because hits are filtered by build afterwards and several chunks of one
	// page can occupy the top of the list. Asking for exactly TopK would let a single
	// long page crowd out every other answer.
	hits, err := s.retrieval.SearchHybrid(ctx, owner, retrieval.SearchRequest{
		Namespace: retrieval.NamespaceKnowledgeWiki,
		Query:     req.Query,
		TopK:      req.TopK * wikiSearchOverFetch,
	})
	if err != nil {
		return nil, err
	}

	byPage := make(map[string]*WikiSearchHit)
	order := make([]string, 0, len(hits))
	for _, hit := range hits {
		if hit.Document == nil {
			continue
		}
		metadata := documentMetadata(hit.Document.Metadata)
		buildID := metadataString(metadata, "build_id")
		sourceID, active := activeBuilds[buildID]
		if !active {
			continue
		}
		pageID := metadataString(metadata, "page_id")
		if pageID == "" {
			continue
		}
		if existing, seen := byPage[pageID]; seen {
			// Keep the best-scoring chunk as the page's score and count the rest: a page
			// matching in several places is more relevant, but reporting it several times
			// would make one page look like several answers.
			existing.MatchedChunks++
			if hit.Score > existing.Score {
				existing.Score = hit.Score
				existing.Snippet = truncateChunkRunes(hit.Document.Content, snippetRunes)
				existing.HeadingPath = metadataString(metadata, "heading_path")
			}
			continue
		}
		item := &WikiSearchHit{
			PageID: pageID, SourceID: sourceID, BuildID: buildID,
			Path: metadataString(metadata, "path"), Kind: metadataString(metadata, "kind"),
			Title: metadataString(metadata, "page_title"), KnowledgeBase: metadataString(metadata, "knowledge_base"),
			HeadingPath: metadataString(metadata, "heading_path"),
			Snippet:     truncateChunkRunes(hit.Document.Content, snippetRunes),
			Score:       hit.Score, MatchedChunks: 1,
			DerivedFromBuildID: metadataString(metadata, "derived_from_build_id"),
		}
		byPage[pageID] = item
		order = append(order, pageID)
	}

	ranked := make([]*WikiSearchHit, 0, len(order))
	for _, pageID := range order {
		ranked = append(ranked, byPage[pageID])
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if len(ranked) > req.TopK {
		ranked = ranked[:req.TopK]
	}

	for _, item := range ranked {
		// Citations and links come from the database, not from the index: they are the
		// path down to evidence and must reflect the page as stored rather than as it was
		// projected for search.
		page, pageErr := s.repo.GetPage(ctx, owner.Account(), item.BuildID, item.Path)
		if pageErr != nil {
			continue
		}
		item.Citations = page.Citations
		item.Links = page.Links
		if req.IncludeContent {
			item.Content = page.Content
		}
		response.Items = append(response.Items, *item)
	}
	return response, nil
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key].(string); ok {
		return value
	}
	return ""
}

// WikiIndexStatuses reports which build each source's searchable wiki reflects.
//
// Exposed as its own read because the gap between active and indexed is the one thing a
// caller cannot infer from a search result: filtering by active build makes a stale index
// look like a wiki with nothing to say.
func (s *Service) WikiIndexStatuses(ctx context.Context, accountID, sourceID string) ([]WikiIndexStatus, error) {
	return s.repo.WikiIndexStatuses(ctx, accountID, strings.TrimSpace(sourceID))
}
