package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/claw-works/agentmate/internal/llm"
)

// ─── K3.8: review ───
//
// The tests hold three lines that are easy to lose: review must never affect whether a
// build serves, "clean" must never cover pages nobody examined, and a same-model reviewer
// must be refused rather than run.

// scriptedReviewer answers per page, so a test can flag one page and clear another. Keying
// on the page path rather than call order matters: review's page order is by citation
// count, and a test that depended on call order would silently test the ordering instead of
// the behaviour.
type scriptedReviewer struct {
	byPage map[string][]map[string]any
	err    error
	// failPages fails specific pages, which is how partial coverage is scripted.
	failPages map[string]error
	model     string
	calls     int
	prompts   []string
}

func (c *scriptedReviewer) Complete(_ context.Context, messages []llm.Message, _ ...llm.Option) (*llm.Completion, error) {
	c.calls++
	prompt := ""
	for _, message := range messages {
		if message.Role == "user" {
			prompt = message.Content
		}
	}
	c.prompts = append(c.prompts, prompt)
	if c.err != nil {
		return nil, c.err
	}
	page := ""
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "path: ") {
			page = strings.TrimPrefix(line, "path: ")
			break
		}
	}
	if failure, ok := c.failPages[page]; ok {
		return nil, failure
	}
	findings := c.byPage[page]
	if findings == nil {
		findings = []map[string]any{}
	}
	payload, err := json.Marshal(map[string]any{"findings": findings})
	if err != nil {
		return nil, err
	}
	return &llm.Completion{
		Content: string(payload), Model: c.Model(),
		Usage: llm.Usage{PromptTokens: 500, CompletionTokens: 100, TotalTokens: 600},
	}, nil
}

func (c *scriptedReviewer) Model() string {
	if c.model != "" {
		return c.model
	}
	return "scripted-reviewer"
}
func (c *scriptedReviewer) Configured() bool { return true }

// newReviewService wires a compiler and a reviewer with a declared independence.
func newReviewService(t *testing.T, ctx context.Context, compiler llm.Client, reviewer llm.Client, independence string) (*Service, *Worker) {
	t.Helper()
	service, worker := newWikiService(t, ctx, compiler)
	service.WithLLM(LLMSetup{
		Compiler:        compiler,
		Reviewer:        reviewer,
		Independence:    independence,
		CompilerPricing: llm.Pricing{InputMicrosPer1KTokens: 2000, OutputMicrosPer1KTokens: 6000},
		// A configured reviewer price so review cost is exercised rather than trivially
		// zero: 500 in + 100 out per call at these rates is 1100 micros.
		ReviewerPricing: llm.Pricing{InputMicrosPer1KTokens: 1000, OutputMicrosPer1KTokens: 6000},
	})
	return service, worker
}

func twoPageReply() string {
	return wikiReply(
		wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Retention is always 30 days.",
			[]string{"raw/overview.md", "raw/details.md"}, nil),
		wikiPage("wiki/details.md", PageKindConcept, "Details", "Details body.",
			[]string{"raw/details.md"}, nil),
	)
}

// TestReviewFlagsWithoutBlocking is the core guarantee: a flagged build still serves. If
// review could unseat a build, the same build retried twice could land differently, because
// its verdicts are not reproducible.
func TestReviewFlagsWithoutBlocking(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{byPage: map[string][]map[string]any{
		"wiki/overview.md": {{
			"kind": "overstated", "claim": "Retention is always 30 days.",
			"detail": "the source says the window is usually 30 days",
		}},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-flag")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-flag", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusSucceeded || build.CheckStatus != CheckStatusPassed {
		t.Fatalf("build must succeed: %s / %s (%s)", build.Status, build.CheckStatus, build.Error)
	}

	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus != ReviewStatusFlagged {
		t.Fatalf("want flagged, got %s (%s)", review.Build.ReviewStatus, review.Build.ReviewNote)
	}
	if len(review.Findings) != 1 {
		t.Fatalf("want one finding, got %+v", review.Findings)
	}
	if review.Findings[0].Kind != reviewOverstated || review.Findings[0].PagePath != "wiki/overview.md" {
		t.Fatalf("wrong finding: %+v", review.Findings[0])
	}
	if review.Findings[0].Claim == "" {
		t.Fatalf("a finding must quote the claim, or nobody can locate it")
	}

	// The build is serving despite the finding. This is the line between review and check.
	refreshed, err := service.GetSource(ctx, owner.Account(), source.ID)
	if err != nil {
		t.Fatalf("reload source: %v", err)
	}
	if refreshed.ActiveBuildID == nil || *refreshed.ActiveBuildID != build.ID {
		t.Fatalf("a flagged build must still be activated; review does not gate")
	}

	// Provenance and cost must be recorded, or the verdict cannot be attributed later.
	if review.Build.ReviewerModel != reviewer.Model() {
		t.Fatalf("reviewer model not recorded: %q", review.Build.ReviewerModel)
	}
	if review.Build.ReviewTokens == 0 || review.Build.ReviewCostMicros == 0 {
		t.Fatalf("review spent money and it must show up: tokens=%d cost=%d",
			review.Build.ReviewTokens, review.Build.ReviewCostMicros)
	}
	if review.Build.ReviewPagesExamined != 2 || review.Build.ReviewPagesTotal != 2 {
		t.Fatalf("coverage wrong: %d of %d", review.Build.ReviewPagesExamined, review.Build.ReviewPagesTotal)
	}
}

// TestReviewJudgesAgainstSourceTextNotCompilerExcerpts: the reviewer must receive the raw
// document text. Judging the compiler's page against the compiler's own chosen excerpt is
// circular — a page that quotes selectively would be validated by the selection that makes
// it wrong.
func TestReviewJudgesAgainstSourceTextNotCompilerExcerpts(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-sources")
	defer cleanup()
	// A sentence that exists only in the raw document, never in any citation excerpt.
	source := seedWikiSource(t, ctx, service, owner, "review-sources", "Retention is usually 30 days and configurable per tenant.")

	if build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID}); build.Status != BuildStatusSucceeded {
		t.Fatalf("build must succeed, got %s", build.Status)
	}
	if reviewer.calls == 0 {
		t.Fatalf("reviewer was never called")
	}

	joined := strings.Join(reviewer.prompts, "\n")
	if !strings.Contains(joined, "configurable per tenant") {
		t.Fatalf("the reviewer must see the raw document text, not only citation excerpts")
	}
	if !strings.Contains(joined, "# Source documents this page cites") {
		t.Fatalf("sources section missing from the review prompt")
	}
	// The compiler's own account may be shown, but must be labelled as its account rather
	// than as evidence.
	if strings.Contains(joined, "Claims the compiler recorded") &&
		!strings.Contains(joined, "not evidence") {
		t.Fatalf("the compiler's claims must be labelled as its own account, not as evidence")
	}
}

// TestReviewSameModelIsRefused: a model cannot find the mistakes its own priors produced.
// Running it anyway would manufacture the appearance of verification, which is worse than
// no verification because it invites the reader to stop looking.
func TestReviewSameModelIsRefused(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{model: "scripted-compiler", byPage: map[string][]map[string]any{
		"wiki/overview.md": {{"kind": "unsupported", "claim": "anything", "detail": "would be reported if it ran"}},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceSameModel)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-same-model")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-same-model", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus != ReviewStatusSkipped {
		t.Fatalf("same-model review must be skipped, got %s", review.Build.ReviewStatus)
	}
	if reviewer.calls != 0 {
		t.Fatalf("a same-model reviewer must not be called at all, got %d calls", reviewer.calls)
	}
	if !strings.Contains(review.Build.ReviewNote, "collude") {
		t.Fatalf("the note must say why it was refused, got %q", review.Build.ReviewNote)
	}
	if len(review.Findings) != 0 {
		t.Fatalf("a refused review produces no findings")
	}
}

// TestReviewPartialNeverReportsClean: coverage and verdict are different facts. A review
// that examined some pages and found nothing must not claim the wiki is clean.
func TestReviewPartialNeverReportsClean(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{wikiReply(
		wikiPage("wiki/a.md", PageKindConcept, "A", "Body A.", []string{"raw/details.md"}, nil),
		wikiPage("wiki/b.md", PageKindConcept, "B", "Body B.", []string{"raw/details.md"}, nil),
		wikiPage("wiki/c.md", PageKindOverview, "C", "Body C.", []string{"raw/overview.md"}, nil),
	)}}
	// One page's reviewer call fails; the others come back clean.
	reviewer := &scriptedReviewer{failPages: map[string]error{
		"wiki/b.md": errors.New("provider unreachable"),
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-partial")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-partial", "Overview body.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus != ReviewStatusPartial {
		t.Fatalf("one failed page means partial, not %s (%s)",
			review.Build.ReviewStatus, review.Build.ReviewNote)
	}
	if review.Build.ReviewStatus == ReviewStatusClean {
		t.Fatalf("clean would be a claim about the page that was never reviewed")
	}
	if review.Build.ReviewPagesExamined != 2 || review.Build.ReviewPagesTotal != 3 {
		t.Fatalf("coverage must be exact: %d of %d",
			review.Build.ReviewPagesExamined, review.Build.ReviewPagesTotal)
	}
	if !strings.Contains(review.Build.ReviewNote, "carry no verdict") {
		t.Fatalf("the note must say the unexamined pages carry no verdict, got %q", review.Build.ReviewNote)
	}
	// And it must name the cause: "one page could not be reviewed" without a reason tells
	// an operator that work is missing but not what to do about it.
	if !strings.Contains(review.Build.ReviewNote, "provider unreachable") {
		t.Fatalf("the note must carry the failure reason, got %q", review.Build.ReviewNote)
	}
	// A page failing must not abandon the rest: partial information beats none.
	if reviewer.calls != 3 {
		t.Fatalf("every eligible page must be attempted, got %d calls", reviewer.calls)
	}
}

// TestReviewAllCallsFailingIsFailedNotClean: an empty finding set from a review that never
// ran looks exactly like a clean wiki unless the status distinguishes them.
func TestReviewAllCallsFailingIsFailedNotClean(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{err: errors.New("provider down")}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-failed")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-failed", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	// The build must be unharmed: an advisory step cannot undo a compile that passed the
	// only gate there is.
	if build.Status != BuildStatusSucceeded || build.CheckStatus != CheckStatusPassed {
		t.Fatalf("a failing reviewer must not fail the build: %s / %s", build.Status, build.CheckStatus)
	}
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus != ReviewStatusFailed {
		t.Fatalf("want failed, got %s (%s)", review.Build.ReviewStatus, review.Build.ReviewNote)
	}
	if !strings.Contains(review.Build.ReviewNote, "provider down") {
		t.Fatalf("the note must carry the provider's reason, got %q", review.Build.ReviewNote)
	}
}

// TestReviewRerunReplacesFindings: a build is immutable, so two sets of verdicts on it
// cannot both be current. Accumulating them would also inflate the finding count every time
// a flaky provider forced a retry.
func TestReviewRerunReplacesFindings(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{byPage: map[string][]map[string]any{
		"wiki/overview.md": {{"kind": "overstated", "claim": "Retention is always 30 days.", "detail": "source hedges"}},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-rerun")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-rerun", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	first, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if len(first.Findings) != 1 {
		t.Fatalf("want one finding, got %d", len(first.Findings))
	}
	tokensAfterFirst := first.Build.ReviewTokens

	// The page is now judged clean on re-review.
	reviewer.byPage = map[string][]map[string]any{}
	second, err := service.ReviewBuild(ctx, owner, build.ID)
	if err != nil {
		t.Fatalf("re-review: %v", err)
	}
	if len(second.Findings) != 0 {
		t.Fatalf("stale findings survived a re-review: %+v", second.Findings)
	}
	if second.Build.ReviewStatus != ReviewStatusClean {
		t.Fatalf("want clean after re-review, got %s (%s)", second.Build.ReviewStatus, second.Build.ReviewNote)
	}
	stored, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review after rerun: %v", err)
	}
	if len(stored.Findings) != 0 {
		t.Fatalf("the previous findings must be gone from storage too: %+v", stored.Findings)
	}
	// Money already spent stays on the bill: the first attempt cost real tokens.
	if stored.Build.ReviewTokens <= tokensAfterFirst {
		t.Fatalf("re-review cost must accumulate, not replace: %d then %d",
			tokensAfterFirst, stored.Build.ReviewTokens)
	}
}

// TestReviewSkippedWithoutReviewer: with no reviewer, the build must say so rather than
// leaving a status that reads like a verdict.
func TestReviewSkippedWithoutReviewer(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	service, worker := newWikiService(t, ctx, compiler)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-none")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-none", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus != ReviewStatusSkipped {
		t.Fatalf("want skipped, got %s", review.Build.ReviewStatus)
	}
	if !strings.Contains(review.Build.ReviewNote, "no reviewer") {
		t.Fatalf("the note must say a reviewer is missing, got %q", review.Build.ReviewNote)
	}
	// And the warning surfaced to callers must say the same thing, not stay silent because
	// the configuration improved elsewhere.
	warnings := strings.Join(service.reviewWarnings(), " | ")
	if !strings.Contains(warnings, "no reviewer") {
		t.Fatalf("callers must be told review did not run: %q", warnings)
	}
}

// TestReviewOnlyChangedPagesOnIncremental: reused pages were reviewed when they were
// written, and paying to re-judge unchanged text against unchanged sources buys nothing.
func TestReviewOnlyChangedPagesOnIncremental(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{
		wikiReply(
			wikiPage("wiki/overview.md", PageKindOverview, "Overview", "Overview body.",
				[]string{"raw/overview.md"}, nil),
			wikiPage("wiki/details.md", PageKindConcept, "Details", "Details body.",
				[]string{"raw/details.md"}, nil),
		),
		// The incremental reply rewrites only details.md.
		wikiReply(
			wikiPage("wiki/details.md", PageKindConcept, "Details", "Details body, revised.",
				[]string{"raw/details.md"}, nil),
		),
	}}
	reviewer := &scriptedReviewer{}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-incremental")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-incremental", "Overview body.")

	first := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if first.Status != BuildStatusSucceeded {
		t.Fatalf("first build must succeed: %s", first.Error)
	}
	callsAfterFull := reviewer.calls
	if callsAfterFull != 2 {
		t.Fatalf("a full build reviews every content page, want 2 calls, got %d", callsAfterFull)
	}
	reviewer.prompts = nil

	if _, err := service.SubmitSnapshot(ctx, owner, source.ID, SubmitSnapshotRequest{Files: []SnapshotFile{
		{Path: "KNOWLEDGE.yaml", Content: "name: wiki-kb\nprofile: wiki-test\ninclude:\n  - \"raw/**\"\n"},
		{Path: "raw/overview.md", Content: "# Overview\n\nOverview body."},
		{Path: "raw/details.md", Content: "# Details\n\nThe retention window is 90 days.\n"},
	}}); err != nil {
		t.Fatalf("submit snapshot: %v", err)
	}
	second := compileNow(t, ctx, service, worker, owner, CompileRequest{
		SourceID: source.ID, Mode: BuildModeIncremental,
	})
	if second.Status != BuildStatusSucceeded {
		t.Fatalf("incremental build must succeed: %s", second.Error)
	}

	incrementalCalls := reviewer.calls - callsAfterFull
	if incrementalCalls != 1 {
		t.Fatalf("only the recompiled page should be reviewed, want 1 call, got %d", incrementalCalls)
	}
	joined := strings.Join(reviewer.prompts, "\n")
	if !strings.Contains(joined, "path: wiki/details.md") {
		t.Fatalf("the recompiled page must be reviewed: %q", joined)
	}
	if strings.Contains(joined, "path: wiki/overview.md") {
		t.Fatalf("a reused page must not be reviewed again")
	}

	review, err := service.GetBuildReview(ctx, owner.Account(), second.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	// Coverage is stated against what this build wrote, so "1 of 1" does not pretend the
	// reused pages were re-judged here.
	if review.Build.ReviewPagesTotal != 1 || review.Build.ReviewPagesExamined != 1 {
		t.Fatalf("incremental coverage should be 1 of 1, got %d of %d",
			review.Build.ReviewPagesExamined, review.Build.ReviewPagesTotal)
	}
}

// TestReviewRejectsUnusableFindings: output that cannot be located or categorised is dropped
// rather than coerced. A finding filed under a kind the reviewer did not choose misreports
// what it found, and one without a quoted claim cannot be acted on.
func TestReviewRejectsUnusableFindings(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{byPage: map[string][]map[string]any{
		"wiki/overview.md": {
			{"kind": "made_up_kind", "claim": "some claim", "detail": "unknown category"},
			{"kind": "unsupported", "claim": "", "detail": "no quoted claim"},
			{"kind": "UNSUPPORTED", "claim": "Retention is always 30 days.", "detail": "case is normalised"},
		},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-garbage")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-garbage", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if len(review.Findings) != 1 {
		t.Fatalf("only the usable finding should survive, got %+v", review.Findings)
	}
	if review.Findings[0].Kind != reviewUnsupported {
		t.Fatalf("kind should be normalised to lower case, got %q", review.Findings[0].Kind)
	}
}

// TestReviewBuildRejectsFailedBuild: a failed build has no committed pages, so there is
// nothing whose faithfulness could be judged. Saying so beats returning an empty verdict.
func TestReviewBuildRejectsFailedBuild(t *testing.T) {
	ctx := context.Background()
	// A reply the check gate rejects: a page citing a document that does not exist.
	compiler := &scriptedClient{replies: []string{wikiReply(
		wikiPage("wiki/ghost.md", PageKindConcept, "Ghost", "Body.",
			[]string{"raw/missing.md"}, nil),
	)}}
	reviewer := &scriptedReviewer{}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-failed-build")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-failed-build", "Overview body.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status == BuildStatusSucceeded {
		t.Fatalf("this reply should have failed check, got %s", build.CheckStatus)
	}
	if reviewer.calls != 0 {
		t.Fatalf("a build that never committed must not be reviewed, got %d calls", reviewer.calls)
	}
	if _, err := service.ReviewBuild(ctx, owner, build.ID); err == nil {
		t.Fatalf("reviewing a failed build must be refused")
	}
	if _, err := service.ReviewBuild(ctx, owner, ""); err == nil {
		t.Fatalf("an empty build_id must be rejected")
	}
}

// TestReviewOutcomePrecedence pins the mapping directly, because the rule it encodes — a
// status may never claim more coverage than review achieved — is easy to break by accident
// when a new branch is added.
func TestReviewOutcomePrecedence(t *testing.T) {
	cases := []struct {
		name                                     string
		eligible, attempted, succeeded, failures int
		capped                                   bool
		findings                                 int
		want                                     string
	}{
		{"nothing reviewable", 0, 0, 0, 0, false, 0, ReviewStatusSkipped},
		{"all calls failed", 3, 3, 0, 3, false, 0, ReviewStatusFailed},
		{"findings win over partial", 5, 2, 2, 0, true, 1, ReviewStatusFlagged},
		{"capped without findings", 5, 2, 2, 0, true, 0, ReviewStatusPartial},
		{"some pages failed", 3, 3, 2, 1, false, 0, ReviewStatusPartial},
		{"full and clean", 3, 3, 3, 0, false, 0, ReviewStatusClean},
	}
	for _, testCase := range cases {
		got, note := reviewOutcome(testCase.eligible, testCase.attempted, testCase.succeeded,
			testCase.failures, testCase.capped, "boom", testCase.findings)
		if got != testCase.want {
			t.Fatalf("%s: want %s, got %s (%s)", testCase.name, testCase.want, got, note)
		}
		if note == "" {
			t.Fatalf("%s: every outcome needs a reason", testCase.name)
		}
	}
}

// TestReviewablePagesExcludesGeneratedPages: the index and log make no claims about the
// sources, so faithfulness has nothing to be true of.
func TestReviewablePagesExcludesGeneratedPages(t *testing.T) {
	pages := []WikiPage{
		{Path: "wiki/index.md", Kind: PageKindIndex},
		{Path: "wiki/log.md", Kind: PageKindLog},
		{Path: "wiki/one.md", Kind: PageKindConcept, Citations: []PageCitation{{DocumentPath: "a"}}},
		{Path: "wiki/two.md", Kind: PageKindConcept, Citations: []PageCitation{{DocumentPath: "a"}, {DocumentPath: "b"}}},
	}
	eligible := reviewablePages(pages, nil)
	if len(eligible) != 2 {
		t.Fatalf("want 2 eligible pages, got %d", len(eligible))
	}
	// Most claim-bearing page first, so a capped review spends its budget where there is
	// most to get wrong.
	if eligible[0].Path != "wiki/two.md" {
		t.Fatalf("pages must be ordered by citation count, got %s first", eligible[0].Path)
	}

	restricted := reviewablePages(pages, map[string]struct{}{"wiki/one.md": {}})
	if len(restricted) != 1 || restricted[0].Path != "wiki/one.md" {
		t.Fatalf("restriction to written pages ignored: %+v", restricted)
	}
}

// TestReviewOfStoredBuildLoadsCitations guards a bug that shipped and was caught only by
// running review against a real build.
//
// ListPages omits citations, which is correct for listing endpoints. Reviewing a stored
// build with it handed the reviewer no sources, and a real reviewer then reported every
// claim on those pages as "unsupported" with the reason "no source documents are provided".
// That is a verdict about our loading path wearing the costume of a verdict about the wiki —
// the most damaging false positive review can produce, because it blames the compiler for
// our mistake.
//
// The original re-review test could not catch it: the scripted reviewer answers by page
// path and never looks at whether sources were present.
func TestReviewOfStoredBuildLoadsCitations(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-stored")
	defer cleanup()
	// A phrase that exists only in the raw document.
	source := seedWikiSource(t, ctx, service, owner, "review-stored",
		"Retention is usually 30 days and configurable per tenant.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if build.Status != BuildStatusSucceeded {
		t.Fatalf("build must succeed: %s", build.Error)
	}
	reviewer.prompts = nil

	// The path under test: review a build that is only in the database now.
	response, err := service.ReviewBuild(ctx, owner, build.ID)
	if err != nil {
		t.Fatalf("review stored build: %v", err)
	}

	joined := strings.Join(reviewer.prompts, "\n")
	if strings.Contains(joined, "the cited documents are not present in this revision") {
		t.Fatalf("the reviewer was told there are no sources for a build whose citations resolve")
	}
	if !strings.Contains(joined, "configurable per tenant") {
		t.Fatalf("reviewing a stored build must load the raw source text: %q", joined)
	}
	if response.Build.ReviewStatus != ReviewStatusClean {
		t.Fatalf("want clean, got %s (%s)", response.Build.ReviewStatus, response.Build.ReviewNote)
	}
	// Every content page must have been examined; a page whose citations failed to load
	// would have been counted as a failure instead.
	if response.Build.ReviewPagesExamined != response.Build.ReviewPagesTotal {
		t.Fatalf("pages went unexamined: %d of %d (%s)",
			response.Build.ReviewPagesExamined, response.Build.ReviewPagesTotal, response.Build.ReviewNote)
	}

	// And the pages really did carry citations, so this test fails if the loader silently
	// returns empty citation lists again.
	pages, err := service.repo.ListPagesWithCitations(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("load pages with citations: %v", err)
	}
	withCitations := 0
	for _, page := range pages {
		if len(page.Citations) > 0 {
			withCitations++
		}
	}
	if withCitations < 2 {
		t.Fatalf("expected the content pages to carry citations, got %d", withCitations)
	}
}

// TestReviewRefusesPageWhoseSourcesVanished: when a page cites documents and none can be
// loaded, the page must be counted as unreviewable rather than sent to the reviewer. The
// alternative is a page full of "unsupported" findings that say nothing about the page.
func TestReviewRefusesPageWhoseSourcesVanished(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-nosources")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-nosources", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	pages, err := service.repo.ListPagesWithCitations(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("load pages: %v", err)
	}
	reviewer.calls = 0

	// No documents at all, while the pages still carry citations.
	response, err := service.runReview(ctx, owner, build.ID, build.Model, pages, nil, nil)
	if err != nil {
		t.Fatalf("run review: %v", err)
	}
	if reviewer.calls != 0 {
		t.Fatalf("the reviewer must not be asked to judge a page with no sources, got %d calls", reviewer.calls)
	}
	if response.Build.ReviewStatus != ReviewStatusFailed {
		t.Fatalf("want failed, got %s (%s)", response.Build.ReviewStatus, response.Build.ReviewNote)
	}
	if !strings.Contains(response.Build.ReviewNote, "none could be loaded") {
		t.Fatalf("the note must name the real cause, got %q", response.Build.ReviewNote)
	}
	if len(response.Findings) != 0 {
		t.Fatalf("a review that could not read the sources must not produce findings: %+v", response.Findings)
	}
}

// TestReviewRefusesSameModelAsBuildCompiler: the guarantee has to be enforced against the
// build's own recorded compiler model, not against the configured independence value.
// Independence is derived from base URLs first, so one model served from two endpoints
// classifies as cross_provider and would walk straight past a check that trusted it.
func TestReviewRefusesSameModelAsBuildCompiler(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	// Same model name as the compiler, but the configuration claims cross-provider —
	// exactly what two base URLs serving one model produces.
	reviewer := &scriptedReviewer{model: "scripted-compiler", byPage: map[string][]map[string]any{
		"wiki/overview.md": {{"kind": "unsupported", "claim": "x", "detail": "would appear if it ran"}},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-modelmatch")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-modelmatch", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus != ReviewStatusSkipped {
		t.Fatalf("a reviewer whose model compiled the build must be refused, got %s (%s)",
			review.Build.ReviewStatus, review.Build.ReviewNote)
	}
	if reviewer.calls != 0 {
		t.Fatalf("no call may be made at all, got %d", reviewer.calls)
	}
	if !strings.Contains(review.Build.ReviewNote, "compiled this build") {
		t.Fatalf("the note must say the reviewer compiled this build, got %q", review.Build.ReviewNote)
	}
}

// TestReviewSkippedDoesNotDestroyPriorFindings: a review that could not run must not erase
// the verdict of one that did. Otherwise pointing at an unreachable reviewer silently
// replaces real findings with "no review happened".
func TestReviewSkippedDoesNotDestroyPriorFindings(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{byPage: map[string][]map[string]any{
		"wiki/overview.md": {{"kind": "overstated", "claim": "Retention is always 30 days.", "detail": "source hedges"}},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-preserve")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-preserve", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	first, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if len(first.Findings) != 1 {
		t.Fatalf("want one finding to start with, got %d", len(first.Findings))
	}

	// The reviewer disappears, and a re-review is attempted.
	service.WithLLM(LLMSetup{Compiler: compiler, Independence: llm.IndependenceUnavailable})
	if _, err := service.ReviewBuild(ctx, owner, build.ID); err != nil {
		t.Fatalf("re-review with no reviewer should record a skip, not fail: %v", err)
	}

	after, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review after skip: %v", err)
	}
	if len(after.Findings) != 1 {
		t.Fatalf("a skipped review destroyed real findings: %+v", after.Findings)
	}
	if after.Build.ReviewStatus != ReviewStatusSkipped {
		t.Fatalf("the status should record the skip, got %s", after.Build.ReviewStatus)
	}
}

// TestReviewContractViolationIsNotClean covers the two ways a reply can be parseable and
// still not answer the question. Both used to end up counted as a clean page.
func TestReviewContractViolationIsNotClean(t *testing.T) {
	// An empty array is a real answer: "nothing wrong".
	if output, err := parseReviewOutput(`{"findings":[]}`); err != nil {
		t.Fatalf("an explicit empty findings array is a valid verdict: %v", err)
	} else if output.Findings == nil || len(*output.Findings) != 0 {
		t.Fatalf("empty array should parse to an empty slice, got %+v", output.Findings)
	}
	// A missing key is not an answer at all.
	for _, reply := range []string{`{}`, `{"findings":null}`, "```json\n{}\n```"} {
		if _, err := parseReviewOutput(reply); err == nil {
			t.Fatalf("reply %q has no findings key and must be rejected", reply)
		}
	}
}

// TestReviewAllFindingsUnusableFailsThePage: if the reviewer reported problems and none of
// them survived validation, the page cannot be called clean — that would report the opposite
// of what the reviewer said.
func TestReviewAllFindingsUnusableFailsThePage(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	reviewer := &scriptedReviewer{byPage: map[string][]map[string]any{
		"wiki/overview.md": {
			{"kind": "not_a_kind", "claim": "something", "detail": "unknown category"},
			{"kind": "unsupported", "claim": "", "detail": "no quoted claim"},
		},
	}}
	service, worker := newReviewService(t, ctx, compiler, reviewer, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-unusable")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-unusable", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	review, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review: %v", err)
	}
	if review.Build.ReviewStatus == ReviewStatusClean {
		t.Fatalf("a page whose findings were all unusable must not count as clean")
	}
	if review.Build.ReviewPagesExamined >= review.Build.ReviewPagesTotal {
		t.Fatalf("the unusable page must land in the coverage gap: %d of %d",
			review.Build.ReviewPagesExamined, review.Build.ReviewPagesTotal)
	}
	if !strings.Contains(review.Build.ReviewNote, "none of them usable") {
		t.Fatalf("the note must name the cause, got %q", review.Build.ReviewNote)
	}
}

// TestReviewPromptDeclaresTruncation: an unmarked cut is how a reviewer comes to report a
// supported claim as unsupported — the sentence backing it was simply removed from the input.
func TestReviewPromptDeclaresTruncation(t *testing.T) {
	long := strings.Repeat("supporting sentence. ", 1000)
	page := WikiPage{
		Path: "wiki/x.md", Kind: PageKindConcept, Content: "Body.",
		Citations: []PageCitation{{DocumentPath: "raw/long.md", Claim: "c"}},
	}
	prompt := buildReviewPrompt(page, []KnowledgeDocument{
		{Path: "raw/long.md", ContentSnapshot: long},
	}, 100)
	if !strings.Contains(prompt, "document truncated here") {
		t.Fatalf("a truncated document must be declared as such")
	}
	// And an untruncated document must not carry the marker, or the notice means nothing.
	short := buildReviewPrompt(page, []KnowledgeDocument{
		{Path: "raw/long.md", ContentSnapshot: "short text"},
	}, 100)
	if strings.Contains(short, "document truncated here") {
		t.Fatalf("a complete document must not be marked truncated")
	}
}

// TestReviewProvenanceFollowsTheFindings: a re-review's verdicts must be attributed to the
// reviewer that produced them, not to whichever one was configured when the build was queued.
func TestReviewProvenanceFollowsTheFindings(t *testing.T) {
	ctx := context.Background()
	compiler := &scriptedClient{replies: []string{twoPageReply()}}
	first := &scriptedReviewer{model: "reviewer-one"}
	service, worker := newReviewService(t, ctx, compiler, first, llm.IndependenceCrossProvider)
	pool := integrationPool(t, ctx)
	owner, cleanup := createKnowledgeIntegrationOwner(t, ctx, pool, "review-provenance")
	defer cleanup()
	source := seedWikiSource(t, ctx, service, owner, "review-provenance", "Retention is usually 30 days.")

	build := compileNow(t, ctx, service, worker, owner, CompileRequest{SourceID: source.ID})
	if review, err := service.GetBuildReview(ctx, owner.Account(), build.ID); err != nil {
		t.Fatalf("get review: %v", err)
	} else if review.Build.ReviewerModel != "reviewer-one" {
		t.Fatalf("want reviewer-one, got %q", review.Build.ReviewerModel)
	}

	second := &scriptedReviewer{model: "reviewer-two", byPage: map[string][]map[string]any{
		"wiki/overview.md": {{"kind": "unsupported", "claim": "Retention is always 30 days.", "detail": "not stated"}},
	}}
	service.WithLLM(LLMSetup{
		Compiler: compiler, Reviewer: second, Independence: llm.IndependenceCrossProvider,
	})
	if _, err := service.ReviewBuild(ctx, owner, build.ID); err != nil {
		t.Fatalf("re-review: %v", err)
	}
	after, err := service.GetBuildReview(ctx, owner.Account(), build.ID)
	if err != nil {
		t.Fatalf("get review after rerun: %v", err)
	}
	if after.Build.ReviewerModel != "reviewer-two" {
		t.Fatalf("findings from reviewer-two are attributed to %q", after.Build.ReviewerModel)
	}
	if after.Build.ReviewerPromptVersion != reviewPromptVersion {
		t.Fatalf("prompt version not recorded: %q", after.Build.ReviewerPromptVersion)
	}
}
