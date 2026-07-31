package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/claw-works/agentmate/internal/llm"
	"github.com/claw-works/agentmate/internal/ownership"
)

// ErrCompilerUnavailable separates "the operator has not configured a model" from
// "this build failed". The former is not a defect in the sources and must not
// leave a failed build behind implying otherwise.
var ErrCompilerUnavailable = errors.New("wiki compiler is not configured")

// EnqueueCompile validates the request, resolves the inputs, and queues a build.
//
// It does not compile. A synchronous compile was measured at 200-400 seconds
// against a reasoning model, which is beyond any sane HTTP client default timeout,
// and a caller that gives up loses the work. Raising timeouts does not fix that —
// it only moves the point at which the connection breaks.
//
// Everything cheap and deterministic still happens here: a job that cannot
// possibly succeed is rejected now, while a caller is present to be told why,
// rather than queued and failed minutes later where nobody is looking.
func (s *Service) EnqueueCompile(ctx context.Context, owner ownership.Owner, req CompileRequest) (*EnqueueCompileResponse, error) {
	if s.compiler == nil || !s.compiler.Configured() {
		return nil, ErrCompilerUnavailable
	}
	req.SourceID = strings.TrimSpace(req.SourceID)
	if req.SourceID == "" {
		return nil, fmt.Errorf("source_id required")
	}
	req.Mode = strings.TrimSpace(strings.ToLower(req.Mode))
	if req.Mode == "" {
		req.Mode = BuildModeFull
	}
	if req.Mode != BuildModeFull && req.Mode != BuildModeIncremental {
		return nil, fmt.Errorf("mode must be full or incremental")
	}

	source, err := s.repo.GetSource(ctx, owner.Account(), req.SourceID)
	if err != nil {
		return nil, err
	}
	if source.ActiveRevisionID == nil {
		return nil, fmt.Errorf("source has no active revision; sync it first")
	}
	revisionID := *source.ActiveRevisionID
	revision, err := s.repo.GetRevision(ctx, owner.Account(), revisionID)
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	_ = json.Unmarshal(revision.Manifest, &manifest)

	profile, err := s.repo.EnsureProfileVersion(ctx, owner, manifest.Profile, manifest.Description)
	if err != nil {
		return nil, err
	}

	documentCount, err := s.repo.CountRevisionIndexableDocuments(ctx, owner.Account(), revisionID)
	if err != nil {
		return nil, err
	}
	if documentCount == 0 {
		return nil, fmt.Errorf("revision has no indexable documents to compile")
	}

	response := &EnqueueCompileResponse{Warnings: s.reviewWarnings()}

	input := createBuildInput{
		SourceID:             source.ID,
		SourceRevisionID:     revisionID,
		RawPackageHash:       revision.PackageHash,
		ProfileVersionID:     profile.ID,
		Model:                s.compiler.Model(),
		ReviewerModel:        s.reviewerModel(),
		ReviewerIndependence: s.reviewerIndependence,
		Mode:                 req.Mode,
		ActivateOnSuccess:    req.Activate == nil || *req.Activate,
	}

	if req.Mode == BuildModeIncremental {
		// The parent is a genuine input here, not lineage decoration: what gets
		// recompiled is defined relative to it, so two incremental builds off
		// different parents are different builds and must not share an identity.
		parent, err := s.repo.PreviousSucceededBuild(ctx, owner.Account(), source.ID, "")
		if err != nil {
			return nil, err
		}
		if parent == nil {
			// Refused rather than downgraded. A caller that asked for incremental and
			// silently received a full rebuild believes it saved cost it did not save,
			// and would draw the wrong conclusion about what incremental costs.
			return nil, fmt.Errorf("%w: compile with mode=full first", ErrNoParentBuild)
		}
		input.ParentBuildID = &parent.ID

		// A parent from a different compiler identity cannot be incrementally updated:
		// the raw diff would be empty while every page still needs rewriting. Caught here
		// as well as in the worker, so the caller learns immediately instead of after a
		// build has been queued and failed.
		if parent.Model != input.Model || parent.CompilerVersion != CompilerVersion ||
			parent.PromptVersion != PromptVersion || parent.ProfileVersionID != input.ProfileVersionID {
			return nil, fmt.Errorf("%w: build %s used model %q, prompt %q, compiler %q; compile with mode=full",
				ErrIncompatibleParent, shortID(parent.ID), parent.Model, parent.PromptVersion, parent.CompilerVersion)
		}

		// Nothing to do: the parent already compiled this exact revision under the
		// same profile, compiler and prompt, and revisions are immutable, so the raw
		// sources cannot have moved.
		//
		// This has to be caught here rather than left to the generic identity lookup.
		// Each incremental build becomes the parent of the next one, so identity never
		// repeats and an idle caller polling "bring the wiki up to date" would mint an
		// unbounded chain of builds that each reused everything and compiled nothing.
		if !req.Force && s.parentAlreadyCoversRevision(parent, input) {
			activated, activateErr := s.maybeActivate(ctx, owner, parent, req.Activate)
			response.Build = parent
			response.Reused = true
			response.Activated = activated
			response.Warnings = append(response.Warnings,
				"sources have not changed since build "+shortID(parent.ID)+" compiled them; nothing to update")
			if activateErr != nil {
				response.Warnings = append(response.Warnings, "activation failed: "+activateErr.Error())
			}
			return response, nil
		}
	}

	// Input identity, not content identity: LLM output is not reproducible, so a
	// content hash can neither confirm nor deny that a rebuild is needed.
	if !req.Force {
		existing, err := s.repo.FindBuildByInputIdentity(ctx, owner.Account(), input)
		if err != nil {
			return nil, err
		}
		if existing != nil {
			activated, activateErr := s.maybeActivate(ctx, owner, existing, req.Activate)
			response.Build = existing
			response.Reused = true
			response.Activated = activated
			if activateErr != nil {
				response.Warnings = append(response.Warnings, "activation failed: "+activateErr.Error())
			}
			return response, nil
		}
	}

	build, err := s.repo.CreateBuild(ctx, owner, input)
	if err != nil {
		return nil, err
	}
	response.Build = build
	// Queue depth is returned with the receipt because "queued" alone does not
	// tell the caller whether to expect four minutes or an hour.
	if stats, statsErr := s.repo.QueueDepth(ctx, owner.Account()); statsErr == nil {
		response.Queue = stats
	}
	return response, nil
}

// RunBuild compiles one claimed build. It is the worker side of the queue and
// assumes the caller holds the lease.
//
// Returning an error means the attempt failed in a way the worker must classify;
// returning nil means the build reached a terminal state, successful or not. A
// check failure is *not* an error here: the build is legitimately finished, just
// rejected, and retrying it would be an attempt to get past the gate by repetition.
func (s *Service) RunBuild(ctx context.Context, build *BuildRevision) error {
	if s.compiler == nil || !s.compiler.Configured() {
		return ErrCompilerUnavailable
	}
	owner := ownership.Owner{AccountID: build.AccountID, KeyID: build.KeyID}
	if build.UserID != nil {
		owner.UserID = *build.UserID
	}

	revision, err := s.repo.GetRevision(ctx, owner.Account(), build.SourceRevisionID)
	if err != nil {
		return err
	}
	var manifest Manifest
	_ = json.Unmarshal(revision.Manifest, &manifest)

	profile, err := s.repo.GetProfileVersion(ctx, owner.Account(), build.ProfileVersionID)
	if err != nil {
		return err
	}

	documents, err := s.repo.ListRevisionIndexableDocuments(ctx, owner.Account(), build.SourceRevisionID)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		// The revision lost its documents between enqueue and run. Terminal: no
		// number of retries will bring them back.
		_, finishErr := s.finishBuild(ctx, owner.Account(), build.ID,
			BuildStatusFailed, CheckStatusPending, nil, "revision has no indexable documents to compile")
		return finishErr
	}

	sequence := 0
	events := make([]BuildEvent, 0, 8)
	addEvent := func(eventType, pagePath, detail string) {
		sequence++
		events = append(events, BuildEvent{
			SequenceNo: sequence, EventType: eventType, PagePath: pagePath,
			Detail: detail, OccurredAt: time.Now().UTC(),
		})
	}
	started := fmt.Sprintf("%s compile of revision %s with %s", build.Mode, revision.RevisionKey, build.Model)
	if build.Attempt > 1 {
		started += fmt.Sprintf(" (attempt %d of %d)", build.Attempt, build.MaxAttempts)
	}
	addEvent(BuildEventStarted, "", started)
	addEvent(BuildEventSourceRead, "", fmt.Sprintf("%d indexable documents", len(documents)))

	var (
		pages       []WikiPage
		rejected    []string
		usage       llm.Usage
		reusedCount int
		plan        *IncrementalPlan
	)
	if build.Mode == BuildModeIncremental {
		pages, rejected, usage, reusedCount, plan, err = s.runIncremental(
			ctx, owner.Account(), build, *profile, manifest, documents, addEvent)
		if errors.Is(err, ErrNoParentBuild) || errors.Is(err, ErrIncompatibleParent) {
			// Terminal, and not a downgrade to full: a caller that asked for
			// incremental and silently got a full rebuild believes it saved cost it
			// did not save.
			_, finishErr := s.finishBuild(ctx, owner.Account(), build.ID,
				BuildStatusFailed, CheckStatusPending, nil, err.Error())
			return finishErr
		}
	} else {
		pages, rejected, usage, err = s.compileWithModel(ctx, *profile, manifest, documents)
	}
	if err != nil {
		// Handed back to the worker to classify as retryable or terminal. Cost
		// already incurred is recorded either way: a failed attempt still spent
		// money, and hiding that makes the bill unexplainable.
		if usage.TotalTokens > 0 {
			_ = s.repo.RecordAttemptUsage(ctx, owner.Account(), build.ID,
				usage.PromptTokens, usage.CompletionTokens, s.costMicros(usage))
		}
		return err
	}
	for _, path := range rejected {
		addEvent(BuildEventPageRejected, path, "path is reserved for a platform-generated page")
	}
	if build.Mode == BuildModeIncremental {
		// runIncremental already emitted per-page events, distinguishing written from
		// reused from deleted. Re-emitting page_written here would claim the compiler
		// produced pages it merely copied.
		if plan != nil {
			if encoded, marshalErr := json.Marshal(plan); marshalErr == nil {
				for index := range events {
					if events[index].EventType == BuildEventPlanned {
						events[index].Payload = encoded
					}
				}
			}
		}
	} else {
		for _, page := range pages {
			addEvent(BuildEventPageWritten, page.Path, fmt.Sprintf("%s, %d citations, %d links",
				page.Kind, len(page.Citations), len(page.Links)))
		}
	}

	// index must come after every content page exists, since it links all of them.
	contentPageCount := len(pages)
	pages = append(pages, buildIndexPage(manifest, pages))
	if build.Mode == BuildModeIncremental {
		addEvent(BuildEventFinished, "", fmt.Sprintf(
			"%d content pages (%d reused from the parent build), plus the generated index and this log",
			contentPageCount, reusedCount))
	} else {
		addEvent(BuildEventFinished, "", fmt.Sprintf(
			"%d content pages, plus the generated index and this log", contentPageCount))
	}
	// log is generated last and describes everything before it, so it cannot
	// describe its own writing.
	pages = append(pages, buildLogPage(events))

	knownPaths := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		knownPaths[document.Path] = struct{}{}
	}
	parentPageCount := 0
	// The previous succeeded build is the drift baseline. It is deliberately not
	// recorded as parent_build_id on a full build: parent takes part in the input
	// identity, so stamping the latest build as parent would make every identity
	// unique and reuse would never hit.
	if parent, parentErr := s.repo.PreviousSucceededBuild(ctx, owner.Account(), build.SourceID, build.ID); parentErr == nil && parent != nil {
		if count, countErr := s.repo.CountPages(ctx, owner.Account(), parent.ID); countErr == nil {
			parentPageCount = count
		}
	}

	failures := runChecks(checkInput{
		Profile:            *profile,
		Pages:              pages,
		KnownDocumentPaths: knownPaths,
		ParentPageCount:    parentPageCount,
		TotalTokens:        usage.TotalTokens,
		// Only set for an incremental build, where check additionally verifies that the
		// build honoured its own plan.
		Incremental: plan,
	})

	if len(failures) > 0 {
		// check is the gate: nothing is written when it fails, so a build that
		// violated its invariants leaves no half-visible wiki behind. This is
		// terminal, not retryable — recompiling until the dice fall right would be
		// relaxing the gate by repetition.
		_, finishErr := s.finishBuild(ctx, owner.Account(), build.ID,
			BuildStatusFailed, CheckStatusFailed, failures,
			fmt.Sprintf("%d check failures", len(failures)))
		return finishErr
	}

	succeeded, err := s.repo.CommitBuild(ctx, owner, build.ID, pages, events, buildUsage{
		PagesWritten: len(pages),
		PagesReused:  reusedCount,
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		CostMicros:   s.costMicros(usage),
		LeaseOwner:   build.LeaseOwner,
	})
	if err != nil {
		if errors.Is(err, ErrLeaseLost) {
			// Another worker owns this build now. Its output wins; this one is
			// discarded without touching the row.
			return err
		}
		_, finishErr := s.finishBuild(ctx, owner.Account(), build.ID,
			BuildStatusFailed, CheckStatusPassed, nil, "commit failed: "+err.Error())
		if finishErr != nil {
			return fmt.Errorf("commit failed (%v) and the build could not be marked failed: %w", err, finishErr)
		}
		return nil
	}

	if build.ActivateOnSuccess {
		if _, _, activateErr := s.repo.ActivateBuild(ctx, owner.Account(), succeeded.ID); activateErr != nil {
			// The wiki is committed and valid; only the pointer move failed. Not an
			// attempt failure — retrying would recompile a wiki that already exists.
			return nil
		}
	}

	// K3.8 review runs last, after the build is committed and possibly already serving.
	//
	// That ordering is forced by review not being a gate: if it ran before activation,
	// activation would wait on it, and a slow or unreachable reviewer would delay a wiki
	// that check already cleared. Its verdict annotates what is serving, which is the same
	// position lint occupies.
	//
	// Only the pages this build wrote are reviewed. On an incremental build the reused
	// pages were reviewed when they were written, and paying to re-judge unchanged text
	// against unchanged sources buys nothing.
	//
	// A detached context is used deliberately: review writes what it learned, and the
	// moment that write matters most is when the job's context has just died.
	if _, reviewErr := s.runReview(detachedContext(ctx), owner, succeeded.ID, build.Model,
		pages, writtenPagePaths(pages, plan), documents); reviewErr != nil {
		// Recording the verdict failed, which is not the build's problem: the wiki is
		// committed and valid. review_status stays whatever it was, and a caller can ask
		// for a re-review. Failing the build here would let an advisory step undo a
		// compile that passed the only gate there is.
		//
		// Logged rather than swallowed: without this line a review that never recorded
		// anything is indistinguishable from one that was never attempted, and the
		// build's review_status would be the only clue.
		log.Printf("wiki review of build %s could not be recorded: %v", shortID(succeeded.ID), reviewErr)
		return nil
	}
	return nil
}

// writtenPagePaths returns the paths this build actually compiled, or nil when that is
// every content page.
//
// nil rather than a full set on purpose: nil means "no restriction", and building a set
// containing everything would make the reviewer's page selection depend on a set that says
// nothing.
func writtenPagePaths(pages []WikiPage, plan *IncrementalPlan) map[string]struct{} {
	if plan == nil {
		return nil
	}
	written := make(map[string]struct{}, len(plan.RecompiledPaths))
	for _, path := range plan.RecompiledPaths {
		written[path] = struct{}{}
	}
	return written
}

// costMicros prices one compilation.
//
// Zero when no price is configured. An invented default would put a number in the
// accounting that looks authoritative and is wrong; zero is visibly unknown.
func (s *Service) costMicros(usage llm.Usage) int64 {
	return s.compilerPricing.Cost(usage)
}

// reviewWarnings states what review can and cannot contribute to a build.
//
// The rule this function exists to obey: improving the configuration must never remove
// information about what was actually verified. Before K3.8 landed, the first warning was
// unconditional because review never ran at all — so configuring a genuinely cross-provider
// reviewer would have made the only warning disappear while nothing had changed. Now review
// does run, and each warning below is conditional on a real limitation.
//
// These describe configuration, which is knowable before a build starts. What actually
// happened to a specific build is on the build row: review_status with
// review_pages_examined, review_pages_total and review_note.
func (s *Service) reviewWarnings() []string {
	warnings := make([]string, 0, 2)
	if reason := s.reviewSkipReason(""); reason != "" {
		warnings = append(warnings, reason+
			"; check is the only verification these builds receive")
		return warnings
	}
	if s.reviewerIndependence == llm.IndependenceSameProvider {
		// Not refused, unlike same_model: a different model from one supplier does have
		// different failure modes. But training corpora overlap heavily, so the priors
		// overlap too, and a reviewer drawing on the same data misses the same things.
		// Reduced correlation is not impartiality.
		warnings = append(warnings,
			"reviewer independence is "+s.reviewerIndependence+
				"; its verdicts share some of the compiler's blind spots")
	}
	// Coverage is capped, and a caller comparing review_status across builds needs to know
	// that "clean" is bounded by it before reading too much into a clean result.
	warnings = append(warnings, fmt.Sprintf(
		"review examines at most %d pages per build, most-cited first; review_pages_examined "+
			"against review_pages_total says how much of a build was actually judged",
		s.reviewPageLimit()))
	return warnings
}

// parentAlreadyCoversRevision reports whether a parent build already produced the
// wiki for exactly these inputs, making an incremental update a no-op.
//
// Source revisions are immutable, so an identical revision ID means identical
// documents — there is no diff to compute. The compiler and prompt versions are part
// of the comparison because changing either changes the output even from unchanged
// sources, and a caller asking for an update after a compiler upgrade should get one.
func (s *Service) parentAlreadyCoversRevision(parent *BuildRevision, in createBuildInput) bool {
	return parent.SourceRevisionID == in.SourceRevisionID &&
		parent.ProfileVersionID == in.ProfileVersionID &&
		parent.Model == in.Model &&
		parent.CompilerVersion == CompilerVersion &&
		parent.PromptVersion == PromptVersion
}

// maybeActivate moves the active pointer unless the caller opted out.
func (s *Service) maybeActivate(ctx context.Context, owner ownership.Owner, build *BuildRevision, activate *bool) (bool, error) {
	if activate != nil && !*activate {
		return false, nil
	}
	if build.Status != BuildStatusSucceeded {
		return false, nil
	}
	if _, _, err := s.repo.ActivateBuild(ctx, owner.Account(), build.ID); err != nil {
		return false, err
	}
	build.IsActive = true
	return true, nil
}

// ActivateBuild is also the rollback path: activating an older build reverts the
// wiki. There is no separate rollback operation because there is no separate
// concept — the active wiki is a pointer.
func (s *Service) ActivateBuild(ctx context.Context, owner ownership.Owner, buildID string) (*ActivateBuildResponse, error) {
	buildID = strings.TrimSpace(buildID)
	if buildID == "" {
		return nil, fmt.Errorf("build id required")
	}
	build, previous, err := s.repo.ActivateBuild(ctx, owner.Account(), buildID)
	if err != nil {
		return nil, err
	}
	return &ActivateBuildResponse{Build: build, PreviousBuildID: previous}, nil
}

func (s *Service) GetBuild(ctx context.Context, accountID, buildID string) (*BuildRevision, error) {
	return s.repo.GetBuild(ctx, accountID, buildID)
}

func (s *Service) ListBuilds(ctx context.Context, accountID, sourceID string, limit, offset int) (*BuildListResponse, error) {
	items, total, err := s.repo.ListBuilds(ctx, accountID, sourceID, limit, offset)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return &BuildListResponse{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

func (s *Service) QueueStats(ctx context.Context, accountID string) (*QueueStats, error) {
	return s.repo.QueueDepth(ctx, accountID)
}

func (s *Service) ListPages(ctx context.Context, accountID, buildID string) (*PageListResponse, error) {
	items, err := s.repo.ListPages(ctx, accountID, buildID, false)
	if err != nil {
		return nil, err
	}
	return &PageListResponse{BuildID: buildID, Items: items, Total: len(items)}, nil
}

func (s *Service) GetPage(ctx context.Context, accountID, buildID, path string) (*WikiPage, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("page path required")
	}
	return s.repo.GetPage(ctx, accountID, buildID, path)
}

// DiffBuilds answers "why did this page change" without re-running anything,
// which matters because a build cannot be reproduced.
func (s *Service) DiffBuilds(ctx context.Context, accountID, fromBuildID, toBuildID string) (*BuildDiff, error) {
	fromBuildID = strings.TrimSpace(fromBuildID)
	toBuildID = strings.TrimSpace(toBuildID)
	if toBuildID == "" {
		return nil, fmt.Errorf("to build id required")
	}
	target, err := s.repo.GetBuild(ctx, accountID, toBuildID)
	if err != nil {
		return nil, err
	}
	if fromBuildID == "" {
		// Default to the previous succeeded build of the same source: comparing
		// against the immediate predecessor is the common question.
		previous, err := s.repo.PreviousSucceededBuild(ctx, accountID, target.SourceID, toBuildID)
		if err != nil {
			return nil, err
		}
		if previous == nil {
			return nil, fmt.Errorf("no earlier build to compare against")
		}
		fromBuildID = previous.ID
	} else if _, err := s.repo.GetBuild(ctx, accountID, fromBuildID); err != nil {
		return nil, err
	}
	return s.repo.DiffBuilds(ctx, accountID, fromBuildID, toBuildID)
}

func (s *Service) ListBuildEvents(ctx context.Context, accountID, buildID string) ([]BuildEvent, error) {
	return s.repo.ListBuildEvents(ctx, accountID, buildID)
}

func (s *Service) reviewerModel() string {
	if s.reviewer == nil || !s.reviewer.Configured() {
		return ""
	}
	return s.reviewer.Model()
}

func pagePaths(pages []WikiPage) []string {
	paths := make([]string, 0, len(pages))
	for _, page := range pages {
		paths = append(paths, page.Path)
	}
	return paths
}

// finishBuild writes a build's terminal state on a context that keeps the
// request's values but drops its cancellation.
//
// That write must survive the caller hanging up. With the queue in place the
// caller is a worker rather than an HTTP request, but the same reasoning applies
// during shutdown: the context is cancelled precisely when the build most needs
// its outcome recorded, and a record stuck at `running` lies indefinitely.
func (s *Service) finishBuild(
	ctx context.Context, accountID, buildID, status, checkStatus string,
	failures []CheckFailure, buildError string,
) (*BuildRevision, error) {
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.repo.FinishBuild(finishCtx, accountID, buildID, status, checkStatus, failures, buildError)
}
