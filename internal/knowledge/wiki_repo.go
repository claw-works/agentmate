package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/wellxie/agentmate/internal/ownership"
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
	error, started_at, finished_at, created_at, updated_at`

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
}

func (r *Repo) CreateBuild(ctx context.Context, owner ownership.Owner, in createBuildInput) (*BuildRevision, error) {
	var build BuildRevision
	err := r.pool.QueryRow(ctx,
		`INSERT INTO knowledge_build_revisions
		   (account_id, user_id, key_id, source_id, source_revision_id, raw_package_hash, profile_version_id,
		    compiler_version, model, prompt_version, reviewer_model, reviewer_prompt_version,
		    reviewer_independence, parent_build_id, mode, status, started_at)
		 VALUES ($1, $2, $3, $4::uuid, $5::uuid, $6, $7::uuid, $8, $9, $10, $11, $12, $13, $14, $15, 'running', NOW())
		 RETURNING `+buildColumns,
		owner.Account(), nullableString(owner.UserID), owner.KeyID, in.SourceID, in.SourceRevisionID,
		in.RawPackageHash, in.ProfileVersionID, CompilerVersion, in.Model, PromptVersion,
		in.ReviewerModel, ReviewerPromptVersion, in.ReviewerIndependence, in.ParentBuildID, in.Mode,
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
		    AND status = 'succeeded'
		    AND ($7::uuid IS NULL AND parent_build_id IS NULL OR parent_build_id = $7::uuid)
		  ORDER BY created_at DESC
		  LIMIT 1`,
		accountID, in.SourceRevisionID, in.ProfileVersionID, CompilerVersion, in.Model, PromptVersion,
		in.ParentBuildID,
	).Scan(scanBuild(&build)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &build, nil
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

// CommitBuild writes the whole compiled wiki in one transaction.
//
// All or nothing is a requirement, not an optimisation: half a wiki is worse than
// none, because an agent cannot tell that pages are missing and would answer from
// an incomplete graph as if it were complete.
func (r *Repo) CommitBuild(
	ctx context.Context, owner ownership.Owner, buildID string,
	pages []WikiPage, events []BuildEvent, usage buildUsage,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
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
			return fmt.Errorf("insert page %s: %w", page.Path, err)
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
				return fmt.Errorf("insert link %s -> %s: %w", page.Path, link.TargetPath, err)
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
				return fmt.Errorf("insert citation for %s: %w", page.Path, err)
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
			return fmt.Errorf("insert build event %d: %w", event.SequenceNo, err)
		}
	}

	if _, err := tx.Exec(ctx,
		`UPDATE knowledge_build_revisions
		    SET pages_written = $3, pages_reused = $4,
		        input_tokens = $5, output_tokens = $6, cost_micros = $7,
		        updated_at = NOW()
		  WHERE account_id = $1 AND id = $2::uuid`,
		owner.Account(), buildID, usage.PagesWritten, usage.PagesReused,
		usage.InputTokens, usage.OutputTokens, usage.CostMicros,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type buildUsage struct {
	PagesWritten int
	PagesReused  int
	InputTokens  int
	OutputTokens int
	CostMicros   int64
}

// FinishBuild records the terminal state, including the check verdict. check is
// the only gate, so a failed check means the build never becomes activatable.
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

func sortStrings(values []string) {
	sort.Strings(values)
}
