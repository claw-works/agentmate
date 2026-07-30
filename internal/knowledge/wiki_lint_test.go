package knowledge

import (
	"context"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/ownership"
)

// ─── K3.7: lint ───
//
// These tests hold the line between lint and check. Every case here compiles a build that
// check *passes* and then shows lint finding something in it: if a case could be made to
// fail check instead, the rule belongs there and not here.

func lintFindings(findings []LintFinding, rule string) []LintFinding {
	matched := make([]LintFinding, 0, len(findings))
	for _, finding := range findings {
		if finding.Rule == rule {
			matched = append(matched, finding)
		}
	}
	return matched
}

func lintActive(t *testing.T, ctx context.Context, service *Service, owner ownership.Owner, sourceID string) *LintRunResponse {
	t.Helper()
	response, err := service.LintActiveWiki(ctx, owner, sourceID)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	return response
}

// TestLintOrphanPageIgnoresIndexLinks is the case that decides whether the rule means
// anything. check requires the generated index to link every page, so counting index
// links as inbound connectivity would make orphan_page unable to fire at all.
func TestLintOrphanPageIgnoresIndexLinks(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Overview body.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/details.md", "link_type": "references"},
			}),
		wikiPage("wiki/details.md", PageKindConcept, "Details", "Details body.",
			[]string{"raw/details.md"}, nil),
		// Nothing links here. The index will, and that must not count.
		wikiPage("wiki/stranded.md", PageKindConcept, "Stranded", "Stranded body.",
			[]string{"raw/details.md"}, nil),
	)}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-orphan")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-orphan", "Retention is 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("build must succeed to be linted, got %s (%s)", build.Status, build.Error)
	}
	if build.CheckStatus != CheckStatusPassed {
		t.Fatalf("lint operates on builds check accepted; check said %s", build.CheckStatus)
	}

	response := lintActive(t, ctx, service, owner, source.ID)
	orphans := lintFindings(response.Findings, lintOrphanPage)
	if len(orphans) != 1 {
		t.Fatalf("want exactly one orphan, got %d: %+v", len(orphans), orphans)
	}
	if orphans[0].PagePath != "wiki/stranded.md" {
		t.Fatalf("wrong orphan: %s", orphans[0].PagePath)
	}
	if orphans[0].Severity != lintSeverityWarning {
		t.Fatalf("orphan should be a warning, got %s", orphans[0].Severity)
	}
	for _, finding := range orphans {
		// A page with a real inbound link must not be reported, or the rule is just
		// "list pages".
		if finding.PagePath == "wiki/details.md" {
			t.Fatalf("a page with a real inbound link was reported orphan")
		}
		// An overview has no inbound links by design: readers enter through the index.
		// Reporting it would make the rule fire on every wiki, every run.
		if finding.PagePath == "wiki/overview.md" {
			t.Fatalf("an entry-point page was reported orphan")
		}
	}
}

// TestLintStaleCitationAndCascade covers the two rules check structurally cannot: staleness
// is a relation between an old build and the sources as they are now, which does not exist
// at build time. The cascade is also the only genuine graph query in the system, and the
// reason architecture §14 could reject a graph database.
func TestLintStaleCitationAndCascade(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{wikiReply(
		// Cites the document that will disappear.
		wikiPage("wiki/details.md", PageKindConcept, "Details", "Retention is 30 days.",
			[]string{"raw/details.md"}, nil),
		// Cites a document that will be rewritten rather than removed.
		wikiPage("wiki/notes.md", PageKindConcept, "Notes", "Notes body.",
			[]string{"raw/rewritten.md"}, nil),
		// Cites only a document that never changes, so anything reported about it comes
		// from the graph walk and not from its own citations. One hop from details.md.
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "See details.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/details.md", "link_type": "references"},
			}),
		// Two hops. This is the page that needs the recursion: nothing it cites moved and
		// it does not touch the stale page directly.
		wikiPage("wiki/policy.md", PageKindSummary, "Policy", "Derived from the overview.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/overview.md", "link_type": "references"},
			}),
	)}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-stale")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-stale", "Overview body.")
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: wiki-kb\nprofile: wiki-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\nStable overview body.\n"},
		{Path: "raw/details.md", Content: "# Details\n\nRetention is 30 days.\n"},
		{Path: "raw/rewritten.md", Content: "# Rewritten\n\nFirst version.\n"},
	}}); err != nil {
		t.Fatalf("submit first snapshot: %v", err)
	}

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.CheckStatus != CheckStatusPassed {
		t.Fatalf("check must pass before lint is meaningful: %s", build.CheckStatus)
	}

	// Control: compiled from the current revision, so nothing is stale yet. Without this,
	// a rule that always fires would look correct.
	if findings := lintFindings(lintActive(t, ctx, service, owner, source.ID).Findings, lintStaleCitation); len(findings) != 0 {
		t.Fatalf("a wiki compiled from the current revision has no stale citations, got %+v", findings)
	}

	// Advance the raw layer: details.md is gone, rewritten.md changed, overview.md is
	// untouched, added.md is new and cited by nobody.
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: wiki-kb\nprofile: wiki-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\nStable overview body.\n"},
		{Path: "raw/rewritten.md", Content: "# Rewritten\n\nSecond version, different bytes.\n"},
		{Path: "raw/added.md", Content: "# Added\n\nNobody cites this.\n"},
	}}); err != nil {
		t.Fatalf("submit second snapshot: %v", err)
	}

	response := lintActive(t, ctx, service, owner, source.ID)

	stale := lintFindings(response.Findings, lintStaleCitation)
	staleByPage := make(map[string]LintFinding, len(stale))
	for _, finding := range stale {
		staleByPage[finding.PagePath] = finding
	}
	removed, ok := staleByPage["wiki/details.md"]
	if !ok {
		t.Fatalf("the page citing the removed document must be stale: %+v", stale)
	}
	if !strings.Contains(removed.Detail, "removed") {
		t.Fatalf("a removed document should say so, got %q", removed.Detail)
	}
	// A rewritten document is stale too: the bytes moved under the claim, which is far
	// more common than deletion and no less wrong.
	rewritten, ok := staleByPage["wiki/notes.md"]
	if !ok {
		t.Fatalf("the page citing the rewritten document must be stale: %+v", stale)
	}
	if !strings.Contains(rewritten.Detail, "changed") {
		t.Fatalf("a rewritten document should say so, got %q", rewritten.Detail)
	}
	// The page whose only citation never moved must not be called stale, or the rule
	// cannot distinguish a stale page from any page at all.
	if _, reported := staleByPage["wiki/policy.md"]; reported {
		t.Fatalf("a page citing only unchanged documents was reported stale")
	}

	cascade := lintFindings(response.Findings, lintStaleCascade)
	cascadeDepth := make(map[string]string, len(cascade))
	for _, finding := range cascade {
		cascadeDepth[finding.PagePath] = finding.Detail
	}
	// One hop: links directly to the stale page.
	if _, ok := cascadeDepth["wiki/overview.md"]; !ok {
		t.Fatalf("one-hop page missing from cascade: %+v", cascade)
	}
	// Two hops: reachable only through overview.md. Reporting it proves the recursive
	// walk ran rather than a single join — which is the whole basis for architecture §14
	// rejecting a graph database.
	detail, ok := cascadeDepth["wiki/policy.md"]
	if !ok {
		t.Fatalf("two-hop page missing from cascade: %+v", cascade)
	}
	if !strings.Contains(detail, "2 hop") {
		t.Fatalf("distance matters and must be reported, got %q", detail)
	}
	// Pages already reported stale must not be repeated as their own downstream.
	if _, ok := cascadeDepth["wiki/details.md"]; ok {
		t.Fatalf("a stale page must not also be reported as its own cascade")
	}

	uncovered := lintFindings(response.Findings, lintUncoveredDocument)
	if len(uncovered) != 1 || uncovered[0].RelatedPath != "raw/added.md" {
		t.Fatalf("the new document nothing cites should be reported once, got %+v", uncovered)
	}
}

// TestLintGraphRulesOnPassingBuild groups the rules that fire on builds check is happy
// with, because closure and type validity say nothing about whether the claims the edges
// make are true.
func TestLintGraphRulesOnPassingBuild(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Overview body.",
			[]string{"raw/overview.md"}, []map[string]any{
				// Disagreement recorded by the compiler: valid edge, unresolved question.
				{"target_path": "wiki/details.md", "link_type": "contradicts",
					"note": "overview says 30 days, details says 90"},
				// Claims details.md is an entity page. It is a concept page.
				{"target_path": "wiki/details.md", "link_type": "mentions_entity"},
				// Supersedes the old page, which carries no pointer back here.
				{"target_path": "wiki/legacy.md", "link_type": "supersedes"},
			}),
		wikiPage("wiki/details.md", PageKindConcept, "Details", "Details body.",
			[]string{"raw/details.md"}, nil),
		wikiPage("wiki/legacy.md", PageKindConcept, "Legacy", "Superseded body.",
			[]string{"raw/details.md"}, nil),
	)}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-graph")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-graph", "Overview body.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.CheckStatus != CheckStatusPassed {
		t.Fatalf("these edges are all valid; check must pass, got %s", build.CheckStatus)
	}

	response := lintActive(t, ctx, service, owner, source.ID)

	if got := lintFindings(response.Findings, lintRecordedContradiction); len(got) != 1 {
		t.Fatalf("the contradicts edge must surface exactly once, got %+v", got)
	} else if got[0].PagePath != "wiki/overview.md" || got[0].RelatedPath != "wiki/details.md" {
		t.Fatalf("contradiction names the wrong pair: %+v", got[0])
	} else if !strings.Contains(got[0].Detail, "30 days") {
		t.Fatalf("the compiler's note is the useful part and must be carried through: %q", got[0].Detail)
	}

	if got := lintFindings(response.Findings, lintEntityLinkKind); len(got) != 1 {
		t.Fatalf("mentions_entity at a concept page must be reported, got %+v", got)
	} else if got[0].Severity != lintSeverityInfo {
		t.Fatalf("a wrong claim about a target's kind is info, not warning: %s", got[0].Severity)
	}

	if got := lintFindings(response.Findings, lintUnlabelledSupersede); len(got) != 1 {
		t.Fatalf("the superseded page has no forward pointer and must be reported, got %+v", got)
	} else if got[0].PagePath != "wiki/legacy.md" || got[0].RelatedPath != "wiki/overview.md" {
		t.Fatalf("supersede finding names the wrong pair: %+v", got[0])
	}
}

// TestLintChangesNothingAndBlocksNothing is the rule that separates lint from check. If
// lint could alter a page or unseat an active build, it would be a gate wearing advisory
// clothing.
func TestLintChangesNothingAndBlocksNothing(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Overview body.",
			[]string{"raw/overview.md"}, nil),
		wikiPage("wiki/details.md", PageKindConcept, "Details", "Details body.",
			[]string{"raw/details.md"}, nil),
	)}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-inert")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-inert", "Overview body.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	before, err := service.ListPages(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list pages before lint: %v", err)
	}

	first := lintActive(t, ctx, service, owner, source.ID)
	if first.Run.PagesExamined == 0 {
		t.Fatalf("a run that examined no pages is not evidence of anything")
	}
	if first.Run.FindingsTotal != first.Run.FindingsWarning+first.Run.FindingsInfo {
		t.Fatalf("counts must add up: %+v", first.Run)
	}
	if first.Run.FindingsTotal != len(first.Findings) {
		t.Fatalf("recorded total %d disagrees with %d returned findings",
			first.Run.FindingsTotal, len(first.Findings))
	}

	after, err := service.ListPages(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("list pages after lint: %v", err)
	}
	if len(before.Items) != len(after.Items) {
		t.Fatalf("lint changed the page set: %d -> %d", len(before.Items), len(after.Items))
	}
	for i := range before.Items {
		if before.Items[i].ContentHash != after.Items[i].ContentHash {
			t.Fatalf("lint rewrote %s", before.Items[i].Path)
		}
	}

	// The build is still serving, findings or not.
	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if refreshed.ActiveBuildID == nil || *refreshed.ActiveBuildID != build.ID {
		t.Fatalf("lint must not disturb the active pointer")
	}

	// Runs accumulate rather than overwrite: comparing two runs is the only way to tell a
	// finding that persists from one that cleared, since findings carry no status.
	second := lintActive(t, ctx, service, owner, source.ID)
	if second.Run.ID == first.Run.ID {
		t.Fatalf("a second lint must record a second run")
	}
	runs, err := service.ListLintRuns(ctx, owner.Account(), source.ID, 10, 0)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if runs.Total < 2 {
		t.Fatalf("want at least two recorded runs, got %d", runs.Total)
	}

	fetched, err := service.GetLintRun(ctx, owner.Account(), first.Run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if len(fetched.Findings) != len(first.Findings) {
		t.Fatalf("stored findings %d disagree with returned %d",
			len(fetched.Findings), len(first.Findings))
	}
	if fetched.Run.BuildID != build.ID || fetched.Run.RevisionID == "" {
		t.Fatalf("a run must identify both the build and the revision it was compared against: %+v", fetched.Run)
	}
}

// TestLintRequiresActiveWiki: there is nothing to lint before a wiki serves, and saying so
// is better than returning an empty finding list, which reads as a clean wiki.
func TestLintRequiresActiveWiki(t *testing.T) {
	ctx := context.Background()
	service, _ := newWikiService(t, ctx, &scriptedClient{replies: []string{wikiReply()}})
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-no-build")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-no-build", "Overview body.")

	if _, err := service.LintActiveWiki(ctx, owner, source.ID); err == nil {
		t.Fatalf("linting a source with no active build must fail loudly")
	}
	if _, err := service.LintActiveWiki(ctx, owner, ""); err == nil {
		t.Fatalf("an empty source_id must be rejected")
	}
	// A missing source must produce a message the caller can act on, not the driver's
	// "no rows in result set", which says nothing and leaks the implementation.
	_, err := service.LintActiveWiki(ctx, owner, "00000000-0000-0000-0000-000000000000")
	if err == nil {
		t.Fatalf("linting an unknown source must fail")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown source should say so, got %q", err)
	}
}

// TestLintCascadeOnlyFollowsDependencyEdges: staleness propagates along "builds on", not
// along every edge. A page that supersedes a stale page is its replacement, not its
// dependent, and a page that contradicts one is disagreeing with it, not resting on it.
// Walking every edge type would report the newer page as affected by the one it retired.
func TestLintCascadeOnlyFollowsDependencyEdges(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{wikiReply(
		// Goes stale: its only citation is removed in the second snapshot.
		wikiPage("wiki/details.md", PageKindConcept, "Details", "Retention is 30 days.",
			[]string{"raw/details.md"}, nil),
		// Depends on it. Must appear in the cascade.
		wikiPage("wiki/dependent.md", PageKindConcept, "Dependent", "Builds on details.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/details.md", "link_type": "references"},
			}),
		// Retires it. Must not appear: it is the replacement, not a dependent.
		wikiPage("wiki/replacement.md", PageKindConcept, "Replacement", "Replaces details.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/details.md", "link_type": "supersedes"},
				// Also carries a forward pointer target, so this test does not depend on
				// the supersede rule firing.
			}),
		// Disagrees with it. Must not appear either.
		wikiPage("wiki/dissent.md", PageKindConcept, "Dissent", "Disagrees with details.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/details.md", "link_type": "contradicts", "note": "disputed"},
			}),
	)}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-edges")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-edges", "Overview body.")

	if build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); build.CheckStatus != CheckStatusPassed {
		t.Fatalf("all these edge types are valid; check must pass, got %s", build.CheckStatus)
	}
	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: wiki-kb\nprofile: wiki-test\ninclude:\n  - \"raw/**\"\n"},
		// Byte-identical to the first snapshot on purpose: if this document changed, the
		// three pages citing it would be stale in their own right and excluded from the
		// cascade, and every assertion below would pass without testing anything.
		{Path: "raw/overview.md", Content: "# Overview\n\nOverview body."},
	}}); err != nil {
		t.Fatalf("submit second snapshot: %v", err)
	}

	// Guard the premise: only details.md may be directly stale.
	all := lintActive(t, ctx, service, owner, source.ID).Findings
	for _, finding := range lintFindings(all, lintStaleCitation) {
		if finding.PagePath != "wiki/details.md" {
			t.Fatalf("premise broken: %s is directly stale, so the cascade cannot be tested via it", finding.PagePath)
		}
	}

	cascade := lintFindings(all, lintStaleCascade)
	reported := make(map[string]bool, len(cascade))
	for _, finding := range cascade {
		reported[finding.PagePath] = true
	}
	if !reported["wiki/dependent.md"] {
		t.Fatalf("a references edge must propagate staleness: %+v", cascade)
	}
	if reported["wiki/replacement.md"] {
		t.Fatalf("a supersedes edge must not: the replacement does not rest on what it retired")
	}
	if reported["wiki/dissent.md"] {
		t.Fatalf("a contradicts edge must not: disagreeing is not depending")
	}
}

// TestLintSupersedeForwardPointerMustBeReal: the rule is satisfied by a pointer a reader
// can follow, not by the mere presence of a key or of some unrelated edge.
func TestLintSupersedeForwardPointerMustBeReal(t *testing.T) {
	ctx := context.Background()
	client := &scriptedClient{replies: []string{wikiReply(
		wikiPage("wiki/newer-a.md", PageKindConcept, "Newer A", "Replaces older A.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/older-a.md", "link_type": "supersedes"},
			}),
		// Points back with a contradicts edge only. That is not "here is your
		// replacement", so the finding must still fire.
		wikiPage("wiki/older-a.md", PageKindConcept, "Older A", "Outdated body.",
			[]string{"raw/details.md"}, []map[string]any{
				{"target_path": "wiki/newer-a.md", "link_type": "contradicts", "note": "disputed"},
			}),
		wikiPage("wiki/newer-b.md", PageKindConcept, "Newer B", "Replaces older B.",
			[]string{"raw/overview.md"}, []map[string]any{
				{"target_path": "wiki/older-b.md", "link_type": "supersedes"},
			}),
		// Points back with a real reference. Nothing to report.
		wikiPage("wiki/older-b.md", PageKindConcept, "Older B", "Outdated body.",
			[]string{"raw/details.md"}, []map[string]any{
				{"target_path": "wiki/newer-b.md", "link_type": "references"},
			}),
	)}}
	service, worker := newWikiService(t, ctx, client)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "lint-supersede")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "lint-supersede", "Overview body.")
	if build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); build.CheckStatus != CheckStatusPassed {
		t.Fatalf("check must pass, got %s", build.CheckStatus)
	}

	findings := lintFindings(lintActive(t, ctx, service, owner, source.ID).Findings, lintUnlabelledSupersede)
	reported := make(map[string]bool, len(findings))
	for _, finding := range findings {
		reported[finding.PagePath] = true
	}
	if !reported["wiki/older-a.md"] {
		t.Fatalf("a contradicts edge back is not a replacement pointer; finding must fire: %+v", findings)
	}
	if reported["wiki/older-b.md"] {
		t.Fatalf("a real reference forward satisfies the rule and must silence it")
	}
}
