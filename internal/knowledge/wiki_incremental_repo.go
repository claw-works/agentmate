package knowledge

import (
	"context"
	"sort"
)

// ─── K3.4: incremental compilation, storage side ───
//
// Everything here is deterministic. Which documents changed and which pages that
// touches is computed from the database, never asked of the model: if the model
// decided the impact set, an under-estimate would leave stale pages citing
// documents that no longer say what the page claims, and nothing would detect it.

// DiffRevisionDocuments compares two source revisions by path and content hash.
//
// Hashes are the right tool here, unlike for build identity: raw documents are
// authored by people, so identical bytes really do mean identical content. The
// comparison covers indexable documents only, because those are the ones that ever
// reached the compiler.
func (r *Repo) DiffRevisionDocuments(ctx context.Context, accountID, fromRevisionID, toRevisionID string) (*RevisionDiff, error) {
	load := func(revisionID string) (map[string]string, error) {
		rows, err := r.pool.Query(ctx,
			`SELECT path, sha256 FROM knowledge_documents
			  WHERE account_id = $1 AND revision_id = $2 AND indexable = true AND content_snapshot <> ''`,
			accountID, revisionID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := make(map[string]string)
		for rows.Next() {
			var path, hash string
			if err := rows.Scan(&path, &hash); err != nil {
				return nil, err
			}
			result[path] = hash
		}
		return result, rows.Err()
	}

	from, err := load(fromRevisionID)
	if err != nil {
		return nil, err
	}
	to, err := load(toRevisionID)
	if err != nil {
		return nil, err
	}

	diff := &RevisionDiff{
		FromRevisionID: fromRevisionID, ToRevisionID: toRevisionID,
		Added: make([]string, 0), Removed: make([]string, 0), Changed: make([]string, 0),
	}
	for path, hash := range to {
		previous, existed := from[path]
		switch {
		case !existed:
			diff.Added = append(diff.Added, path)
		case previous != hash:
			diff.Changed = append(diff.Changed, path)
		default:
			diff.Unchanged++
		}
	}
	for path := range from {
		if _, ok := to[path]; !ok {
			diff.Removed = append(diff.Removed, path)
		}
	}
	sort.Strings(diff.Added)
	sort.Strings(diff.Removed)
	sort.Strings(diff.Changed)
	return diff, nil
}

// ImpactedPagePaths resolves which pages of a build must be recompiled given a set
// of raw document paths that changed or disappeared.
//
// Two hops, and both are necessary for different reasons:
//
//   - A page citing a touched document is directly stale: its claims were derived
//     from text that no longer says the same thing.
//   - A page linking *to* such a page is pulled in as well. Not because its content
//     is stale, but because the recompile may drop or rename its target, and a
//     reused page pointing at a page that no longer exists is a dangling link that
//     check refuses. Including the linker means the same compile that removes a
//     page also gets to fix whoever pointed at it.
//
// Deliberately not transitive beyond one hop. Full closure over the link graph
// converges on the whole wiki for any well-connected knowledge base, which is a
// full rebuild wearing an incremental label.
func (r *Repo) ImpactedPagePaths(ctx context.Context, accountID, buildID string, touchedDocumentPaths []string) ([]string, error) {
	if len(touchedDocumentPaths) == 0 {
		return []string{}, nil
	}
	rows, err := r.pool.Query(ctx,
		`WITH cited AS (
		   SELECT DISTINCT page.id, page.path
		     FROM knowledge_page_citations AS citation
		     JOIN knowledge_pages AS page
		       ON page.id = citation.page_id AND page.account_id = citation.account_id
		    WHERE citation.account_id = $1
		      AND citation.build_id = $2::uuid
		      AND citation.document_path = ANY($3::text[])
		 )
		 SELECT path FROM cited
		 UNION
		 SELECT linker.path
		   FROM knowledge_page_links AS link
		   JOIN knowledge_pages AS linker
		     ON linker.id = link.source_page_id AND linker.account_id = link.account_id
		   JOIN cited ON cited.id = link.target_page_id
		  WHERE link.account_id = $1 AND link.build_id = $2::uuid`,
		accountID, buildID, touchedDocumentPaths,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Strings(paths)
	return paths, nil
}

// LoadBuildPages loads every page of a build with its citations and outgoing links,
// which is what reuse needs to copy them forward.
//
// Three queries rather than two per page: a build can hold hundreds of pages, and a
// per-page loop would turn reuse — the operation that exists to be cheap — into the
// slowest part of an incremental compile.
//
// Only outgoing links are loaded. Incoming links are the same rows seen from the
// other end, so copying both directions would duplicate every link.
func (r *Repo) LoadBuildPages(ctx context.Context, accountID, buildID string) ([]WikiPage, error) {
	pages, err := r.ListPages(ctx, accountID, buildID, true)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return pages, nil
	}
	byID := make(map[string]int, len(pages))
	for index := range pages {
		byID[pages[index].ID] = index
		pages[index].Citations = make([]PageCitation, 0)
		pages[index].Links = make([]PageLink, 0)
	}

	citationRows, err := r.pool.Query(ctx,
		`SELECT page_id, document_id::text, document_path, heading_path, chunk_key, claim, excerpt
		   FROM knowledge_page_citations
		  WHERE account_id = $1 AND build_id = $2::uuid
		  ORDER BY page_id, document_path, heading_path, id`,
		accountID, buildID,
	)
	if err != nil {
		return nil, err
	}
	defer citationRows.Close()
	for citationRows.Next() {
		var pageID string
		var citation PageCitation
		if err := citationRows.Scan(&pageID, &citation.DocumentID, &citation.DocumentPath,
			&citation.HeadingPath, &citation.ChunkKey, &citation.Claim, &citation.Excerpt); err != nil {
			return nil, err
		}
		if index, ok := byID[pageID]; ok {
			pages[index].Citations = append(pages[index].Citations, citation)
		}
	}
	if err := citationRows.Err(); err != nil {
		return nil, err
	}

	linkRows, err := r.pool.Query(ctx,
		`SELECT source_page_id, target_path, link_type, note
		   FROM knowledge_page_links
		  WHERE account_id = $1 AND build_id = $2::uuid
		  ORDER BY source_page_id, link_type, target_path`,
		accountID, buildID,
	)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()
	for linkRows.Next() {
		var pageID string
		var link PageLink
		if err := linkRows.Scan(&pageID, &link.TargetPath, &link.LinkType, &link.Note); err != nil {
			return nil, err
		}
		if index, ok := byID[pageID]; ok {
			pages[index].Links = append(pages[index].Links, link)
		}
	}
	return pages, linkRows.Err()
}

// ReuseInput carries what a reused page needs to be written into a new build.
type ReuseInput struct {
	Pages         []WikiPage
	ParentBuildID string
	// CurrentDocumentIDs maps document path to its row ID in the revision being
	// compiled. Documents are stored per revision, so a copied citation's document ID
	// belongs to the parent's revision and has to be re-resolved: otherwise the build
	// declares one source revision while its citations point into another, and the
	// structural check cannot see it because only the path is verified.
	CurrentDocumentIDs map[string]string
}

// prepareReusedPages stamps copied pages with their origin and clears identifiers
// belonging to the parent build.
//
// derived_from_build_id is the audit trail for reuse: without it, an incremental
// build looks like it compiled every page it contains, and there would be no way to
// tell which text a given model run actually produced.
func prepareReusedPages(in ReuseInput) []WikiPage {
	prepared := make([]WikiPage, 0, len(in.Pages))
	for _, page := range in.Pages {
		page.ID = ""
		page.BuildID = ""
		parent := in.ParentBuildID
		page.DerivedFromBuildID = &parent
		for index := range page.Citations {
			page.Citations[index].ID = ""
			page.Citations[index].BuildID = ""
			page.Citations[index].PageID = ""
			// Re-resolve against the revision being compiled. A path that no longer
			// exists leaves this nil, and check's citation_resolvable rule reports it —
			// which is correct: a reused page citing a vanished document is exactly the
			// case reuse must not hide.
			if id, ok := in.CurrentDocumentIDs[page.Citations[index].DocumentPath]; ok {
				resolved := id
				page.Citations[index].DocumentID = &resolved
			} else {
				page.Citations[index].DocumentID = nil
			}
		}
		for index := range page.Links {
			page.Links[index].ID = ""
			page.Links[index].BuildID = ""
			page.Links[index].SourcePageID = ""
			page.Links[index].TargetPageID = nil
			// Direction is a read-time artifact of GetPage, not a stored column.
			page.Links[index].Direction = ""
		}
		prepared = append(prepared, page)
	}
	return prepared
}

// ─── K3.6: wiki retrieval bookkeeping ───

// ListSourcesWithActiveBuild returns sources whose wiki has been activated.
//
// The active pointer is the authority on what is searchable, which is why search resolves
// it here instead of trusting the index: an index left behind by a rollback would
// otherwise serve the wiki that was rolled back while the read API served the restored
// one.
func (r *Repo) ListSourcesWithActiveBuild(ctx context.Context, accountID, sourceID string) ([]KnowledgeSource, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+sourceColumns+` FROM knowledge_sources
		  WHERE account_id = $1
		    AND active_build_id IS NOT NULL
		    AND ($2 = '' OR id::text = $2)
		  ORDER BY name`,
		accountID, sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sources := make([]KnowledgeSource, 0)
	for rows.Next() {
		var source KnowledgeSource
		if err := rows.Scan(scanSource(&source)...); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return sources, rows.Err()
}

// MarkWikiIndexed records which build the retrieval index now reflects.
func (r *Repo) MarkWikiIndexed(ctx context.Context, accountID, sourceID, buildID string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE knowledge_sources
		    SET indexed_build_id = $3::uuid, wiki_indexed_at = NOW(), updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid`,
		accountID, sourceID, buildID,
	)
	return err
}

// WikiIndexStatuses reports, per source, whether the searchable wiki is the active one.
func (r *Repo) WikiIndexStatuses(ctx context.Context, accountID, sourceID string) ([]WikiIndexStatus, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, name, active_build_id::text, indexed_build_id::text,
		        COALESCE(to_char(wiki_indexed_at, 'YYYY-MM-DD"T"HH24:MI:SS"Z"'), '')
		   FROM knowledge_sources
		  WHERE account_id = $1 AND ($2 = '' OR id::text = $2)
		  ORDER BY name`,
		accountID, sourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	statuses := make([]WikiIndexStatus, 0)
	for rows.Next() {
		var status WikiIndexStatus
		if err := rows.Scan(&status.ID, &status.Name, &status.ActiveBuildID,
			&status.IndexedBuildID, &status.WikiIndexedAt); err != nil {
			return nil, err
		}
		// Stale means there is an active wiki the index does not cover. A source with no
		// active build is not stale — there is simply nothing to search yet.
		status.Stale = status.ActiveBuildID != nil &&
			(status.IndexedBuildID == nil || *status.IndexedBuildID != *status.ActiveBuildID)
		statuses = append(statuses, status)
	}
	return statuses, rows.Err()
}
