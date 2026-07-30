package knowledge

import (
	"context"
	"fmt"

	"github.com/claw-works/agentmate/internal/ownership"
)

// lintCascadeMaxDepth bounds the recursive walk in lintStaleCascade.
//
// The bound is not a performance guard, it is a meaning guard. A well-connected wiki's
// transitive closure converges on the entire wiki, so an unbounded cascade would report
// every page as affected and say nothing. This is the same reason the incremental
// compiler's impact closure stops at two hops.
const lintCascadeMaxDepth = 4

// contentPageKinds excludes the two generated pages from graph rules.
//
// check requires the index to link every page, so counting index links as inbound
// connectivity would make orphan_page unable to fire by construction. The log is a
// transcript of compilation, not a reader's path into the wiki.
const contentPageKinds = `('index', 'log')`

// entryPointKinds are pages allowed to have no inbound links.
//
// An overview is the designated way in; readers arrive from the index by design, so
// reporting one as orphaned would be a finding on every wiki on every run. A rule that
// always fires teaches people to ignore the whole report, which costs more than the rule
// is worth. Other kinds are derived views, and one nothing refers to is dead weight.
const entryPointKinds = `('index', 'log', 'overview')`

// dependencyLinkKinds are the edges along which staleness actually propagates.
//
// Only references and elaborates mean "this page builds on that one". A page that
// supersedes a stale page is its replacement, not its dependent; contradicts records a
// disagreement, not a foundation; mentions_entity is a mention. Walking every edge type
// would report the newer page as resting on the one it retired — a systematic false
// positive, and findings nobody can trust are worse than none.
const dependencyLinkKinds = `('references', 'elaborates')`

// RunWikiLint evaluates every rule against one build, comparing it against the source's
// current revision. It writes nothing.
//
// Findings come back grouped by rule in a fixed evaluation order, and within stale_cascade
// nearest-first. Reading a stored run sorts by (rule, page_path, related_path) instead, so
// two runs must be compared on those fields rather than positionally.
//
// Errors name the rule that failed: seven queries share this function, and an error that
// does not say which one produced it costs an afternoon.
func (r *Repo) RunWikiLint(ctx context.Context, accountID, buildID, revisionID string) ([]LintFinding, int, error) {
	var pagesExamined int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_pages WHERE account_id = $1 AND build_id = $2`,
		accountID, buildID,
	).Scan(&pagesExamined); err != nil {
		return nil, 0, err
	}

	findings := make([]LintFinding, 0)

	// ── orphan_page ──
	// A content page no other content page links to. Reachability from a generated table
	// of contents is not connectivity: nobody arrives at such a page while reading.
	rows, err := r.pool.Query(ctx,
		`SELECT page.path
		   FROM knowledge_pages AS page
		  WHERE page.account_id = $1 AND page.build_id = $2
		    AND page.kind NOT IN `+entryPointKinds+`
		    AND NOT EXISTS (
		          SELECT 1
		            FROM knowledge_page_links AS link
		            JOIN knowledge_pages AS src
		              ON src.id = link.source_page_id AND src.account_id = link.account_id
		           WHERE link.account_id = $1 AND link.build_id = $2
		             AND link.target_page_id = page.id
		             AND src.id <> page.id
		             AND src.kind NOT IN `+contentPageKinds+`)
		  ORDER BY page.path`,
		accountID, buildID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "orphan_page", err)
	}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("lint rule %s: %w", "orphan_page", err)
		}
		findings = append(findings, LintFinding{
			Rule: lintOrphanPage, Severity: lintSeverityWarning, PagePath: path,
			Detail: "no other content page links here; only reachable from the index",
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "orphan_page", err)
	}

	// ── stale_citation ──
	// The citation no longer describes the current sources: either the document is gone,
	// or its bytes moved under the claim. check cannot see this — it validates citations
	// against the revision the build compiled, which always passes.
	stalePageIDs := make([]string, 0)
	rows, err = r.pool.Query(ctx,
		`SELECT DISTINCT page.id, page.path, citation.document_path,
		        CASE WHEN current_doc.id IS NULL THEN 'removed' ELSE 'rewritten' END AS reason
		   FROM knowledge_page_citations AS citation
		   JOIN knowledge_pages AS page
		     ON page.id = citation.page_id AND page.account_id = citation.account_id
		   LEFT JOIN knowledge_documents AS current_doc
		     ON current_doc.account_id = citation.account_id
		    AND current_doc.revision_id = $3
		    AND current_doc.path = citation.document_path
		   LEFT JOIN knowledge_documents AS cited_doc
		     ON cited_doc.id = citation.document_id AND cited_doc.account_id = citation.account_id
		  WHERE citation.account_id = $1 AND citation.build_id = $2
		    AND (current_doc.id IS NULL
		         OR (cited_doc.id IS NOT NULL AND cited_doc.sha256 <> current_doc.sha256))
		  ORDER BY page.path, citation.document_path`,
		accountID, buildID, revisionID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "stale_citation", err)
	}
	seenStale := make(map[string]struct{})
	for rows.Next() {
		var pageID, path, docPath, reason string
		if err := rows.Scan(&pageID, &path, &docPath, &reason); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("lint rule %s: %w", "stale_citation", err)
		}
		if _, ok := seenStale[pageID]; !ok {
			seenStale[pageID] = struct{}{}
			stalePageIDs = append(stalePageIDs, pageID)
		}
		detail := "cited document was removed from the current revision"
		if reason == "rewritten" {
			detail = "cited document changed in the current revision; the claim may no longer hold"
		}
		findings = append(findings, LintFinding{
			Rule: lintStaleCitation, Severity: lintSeverityWarning,
			PagePath: path, RelatedPath: docPath, Detail: detail,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "stale_citation", err)
	}

	// ── stale_cascade ──
	// Pages that rest on a stale page. They may cite nothing that moved, yet their
	// conclusions were drawn from text that did. This is the genuine graph query, and the
	// reason architecture §14 could reject a graph database: a recursive CTE covers it.
	if len(stalePageIDs) > 0 {
		rows, err = r.pool.Query(ctx,
			`WITH RECURSIVE walk AS (
			     SELECT page.id AS page_id, 0 AS depth
			       FROM knowledge_pages AS page
			      WHERE page.account_id = $1 AND page.build_id = $2
			        AND page.id::text = ANY($3::text[])
			     UNION
			     SELECT link.source_page_id, walk.depth + 1
			       FROM walk
			       JOIN knowledge_page_links AS link
			         ON link.target_page_id = walk.page_id
			        AND link.account_id = $1 AND link.build_id = $2
			       JOIN knowledge_pages AS src
			         ON src.id = link.source_page_id AND src.account_id = $1
			      WHERE walk.depth < $4
			        AND link.link_type IN `+dependencyLinkKinds+`
			        AND src.kind NOT IN `+contentPageKinds+`
			 )
			 SELECT page.path, min(walk.depth) AS hops
			   FROM walk
			   JOIN knowledge_pages AS page
			     ON page.id = walk.page_id AND page.account_id = $1
			  WHERE page.id::text <> ALL($3::text[])
			    AND page.kind NOT IN `+contentPageKinds+`
			  GROUP BY page.path
			  ORDER BY hops, page.path`,
			accountID, buildID, stalePageIDs, lintCascadeMaxDepth,
		)
		if err != nil {
			return nil, 0, err
		}
		for rows.Next() {
			var path string
			var hops int
			if err := rows.Scan(&path, &hops); err != nil {
				rows.Close()
				return nil, 0, err
			}
			findings = append(findings, LintFinding{
				Rule: lintStaleCascade, Severity: lintSeverityInfo, PagePath: path,
				Detail: fmt.Sprintf("%d hop(s) from a page with stale citations", hops),
			})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, 0, fmt.Errorf("lint rule %s: %w", "stale_cascade", err)
		}
	}

	// ── recorded_contradiction ──
	// The compiler recording a disagreement is the start of the work, not the end. check
	// only validates that the edge type is allowed and resolves.
	rows, err = r.pool.Query(ctx,
		`SELECT src.path, COALESCE(tgt.path, link.target_path), link.note
		   FROM knowledge_page_links AS link
		   JOIN knowledge_pages AS src
		     ON src.id = link.source_page_id AND src.account_id = link.account_id
		   LEFT JOIN knowledge_pages AS tgt
		     ON tgt.id = link.target_page_id AND tgt.account_id = link.account_id
		  WHERE link.account_id = $1 AND link.build_id = $2
		    AND link.link_type = 'contradicts'
		  ORDER BY src.path, 2`,
		accountID, buildID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "recorded_contradiction", err)
	}
	for rows.Next() {
		var path, target, note string
		if err := rows.Scan(&path, &target, &note); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("lint rule %s: %w", "recorded_contradiction", err)
		}
		detail := "pages disagree and nobody has resolved it"
		if note != "" {
			detail = note
		}
		findings = append(findings, LintFinding{
			Rule: lintRecordedContradiction, Severity: lintSeverityWarning,
			PagePath: path, RelatedPath: target, Detail: detail,
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "recorded_contradiction", err)
	}

	// ── unlabelled_supersede ──
	// A reader who lands on the outdated page has no way to reach the one that replaced
	// it: no frontmatter marker, no link forward. check cannot object, because both pages
	// are valid and the edge resolves.
	rows, err = r.pool.Query(ctx,
		`SELECT DISTINCT older.path, newer.path
		   FROM knowledge_page_links AS link
		   JOIN knowledge_pages AS newer
		     ON newer.id = link.source_page_id AND newer.account_id = link.account_id
		   JOIN knowledge_pages AS older
		     ON older.id = link.target_page_id AND older.account_id = link.account_id
		  WHERE link.account_id = $1 AND link.build_id = $2
		    AND link.link_type = 'supersedes'
		    -- Key presence is not a pointer: a null or empty superseded_by would silence
		    -- the finding while telling a reader nothing.
		    AND coalesce(older.frontmatter ->> 'superseded_by', '') = ''
		    AND NOT EXISTS (
		          SELECT 1
		            FROM knowledge_page_links AS back
		           WHERE back.account_id = $1 AND back.build_id = $2
		             AND back.source_page_id = older.id
		             AND back.target_page_id = newer.id
		             -- A contradicts or mentions_entity edge back is not "here is your
		             -- replacement". Only a reference forward counts.
		             AND back.link_type IN ('references', 'supersedes'))
		  ORDER BY older.path, newer.path`,
		accountID, buildID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "unlabelled_supersede", err)
	}
	for rows.Next() {
		var older, newer string
		if err := rows.Scan(&older, &newer); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("lint rule %s: %w", "unlabelled_supersede", err)
		}
		findings = append(findings, LintFinding{
			Rule: lintUnlabelledSupersede, Severity: lintSeverityWarning,
			PagePath: older, RelatedPath: newer,
			Detail: "superseded page carries no forward pointer to its replacement",
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "unlabelled_supersede", err)
	}

	// ── entity_link_kind ──
	// The edge claims its target is an entity; the target says otherwise. Closure holds,
	// so check is satisfied — what is wrong is the claim, not the graph.
	rows, err = r.pool.Query(ctx,
		`SELECT DISTINCT src.path, tgt.path, tgt.kind
		   FROM knowledge_page_links AS link
		   JOIN knowledge_pages AS src
		     ON src.id = link.source_page_id AND src.account_id = link.account_id
		   JOIN knowledge_pages AS tgt
		     ON tgt.id = link.target_page_id AND tgt.account_id = link.account_id
		  WHERE link.account_id = $1 AND link.build_id = $2
		    AND link.link_type = 'mentions_entity'
		    AND tgt.kind <> $3
		  ORDER BY src.path, tgt.path`,
		accountID, buildID, PageKindEntity,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "entity_link_kind", err)
	}
	for rows.Next() {
		var path, target, kind string
		if err := rows.Scan(&path, &target, &kind); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("lint rule %s: %w", "entity_link_kind", err)
		}
		findings = append(findings, LintFinding{
			Rule: lintEntityLinkKind, Severity: lintSeverityInfo,
			PagePath: path, RelatedPath: target,
			Detail: fmt.Sprintf("mentions_entity points at a %s page", kind),
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "entity_link_kind", err)
	}

	// ── uncovered_document ──
	// Material the wiki ignores. Beyond the design's rule list, because it answers the
	// question an owner actually asks, and check cannot: covering half the sources
	// violates no invariant.
	//
	// Coverage is matched on path alone, deliberately. A document whose bytes changed is
	// still covered — some page is about it — and its being out of date is exactly what
	// stale_citation reports. Counting it here as well would make "uncovered" mean both
	// "nobody wrote about this" and "what was written is old", and double-report one
	// problem under two names.
	rows, err = r.pool.Query(ctx,
		`SELECT doc.path
		   FROM knowledge_documents AS doc
		  WHERE doc.account_id = $1 AND doc.revision_id = $3
		    AND doc.indexable = true AND doc.content_snapshot <> ''
		    AND NOT EXISTS (
		          SELECT 1
		            FROM knowledge_page_citations AS citation
		           WHERE citation.account_id = $1 AND citation.build_id = $2
		             AND citation.document_path = doc.path)
		  ORDER BY doc.path`,
		accountID, buildID, revisionID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "uncovered_document", err)
	}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, 0, fmt.Errorf("lint rule %s: %w", "uncovered_document", err)
		}
		findings = append(findings, LintFinding{
			Rule: lintUncoveredDocument, Severity: lintSeverityInfo, RelatedPath: path,
			Detail: "no page cites this document",
		})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "uncovered_document", err)
	}

	return findings, pagesExamined, nil
}

// RecordLintRun stores a run and its findings in one transaction.
//
// Run and findings land together or not at all: a run row with a truncated finding set
// would read as a cleaner wiki than the one that was actually examined.
func (r *Repo) RecordLintRun(ctx context.Context, owner ownership.Owner, in recordLintRunInput) (LintRun, error) {
	var run LintRun
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return run, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var warnings, infos int
	for _, finding := range in.Findings {
		if finding.Severity == lintSeverityInfo {
			infos++
			continue
		}
		warnings++
	}

	var userID *string
	if owner.UserID != "" {
		userID = &owner.UserID
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO knowledge_lint_runs
		   (account_id, user_id, key_id, source_id, build_id, revision_id,
		    pages_examined, findings_total, findings_warning, findings_info,
		    started_at, finished_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		 RETURNING `+lintRunColumns,
		owner.Account(), userID, owner.KeyID, in.SourceID, in.BuildID, in.RevisionID,
		in.PagesExamined, len(in.Findings), warnings, infos, in.StartedAt,
	).Scan(scanLintRun(&run)...); err != nil {
		return run, err
	}

	for _, finding := range in.Findings {
		if _, err := tx.Exec(ctx,
			`INSERT INTO knowledge_lint_findings
			   (account_id, run_id, rule, severity, page_path, related_path, detail)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			owner.Account(), run.ID, finding.Rule, finding.Severity,
			finding.PagePath, finding.RelatedPath, finding.Detail,
		); err != nil {
			return run, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return run, err
	}
	return run, nil
}

func (r *Repo) ListLintRuns(ctx context.Context, accountID, sourceID string, limit, offset int) ([]LintRun, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_lint_runs
		  WHERE account_id = $1 AND ($2 = '' OR source_id::text = $2)`,
		accountID, sourceID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+lintRunColumns+`
		   FROM knowledge_lint_runs
		  WHERE account_id = $1 AND ($2 = '' OR source_id::text = $2)
		  ORDER BY created_at DESC, id
		  LIMIT $3 OFFSET $4`,
		accountID, sourceID, limit, offset,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("lint rule %s: %w", "uncovered_document", err)
	}
	defer rows.Close()
	items := make([]LintRun, 0)
	for rows.Next() {
		var run LintRun
		if err := rows.Scan(scanLintRun(&run)...); err != nil {
			return nil, 0, err
		}
		items = append(items, run)
	}
	return items, total, rows.Err()
}

func (r *Repo) GetLintRun(ctx context.Context, accountID, runID string) (LintRun, error) {
	var run LintRun
	if runID == "" {
		return run, fmt.Errorf("run_id required")
	}
	err := r.pool.QueryRow(ctx,
		`SELECT `+lintRunColumns+`
		   FROM knowledge_lint_runs WHERE account_id = $1 AND id::text = $2`,
		accountID, runID,
	).Scan(scanLintRun(&run)...)
	return run, err
}

func (r *Repo) ListLintFindings(ctx context.Context, accountID, runID string) ([]LintFinding, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, run_id, rule, severity, page_path, related_path, detail, created_at
		   FROM knowledge_lint_findings
		  WHERE account_id = $1 AND run_id::text = $2
		  ORDER BY rule, page_path, related_path`,
		accountID, runID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]LintFinding, 0)
	for rows.Next() {
		var finding LintFinding
		if err := rows.Scan(&finding.ID, &finding.RunID, &finding.Rule, &finding.Severity,
			&finding.PagePath, &finding.RelatedPath, &finding.Detail, &finding.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, finding)
	}
	return items, rows.Err()
}
