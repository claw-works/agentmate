package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wellxie/agentmate/internal/llm"
	"github.com/wellxie/agentmate/internal/ownership"
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
	if req.Mode == BuildModeIncremental {
		// Rejected rather than silently downgraded to a full build: a caller that
		// asked for incremental would otherwise believe it saved cost it did not.
		return nil, fmt.Errorf("incremental compilation is not implemented yet; use mode=full")
	}
	if req.Mode != BuildModeFull {
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

	response := &EnqueueCompileResponse{Warnings: s.reviewWarnings()}

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
	started := fmt.Sprintf("full compile of revision %s with %s", revision.RevisionKey, build.Model)
	if build.Attempt > 1 {
		started += fmt.Sprintf(" (attempt %d of %d)", build.Attempt, build.MaxAttempts)
	}
	addEvent(BuildEventStarted, "", started)
	addEvent(BuildEventSourceRead, "", fmt.Sprintf("%d indexable documents", len(documents)))

	pages, rejected, usage, err := s.compileWithModel(ctx, *profile, manifest, documents)
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
	for _, page := range pages {
		addEvent(BuildEventPageWritten, page.Path, fmt.Sprintf("%s, %d citations, %d links",
			page.Kind, len(page.Citations), len(page.Links)))
	}

	// index must come after every content page exists, since it links all of them.
	contentPageCount := len(pages)
	pages = append(pages, buildIndexPage(manifest, pages))
	addEvent(BuildEventFinished, "", fmt.Sprintf(
		"%d content pages, plus the generated index and this log", contentPageCount))
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
	return nil
}

// costMicros prices one compilation.
//
// Zero when no price is configured. An invented default would put a number in the
// accounting that looks authoritative and is wrong; zero is visibly unknown.
func (s *Service) costMicros(usage llm.Usage) int64 {
	return s.compilerPricing.Cost(usage)
}

// reviewWarnings states what review did and did not contribute to a build.
//
// The first warning is unconditional while review is unimplemented, and that is
// the point: it used to be the same_provider warning that told an operator not to
// lean on review, so configuring a genuinely cross-provider reviewer would have
// made the only signal disappear while review still never ran. An improvement to
// the configuration must not quietly remove information about what was verified.
//
// The independence warning stays for the case where a reviewer shares the
// compiler's priors, because a reviewer drawing on the same training data misses
// the same things — reduced correlation is not impartiality.
func (s *Service) reviewWarnings() []string {
	warnings := make([]string, 0, 2)
	// K3.8 has not landed: RunBuild never calls s.reviewer, so review_status stays
	// skipped on every build. Say so where the result is consumed rather than only
	// in the design document.
	warnings = append(warnings,
		"review is not implemented yet: review_status stays \"skipped\" and no faithfulness check ran on this build; "+
			"check is the only verification it received")
	if s.reviewerIndependence == llm.IndependenceSameProvider || s.reviewerIndependence == llm.IndependenceSameModel {
		warnings = append(warnings,
			"reviewer independence is "+s.reviewerIndependence+
				"; once review runs, its verdicts will share the compiler's blind spots")
	}
	return warnings
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
