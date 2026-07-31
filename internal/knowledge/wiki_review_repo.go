package knowledge

import (
	"context"

	"github.com/claw-works/agentmate/internal/ownership"
)

// RecordReviewResult stores a verdict and its findings in one transaction.
//
// Previous findings for the build are deleted first. A build is immutable, so two sets of
// verdicts on it cannot both be current — accumulating them across re-reviews would leave
// a reader unable to tell which reviewer said what, and would make the finding count grow
// every time a flaky provider forced a retry.
//
// Token and cost figures are added rather than replaced, because the money spent on a
// failed first attempt was still spent. Hiding it makes the bill unexplainable, which is
// the same rule the compile path follows for failed attempts.
func (r *Repo) RecordReviewResult(ctx context.Context, owner ownership.Owner, in recordReviewInput) (*ReviewResponse, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Take the build row first. Without this lock two reviews of the same build can both
	// delete zero rows, both insert their own findings, and only serialise on the final
	// update — leaving the status from one reviewer beside the union of both reviewers'
	// findings. The automatic review runs right after commit, when a manual review can
	// already see the build, so the race is reachable rather than theoretical.
	var locked string
	if err := tx.QueryRow(ctx,
		`SELECT id::text FROM knowledge_build_revisions
		  WHERE account_id = $1 AND id::text = $2 FOR UPDATE`,
		owner.Account(), in.BuildID,
	).Scan(&locked); err != nil {
		return nil, err
	}

	// A review that could not run must not destroy the verdict of one that did. Deleting
	// on skipped would let a re-review against an unconfigured or same-model reviewer
	// silently erase real findings and replace them with "no review happened".
	if in.Status != ReviewStatusSkipped {
		if _, err := tx.Exec(ctx,
			`DELETE FROM knowledge_review_findings WHERE account_id = $1 AND build_id = $2`,
			owner.Account(), in.BuildID,
		); err != nil {
			return nil, err
		}
	}

	for _, finding := range in.Findings {
		if _, err := tx.Exec(ctx,
			`INSERT INTO knowledge_review_findings
			   (account_id, build_id, page_path, kind, claim, detail)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			owner.Account(), in.BuildID, finding.PagePath, finding.Kind,
			finding.Claim, finding.Detail,
		); err != nil {
			return nil, err
		}
	}

	var build BuildRevision
	if err := tx.QueryRow(ctx,
		`UPDATE knowledge_build_revisions
		    SET review_status = $3,
		        review_note = $4,
		        review_pages_examined = $5,
		        review_pages_total = $6,
		        review_tokens = review_tokens + $7,
		        review_cost_micros = review_cost_micros + $8,
		        -- Provenance is rewritten with the findings it belongs to. Leaving the
		        -- values recorded at enqueue would attribute a re-review's verdicts to
		        -- whichever reviewer happened to be configured back then, which is exactly
		        -- the confusion provenance exists to prevent.
		        reviewer_model = COALESCE(NULLIF($9, ''), reviewer_model),
		        reviewer_prompt_version = COALESCE(NULLIF($10, ''), reviewer_prompt_version),
		        reviewer_independence = COALESCE(NULLIF($11, ''), reviewer_independence),
		        updated_at = NOW()
		  WHERE account_id = $1 AND id::text = $2
		 RETURNING `+buildColumns,
		owner.Account(), in.BuildID, in.Status, in.Note,
		in.PagesExamined, in.PagesTotal, in.Tokens, in.CostMicros,
		in.ReviewerModel, in.ReviewerPromptVersion, in.ReviewerIndependence,
	).Scan(scanBuild(&build)...); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	findings := in.Findings
	if findings == nil {
		findings = make([]ReviewFinding, 0)
	}
	return &ReviewResponse{Build: build, Findings: findings}, nil
}

func (r *Repo) ListReviewFindings(ctx context.Context, accountID, buildID string) ([]ReviewFinding, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, build_id, page_path, kind, claim, detail, created_at
		   FROM knowledge_review_findings
		  WHERE account_id = $1 AND build_id::text = $2
		  ORDER BY page_path, kind, id`,
		accountID, buildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ReviewFinding, 0)
	for rows.Next() {
		var finding ReviewFinding
		if err := rows.Scan(&finding.ID, &finding.BuildID, &finding.PagePath,
			&finding.Kind, &finding.Claim, &finding.Detail, &finding.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, finding)
	}
	return items, rows.Err()
}

// ListPagesWithCitations returns every page of a build with its bodies and citations
// attached, in one pass over each table.
//
// ListPages deliberately omits citations, which is right for listing endpoints and wrong
// for review: a page arriving with an empty citation list looks exactly like a page that
// cites nothing, so the reviewer is handed no sources and reports every claim as
// unsupported. That failure blames the compiler for a loading mistake on our side, which is
// the most damaging kind of false positive a review can produce.
//
// Links are not loaded: faithfulness is a relation between a page and its sources, and the
// graph plays no part in it.
func (r *Repo) ListPagesWithCitations(ctx context.Context, accountID, buildID string) ([]WikiPage, error) {
	pages, err := r.ListPages(ctx, accountID, buildID, true)
	if err != nil {
		return nil, err
	}
	if len(pages) == 0 {
		return pages, nil
	}

	rows, err := r.pool.Query(ctx,
		`SELECT id, build_id, page_id, document_id, document_path, heading_path,
		        chunk_key, claim, excerpt, created_at
		   FROM knowledge_page_citations
		  WHERE account_id = $1 AND build_id = $2::uuid
		  ORDER BY page_id, id`,
		accountID, buildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byPage := make(map[string][]PageCitation, len(pages))
	for rows.Next() {
		var citation PageCitation
		if err := rows.Scan(&citation.ID, &citation.BuildID, &citation.PageID,
			&citation.DocumentID, &citation.DocumentPath, &citation.HeadingPath,
			&citation.ChunkKey, &citation.Claim, &citation.Excerpt, &citation.CreatedAt); err != nil {
			return nil, err
		}
		byPage[citation.PageID] = append(byPage[citation.PageID], citation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for index := range pages {
		if citations, ok := byPage[pages[index].ID]; ok {
			pages[index].Citations = citations
			continue
		}
		pages[index].Citations = make([]PageCitation, 0)
	}
	return pages, nil
}

// RecordReviewUsage adds one page's token spend immediately.
//
// Cost is recorded per page rather than once at the end because the end may never arrive: a
// process that dies after fifteen reviewer calls has spent real money, and a bill that
// silently loses it cannot be explained. This mirrors what the compile path does for failed
// attempts.
func (r *Repo) RecordReviewUsage(ctx context.Context, accountID, buildID string, tokens int, costMicros int64) error {
	if tokens == 0 && costMicros == 0 {
		return nil
	}
	_, err := r.pool.Exec(ctx,
		`UPDATE knowledge_build_revisions
		    SET review_tokens = review_tokens + $3,
		        review_cost_micros = review_cost_micros + $4,
		        updated_at = NOW()
		  WHERE account_id = $1 AND id::text = $2`,
		accountID, buildID, tokens, costMicros,
	)
	return err
}
