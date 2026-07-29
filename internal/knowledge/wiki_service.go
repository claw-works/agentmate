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

// Compile builds the wiki for one source's active raw revision.
//
// The sequence is: resolve inputs, reuse if an identical build exists, compile,
// generate index and log, run check, commit, then activate if check passed.
//
// Activation is automatic by design (K3 §2.3): the knowledge base belongs to the
// user while the quality standard belongs to the platform, so asking a SaaS user
// to approve compiler output is an identity mismatch that yields either rubber
// stamping or a wiki that never updates. check is what makes automatic activation
// safe, which is why the two ship together.
func (s *Service) Compile(ctx context.Context, owner ownership.Owner, req CompileRequest) (*CompileResponse, error) {
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

	documents, err := s.repo.ListRevisionIndexableDocuments(ctx, owner.Account(), revisionID)
	if err != nil {
		return nil, err
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("revision has no indexable documents to compile")
	}

	// The previous succeeded build is the drift baseline for check. It is
	// deliberately *not* recorded as parent_build_id on a full build: parent takes
	// part in the input identity, so stamping the latest build as parent would
	// make every identity unique and reuse would never hit. Lineage for full
	// builds is recoverable from source plus time order; parent_build_id is
	// reserved for incremental builds, where it is a genuine input.
	parent, err := s.repo.PreviousSucceededBuild(ctx, owner.Account(), source.ID, "")
	if err != nil {
		return nil, err
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
			response := &CompileResponse{Build: existing, Reused: true, Activated: activated}
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

	sequence := 0
	events := make([]BuildEvent, 0, 8)
	addEvent := func(eventType, pagePath, detail string) {
		sequence++
		events = append(events, BuildEvent{
			SequenceNo: sequence, EventType: eventType, PagePath: pagePath,
			Detail: detail, OccurredAt: time.Now().UTC(),
		})
	}
	addEvent(BuildEventStarted, "", fmt.Sprintf("full compile of revision %s with %s", revision.RevisionKey, s.compiler.Model()))
	addEvent(BuildEventSourceRead, "", fmt.Sprintf("%d indexable documents", len(documents)))

	pages, usage, err := s.compileWithModel(ctx, *profile, manifest, documents)
	if err != nil {
		// A compilation failure is recorded as a failed build rather than
		// discarded: the attempt, its cost and its error are part of the audit
		// trail even though it produced nothing usable.
		failed, finishErr := s.repo.FinishBuild(ctx, owner.Account(), build.ID,
			BuildStatusFailed, CheckStatusPending, nil, err.Error())
		if finishErr != nil {
			return nil, fmt.Errorf("compile failed (%v) and the build could not be marked failed: %w", err, finishErr)
		}
		return &CompileResponse{Build: failed}, nil
	}
	for _, page := range pages {
		addEvent(BuildEventPageWritten, page.Path, fmt.Sprintf("%s, %d citations, %d links",
			page.Kind, len(page.Citations), len(page.Links)))
	}

	// index must come after every content page exists, since it links all of them.
	pages = append(pages, buildIndexPage(manifest, pages))
	// log is generated last and describes everything before it, so it cannot
	// describe its own writing.
	addEvent(BuildEventFinished, "", fmt.Sprintf("%d pages compiled", len(pages)))
	pages = append(pages, buildLogPage(events))

	knownPaths := make(map[string]struct{}, len(documents))
	for _, document := range documents {
		knownPaths[document.Path] = struct{}{}
	}
	parentPageCount := 0
	if parent != nil {
		if count, err := s.repo.CountPages(ctx, owner.Account(), parent.ID); err == nil {
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
		// violated its invariants leaves no half-visible wiki behind.
		for _, failure := range failures {
			addEvent(BuildEventCheckFailed, failure.PagePath, failure.Rule+": "+failure.Detail)
		}
		failed, finishErr := s.repo.FinishBuild(ctx, owner.Account(), build.ID,
			BuildStatusFailed, CheckStatusFailed, failures,
			fmt.Sprintf("%d check failures", len(failures)))
		if finishErr != nil {
			return nil, finishErr
		}
		return &CompileResponse{Build: failed}, nil
	}

	if err := s.repo.CommitBuild(ctx, owner, build.ID, pages, events, buildUsage{
		PagesWritten: len(pages),
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
	}); err != nil {
		failed, finishErr := s.repo.FinishBuild(ctx, owner.Account(), build.ID,
			BuildStatusFailed, CheckStatusPassed, nil, "commit failed: "+err.Error())
		if finishErr != nil {
			return nil, fmt.Errorf("commit failed (%v) and the build could not be marked failed: %w", err, finishErr)
		}
		return &CompileResponse{Build: failed}, nil
	}

	succeeded, err := s.repo.FinishBuild(ctx, owner.Account(), build.ID,
		BuildStatusSucceeded, CheckStatusPassed, nil, "")
	if err != nil {
		return nil, err
	}

	response := &CompileResponse{Build: succeeded, Pages: pagePaths(pages)}
	activated, activateErr := s.maybeActivate(ctx, owner, succeeded, req.Activate)
	response.Activated = activated
	if activateErr != nil {
		// The build is committed and valid; only the pointer move failed. Report
		// it instead of failing, so the caller knows to activate explicitly rather
		// than assuming the compilation was lost.
		response.Warnings = append(response.Warnings, "activation failed: "+activateErr.Error())
	}
	if s.reviewerIndependence == llm.IndependenceSameProvider || s.reviewerIndependence == llm.IndependenceSameModel {
		// Say this on every build rather than only in documentation: a reviewer
		// sharing the compiler's priors misses the compiler's mistakes, and the
		// operator should see that where the result is consumed.
		response.Warnings = append(response.Warnings,
			"reviewer independence is "+s.reviewerIndependence+"; review verdicts share the compiler's blind spots")
	}
	return response, nil
}

// maybeActivate moves the active pointer unless the caller opted out.
func (s *Service) maybeActivate(ctx context.Context, owner ownership.Owner, build *BuildRevision, activate *bool) (bool, error) {
	if activate != nil && !*activate {
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
