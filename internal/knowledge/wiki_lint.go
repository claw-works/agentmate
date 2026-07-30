package knowledge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/jackc/pgx/v5"
)

// ─── K3.7: wiki lint ───
//
// lint is not a second check, and keeping them apart is the whole design.
//
// check is a gate. It runs on a build nobody has seen, its rules are invariants, and
// failing one means the build never becomes visible. lint runs on a wiki that is already
// serving, is read-only, and blocks nothing — it reports what deserves a human's
// attention. Every rule here is therefore something check deliberately does not cover:
// either because it cannot be known at build time, or because it is a judgement about
// quality rather than a violation.
//
// The rules are all PostgreSQL, including the cascade, which needs a recursive CTE. That
// is the point: architecture §14 rejected a graph database because KB lint is the only
// genuine graph workload and recursive CTEs are enough. This file is where that holds or
// does not.

// Lint rule names. They appear in findings, so an operator can grep for a recurring one.
const (
	// lintOrphanPage: a page nothing links to except the generated index.
	//
	// Not covered by check: check requires the index to link every page, so by its rules
	// every page has an inbound link and none is ever orphaned. Reachability from a
	// table of contents is not the same as being connected to the wiki — a page no other
	// page refers to is one nobody arrives at while reading.
	lintOrphanPage = "orphan_page"

	// lintStaleCitation: a citation whose document no longer exists in the source's
	// current revision.
	//
	// Not covered by check: check verifies citations against the revision the build
	// compiled, and that always passes. Staleness is a relation between an old build and
	// the sources as they are now, which cannot be known at build time.
	lintStaleCitation = "stale_citation"

	// lintStaleCascade: a page reachable from a page with stale citations.
	//
	// The page itself may cite nothing that moved, but it rests on conclusions drawn from
	// text that did. This is the rule that needs the recursive CTE, and the one that
	// justifies not reaching for a graph database.
	lintStaleCascade = "stale_cascade"

	// lintRecordedContradiction: a contradicts edge the compiler wrote.
	//
	// Not covered by check: check validates that the link type is allowed and that it
	// resolves, which says nothing about whether anyone has looked at the disagreement.
	// The compiler recording a contradiction is the beginning of the work, not the end.
	lintRecordedContradiction = "recorded_contradiction"

	// lintUnlabelledSupersede: a superseded page with no pointer to its replacement —
	// neither a non-empty superseded_by in frontmatter nor a reference forward.
	//
	// A reader who lands on it has no way to know a newer page replaced it. check cannot
	// object: both pages are valid and the link resolves.
	lintUnlabelledSupersede = "unlabelled_supersede"

	// lintEntityLinkKind: a mentions_entity link pointing at a page that is not an entity.
	//
	// Not covered by check: the link type is allowed and the target exists, so closure
	// holds. What is wrong is the claim the edge makes about its target.
	lintEntityLinkKind = "entity_link_kind"

	// lintUncoveredDocument: an indexable document no page cites.
	//
	// Added beyond the rules listed in the design, because it answers the question a
	// knowledge base owner actually asks — is any of my material being ignored — and
	// nothing else reports it. check cannot: a wiki that covers half its sources violates
	// no invariant.
	lintUncoveredDocument = "uncovered_document"
)

const (
	lintSeverityWarning = "warning"
	lintSeverityInfo    = "info"
)

// LintActiveWiki runs every rule against a source's active build.
//
// Nothing is modified and nothing is blocked. The run and its findings are stored so a
// second run can be compared with the first: a finding that persists across syncs is a
// different signal from one that appears and clears.
func (s *Service) LintActiveWiki(ctx context.Context, owner ownership.Owner, sourceID string) (*LintRunResponse, error) {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return nil, fmt.Errorf("source_id required")
	}
	source, err := s.repo.GetSource(ctx, owner.Account(), sourceID)
	if err != nil {
		// A missing source is a caller mistake, and saying so is the whole job of the
		// message. Passing the driver's "no rows in result set" through tells the caller
		// nothing it can act on and leaks how the lookup is implemented.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("source %s not found", sourceID)
		}
		return nil, err
	}
	if source.ActiveBuildID == nil {
		return nil, fmt.Errorf("source has no active wiki build to lint")
	}
	if source.ActiveRevisionID == nil {
		return nil, fmt.Errorf("source has no active revision")
	}

	startedAt := time.Now().UTC()
	findings, pagesExamined, err := s.repo.RunWikiLint(ctx, owner.Account(),
		*source.ActiveBuildID, *source.ActiveRevisionID)
	if err != nil {
		return nil, err
	}
	run, err := s.repo.RecordLintRun(ctx, owner, recordLintRunInput{
		SourceID:      source.ID,
		BuildID:       *source.ActiveBuildID,
		RevisionID:    *source.ActiveRevisionID,
		StartedAt:     startedAt,
		PagesExamined: pagesExamined,
		Findings:      findings,
	})
	if err != nil {
		return nil, err
	}
	return &LintRunResponse{Run: run, Findings: findings}, nil
}

func (s *Service) ListLintRuns(ctx context.Context, accountID, sourceID string, limit, offset int) (*LintRunListResponse, error) {
	items, total, err := s.repo.ListLintRuns(ctx, accountID, strings.TrimSpace(sourceID), limit, offset)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return &LintRunListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *Service) GetLintRun(ctx context.Context, accountID, runID string) (*LintRunResponse, error) {
	run, err := s.repo.GetLintRun(ctx, accountID, strings.TrimSpace(runID))
	if err != nil {
		return nil, err
	}
	findings, err := s.repo.ListLintFindings(ctx, accountID, run.ID)
	if err != nil {
		return nil, err
	}
	return &LintRunResponse{Run: run, Findings: findings}, nil
}
