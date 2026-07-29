package knowledge

import (
	"strings"
	"testing"
)

func testProfile() ProfileVersion {
	return ProfileVersion{
		AllowedPageKinds:  []string{PageKindSummary, PageKindEntity, PageKindConcept, PageKindIndex, PageKindLog},
		AllowedLinkTypes:  []string{LinkReferences, LinkContradicts, LinkMentionsEntity},
		CitationPolicy:    CitationPolicyRequired,
		MaxPages:          50,
		MaxPageChars:      1000,
		MaxBuildTokens:    100000,
		MaxPageCountDrift: 0.5,
	}
}

func citedPage(path, kind string) WikiPage {
	return WikiPage{
		Path: path, Kind: kind, Content: "正文内容。",
		Citations: []PageCitation{{DocumentPath: "raw/a.md", Claim: "断言"}},
	}
}

func knownPaths(paths ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		set[path] = struct{}{}
	}
	return set
}

func rules(failures []CheckFailure) []string {
	names := make([]string, 0, len(failures))
	for _, failure := range failures {
		names = append(names, failure.Rule)
	}
	return names
}

func hasRule(failures []CheckFailure, rule string) bool {
	for _, failure := range failures {
		if failure.Rule == rule {
			return true
		}
	}
	return false
}

func TestChecksPassOnAWellFormedBuild(t *testing.T) {
	pages := []WikiPage{
		citedPage("wiki/summary-a.md", PageKindSummary),
		citedPage("wiki/entity-b.md", PageKindEntity),
		{Path: "wiki/index.md", Kind: PageKindIndex, Content: "目录", Links: []PageLink{
			{TargetPath: "wiki/summary-a.md", LinkType: LinkReferences},
			{TargetPath: "wiki/entity-b.md", LinkType: LinkReferences},
		}},
	}
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: pages, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if len(failures) != 0 {
		t.Fatalf("expected no failures, got %v", rules(failures))
	}
}

// A citation that cannot be resolved defeats the only mechanism making a
// generated page checkable, so it must fail rather than warn.
func TestCheckFailsUnresolvableCitation(t *testing.T) {
	page := citedPage("wiki/a.md", PageKindSummary)
	page.Citations[0].DocumentPath = "raw/does-not-exist.md"
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: []WikiPage{page}, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if !hasRule(failures, ruleCitationResolvable) {
		t.Fatalf("failures = %v", rules(failures))
	}
	if !strings.Contains(failures[0].Detail, "raw/does-not-exist.md") {
		t.Fatalf("detail should name the missing path: %q", failures[0].Detail)
	}
}

func TestCheckEnforcesCitationPolicy(t *testing.T) {
	uncited := WikiPage{Path: "wiki/a.md", Kind: PageKindSummary, Content: "正文"}

	required := runChecks(checkInput{Profile: testProfile(), Pages: []WikiPage{uncited}})
	if !hasRule(required, ruleCitationRequired) {
		t.Fatalf("required policy should fail: %v", rules(required))
	}

	profile := testProfile()
	profile.CitationPolicy = CitationPolicyOptional
	optional := runChecks(checkInput{Profile: profile, Pages: []WikiPage{uncited}})
	if hasRule(optional, ruleCitationRequired) {
		t.Fatalf("optional policy should not fail: %v", rules(optional))
	}
}

// index and log are generated from the build itself, so demanding citations on
// them would mean demanding evidence for a table of contents.
func TestCheckExemptsIndexAndLogFromCitations(t *testing.T) {
	pages := []WikiPage{
		{Path: "wiki/index.md", Kind: PageKindIndex, Content: "目录"},
		{Path: "wiki/log.md", Kind: PageKindLog, Content: "时间线"},
	}
	failures := runChecks(checkInput{Profile: testProfile(), Pages: pages})
	if hasRule(failures, ruleCitationRequired) {
		t.Fatalf("index and log must not require citations: %v", rules(failures))
	}
}

// A build whose links leave it is not a self-consistent graph and cannot be
// rolled back as a unit.
func TestCheckRequiresLinksToCloseInsideTheBuild(t *testing.T) {
	page := citedPage("wiki/a.md", PageKindSummary)
	page.Links = []PageLink{{TargetPath: "wiki/gone.md", LinkType: LinkReferences}}
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: []WikiPage{page}, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if !hasRule(failures, ruleLinkClosed) {
		t.Fatalf("failures = %v", rules(failures))
	}
}

func TestCheckRejectsDisallowedKindsAndLinkTypes(t *testing.T) {
	page := citedPage("wiki/a.md", "manifesto")
	page.Links = []PageLink{{TargetPath: "wiki/a.md", LinkType: "vibes"}}
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: []WikiPage{page}, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if !hasRule(failures, rulePageKindAllowed) || !hasRule(failures, ruleLinkTypeAllowed) {
		t.Fatalf("failures = %v", rules(failures))
	}
}

// The scenario the drift rule exists for: a compiler failure that collapses a
// wiki into a handful of plausible-looking pages.
func TestCheckCatchesPageCountCollapse(t *testing.T) {
	pages := []WikiPage{
		citedPage("wiki/a.md", PageKindSummary),
		citedPage("wiki/b.md", PageKindSummary),
		citedPage("wiki/c.md", PageKindSummary),
	}
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: pages,
		KnownDocumentPaths: knownPaths("raw/a.md"), ParentPageCount: 30,
	})
	if !hasRule(failures, rulePageCountDrift) {
		t.Fatalf("30 -> 3 must be caught: %v", rules(failures))
	}
}

// Growth and shrinkage are equally suspicious, and a modest change is fine.
func TestCheckDriftIsSymmetricAndTolerant(t *testing.T) {
	build := func(count int) []WikiPage {
		pages := make([]WikiPage, 0, count)
		for index := 0; index < count; index++ {
			pages = append(pages, citedPage("wiki/p"+string(rune('a'+index%26))+string(rune('a'+index/26))+".md", PageKindSummary))
		}
		return pages
	}
	known := knownPaths("raw/a.md")

	if failures := runChecks(checkInput{
		Profile: testProfile(), Pages: build(12), KnownDocumentPaths: known, ParentPageCount: 10,
	}); hasRule(failures, rulePageCountDrift) {
		t.Fatalf("10 -> 12 should be tolerated: %v", rules(failures))
	}
	if failures := runChecks(checkInput{
		Profile: testProfile(), Pages: build(30), KnownDocumentPaths: known, ParentPageCount: 3,
	}); !hasRule(failures, rulePageCountDrift) {
		t.Fatalf("3 -> 30 should be caught too: %v", rules(failures))
	}
	// A first build has no baseline, so the rule must not fire against nothing.
	if failures := runChecks(checkInput{
		Profile: testProfile(), Pages: build(3), KnownDocumentPaths: known, ParentPageCount: 0,
	}); hasRule(failures, rulePageCountDrift) {
		t.Fatalf("a baseline-free build must not drift-fail: %v", rules(failures))
	}
}

func TestCheckEnforcesSizeAndBudgetCeilings(t *testing.T) {
	profile := testProfile()
	profile.MaxPageChars = 10
	profile.MaxPages = 1
	profile.MaxBuildTokens = 100

	pages := []WikiPage{
		citedPage("wiki/a.md", PageKindSummary),
		citedPage("wiki/b.md", PageKindSummary),
	}
	pages[0].Content = strings.Repeat("长", 50)

	failures := runChecks(checkInput{
		Profile: profile, Pages: pages, KnownDocumentPaths: knownPaths("raw/a.md"), TotalTokens: 500,
	})
	for _, rule := range []string{rulePageSize, rulePageCount, ruleTokenBudget} {
		if !hasRule(failures, rule) {
			t.Fatalf("missing %s in %v", rule, rules(failures))
		}
	}
}

// Page size is measured in runes: a CJK wiki would otherwise fail on byte length
// while an English one of the same length passes.
func TestCheckPageSizeCountsRunes(t *testing.T) {
	profile := testProfile()
	profile.MaxPageChars = 100
	page := citedPage("wiki/a.md", PageKindSummary)
	page.Content = strings.Repeat("知", 100)

	failures := runChecks(checkInput{
		Profile: profile, Pages: []WikiPage{page}, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if hasRule(failures, rulePageSize) {
		t.Fatalf("100 runes must fit a 100-rune limit: %v", rules(failures))
	}
}

// A page missing from the index is effectively invisible to an agent that
// navigates by reading the index first.
func TestCheckIndexCoverage(t *testing.T) {
	pages := []WikiPage{
		citedPage("wiki/covered.md", PageKindSummary),
		citedPage("wiki/orphan.md", PageKindSummary),
		{Path: "wiki/index.md", Kind: PageKindIndex, Content: "目录", Links: []PageLink{
			{TargetPath: "wiki/covered.md", LinkType: LinkReferences},
		}},
	}
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: pages, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if !hasRule(failures, ruleIndexCoverage) {
		t.Fatalf("failures = %v", rules(failures))
	}
	for _, failure := range failures {
		if failure.Rule == ruleIndexCoverage && failure.PagePath != "wiki/orphan.md" {
			t.Fatalf("wrong page reported: %#v", failure)
		}
	}

	// With no index page the rule cannot apply, and must not fire spuriously.
	noIndex := runChecks(checkInput{
		Profile: testProfile(), Pages: pages[:2], KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if hasRule(noIndex, ruleIndexCoverage) {
		t.Fatalf("index coverage must not fire without an index: %v", rules(noIndex))
	}
}

func TestCheckRejectsDuplicatePathsAndEmptyContent(t *testing.T) {
	pages := []WikiPage{
		citedPage("wiki/a.md", PageKindSummary),
		citedPage("wiki/a.md", PageKindSummary),
		{Path: "wiki/blank.md", Kind: PageKindSummary, Content: "   ",
			Citations: []PageCitation{{DocumentPath: "raw/a.md"}}},
	}
	failures := runChecks(checkInput{
		Profile: testProfile(), Pages: pages, KnownDocumentPaths: knownPaths("raw/a.md"),
	})
	if !hasRule(failures, rulePathUnique) || !hasRule(failures, ruleContentNonEmpty) {
		t.Fatalf("failures = %v", rules(failures))
	}
}

// check gates activation, so its verdict must be identical across runs. A map
// iteration leaking into the output would make a retried build pass or fail at
// random.
func TestChecksAreDeterministic(t *testing.T) {
	pages := []WikiPage{
		{Path: "wiki/b.md", Kind: "bogus", Content: "x"},
		{Path: "wiki/a.md", Kind: "bogus", Content: "y"},
		{Path: "wiki/c.md", Kind: "bogus", Content: "z"},
	}
	input := checkInput{Profile: testProfile(), Pages: pages}
	first := rules(runChecks(input))
	for attempt := 0; attempt < 20; attempt++ {
		if got := rules(runChecks(input)); strings.Join(got, ",") != strings.Join(first, ",") {
			t.Fatalf("non-deterministic output:\n%v\n%v", first, got)
		}
	}
}
