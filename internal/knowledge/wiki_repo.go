package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/claw-works/agentmate/internal/ownership"
)

// ─── profiles ───

const profileColumns = `id, account_id, name, description, created_at, updated_at`

const profileVersionColumns = `id, account_id, profile_id, version, allowed_page_kinds, allowed_link_types,
	citation_policy, max_pages, max_page_chars, max_build_tokens, max_page_count_drift, instructions, created_at`

// EnsureProfileVersion resolves a profile name to its current version, creating
// the profile and a default version on first use.
//
// Auto-creation is deliberate: KNOWLEDGE.yaml already carries a `profile` string,
// and refusing to compile until an operator separately registers that name would
// block the common path for no safety gain. The defaults come from the migration,
// so a first build has explicit, inspectable limits rather than none.
func (r *Repo) EnsureProfileVersion(ctx context.Context, owner ownership.Owner, name, description string) (*ProfileVersion, error) {
	if name == "" {
		name = "default"
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var profileID string
	err = tx.QueryRow(ctx,
		`INSERT INTO knowledge_profiles (account_id, name, description)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (account_id, name) DO UPDATE SET updated_at = NOW()
		 RETURNING id::text`,
		owner.Account(), name, description,
	).Scan(&profileID)
	if err != nil {
		return nil, err
	}

	var version ProfileVersion
	err = tx.QueryRow(ctx,
		`SELECT `+profileVersionColumns+`
		   FROM knowledge_profile_versions
		  WHERE account_id = $1 AND profile_id = $2::uuid
		  ORDER BY version DESC LIMIT 1`,
		owner.Account(), profileID,
	).Scan(scanProfileVersion(&version)...)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return &version, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	err = tx.QueryRow(ctx,
		`INSERT INTO knowledge_profile_versions (account_id, profile_id, version)
		 VALUES ($1, $2::uuid, 1)
		 RETURNING `+profileVersionColumns,
		owner.Account(), profileID,
	).Scan(scanProfileVersion(&version)...)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &version, nil
}

func scanProfileVersion(version *ProfileVersion) []any {
	return []any{
		&version.ID, &version.AccountID, &version.ProfileID, &version.Version,
		&version.AllowedPageKinds, &version.AllowedLinkTypes, &version.CitationPolicy,
		&version.MaxPages, &version.MaxPageChars, &version.MaxBuildTokens,
		&version.MaxPageCountDrift, &version.Instructions, &version.CreatedAt,
	}
}

// ─── builds ───

const buildColumns = `id, account_id, user_id, key_id, source_id, source_revision_id, raw_package_hash,
	profile_version_id, compiler_version, model, prompt_version, reviewer_model, reviewer_prompt_version,
	reviewer_independence, parent_build_id, mode, status, check_status, check_failures, review_status,
	pages_written, pages_reused, input_tokens, output_tokens, cost_micros, review_tokens, review_cost_micros,
	error, started_at, finished_at, created_at, updated_at,
	lease_owner, lease_expires_at, heartbeat_at, attempt, max_attempts, next_attempt_at, queued_at,
	activate_on_success`

func scanBuild(build *BuildRevision) []any {
	return []any{
		&build.ID, &build.AccountID, &build.UserID, &build.KeyID, &build.SourceID,
		&build.SourceRevisionID, &build.RawPackageHash, &build.ProfileVersionID,
		&build.CompilerVersion, &build.Model, &build.PromptVersion, &build.ReviewerModel,
		&build.ReviewerPromptVersion, &build.ReviewerIndependence, &build.ParentBuildID,
		&build.Mode, &build.Status, &build.CheckStatus, &build.CheckFailures, &build.ReviewStatus,
		&build.PagesWritten, &build.PagesReused, &build.InputTokens, &build.OutputTokens,
		&build.CostMicros, &build.ReviewTokens, &build.ReviewCostMicros, &build.Error,
		&build.StartedAt, &build.FinishedAt, &build.CreatedAt, &build.UpdatedAt,
		&build.LeaseOwner, &build.LeaseExpiresAt, &build.HeartbeatAt,
		&build.Attempt, &build.MaxAttempts, &build.NextAttemptAt, &build.QueuedAt,
		&build.ActivateOnSuccess,
	}
}

type createBuildInput struct {
	SourceID             string
	SourceRevisionID     string
	RawPackageHash       string
	ProfileVersionID     string
	Model                string
	ReviewerModel        string
	ReviewerIndependence string
	ParentBuildID        *string
	Mode                 string
	MaxAttempts          int
	ActivateOnSuccess    bool
}

// CreateBuild enqueues a build. It starts `queued`, not `running`: the caller
// enqueues and returns, and a worker claims it later. started_at is therefore
// left NULL until a worker actually begins, so queue wait and compile time stay
// distinguishable.
func (r *Repo) CreateBuild(ctx context.Context, owner ownership.Owner, in createBuildInput) (*BuildRevision, error) {
	if in.MaxAttempts <= 0 {
		in.MaxAttempts = 3
	}
	var build BuildRevision
	err := r.pool.QueryRow(ctx,
		`INSERT INTO knowledge_build_revisions
		   (account_id, user_id, key_id, source_id, source_revision_id, raw_package_hash, profile_version_id,
		    compiler_version, model, prompt_version, reviewer_model, reviewer_prompt_version,
		    reviewer_independence, parent_build_id, mode, status, max_attempts, activate_on_success)
		 VALUES ($1, $2, $3, $4::uuid, $5::uuid, $6, $7::uuid, $8, $9, $10, $11, $12, $13, $14::uuid, $15, 'queued', $16, $17)
		 RETURNING `+buildColumns,
		owner.Account(), nullableString(owner.UserID), owner.KeyID, in.SourceID, in.SourceRevisionID,
		in.RawPackageHash, in.ProfileVersionID, CompilerVersion, in.Model, PromptVersion,
		in.ReviewerModel, ReviewerPromptVersion, in.ReviewerIndependence, in.ParentBuildID, in.Mode,
		in.MaxAttempts, in.ActivateOnSuccess,
	).Scan(scanBuild(&build)...)
	if err != nil {
		return nil, err
	}
	return &build, nil
}

// FindBuildByInputIdentity looks for an existing succeeded build with the same
// inputs.
//
// Input identity, not content identity: LLM output is not reproducible, so a
// content hash can neither confirm nor deny that a rebuild is needed. The inputs
// are what a caller can reason about.
//
// mode and parent are part of the identity. A full build and an incremental build
// off the same revision are different operations producing different page sets, and
// an incremental build's output is defined relative to its parent — so two
// incremental builds off different parents must not be treated as the same work.
func (r *Repo) FindBuildByInputIdentity(ctx context.Context, accountID string, in createBuildInput) (*BuildRevision, error) {
	var build BuildRevision
	err := r.pool.QueryRow(ctx,
		`SELECT `+buildColumns+`
		   FROM knowledge_build_revisions
		  WHERE account_id = $1
		    AND source_revision_id = $2::uuid
		    AND profile_version_id = $3::uuid
		    AND compiler_version = $4
		    AND model = $5
		    AND prompt_version = $6
		    AND mode = $8
		    AND status = 'succeeded'
		    AND (($7::uuid IS NULL AND parent_build_id IS NULL)
		         OR ($7::uuid IS NOT NULL AND parent_build_id = $7::uuid))
		  ORDER BY created_at DESC
		  LIMIT 1`,
		accountID, in.SourceRevisionID, in.ProfileVersionID, CompilerVersion, in.Model, PromptVersion,
		in.ParentBuildID, in.Mode,
	).Scan(scanBuild(&build)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &build, nil
}

// ─── queue: claim, heartbeat, release (K3.3) ───

// ClaimNextBuild leases one eligible build to a worker.
//
// Eligibility is `queued` or `running` with an expired lease. Including `running`
// is what makes a crashed worker recoverable: nothing else will ever release that
// row. Recovery keys on lease expiry rather than on worker liveness, because a
// partitioned worker looks identical from here and has the same consequence —
// nobody is making progress.
//
// SKIP LOCKED so several workers can poll the same queue without serialising on
// the head of it.
func (r *Repo) ClaimNextBuild(ctx context.Context, leaseOwner string, leaseFor time.Duration) (*BuildRevision, error) {
	if leaseOwner == "" {
		return nil, fmt.Errorf("lease owner required")
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var buildID, accountID string
	var attempt, maxAttempts int
	err = tx.QueryRow(ctx,
		`SELECT id::text, account_id::text, attempt, max_attempts
		   FROM knowledge_build_revisions
		  WHERE status IN ('queued', 'running')
		    AND next_attempt_at <= NOW()
		    AND (lease_expires_at IS NULL OR lease_expires_at <= NOW())
		  ORDER BY next_attempt_at, queued_at
		  LIMIT 1
		  FOR UPDATE SKIP LOCKED`,
	).Scan(&buildID, &accountID, &attempt, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// The attempt budget is checked at claim time, not at failure time. A worker
	// that dies without reporting anything still consumed an attempt, so a build
	// that reliably kills its worker retires instead of cycling forever.
	if attempt >= maxAttempts {
		var build BuildRevision
		if err := tx.QueryRow(ctx,
			`UPDATE knowledge_build_revisions
			    SET status = 'failed', lease_owner = '', lease_expires_at = NULL,
			        error = CASE WHEN error = '' THEN $3 ELSE error || ' (' || $3 || ')' END,
			        finished_at = NOW(), updated_at = NOW()
			  WHERE id = $1::uuid AND account_id = $2::uuid
			 RETURNING `+buildColumns,
			buildID, accountID,
			fmt.Sprintf("gave up after %d attempts", attempt),
		).Scan(scanBuild(&build)...); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		// Reported as "nothing claimed": the caller has no work to do, and the
		// build is already closed out.
		return nil, nil
	}

	// A reclaimed build may carry output from the attempt that died. Since a
	// successful commit sets `succeeded` in the same transaction that writes the
	// pages, a claimable row should have none — this clears them anyway so recovery
	// does not depend on that invariant holding forever somewhere else.
	for _, table := range []string{
		"knowledge_page_citations", "knowledge_page_links",
		"knowledge_build_events", "knowledge_pages",
	} {
		if _, err := tx.Exec(ctx,
			`DELETE FROM `+table+` WHERE account_id = $1::uuid AND build_id = $2::uuid`,
			accountID, buildID,
		); err != nil {
			return nil, fmt.Errorf("clear abandoned %s: %w", table, err)
		}
	}

	var build BuildRevision
	if err := tx.QueryRow(ctx,
		`UPDATE knowledge_build_revisions
		    SET status = 'running', attempt = attempt + 1,
		        lease_owner = $3, lease_expires_at = NOW() + make_interval(secs => $4),
		        heartbeat_at = NOW(),
		        started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		  WHERE id = $1::uuid AND account_id = $2::uuid
		 RETURNING `+buildColumns,
		buildID, accountID, leaseOwner, leaseFor.Seconds(),
	).Scan(scanBuild(&build)...); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &build, nil
}

// HeartbeatBuild extends the lease. It fails with ErrLeaseLost once the lease has
// moved on, which is how a worker learns to abandon work another worker has taken
// over instead of racing it to the commit.
func (r *Repo) HeartbeatBuild(ctx context.Context, accountID, buildID, leaseOwner string, leaseFor time.Duration) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE knowledge_build_revisions
		    SET lease_expires_at = NOW() + make_interval(secs => $4), heartbeat_at = NOW(), updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid AND lease_owner = $3 AND status = 'running'`,
		accountID, buildID, leaseOwner, leaseFor.Seconds(),
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: build %s", ErrLeaseLost, buildID)
	}
	return nil
}

// RequeueBuild returns a build to the queue after a retryable failure, with the
// error recorded and the next attempt pushed out by the backoff.
//
// Intervals are built with make_interval from a seconds value rather than by
// casting a Go duration string: Go renders sub-second durations as "1ns", which
// Postgres rejects outright, and "1m0s" happens to parse only by luck of
// abbreviation. Passing a number leaves nothing to interpretation.
func (r *Repo) RequeueBuild(
	ctx context.Context, accountID, buildID, leaseOwner, buildError string, retryIn time.Duration,
) (*BuildRevision, error) {
	var build BuildRevision
	err := r.pool.QueryRow(ctx,
		`UPDATE knowledge_build_revisions
		    SET status = 'queued', lease_owner = '', lease_expires_at = NULL,
		        error = $4, next_attempt_at = NOW() + make_interval(secs => $5), updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid AND lease_owner = $3
		 RETURNING `+buildColumns,
		accountID, buildID, leaseOwner, buildError, retryIn.Seconds(),
	).Scan(scanBuild(&build)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: build %s", ErrLeaseLost, buildID)
	}
	if err != nil {
		return nil, err
	}
	return &build, nil
}

// YieldBuild hands a build back on graceful shutdown.
//
// The attempt is refunded, unlike on a crash. A rolling deploy must not spend the
// retry budget of every in-flight build: the attempt never got a fair chance to
// fail. A crash cannot reach this path, so the poison-build protection that makes
// attempts count claims still holds.
func (r *Repo) YieldBuild(ctx context.Context, accountID, buildID, leaseOwner string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE knowledge_build_revisions
		    SET status = 'queued', lease_owner = '', lease_expires_at = NULL,
		        attempt = GREATEST(attempt - 1, 0),
		        next_attempt_at = NOW(), updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid AND lease_owner = $3 AND status = 'running'`,
		accountID, buildID, leaseOwner,
	)
	return err
}

// QueueDepth reports how much work is waiting, per status. Used by the queue
// observability endpoint: without it, "my compile is slow" is unanswerable.
func (r *Repo) QueueDepth(ctx context.Context, accountID string) (*QueueStats, error) {
	stats := &QueueStats{}
	err := r.pool.QueryRow(ctx,
		`SELECT
		   count(*) FILTER (WHERE status = 'queued'),
		   count(*) FILTER (WHERE status = 'running'),
		   count(*) FILTER (WHERE status = 'queued' AND next_attempt_at > NOW()),
		   COALESCE(EXTRACT(EPOCH FROM (NOW() - min(queued_at) FILTER (WHERE status = 'queued')))::bigint, 0)
		 FROM knowledge_build_revisions
		 WHERE ($1 = '' OR account_id::text = $1)`,
		accountID,
	).Scan(&stats.Queued, &stats.Running, &stats.WaitingForRetry, &stats.OldestQueuedSeconds)
	if err != nil {
		return nil, err
	}
	return stats, nil
}

func (r *Repo) GetBuild(ctx context.Context, accountID, buildID string) (*BuildRevision, error) {
	var build BuildRevision
	err := r.pool.QueryRow(ctx,
		`SELECT `+buildColumns+`,
		        EXISTS (SELECT 1 FROM knowledge_sources AS source
		                 WHERE source.account_id = build.account_id AND source.active_build_id = build.id)
		   FROM knowledge_build_revisions AS build
		  WHERE build.account_id = $1 AND build.id = $2::uuid`,
		accountID, buildID,
	).Scan(append(scanBuild(&build), &build.IsActive)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("build not found: %s", buildID)
	}
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func (r *Repo) ListBuilds(ctx context.Context, accountID, sourceID string, limit, offset int) ([]BuildRevision, int, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_build_revisions
		  WHERE account_id = $1 AND ($2 = '' OR source_id::text = $2)`,
		accountID, sourceID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT `+buildColumns+`,
		        EXISTS (SELECT 1 FROM knowledge_sources AS source
		                 WHERE source.account_id = build.account_id AND source.active_build_id = build.id)
		   FROM knowledge_build_revisions AS build
		  WHERE build.account_id = $1 AND ($2 = '' OR build.source_id::text = $2)
		  ORDER BY build.created_at DESC, build.id
		  LIMIT $3 OFFSET $4`,
		accountID, sourceID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := make([]BuildRevision, 0)
	for rows.Next() {
		var build BuildRevision
		if err := rows.Scan(append(scanBuild(&build), &build.IsActive)...); err != nil {
			return nil, 0, err
		}
		items = append(items, build)
	}
	return items, total, rows.Err()
}

// CommitBuild writes the whole compiled wiki and marks the build succeeded in one
// transaction.
//
// All or nothing is a requirement, not an optimisation: half a wiki is worse than
// none, because an agent cannot tell that pages are missing and would answer from
// an incomplete graph as if it were complete.
//
// The terminal status is set in the same transaction on purpose. When it was a
// separate statement, a worker killed in between left pages behind on a build that
// still said `running` — and once builds are reclaimed by another worker, that
// window becomes a source of duplicated pages. Committing them together makes
// "this build has pages" and "this build succeeded" the same fact.
func (r *Repo) CommitBuild(
	ctx context.Context, owner ownership.Owner, buildID string,
	pages []WikiPage, events []BuildEvent, usage buildUsage,
) (*BuildRevision, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	pageIDs := make(map[string]string, len(pages))
	for _, page := range pages {
		frontmatter := page.Frontmatter
		if len(frontmatter) == 0 {
			frontmatter = json.RawMessage(`{}`)
		}
		var pageID string
		if err := tx.QueryRow(ctx,
			`INSERT INTO knowledge_pages
			   (account_id, build_id, path, kind, title, content, frontmatter, content_hash, derived_from_build_id)
			 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7, $8, $9)
			 RETURNING id::text`,
			owner.Account(), buildID, page.Path, page.Kind, page.Title, page.Content,
			frontmatter, page.ContentHash, page.DerivedFromBuildID,
		).Scan(&pageID); err != nil {
			return nil, fmt.Errorf("insert page %s: %w", page.Path, err)
		}
		pageIDs[page.Path] = pageID
	}

	// Links are written after every page exists so a forward reference resolves.
	for _, page := range pages {
		sourcePageID := pageIDs[page.Path]
		for _, link := range page.Links {
			var targetPageID *string
			if id, ok := pageIDs[link.TargetPath]; ok {
				targetPageID = &id
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO knowledge_page_links
				   (account_id, build_id, source_page_id, target_page_id, target_path, link_type, note)
				 VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7)
				 ON CONFLICT (build_id, source_page_id, target_path, link_type) DO NOTHING`,
				owner.Account(), buildID, sourcePageID, targetPageID, link.TargetPath, link.LinkType, link.Note,
			); err != nil {
				return nil, fmt.Errorf("insert link %s -> %s: %w", page.Path, link.TargetPath, err)
			}
		}
		for _, citation := range page.Citations {
			if _, err := tx.Exec(ctx,
				`INSERT INTO knowledge_page_citations
				   (account_id, build_id, page_id, document_id, document_path, heading_path, chunk_key, claim, excerpt)
				 VALUES ($1, $2::uuid, $3::uuid, $4, $5, $6, $7, $8, $9)`,
				owner.Account(), buildID, sourcePageID, citation.DocumentID, citation.DocumentPath,
				citation.HeadingPath, citation.ChunkKey, citation.Claim, citation.Excerpt,
			); err != nil {
				return nil, fmt.Errorf("insert citation for %s: %w", page.Path, err)
			}
		}
	}

	for _, event := range events {
		payload := event.Payload
		if len(payload) == 0 {
			payload = json.RawMessage(`{}`)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO knowledge_build_events
			   (account_id, build_id, sequence_no, event_type, page_path, detail, payload)
			 VALUES ($1, $2::uuid, $3, $4, $5, $6, $7)`,
			owner.Account(), buildID, event.SequenceNo, event.EventType, event.PagePath, event.Detail, payload,
		); err != nil {
			return nil, fmt.Errorf("insert build event %d: %w", event.SequenceNo, err)
		}
	}

	// Terminal state, usage and lease release all land here. The lease is cleared
	// because a succeeded build must never look claimable again.
	var build BuildRevision
	if err := tx.QueryRow(ctx,
		`UPDATE knowledge_build_revisions
		    SET status = 'succeeded', check_status = 'passed', check_failures = '[]'::jsonb, error = '',
		        pages_written = $3, pages_reused = $4,
		        input_tokens = $5, output_tokens = $6, cost_micros = $7,
		        lease_owner = '', lease_expires_at = NULL,
		        finished_at = NOW(), updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid AND lease_owner = $8
		 RETURNING `+buildColumns,
		owner.Account(), buildID, usage.PagesWritten, usage.PagesReused,
		usage.InputTokens, usage.OutputTokens, usage.CostMicros, usage.LeaseOwner,
	).Scan(scanBuild(&build)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The lease moved to another worker while this one was compiling. Its
			// output is discarded rather than merged: the other worker is producing
			// a complete wiki, and interleaving two of them yields a graph neither
			// of them checked.
			return nil, fmt.Errorf("%w: build %s", ErrLeaseLost, buildID)
		}
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &build, nil
}

type buildUsage struct {
	PagesWritten int
	PagesReused  int
	InputTokens  int
	OutputTokens int
	CostMicros   int64
	// LeaseOwner guards the commit: only the worker still holding the lease may
	// write the wiki.
	LeaseOwner string
}

// FinishBuild records the terminal state, including the check verdict. check is
// the only gate, so a failed check means the build never becomes activatable.
//
// The lease is cleared here too: a terminal build must never look claimable again.
func (r *Repo) FinishBuild(ctx context.Context, accountID, buildID, status, checkStatus string, failures []CheckFailure, buildError string) (*BuildRevision, error) {
	encoded, err := json.Marshal(failures)
	if err != nil {
		return nil, err
	}
	if failures == nil {
		encoded = []byte(`[]`)
	}
	var build BuildRevision
	if err := r.pool.QueryRow(ctx,
		`UPDATE knowledge_build_revisions
		    SET status = $3, check_status = $4, check_failures = $5, error = $6,
		        lease_owner = '', lease_expires_at = NULL,
		        finished_at = NOW(), updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid
		 RETURNING `+buildColumns,
		accountID, buildID, status, checkStatus, encoded, buildError,
	).Scan(scanBuild(&build)...); err != nil {
		return nil, err
	}
	return &build, nil
}

// ActivateBuild moves the source's wiki pointer. Rollback is the same operation
// pointed at an older build, which is why there is no separate rollback path.
func (r *Repo) ActivateBuild(ctx context.Context, accountID, buildID string) (*BuildRevision, *string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var build BuildRevision
	if err := tx.QueryRow(ctx,
		`SELECT `+buildColumns+` FROM knowledge_build_revisions
		  WHERE account_id = $1 AND id = $2::uuid FOR UPDATE`,
		accountID, buildID,
	).Scan(scanBuild(&build)...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, fmt.Errorf("build not found: %s", buildID)
		}
		return nil, nil, err
	}
	if build.Status != BuildStatusSucceeded {
		return nil, nil, fmt.Errorf("%w: build %s has status %s", ErrBuildNotActivatable, buildID, build.Status)
	}
	// check is the gate. A build that failed its invariants must never become the
	// active wiki, no matter who asks.
	if build.CheckStatus != CheckStatusPassed {
		return nil, nil, fmt.Errorf("%w: build %s did not pass check", ErrBuildNotActivatable, buildID)
	}

	// Read the outgoing pointer before overwriting it: reporting it lets a
	// rollback be undone by activating it again. A RETURNING subquery would see
	// the post-update value.
	var previous *string
	if err := tx.QueryRow(ctx,
		`SELECT active_build_id::text FROM knowledge_sources
		  WHERE account_id = $1 AND id = $2::uuid FOR UPDATE`,
		accountID, build.SourceID,
	).Scan(&previous); err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE knowledge_sources
		    SET active_build_id = $3::uuid, updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid`,
		accountID, build.SourceID, buildID,
	); err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	build.IsActive = true
	return &build, previous, nil
}

// ─── pages ───

const pageColumns = `id, account_id, build_id, path, kind, title, content, frontmatter, content_hash,
	derived_from_build_id, created_at`

func scanPage(page *WikiPage) []any {
	return []any{
		&page.ID, &page.AccountID, &page.BuildID, &page.Path, &page.Kind, &page.Title,
		&page.Content, &page.Frontmatter, &page.ContentHash, &page.DerivedFromBuildID, &page.CreatedAt,
	}
}

// ListPages returns page metadata without bodies unless requested: a build can
// hold hundreds of pages and callers listing them rarely want the text.
func (r *Repo) ListPages(ctx context.Context, accountID, buildID string, includeContent bool) ([]WikiPage, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, build_id, path, kind, title,
		        CASE WHEN $3 THEN content ELSE '' END AS content,
		        frontmatter, content_hash, derived_from_build_id, created_at
		   FROM knowledge_pages
		  WHERE account_id = $1 AND build_id = $2::uuid
		  ORDER BY kind, path`,
		accountID, buildID, includeContent,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WikiPage, 0)
	for rows.Next() {
		var page WikiPage
		if err := rows.Scan(scanPage(&page)...); err != nil {
			return nil, err
		}
		items = append(items, page)
	}
	return items, rows.Err()
}

// GetPage returns one page with its citations and both link directions, which is
// what an agent needs to check a claim and keep navigating.
func (r *Repo) GetPage(ctx context.Context, accountID, buildID, path string) (*WikiPage, error) {
	var page WikiPage
	err := r.pool.QueryRow(ctx,
		`SELECT `+pageColumns+` FROM knowledge_pages
		  WHERE account_id = $1 AND build_id = $2::uuid AND path = $3`,
		accountID, buildID, path,
	).Scan(scanPage(&page)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("page not found: %s", path)
	}
	if err != nil {
		return nil, err
	}

	citationRows, err := r.pool.Query(ctx,
		`SELECT id, build_id, page_id, document_id::text, document_path, heading_path, chunk_key, claim, excerpt, created_at
		   FROM knowledge_page_citations
		  WHERE account_id = $1 AND page_id = $2::uuid
		  ORDER BY document_path, heading_path, id`,
		accountID, page.ID,
	)
	if err != nil {
		return nil, err
	}
	defer citationRows.Close()
	page.Citations = make([]PageCitation, 0)
	for citationRows.Next() {
		var citation PageCitation
		if err := citationRows.Scan(&citation.ID, &citation.BuildID, &citation.PageID,
			&citation.DocumentID, &citation.DocumentPath, &citation.HeadingPath,
			&citation.ChunkKey, &citation.Claim, &citation.Excerpt, &citation.CreatedAt); err != nil {
			return nil, err
		}
		page.Citations = append(page.Citations, citation)
	}
	if err := citationRows.Err(); err != nil {
		return nil, err
	}

	linkRows, err := r.pool.Query(ctx,
		`SELECT id, build_id, source_page_id, target_page_id::text, target_path, link_type, note, created_at, 'out' AS direction
		   FROM knowledge_page_links
		  WHERE account_id = $1 AND source_page_id = $2::uuid
		 UNION ALL
		 SELECT link.id, link.build_id, link.source_page_id, link.target_page_id::text,
		        source.path, link.link_type, link.note, link.created_at, 'in' AS direction
		   FROM knowledge_page_links AS link
		   JOIN knowledge_pages AS source
		     ON source.id = link.source_page_id AND source.account_id = link.account_id
		  WHERE link.account_id = $1 AND link.target_page_id = $2::uuid
		 ORDER BY direction DESC, link_type, target_path`,
		accountID, page.ID,
	)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()
	page.Links = make([]PageLink, 0)
	for linkRows.Next() {
		var link PageLink
		if err := linkRows.Scan(&link.ID, &link.BuildID, &link.SourcePageID, &link.TargetPageID,
			&link.TargetPath, &link.LinkType, &link.Note, &link.CreatedAt, &link.Direction); err != nil {
			return nil, err
		}
		page.Links = append(page.Links, link)
	}
	return &page, linkRows.Err()
}

// DiffBuilds compares two builds by page path and content hash.
//
// Content hashes are the right tool here — this is exactly the diffing use they
// exist for — while they remain unusable as an identity or idempotency key.
func (r *Repo) DiffBuilds(ctx context.Context, accountID, fromBuildID, toBuildID string) (*BuildDiff, error) {
	hashes := func(buildID string) (map[string]string, error) {
		rows, err := r.pool.Query(ctx,
			`SELECT path, content_hash FROM knowledge_pages
			  WHERE account_id = $1 AND build_id = $2::uuid`,
			accountID, buildID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := make(map[string]string)
		for rows.Next() {
			var path, hash string
			if err := rows.Scan(&path, &hash); err != nil {
				return nil, err
			}
			result[path] = hash
		}
		return result, rows.Err()
	}

	from, err := hashes(fromBuildID)
	if err != nil {
		return nil, err
	}
	to, err := hashes(toBuildID)
	if err != nil {
		return nil, err
	}

	diff := &BuildDiff{
		FromBuildID: fromBuildID, ToBuildID: toBuildID,
		Added: make([]string, 0), Removed: make([]string, 0), Changed: make([]string, 0),
	}
	for path, hash := range to {
		previous, existed := from[path]
		switch {
		case !existed:
			diff.Added = append(diff.Added, path)
		case previous != hash:
			diff.Changed = append(diff.Changed, path)
		default:
			diff.Unchanged++
		}
	}
	for path := range from {
		if _, ok := to[path]; !ok {
			diff.Removed = append(diff.Removed, path)
		}
	}
	sortStrings(diff.Added)
	sortStrings(diff.Removed)
	sortStrings(diff.Changed)
	diff.Summary = BuildDiffCounts{
		Added: len(diff.Added), Removed: len(diff.Removed),
		Changed: len(diff.Changed), Unchanged: diff.Unchanged,
	}
	return diff, nil
}

// ListBuildEvents returns the structured build log, which the log page renders.
func (r *Repo) ListBuildEvents(ctx context.Context, accountID, buildID string) ([]BuildEvent, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, build_id, sequence_no, event_type, page_path, detail, payload, occurred_at
		   FROM knowledge_build_events
		  WHERE account_id = $1 AND build_id = $2::uuid
		  ORDER BY sequence_no`,
		accountID, buildID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]BuildEvent, 0)
	for rows.Next() {
		var event BuildEvent
		if err := rows.Scan(&event.ID, &event.BuildID, &event.SequenceNo, &event.EventType,
			&event.PagePath, &event.Detail, &event.Payload, &event.OccurredAt); err != nil {
			return nil, err
		}
		items = append(items, event)
	}
	return items, rows.Err()
}

// PreviousSucceededBuild is the baseline for drift checks and the default target
// for a rollback.
func (r *Repo) PreviousSucceededBuild(ctx context.Context, accountID, sourceID, excludeBuildID string) (*BuildRevision, error) {
	var build BuildRevision
	err := r.pool.QueryRow(ctx,
		`SELECT `+buildColumns+` FROM knowledge_build_revisions
		  WHERE account_id = $1 AND source_id = $2::uuid AND status = 'succeeded'
		    AND ($3 = '' OR id::text <> $3)
		  ORDER BY created_at DESC LIMIT 1`,
		accountID, sourceID, excludeBuildID,
	).Scan(scanBuild(&build)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &build, nil
}

func (r *Repo) CountPages(ctx context.Context, accountID, buildID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_pages WHERE account_id = $1 AND build_id = $2::uuid`,
		accountID, buildID,
	).Scan(&count)
	return count, err
}

// GetProfileVersion loads an immutable profile version by ID. The worker resolves
// the profile from the build rather than from the manifest, so a manifest edited
// after enqueue cannot change the rules the build is checked against.
func (r *Repo) GetProfileVersion(ctx context.Context, accountID, id string) (*ProfileVersion, error) {
	var version ProfileVersion
	err := r.pool.QueryRow(ctx,
		`SELECT `+profileVersionColumns+` FROM knowledge_profile_versions
		  WHERE account_id = $1 AND id = $2::uuid`,
		accountID, id,
	).Scan(scanProfileVersion(&version)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("profile version not found: %s", id)
	}
	if err != nil {
		return nil, err
	}
	return &version, nil
}

// CountRevisionIndexableDocuments is the enqueue-time check that there is
// anything to compile, without loading every document body just to count them.
func (r *Repo) CountRevisionIndexableDocuments(ctx context.Context, accountID, revisionID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM knowledge_documents
		  WHERE account_id = $1 AND revision_id = $2 AND indexable = true AND content_snapshot <> ''`,
		accountID, revisionID,
	).Scan(&count)
	return count, err
}

// RecordAttemptUsage accumulates the cost of a failed attempt.
//
// Additive rather than overwriting: a build that failed twice before succeeding
// cost all three attempts, and reporting only the last makes the bill
// unexplainable. It runs on its own context for the same reason terminal writes
// do — the attempt failed, quite possibly because that context died.
func (r *Repo) RecordAttemptUsage(ctx context.Context, accountID, buildID string, inputTokens, outputTokens int, costMicros int64) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	_, err := r.pool.Exec(writeCtx,
		`UPDATE knowledge_build_revisions
		    SET input_tokens = input_tokens + $3, output_tokens = output_tokens + $4,
		        cost_micros = cost_micros + $5, updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid`,
		accountID, buildID, inputTokens, outputTokens, costMicros,
	)
	return err
}

func sortStrings(values []string) {
	sort.Strings(values)
}
