package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ─── Skill Sources ───

func (r *Repo) UpsertSource(ctx context.Context, userID string, req CreateSkillSourceRequest) (*SkillSource, error) {
	metadata, err := sourceMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}
	var s SkillSource
	err = r.pool.QueryRow(ctx,
		`INSERT INTO skill_sources
		 (user_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (user_id, type, repository_url, package_path)
		 DO UPDATE SET
		   name = EXCLUDED.name,
		   default_ref = EXCLUDED.default_ref,
		   sync_mode = EXCLUDED.sync_mode,
		   visibility = EXCLUDED.visibility,
		   status = EXCLUDED.status,
		   metadata = EXCLUDED.metadata,
		   updated_at = NOW()
		 RETURNING id, user_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at`,
		userID, req.Name, req.Type, req.RepositoryURL, req.PackagePath, req.DefaultRef, req.SyncMode, req.Visibility, req.Status, metadata,
	).Scan(scanSource(&s)...)
	return &s, err
}

func (r *Repo) ListSources(ctx context.Context, userID string, params SkillSourceListParams) ([]SkillSource, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at
		 FROM skill_sources
		 WHERE user_id = $1
		   AND ($2 = '' OR type = $2)
		   AND ($3 = '' OR status = $3)
		 ORDER BY updated_at DESC, created_at DESC
		 LIMIT $4 OFFSET $5`,
		userID, params.Type, params.Status, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillSource, 0)
	for rows.Next() {
		var s SkillSource
		if err := rows.Scan(scanSource(&s)...); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return items, rows.Err()
}

func (r *Repo) GetSource(ctx context.Context, userID, id string) (*SkillSource, error) {
	var s SkillSource
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at
		 FROM skill_sources
		 WHERE id = $1 AND user_id = $2`,
		id, userID,
	).Scan(scanSource(&s)...)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repo) ListSourceRevisions(ctx context.Context, userID, sourceID string, limit, offset int) ([]SkillSourceRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at
		 FROM skill_source_revisions
		 WHERE user_id = $1 AND source_id = $2
		 ORDER BY created_at DESC
		 LIMIT $3 OFFSET $4`,
		userID, sourceID, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillSourceRevision, 0)
	for rows.Next() {
		var rev SkillSourceRevision
		if err := rows.Scan(scanRevision(&rev)...); err != nil {
			return nil, err
		}
		items = append(items, rev)
	}
	return items, rows.Err()
}

// ─── Skill Logs ───

func (r *Repo) CreateLog(ctx context.Context, userID string, req CreateLogRequest) (*SkillLog, error) {
	wasTriggered := true
	if req.WasTriggered != nil {
		wasTriggered = *req.WasTriggered
	}
	version := req.SkillVersion
	if version == "" {
		version = "unknown"
	}
	var toolCalls []byte
	if req.ToolCalls != nil {
		toolCalls = req.ToolCalls
	}

	var l SkillLog
	err := r.pool.QueryRow(ctx,
		`INSERT INTO skill_logs (user_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 RETURNING id, user_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms, created_at`,
		userID, req.SkillName, version, req.AgentID, req.SessionID, req.TriggerText,
		wasTriggered, req.Outcome, req.FailureReason, req.UserCorrection, toolCalls, req.DurationMs,
	).Scan(&l.ID, &l.UserID, &l.SkillName, &l.SkillVersion, &l.AgentID, &l.SessionID, &l.TriggerText,
		&l.WasTriggered, &l.Outcome, &l.FailureReason, &l.UserCorrection, &l.ToolCalls, &l.DurationMs, &l.CreatedAt)
	return &l, err
}

func (r *Repo) ListLogs(ctx context.Context, userID string, params LogListParams) ([]SkillLog, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms, created_at
		 FROM skill_logs
		 WHERE user_id = $1
		   AND ($2 = '' OR skill_name = $2)
		   AND ($3 = '' OR agent_id = $3)
		   AND ($4 = '' OR outcome = $4)
		 ORDER BY created_at DESC LIMIT $5 OFFSET $6`,
		userID, params.SkillName, params.AgentID, params.Outcome, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillLog, 0)
	for rows.Next() {
		var l SkillLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.SkillName, &l.SkillVersion, &l.AgentID, &l.SessionID, &l.TriggerText,
			&l.WasTriggered, &l.Outcome, &l.FailureReason, &l.UserCorrection, &l.ToolCalls, &l.DurationMs, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, nil
}

func (r *Repo) CountLogs(ctx context.Context, userID string, params LogListParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM skill_logs
		 WHERE user_id = $1
		   AND ($2 = '' OR skill_name = $2)
		   AND ($3 = '' OR agent_id = $3)
		   AND ($4 = '' OR outcome = $4)`,
		userID, params.SkillName, params.AgentID, params.Outcome,
	).Scan(&count)
	return count, err
}

// ─── Skill Versions ───

func (r *Repo) CreateVersion(ctx context.Context, userID string, req CreateVersionRequest) (*SkillVersion, error) {
	hash := sha256.Sum256([]byte(req.Content))
	contentHash := hex.EncodeToString(hash[:])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if req.Activate {
		_, err = tx.Exec(ctx,
			`UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND user_id = $2 AND is_active = true`,
			req.SkillName, userID)
		if err != nil {
			return nil, err
		}
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`INSERT INTO skill_versions (user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
		userID, req.SkillName, req.Version, req.Content, contentHash, req.AgentID, req.ChangeSummary, req.EvalPassRate, req.Activate,
	).Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListVersions(ctx context.Context, userID string, params VersionListParams) ([]SkillVersion, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE user_id = $1 AND ($2 = '' OR skill_name = $2)
		 ORDER BY published_at DESC LIMIT $3 OFFSET $4`,
		userID, params.SkillName, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillVersion, 0)
	for rows.Next() {
		var v SkillVersion
		if err := rows.Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, nil
}

func (r *Repo) GetActiveVersion(ctx context.Context, userID, skillName string) (*SkillVersion, error) {
	var v SkillVersion
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE user_id = $1 AND skill_name = $2 AND is_active = true`,
		userID, skillName,
	).Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListActiveVersions(ctx context.Context, userID, skillName string) ([]SkillVersion, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE user_id = $1 AND is_active = true AND ($2 = '' OR skill_name = $2)
		 ORDER BY skill_name, published_at DESC`,
		userID, skillName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillVersion, 0)
	for rows.Next() {
		var v SkillVersion
		if err := rows.Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (r *Repo) ActivateVersion(ctx context.Context, userID, id string) (*SkillVersion, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Get the version to find its skill_name
	var skillName string
	err = tx.QueryRow(ctx, `SELECT skill_name FROM skill_versions WHERE id = $1 AND user_id = $2`, id, userID).Scan(&skillName)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND user_id = $2 AND is_active = true`, skillName, userID)
	if err != nil {
		return nil, err
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`UPDATE skill_versions SET is_active = true WHERE id = $1 AND user_id = $2
		 RETURNING id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
		id, userID,
	).Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListVersionFiles(ctx context.Context, userID, versionID string) ([]SkillVersionFile, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM skill_version_files
		 WHERE user_id = $1 AND version_id = $2
		 ORDER BY path`,
		userID, versionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillVersionFile, 0)
	for rows.Next() {
		var f SkillVersionFile
		if err := rows.Scan(scanVersionFile(&f)...); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, rows.Err()
}

func (r *Repo) IngestLocalSnapshot(ctx context.Context, userID string, source *SkillSource, versionReq CreateVersionRequest, revisionIn SkillSourceRevision, fileInputs []SkillVersionFile) (*SkillSourceRevision, *SkillVersion, []SkillVersionFile, error) {
	hash := sha256.Sum256([]byte(versionReq.Content))
	contentHash := hex.EncodeToString(hash[:])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE skill_sources SET status = 'active', updated_at = NOW() WHERE id = $1 AND user_id = $2`,
		source.ID, userID,
	); err != nil {
		return nil, nil, nil, err
	}

	if versionReq.Activate {
		_, err = tx.Exec(ctx,
			`UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND user_id = $2 AND is_active = true`,
			versionReq.SkillName, userID)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE user_id = $1 AND skill_name = $2 AND content_hash = $3`,
		userID, versionReq.SkillName, contentHash,
	).Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_versions (user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
			 RETURNING id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
			userID, versionReq.SkillName, versionReq.Version, versionReq.Content, contentHash, versionReq.AgentID, versionReq.ChangeSummary, versionReq.EvalPassRate, versionReq.Activate,
		).Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
		if err != nil {
			return nil, nil, nil, err
		}
	} else if err != nil {
		return nil, nil, nil, err
	} else if versionReq.Activate {
		err = tx.QueryRow(ctx,
			`UPDATE skill_versions SET is_active = true WHERE id = $1 AND user_id = $2
			 RETURNING id, user_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
			v.ID, userID,
		).Scan(&v.ID, &v.UserID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	var rev SkillSourceRevision
	err = tx.QueryRow(ctx,
		`SELECT id, user_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at
		 FROM skill_source_revisions
		 WHERE user_id = $1 AND source_id = $2 AND local_snapshot_id = $3`,
		userID, source.ID, revisionIn.LocalSnapshotID,
	).Scan(scanRevision(&rev)...)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_source_revisions
			 (user_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error)
			 VALUES ($1, $2, $3, '', $4, $5, $6, 'ingested', '')
			 RETURNING id, user_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`,
			userID, source.ID, v.ID, revisionIn.LocalSnapshotID, revisionIn.TreeHash, revisionIn.PackageHash,
		).Scan(scanRevision(&rev)...)
		if err != nil {
			return nil, nil, nil, err
		}
	} else if err != nil {
		return nil, nil, nil, err
	} else {
		err = tx.QueryRow(ctx,
			`UPDATE skill_source_revisions
			 SET skill_version_id = $1, tree_hash = $2, package_hash = $3, status = 'ingested', error = ''
			 WHERE id = $4 AND user_id = $5
			 RETURNING id, user_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`,
			v.ID, revisionIn.TreeHash, revisionIn.PackageHash, rev.ID, userID,
		).Scan(scanRevision(&rev)...)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM skill_version_files WHERE source_revision_id = $1 AND user_id = $2`, rev.ID, userID); err != nil {
		return nil, nil, nil, err
	}
	files := make([]SkillVersionFile, 0, len(fileInputs))
	for _, input := range fileInputs {
		var f SkillVersionFile
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_version_files
			 (user_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
			 RETURNING id, user_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at`,
			userID, rev.ID, v.ID, input.Path, input.Kind, input.SHA256, input.SizeBytes, input.MimeType, input.Indexable, input.ContentSnapshot,
		).Scan(scanVersionFile(&f)...)
		if err != nil {
			return nil, nil, nil, err
		}
		files = append(files, f)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	return &rev, &v, files, nil
}

// ─── Stats (for SkillEvolver) ───

type SkillStats struct {
	SkillName      string  `json:"skill_name"`
	TotalRuns      int     `json:"total_runs"`
	SuccessRate    float64 `json:"success_rate"`
	FailureRate    float64 `json:"failure_rate"`
	CorrectionRate float64 `json:"correction_rate"`
}

func (r *Repo) GetSkillStats(ctx context.Context, userID, skillName string) (*SkillStats, error) {
	var stats SkillStats
	stats.SkillName = skillName
	err := r.pool.QueryRow(ctx,
		`SELECT
			COUNT(*),
			COALESCE(AVG(CASE WHEN outcome = 'success' THEN 1.0 ELSE 0.0 END), 0),
			COALESCE(AVG(CASE WHEN outcome = 'failure' THEN 1.0 ELSE 0.0 END), 0),
			COALESCE(AVG(CASE WHEN outcome = 'user_corrected' THEN 1.0 ELSE 0.0 END), 0)
		 FROM skill_logs
		 WHERE user_id = $1 AND skill_name = $2`,
		userID, skillName,
	).Scan(&stats.TotalRuns, &stats.SuccessRate, &stats.FailureRate, &stats.CorrectionRate)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

// SkillSignals returns recent failure/correction logs for a skill (learning signal for SkillEvolver)
func (r *Repo) SkillSignals(ctx context.Context, userID, skillName string, limit int) ([]SkillLog, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms, created_at
		 FROM skill_logs
		 WHERE user_id = $1 AND skill_name = $2 AND outcome IN ('failure', 'user_corrected', 'partial')
		 ORDER BY created_at DESC LIMIT $3`,
		userID, skillName, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillLog, 0)
	for rows.Next() {
		var l SkillLog
		if err := rows.Scan(&l.ID, &l.UserID, &l.SkillName, &l.SkillVersion, &l.AgentID, &l.SessionID, &l.TriggerText,
			&l.WasTriggered, &l.Outcome, &l.FailureReason, &l.UserCorrection, &l.ToolCalls, &l.DurationMs, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, nil
}

func sourceMetadata(raw json.RawMessage) ([]byte, error) {
	if len(raw) == 0 {
		return []byte(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("metadata must be valid JSON")
	}
	return []byte(raw), nil
}

func scanSource(s *SkillSource) []any {
	return []any{
		&s.ID,
		&s.UserID,
		&s.Name,
		&s.Type,
		&s.RepositoryURL,
		&s.PackagePath,
		&s.DefaultRef,
		&s.SyncMode,
		&s.Visibility,
		&s.Status,
		&s.Metadata,
		&s.CreatedAt,
		&s.UpdatedAt,
	}
}

func scanRevision(rev *SkillSourceRevision) []any {
	return []any{
		&rev.ID,
		&rev.UserID,
		&rev.SourceID,
		&rev.SkillVersionID,
		&rev.CommitSHA,
		&rev.LocalSnapshotID,
		&rev.TreeHash,
		&rev.PackageHash,
		&rev.Status,
		&rev.Error,
		&rev.CreatedAt,
	}
}

func scanVersionFile(f *SkillVersionFile) []any {
	return []any{
		&f.ID,
		&f.UserID,
		&f.SourceRevisionID,
		&f.VersionID,
		&f.Path,
		&f.Kind,
		&f.SHA256,
		&f.SizeBytes,
		&f.MimeType,
		&f.Indexable,
		&f.ContentSnapshot,
		&f.CreatedAt,
	}
}
