package knowledge

import (
	"context"
	"errors"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/claw-works/agentmate/internal/retrieval"
	"github.com/jackc/pgx/v5"
)

func (r *Repo) InsertValidationSignal(ctx context.Context, owner ownership.Owner, in insertSignalInput) (*ValidationSignal, error) {
	var signal ValidationSignal
	err := r.pool.QueryRow(ctx,
		`INSERT INTO knowledge_validation_signals
		   (account_id, user_id, key_id, source_id, build_id, page_path, query_id,
		    session_id, skill_version_id,
		    signal, direction, origin, cause, attribution_basis, detail)
		 VALUES ($1, $2, $3, $4, NULLIF($5, '')::uuid, $6, NULLIF($7, '')::uuid,
		         NULLIF($8, '')::uuid, NULLIF($9, '')::uuid,
		         $10, $11, $12, $13, $14, $15)
		 RETURNING `+validationSignalColumns,
		owner.Account(), nullableString(owner.UserID), owner.KeyID, in.SourceID,
		in.BuildID, in.PagePath, in.QueryID, in.SessionID, in.SkillVersionID,
		in.Signal, in.Direction, in.Origin,
		in.Cause, in.AttributionBasis, in.Detail,
	).Scan(scanValidationSignal(&signal)...)
	if err != nil {
		return nil, err
	}
	return &signal, nil
}

// SignalEvidence reports what a retrieval query returned, which is what attribution needs.
//
// A missing or unknown query is not an error: a caller may report a signal without one, and
// attribution answers "unattributed" in that case rather than pretending the query said
// nothing. Those two are different and must not collapse.
func (r *Repo) SignalEvidence(ctx context.Context, accountID, queryID string) (signalEvidence, error) {
	var evidence signalEvidence
	if queryID == "" {
		return evidence, nil
	}
	var namespace string
	var candidateCount, selectedCount int
	err := r.pool.QueryRow(ctx,
		`SELECT namespace, candidate_count, selected_count
		   FROM retrieval_queries
		  WHERE id::text = $1 AND (account_id = $2 OR account_id IS NULL)`,
		queryID, accountID,
	).Scan(&namespace, &candidateCount, &selectedCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence, nil
	}
	if err != nil {
		return evidence, err
	}
	evidence.QueryKnown = true
	if namespace == retrieval.NamespaceKnowledgeWiki {
		evidence.WikiHitCount = selectedCount
	} else {
		evidence.RawCandidateCount = candidateCount
	}

	// The other layer's view of the same question, matched on the query text. Without it a
	// wiki miss cannot be told apart from a source gap, which are the two causes with the
	// most different fixes: index something versus go and write it.
	otherNamespace := retrieval.NamespaceKnowledgeWiki
	if namespace == retrieval.NamespaceKnowledgeWiki {
		otherNamespace = retrieval.NamespaceKnowledge
	}
	var otherCandidates, otherSelected int
	err = r.pool.QueryRow(ctx,
		`SELECT COALESCE(max(candidate_count), 0), COALESCE(max(selected_count), 0)
		   FROM retrieval_queries
		  WHERE namespace = $2
		    AND (account_id = $3 OR account_id IS NULL)
		    AND query_hash = (SELECT query_hash FROM retrieval_queries WHERE id::text = $1)`,
		queryID, otherNamespace, accountID,
	).Scan(&otherCandidates, &otherSelected)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return evidence, err
	}
	if otherNamespace == retrieval.NamespaceKnowledgeWiki {
		evidence.WikiHitCount += otherSelected
	} else {
		evidence.RawCandidateCount += otherCandidates
	}
	return evidence, nil
}

// SweepNeverRetrieved records the derived signal for sources with an active wiki that no
// retrieval query has ever touched.
//
// "Never touched" is judged by the absence of any retrieval query for the account since the
// source became active, which is coarse: queries are not attributed to a source. Being coarse
// and honest beats being precise and wrong — the signal says "this account is not querying at
// all", and the basis string says exactly that so nobody reads more into it.
func (r *Repo) SweepNeverRetrieved(ctx context.Context, owner ownership.Owner, idleDays int) (int, int, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id::text, active_build_id::text
		   FROM knowledge_sources
		  WHERE account_id = $1 AND active_build_id IS NOT NULL
		    AND updated_at < NOW() - make_interval(days => $2)
		    AND NOT EXISTS (
		          SELECT 1 FROM retrieval_queries
		           WHERE retrieval_queries.account_id = knowledge_sources.account_id
		             AND retrieval_queries.created_at > knowledge_sources.updated_at)`,
		owner.Account(), idleDays,
	)
	if err != nil {
		return 0, 0, err
	}
	type candidate struct{ sourceID, buildID string }
	candidates := make([]candidate, 0)
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.sourceID, &item.buildID); err != nil {
			rows.Close()
			return 0, 0, err
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	recorded, skipped := 0, 0
	for _, item := range candidates {
		// ON CONFLICT DO NOTHING against the per-day unique index. A sweep on a timer must
		// not turn one unchanged fact into a rising trend.
		tag, err := r.pool.Exec(ctx,
			`INSERT INTO knowledge_validation_signals
			   (account_id, user_id, key_id, source_id, build_id, signal, direction, origin,
			    cause, attribution_basis, detail)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 ON CONFLICT DO NOTHING`,
			owner.Account(), nullableString(owner.UserID), owner.KeyID, item.sourceID, item.buildID,
			signalNeverRetrieved, signalDirectionNegative, signalOriginDerived,
			causeUnattributed,
			"a source that was never queried carries no evidence about which layer failed",
			"no retrieval query touched this account since the source was last updated",
		)
		if err != nil {
			return recorded, skipped, err
		}
		if tag.RowsAffected() == 0 {
			skipped++
			continue
		}
		recorded++
	}
	return recorded, skipped, nil
}

func (r *Repo) ListValidationSignals(ctx context.Context, accountID string, filter SignalFilter, limit, offset int) ([]ValidationSignal, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	where := `account_id = $1
		  AND ($2 = '' OR source_id::text = $2)
		  AND ($3 = '' OR page_path = $3)
		  AND ($4 = '' OR direction = $4)
		  AND ($5 = '' OR cause = $5)`
	args := []any{accountID, filter.SourceID, filter.PagePath, filter.Direction, filter.Cause}

	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_validation_signals WHERE `+where, args...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+validationSignalColumns+`
		   FROM knowledge_validation_signals
		  WHERE `+where+`
		  ORDER BY created_at DESC, id
		  LIMIT $6 OFFSET $7`,
		append(args, limit, offset)...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]ValidationSignal, 0)
	for rows.Next() {
		var signal ValidationSignal
		if err := rows.Scan(scanValidationSignal(&signal)...); err != nil {
			return nil, 0, err
		}
		items = append(items, signal)
	}
	return items, total, rows.Err()
}

func (r *Repo) SummariseValidationSignals(ctx context.Context, accountID, sourceID string) (*SignalSummaryResponse, error) {
	summary := &SignalSummaryResponse{
		SourceID: sourceID,
		ByPage:   make([]SignalCount, 0),
		ByCause:  make([]SignalCount, 0),
		BySignal: make([]SignalCount, 0),
	}
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE direction = 'positive'),
		        count(*) FILTER (WHERE direction = 'negative'),
		        count(*) FILTER (WHERE origin = 'reported'),
		        count(*) FILTER (WHERE origin = 'derived')
		   FROM knowledge_validation_signals
		  WHERE account_id = $1 AND source_id::text = $2`,
		accountID, sourceID,
	).Scan(&summary.Total, &summary.Positive, &summary.Negative,
		&summary.Reported, &summary.Derived); err != nil {
		return nil, err
	}

	for _, group := range []struct {
		column string
		target *[]SignalCount
		filter string
	}{
		{"page_path", &summary.ByPage, ` AND page_path <> ''`},
		{"cause", &summary.ByCause, ``},
		{"signal", &summary.BySignal, ``},
	} {
		rows, err := r.pool.Query(ctx,
			`SELECT `+group.column+`,
			        count(*) FILTER (WHERE direction = 'positive'),
			        count(*) FILTER (WHERE direction = 'negative')
			   FROM knowledge_validation_signals
			  WHERE account_id = $1 AND source_id::text = $2`+group.filter+`
			  GROUP BY `+group.column+`
			  ORDER BY count(*) FILTER (WHERE direction = 'negative') DESC, `+group.column,
			accountID, sourceID,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var count SignalCount
			if err := rows.Scan(&count.Key, &count.Positive, &count.Negative); err != nil {
				rows.Close()
				return nil, err
			}
			*group.target = append(*group.target, count)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return summary, nil
}

// SkillSignalPatterns groups negative signals by the skill version that was running.
//
// This is where skill_approach becomes visible, and it can only be here. Per signal the
// cause is about the page: one page failing points at the page. What points at the skill is
// the same version accumulating negatives across several distinct pages and sources, because
// then the page has stopped being the common factor. Storing that conclusion on individual
// signals would be wrong twice over — it is not knowable when the first signal arrives, and
// a later signal would silently change the meaning of an earlier one.
func (r *Repo) SkillSignalPatterns(ctx context.Context, accountID string, limit int) ([]SkillSignalPattern, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT skill_version_id::text,
		        count(*) FILTER (WHERE direction = 'negative'),
		        count(*) FILTER (WHERE direction = 'positive'),
		        count(DISTINCT page_path) FILTER (WHERE direction = 'negative' AND page_path <> ''),
		        count(DISTINCT source_id) FILTER (WHERE direction = 'negative')
		   FROM knowledge_validation_signals
		  WHERE account_id = $1 AND skill_version_id IS NOT NULL
		  GROUP BY skill_version_id
		 HAVING count(*) FILTER (WHERE direction = 'negative') > 0
		  ORDER BY count(DISTINCT source_id) FILTER (WHERE direction = 'negative') DESC,
		           count(*) FILTER (WHERE direction = 'negative') DESC
		  LIMIT $2`,
		accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillSignalPattern, 0)
	for rows.Next() {
		var pattern SkillSignalPattern
		if err := rows.Scan(&pattern.SkillVersionID, &pattern.Negative, &pattern.Positive,
			&pattern.DistinctPages, &pattern.DistinctSource); err != nil {
			return nil, err
		}
		pattern.Interpretation = interpretSkillPattern(pattern)
		items = append(items, pattern)
	}
	return items, rows.Err()
}

// interpretSkillPattern says what the counts support, and refuses to overclaim.
//
// The thresholds are deliberately unimpressive: two sources or three pages is enough to say
// "the page is no longer the common factor", and nothing here is enough to say a skill is
// wrong. Naming the weaker readings explicitly is what stops a two-page coincidence from
// being read as a verdict.
func interpretSkillPattern(pattern SkillSignalPattern) string {
	switch {
	case pattern.DistinctSource >= 2:
		return "negatives span more than one knowledge source, so the page is not the common factor; " +
			"this is where to look for a skill_approach problem, not proof of one"
	case pattern.DistinctPages >= 3:
		return "negatives span several pages of one source; the source or the skill's use of it is " +
			"a better suspect than any single page"
	default:
		return "too few pages to separate a skill problem from a page problem"
	}
}
