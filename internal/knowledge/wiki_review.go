package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/claw-works/agentmate/internal/llm"
	"github.com/claw-works/agentmate/internal/ownership"
)

// ─── K3.8: review ───
//
// review answers exactly one question per page: are its claims faithful to the sources it
// cites. Not "is the page good", not "is the wiki well organised" — faithfulness, because
// that is the question where judging is genuinely easier than producing, which is the only
// reason handing it to a model is defensible at all.
//
// Three hard requirements from the design, and each one is load-bearing:
//
//  1. A heterogeneous model. A model reviewing its own output brings the same priors that
//     produced the error, so it cannot see it. Same-model review is refused outright
//     rather than warned about: a verdict that cannot detect anything is worse than no
//     verdict, because it looks like verification.
//  2. Only the pages this build wrote. Reused pages were already reviewed when they were
//     written, and paying to review them again buys nothing.
//  3. Never blocks. check already decided whether this build may serve. review's verdicts
//     are not reproducible, and giving an irreproducible judgement blocking power means
//     the same build retried twice can land differently.

const reviewPromptVersion = "review-v1"

// Finding kinds. These are the failure modes review is actually asked to look for, rather
// than a generic quality scale a model cannot calibrate.
const (
	// reviewUnsupported: the sources do not say this at all.
	reviewUnsupported = "unsupported"
	// reviewOverstated: a hedge hardened into an absolute — "usually" became "always".
	reviewOverstated = "overstated"
	// reviewFabricatedCausality: a causal link the sources never draw.
	reviewFabricatedCausality = "fabricated_causality"
	// reviewConflated: two sources' conclusions merged into one neither of them states.
	reviewConflated = "conflated"
)

var reviewKinds = map[string]struct{}{
	reviewUnsupported:         {},
	reviewOverstated:          {},
	reviewFabricatedCausality: {},
	reviewConflated:           {},
}

const reviewSystemPrompt = `You verify whether a wiki page is faithful to the source documents it cites.

You are given one wiki page and the source documents it cites. A long document may be cut
short, and a document that was cut is marked as such. Your only job is to find claims on the
page that the sources do not support.

Report a finding only when you can point at a specific claim. Use these kinds:

- "unsupported": the sources do not state this.
- "overstated": the sources hedge ("usually", "in most cases", "may") and the page asserts
  it absolutely, or the page strengthens a quantity or guarantee.
- "fabricated_causality": the page asserts that one thing causes or requires another and
  the sources never draw that link, even if both facts appear separately.
- "conflated": the page merges conclusions from two sources into a statement neither makes.

Rules:
1. Judge only against the source text provided. If the page states something you believe is
   true from your own knowledge but the sources do not support it, that is "unsupported".
   But if a document is marked as truncated, do not report a claim as unsupported merely
   because the visible part does not mention it — the support may be in the part you cannot
   see. Report it only when the visible text contradicts the claim.
2. Do not report style, structure, wording, ordering or completeness. A terse page that is
   accurate has no findings.
3. Do not report a missing citation as a finding; that is checked elsewhere.
4. Quote the claim as it appears on the page, so a reader can find it. Keep "detail" to one
   or two sentences: say what the sources actually state, not how you reasoned. A reply that
   runs past the token limit is discarded whole, so brevity protects the verdict.
5. If every claim is supported, return an empty findings array. An empty result is a normal
   and expected outcome — do not invent findings to appear thorough.

Reply with JSON only:
{"findings":[{"kind":"overstated","claim":"exact text from the page","detail":"what the sources actually say"}]}`

type reviewOutput struct {
	// A pointer so an absent or null "findings" key is distinguishable from an empty array.
	// They mean different things: an empty array is the reviewer saying "nothing wrong",
	// while a missing key is the reviewer ignoring the output contract. Treating the second
	// as the first turns a non-answer into a clean bill of health.
	Findings *[]struct {
		Kind   string `json:"kind"`
		Claim  string `json:"claim"`
		Detail string `json:"detail"`
	} `json:"findings"`
}

// buildReviewPrompt assembles one page and its sources.
//
// The sources are the raw document text, never the excerpt the compiler recorded on the
// citation. Judging the compiler's page against the compiler's own choice of excerpt is
// circular: a page that quotes selectively would be validated by the very selection that
// makes it wrong.
func buildReviewPrompt(page WikiPage, sources []KnowledgeDocument, perDocumentChars int) string {
	var builder strings.Builder
	builder.WriteString("# Wiki page under review\n\n")
	builder.WriteString("path: " + page.Path + "\n")
	if page.Title != "" {
		builder.WriteString("title: " + page.Title + "\n")
	}
	builder.WriteString("\n---\n")
	builder.WriteString(page.Content)
	builder.WriteString("\n---\n\n")

	builder.WriteString("# Source documents this page cites\n\n")
	if len(sources) == 0 {
		// Stated rather than left implicit: with no sources, every claim is unsupported by
		// construction, and a reviewer that does not know why would guess.
		builder.WriteString("(none available — the cited documents are not present in this revision)\n")
	}
	for _, document := range sources {
		builder.WriteString("## " + document.Path + "\n\n")
		text := truncateChunkRunes(document.ContentSnapshot, perDocumentChars)
		builder.WriteString(text)
		if len([]rune(document.ContentSnapshot)) > len([]rune(text)) {
			// Unmarked truncation is how a reviewer comes to report a supported claim as
			// unsupported: the sentence backing it was simply cut off. That failure blames
			// the compiler for our input budget, so the cut is always declared.
			builder.WriteString("\n\n[document truncated here — the rest was not provided]")
		}
		builder.WriteString("\n\n")
	}

	if len(page.Citations) > 0 {
		// The claims the compiler said it was supporting, as a reading order for the
		// reviewer. Deliberately labelled as the compiler's assertion, not as evidence.
		builder.WriteString("# Claims the compiler recorded (its own account, not evidence)\n\n")
		for _, citation := range page.Citations {
			claim := strings.TrimSpace(citation.Claim)
			if claim == "" {
				continue
			}
			builder.WriteString("- " + claim + " [" + citation.DocumentPath + "]\n")
		}
	}
	return builder.String()
}

func parseReviewOutput(content string) (*reviewOutput, error) {
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "```") {
		trimmed = strings.TrimPrefix(trimmed, "```json")
		trimmed = strings.TrimPrefix(trimmed, "```")
		if index := strings.LastIndex(trimmed, "```"); index >= 0 {
			trimmed = trimmed[:index]
		}
		trimmed = strings.TrimSpace(trimmed)
	}
	var output reviewOutput
	if err := json.Unmarshal([]byte(trimmed), &output); err != nil {
		return nil, fmt.Errorf("reviewer returned unparseable output: %w", err)
	}
	if output.Findings == nil {
		return nil, fmt.Errorf("reviewer output has no \"findings\" key; the reply does not answer the question")
	}
	return &output, nil
}

// reviewPageLimit bounds how many pages one build's review will examine.
//
// Without a bound, a large first compile turns into hundreds of reviewer calls. The bound
// is honest rather than hidden: whatever it leaves unexamined is reported in
// review_pages_examined against review_pages_total, and a build that hit it is never
// called "clean".
func (s *Service) reviewPageLimit() int {
	limit := envPositiveInt("AGENTMATE_WIKI_REVIEW_MAX_PAGES", 20)
	if limit > 500 {
		limit = 500
	}
	return limit
}

func (s *Service) reviewPerDocumentChars() int {
	// Smaller than the compiler's budget: review sees one page's sources at a time, not a
	// whole package, and a reviewer that has to skim is a reviewer that misses things.
	return 8000
}

// reviewSkipReason reports why review cannot run against a build compiled by
// compilerModel, or "" when it can.
//
// The model comparison is the actual guarantee, not the configured independence value.
// Independence is derived from base URLs first, so the same model served from two endpoints
// classifies as cross_provider and would sail past a check that trusted it. And when an old
// build is re-reviewed, the configured value describes today's setup while the build was
// compiled by whatever ran back then. Comparing against the build's own recorded model
// closes both holes.
func (s *Service) reviewSkipReason(compilerModel string) string {
	if s.reviewer == nil || !s.reviewer.Configured() {
		return "no reviewer is configured; this build received check only"
	}
	reviewerModel := strings.TrimSpace(s.reviewer.Model())
	if compilerModel != "" && strings.EqualFold(reviewerModel, strings.TrimSpace(compilerModel)) {
		// Refusal, not a warning. A model cannot find the mistakes its own priors
		// produced, so running it would manufacture the appearance of verification.
		return "reviewer model " + reviewerModel +
			" is the model that compiled this build; self-review would collude, so no review ran"
	}
	if s.reviewerIndependence == llm.IndependenceSameModel {
		return "reviewer is the same model as the compiler; self-review would collude, so no review ran"
	}
	return ""
}

// reviewablePages selects what this build wrote and can be judged against sources.
//
// Generated pages are excluded: the index and the log make no claims about the sources, so
// there is nothing for faithfulness to be true of. Reused pages are excluded because they
// were reviewed when they were written.
func reviewablePages(pages []WikiPage, writtenPaths map[string]struct{}) []WikiPage {
	eligible := make([]WikiPage, 0, len(pages))
	for _, page := range pages {
		if page.Kind == PageKindIndex || page.Kind == PageKindLog {
			continue
		}
		if writtenPaths != nil {
			if _, written := writtenPaths[page.Path]; !written {
				continue
			}
		}
		eligible = append(eligible, page)
	}
	// Most claim-bearing pages first, so a capped review spends its budget where there is
	// most to get wrong. Path breaks ties to keep the selection reproducible.
	sort.SliceStable(eligible, func(i, j int) bool {
		if len(eligible[i].Citations) != len(eligible[j].Citations) {
			return len(eligible[i].Citations) > len(eligible[j].Citations)
		}
		return eligible[i].Path < eligible[j].Path
	})
	return eligible
}

// reviewOnePage runs one reviewer call and returns validated findings.
func (s *Service) reviewOnePage(
	ctx context.Context, page WikiPage, documentsByPath map[string]KnowledgeDocument,
) ([]ReviewFinding, llm.Usage, error) {
	seen := make(map[string]struct{}, len(page.Citations))
	sources := make([]KnowledgeDocument, 0, len(page.Citations))
	for _, citation := range page.Citations {
		path := citation.DocumentPath
		if _, done := seen[path]; done {
			continue
		}
		seen[path] = struct{}{}
		if document, ok := documentsByPath[path]; ok {
			sources = append(sources, document)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Path < sources[j].Path })
	if len(page.Citations) > 0 && len(sources) == 0 {
		// The page cites documents and not one of them could be loaded. Asking the
		// reviewer anyway produces "unsupported" for every claim on the page — a verdict
		// about our plumbing wearing the costume of a verdict about the wiki. Fail the
		// page instead, so it lands in the coverage gap where it belongs.
		return nil, llm.Usage{}, fmt.Errorf(
			"page %s cites %d document(s) but none could be loaded from the revision",
			page.Path, len(page.Citations))
	}

	completion, err := s.reviewer.Complete(ctx, []llm.Message{
		{Role: "system", Content: reviewSystemPrompt},
		{Role: "user", Content: buildReviewPrompt(page, sources, s.reviewPerDocumentChars())},
	}, llm.WithJSONObject())
	if err != nil {
		return nil, llm.Usage{}, err
	}
	output, parseErr := parseReviewOutput(completion.Content)
	if parseErr != nil {
		return nil, completion.Usage, parseErr
	}

	returned := *output.Findings
	findings := make([]ReviewFinding, 0, len(returned))
	for _, raw := range returned {
		kind := strings.ToLower(strings.TrimSpace(raw.Kind))
		if _, ok := reviewKinds[kind]; !ok {
			// An unrecognised kind is dropped rather than coerced into a neighbour. A
			// finding filed under a category the reviewer did not choose misreports what
			// it found.
			continue
		}
		claim := strings.TrimSpace(raw.Claim)
		if claim == "" {
			// A finding that cannot be located on the page is not actionable.
			continue
		}
		findings = append(findings, ReviewFinding{
			PagePath: page.Path, Kind: kind,
			Claim:  truncateChunkRunes(claim, 2000),
			Detail: truncateChunkRunes(strings.TrimSpace(raw.Detail), 2000),
		})
	}
	if len(returned) > 0 && len(findings) == 0 {
		// The reviewer reported problems and not one of them was usable. Counting the page
		// as reviewed-and-clean would report the opposite of what it said.
		return nil, completion.Usage, fmt.Errorf(
			"reviewer returned %d finding(s) for %s, none of them usable", len(returned), page.Path)
	}
	return findings, completion.Usage, nil
}

// runReview reviews one build and records the outcome. It never returns an error that
// should fail the build: every outcome, including total failure, is recorded on the build
// row and the build keeps whatever status check gave it.
//
// writtenPaths limits review to the pages this build actually wrote; nil means all content
// pages (a full compile).
func (s *Service) runReview(
	ctx context.Context, owner ownership.Owner, buildID, compilerModel string,
	pages []WikiPage, writtenPaths map[string]struct{}, documents []KnowledgeDocument,
) (*ReviewResponse, error) {
	if reason := s.reviewSkipReason(compilerModel); reason != "" {
		return s.repo.RecordReviewResult(ctx, owner, recordReviewInput{
			BuildID: buildID, Status: ReviewStatusSkipped, Note: reason,
		})
	}

	// A total budget for the whole review. The per-call HTTP timeout alone does not bound
	// this loop: twenty serial calls at two minutes each is forty minutes of spending that
	// ignores a shutdown, and the automatic path deliberately detaches from the job's
	// context so its result survives cancellation. Detaching from cancellation must not
	// mean detaching from all limits.
	budget := time.Duration(envPositiveInt("AGENTMATE_WIKI_REVIEW_BUDGET_SECONDS", 900)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	documentsByPath := make(map[string]KnowledgeDocument, len(documents))
	for _, document := range documents {
		documentsByPath[document.Path] = document
	}

	eligible := reviewablePages(pages, writtenPaths)
	limit := s.reviewPageLimit()
	examined := eligible
	capped := false
	if len(examined) > limit {
		examined = examined[:limit]
		capped = true
	}

	findings := make([]ReviewFinding, 0)
	var usage llm.Usage
	var failures int
	var firstFailure string
	for _, page := range examined {
		pageFindings, pageUsage, err := s.reviewOnePage(ctx, page, documentsByPath)
		usage.PromptTokens += pageUsage.PromptTokens
		usage.CompletionTokens += pageUsage.CompletionTokens
		usage.TotalTokens += pageUsage.TotalTokens
		// Recorded now, not at the end: this call cost money whether or not the loop
		// finishes, and a process that dies partway must not take the bill with it.
		if pageUsage.TotalTokens > 0 {
			_ = s.repo.RecordReviewUsage(detachedContext(ctx), owner.Account(), buildID,
				pageUsage.TotalTokens, s.reviewerPricing.Cost(pageUsage))
		}
		if err != nil {
			// One page failing does not abandon the rest: partial faithfulness
			// information is worth more than none, as long as the gap is reported.
			failures++
			if firstFailure == "" {
				firstFailure = err.Error()
			}
			continue
		}
		findings = append(findings, pageFindings...)
	}

	succeeded := len(examined) - failures
	status, note := reviewOutcome(len(eligible), len(examined), succeeded, failures, capped, firstFailure, len(findings))

	// Tokens are zero here because each page already recorded its own spend. Passing the
	// total again would bill it twice.
	return s.repo.RecordReviewResult(detachedContext(ctx), owner, recordReviewInput{
		BuildID:               buildID,
		Status:                status,
		Note:                  note,
		ReviewerModel:         s.reviewer.Model(),
		ReviewerPromptVersion: reviewPromptVersion,
		ReviewerIndependence:  s.reviewerIndependence,
		PagesExamined:         succeeded,
		PagesTotal:            len(eligible),
		Findings:              findings,
	})
}

// reviewOutcome maps what happened onto a status and a human-readable note.
//
// Precedence is failed > flagged > partial > clean, and the ordering encodes a rule: a
// status may never claim more coverage than review achieved. "clean" is reserved for the
// case where every eligible page was examined and nothing was found; anything less is
// "partial", because calling it clean would be a statement about pages nobody read.
func reviewOutcome(eligible, attempted, succeeded, failures int, capped bool, firstFailure string, findingCount int) (string, string) {
	switch {
	case eligible == 0:
		return ReviewStatusSkipped, "this build wrote no reviewable pages"
	case succeeded == 0:
		note := fmt.Sprintf("all %d reviewer calls failed", attempted)
		if firstFailure != "" {
			note += ": " + firstFailure
		}
		return ReviewStatusFailed, note
	case findingCount > 0:
		note := fmt.Sprintf("%d finding(s) across %d of %d pages", findingCount, succeeded, eligible)
		if failures > 0 {
			note += fmt.Sprintf("; %d page(s) could not be reviewed", failures)
			if firstFailure != "" {
				note += " (" + firstFailure + ")"
			}
		}
		return ReviewStatusFlagged, note
	case succeeded < eligible:
		note := fmt.Sprintf("examined %d of %d pages, no findings among them", succeeded, eligible)
		if capped {
			note += fmt.Sprintf("; capped at %d pages per build", attempted)
		}
		if failures > 0 {
			note += fmt.Sprintf("; %d page(s) could not be reviewed", failures)
			if firstFailure != "" {
				// Naming the cause is the difference between a note someone can act on and
				// one that only says work is missing. Review is not retried internally:
				// re-review is a single call away, and silently doubling the worst-case
				// bill to paper over a provider blip is not a trade to make on the
				// caller's behalf without telling them.
				note += " (" + firstFailure + ")"
			}
		}
		note += " — the unexamined pages carry no verdict"
		return ReviewStatusPartial, note
	default:
		return ReviewStatusClean, fmt.Sprintf("all %d pages examined, no findings", succeeded)
	}
}

// ReviewBuild reviews, or re-reviews, an already committed build.
//
// Needed for two situations that will keep happening: a reviewer that was unreachable when
// the build ran, and builds compiled before review existed. Re-review replaces the previous
// findings for the build rather than accumulating them, because two sets of verdicts on one
// immutable build cannot both be current.
func (s *Service) ReviewBuild(ctx context.Context, owner ownership.Owner, buildID string) (*ReviewResponse, error) {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return nil, fmt.Errorf("build_id required")
	}
	build, err := s.repo.GetBuild(ctx, owner.Account(), buildID)
	if err != nil {
		return nil, err
	}
	if build.Status != BuildStatusSucceeded {
		// A failed build has no committed pages, so there is nothing whose faithfulness
		// could be judged.
		return nil, fmt.Errorf("build %s is %s; only a succeeded build has pages to review", shortID(build.ID), build.Status)
	}

	// Citations must come with the pages. Without them the reviewer receives no sources
	// and dutifully reports every claim as unsupported, which blames the compiler for our
	// own loading mistake.
	pageList, err := s.repo.ListPagesWithCitations(ctx, owner.Account(), build.ID)
	if err != nil {
		return nil, err
	}
	documents, err := s.repo.ListRevisionIndexableDocuments(ctx, owner.Account(), build.SourceRevisionID)
	if err != nil {
		return nil, err
	}
	// Every content page, not just the ones this build wrote. The automatic path reviews
	// only what was compiled, because re-judging unchanged text against unchanged sources
	// buys nothing. A caller explicitly asking to review a build wants a verdict on the
	// wiki as it stands, and on an incremental build the reused pages are part of what that
	// build serves — they were copied into it, with derived_from_build_id recording where
	// they came from. The coverage numbers are therefore against all content pages, and the
	// note says so, so "17/18" cannot be misread as "17 of the 18 pages this build wrote".
	return s.runReview(ctx, owner, build.ID, build.Model, pageList, nil, documents)
}

// GetBuildReview returns the recorded verdict and its findings.
func (s *Service) GetBuildReview(ctx context.Context, accountID, buildID string) (*ReviewResponse, error) {
	build, err := s.repo.GetBuild(ctx, accountID, strings.TrimSpace(buildID))
	if err != nil {
		return nil, err
	}
	findings, err := s.repo.ListReviewFindings(ctx, accountID, build.ID)
	if err != nil {
		return nil, err
	}
	return &ReviewResponse{Build: *build, Findings: findings}, nil
}
