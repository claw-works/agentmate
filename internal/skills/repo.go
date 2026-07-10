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
	"github.com/wellxie/agentmate/internal/ownership"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ─── Skill Sources ───

func (r *Repo) UpsertSource(ctx context.Context, owner ownership.Owner, req CreateSkillSourceRequest) (*SkillSource, error) {
	metadata, err := sourceMetadata(req.Metadata)
	if err != nil {
		return nil, err
	}
	var s SkillSource
	err = r.pool.QueryRow(ctx,
		`INSERT INTO skill_sources
		 (account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 ON CONFLICT (account_id, type, repository_url, package_path)
		 DO UPDATE SET
		   user_id = EXCLUDED.user_id,
		   key_id = EXCLUDED.key_id,
		   name = EXCLUDED.name,
		   default_ref = EXCLUDED.default_ref,
		   sync_mode = EXCLUDED.sync_mode,
		   visibility = EXCLUDED.visibility,
		   status = EXCLUDED.status,
		   metadata = EXCLUDED.metadata,
		   updated_at = NOW()
		 RETURNING id, account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at`,
		owner.Account(), owner.UserID, owner.KeyID, req.Name, req.Type, req.RepositoryURL, req.PackagePath, req.DefaultRef, req.SyncMode, req.Visibility, req.Status, metadata,
	).Scan(scanSource(&s)...)
	return &s, err
}

func (r *Repo) ListSources(ctx context.Context, accountID string, params SkillSourceListParams) ([]SkillSource, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at
		 FROM skill_sources
		 WHERE account_id = $1
		   AND ($2 = '' OR type = $2)
		   AND ($3 = '' OR status = $3)
		 ORDER BY updated_at DESC, created_at DESC
		 LIMIT $4 OFFSET $5`,
		accountID, params.Type, params.Status, limit, params.Offset,
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

func (r *Repo) GetSource(ctx context.Context, accountID, id string) (*SkillSource, error) {
	var s SkillSource
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at
		 FROM skill_sources
		 WHERE id = $1 AND account_id = $2`,
		id, accountID,
	).Scan(scanSource(&s)...)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repo) ListSourceRevisions(ctx context.Context, accountID, sourceID string, limit, offset int) ([]SkillSourceRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at
		 FROM skill_source_revisions
		 WHERE account_id = $1 AND source_id = $2
		 ORDER BY created_at DESC
		 LIMIT $3 OFFSET $4`,
		accountID, sourceID, limit, offset,
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

func (r *Repo) CreateLog(ctx context.Context, owner ownership.Owner, req CreateLogRequest) (*SkillLog, error) {
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
		`INSERT INTO skill_logs (account_id, user_id, key_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 RETURNING id, account_id, user_id, key_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms, created_at`,
		nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, req.SkillName, version, req.AgentID, req.SessionID, req.TriggerText,
		wasTriggered, req.Outcome, req.FailureReason, req.UserCorrection, toolCalls, req.DurationMs,
	).Scan(&l.ID, &l.AccountID, &l.UserID, &l.KeyID, &l.SkillName, &l.SkillVersion, &l.AgentID, &l.SessionID, &l.TriggerText,
		&l.WasTriggered, &l.Outcome, &l.FailureReason, &l.UserCorrection, &l.ToolCalls, &l.DurationMs, &l.CreatedAt)
	return &l, err
}

func (r *Repo) ListLogs(ctx context.Context, accountID string, params LogListParams) ([]SkillLog, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms, created_at
		 FROM skill_logs
		 WHERE account_id = $1
		   AND ($2 = '' OR skill_name = $2)
		   AND ($3 = '' OR agent_id = $3)
		   AND ($4 = '' OR outcome = $4)
		 ORDER BY created_at DESC LIMIT $5 OFFSET $6`,
		accountID, params.SkillName, params.AgentID, params.Outcome, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillLog, 0)
	for rows.Next() {
		var l SkillLog
		if err := rows.Scan(&l.ID, &l.AccountID, &l.UserID, &l.KeyID, &l.SkillName, &l.SkillVersion, &l.AgentID, &l.SessionID, &l.TriggerText,
			&l.WasTriggered, &l.Outcome, &l.FailureReason, &l.UserCorrection, &l.ToolCalls, &l.DurationMs, &l.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, l)
	}
	return items, nil
}

func (r *Repo) CountLogs(ctx context.Context, accountID string, params LogListParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM skill_logs
		 WHERE account_id = $1
		   AND ($2 = '' OR skill_name = $2)
		   AND ($3 = '' OR agent_id = $3)
		   AND ($4 = '' OR outcome = $4)`,
		accountID, params.SkillName, params.AgentID, params.Outcome,
	).Scan(&count)
	return count, err
}

// ─── Skill Versions ───

func (r *Repo) CreateVersion(ctx context.Context, owner ownership.Owner, req CreateVersionRequest) (*SkillVersion, error) {
	hash := sha256.Sum256([]byte(req.Content))
	contentHash := hex.EncodeToString(hash[:])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if req.Activate {
		_, err = tx.Exec(ctx,
			`UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND account_id = $2 AND is_active = true`,
			req.SkillName, owner.Account())
		if err != nil {
			return nil, err
		}
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`INSERT INTO skill_versions (account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
		nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, req.SkillName, req.Version, req.Content, contentHash, req.AgentID, req.ChangeSummary, req.EvalPassRate, req.Activate,
	).Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListVersions(ctx context.Context, accountID string, params VersionListParams) ([]SkillVersion, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE account_id = $1 AND ($2 = '' OR skill_name = $2)
		 ORDER BY published_at DESC LIMIT $3 OFFSET $4`,
		accountID, params.SkillName, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillVersion, 0)
	for rows.Next() {
		var v SkillVersion
		if err := rows.Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, nil
}

func (r *Repo) GetActiveVersion(ctx context.Context, accountID, skillName string) (*SkillVersion, error) {
	var v SkillVersion
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE account_id = $1 AND skill_name = $2 AND is_active = true`,
		accountID, skillName,
	).Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListActiveVersions(ctx context.Context, accountID, skillName string) ([]SkillVersion, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE account_id = $1 AND is_active = true AND ($2 = '' OR skill_name = $2)
		 ORDER BY skill_name, published_at DESC`,
		accountID, skillName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillVersion, 0)
	for rows.Next() {
		var v SkillVersion
		if err := rows.Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (r *Repo) ActivateVersion(ctx context.Context, accountID, id string) (*SkillVersion, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Get the version to find its skill_name
	var skillName string
	err = tx.QueryRow(ctx, `SELECT skill_name FROM skill_versions WHERE id = $1 AND account_id = $2`, id, accountID).Scan(&skillName)
	if err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND account_id = $2 AND is_active = true`, skillName, accountID)
	if err != nil {
		return nil, err
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`UPDATE skill_versions SET is_active = true WHERE id = $1 AND account_id = $2
		 RETURNING id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
		id, accountID,
	).Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListVersionFiles(ctx context.Context, accountID, versionID string) ([]SkillVersionFile, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM skill_version_files
		 WHERE account_id = $1 AND version_id = $2
		 ORDER BY path`,
		accountID, versionID,
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

func (r *Repo) IngestLocalSnapshot(ctx context.Context, owner ownership.Owner, source *SkillSource, versionReq CreateVersionRequest, revisionIn SkillSourceRevision, fileInputs []SkillVersionFile) (*SkillSourceRevision, *SkillVersion, []SkillVersionFile, error) {
	hash := sha256.Sum256([]byte(versionReq.Content))
	contentHash := hex.EncodeToString(hash[:])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE skill_sources SET status = 'active', updated_at = NOW() WHERE id = $1 AND account_id = $2`,
		source.ID, owner.Account(),
	); err != nil {
		return nil, nil, nil, err
	}

	if versionReq.Activate {
		_, err = tx.Exec(ctx,
			`UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND account_id = $2 AND is_active = true`,
			versionReq.SkillName, owner.Account())
		if err != nil {
			return nil, nil, nil, err
		}
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at
		 FROM skill_versions
		 WHERE account_id = $1 AND skill_name = $2 AND content_hash = $3`,
		owner.Account(), versionReq.SkillName, contentHash,
	).Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_versions (account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			 RETURNING id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
			nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, versionReq.SkillName, versionReq.Version, versionReq.Content, contentHash, versionReq.AgentID, versionReq.ChangeSummary, versionReq.EvalPassRate, versionReq.Activate,
		).Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
		if err != nil {
			return nil, nil, nil, err
		}
	} else if err != nil {
		return nil, nil, nil, err
	} else if versionReq.Activate {
		err = tx.QueryRow(ctx,
			`UPDATE skill_versions SET is_active = true WHERE id = $1 AND account_id = $2
			 RETURNING id, account_id, user_id, key_id, skill_name, version, content, content_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`,
			v.ID, owner.Account(),
		).Scan(&v.ID, &v.AccountID, &v.UserID, &v.KeyID, &v.SkillName, &v.Version, &v.Content, &v.ContentHash, &v.AgentID, &v.ChangeSummary, &v.EvalPassRate, &v.IsActive, &v.PublishedAt)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	var rev SkillSourceRevision
	err = tx.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at
		 FROM skill_source_revisions
		 WHERE account_id = $1 AND source_id = $2 AND local_snapshot_id = $3`,
		owner.Account(), source.ID, revisionIn.LocalSnapshotID,
	).Scan(scanRevision(&rev)...)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_source_revisions
			 (account_id, user_id, key_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error)
			 VALUES ($1, $2, $3, $4, $5, '', $6, $7, $8, 'ingested', '')
			 RETURNING id, account_id, user_id, key_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`,
			owner.Account(), owner.UserID, owner.KeyID, source.ID, v.ID, revisionIn.LocalSnapshotID, revisionIn.TreeHash, revisionIn.PackageHash,
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
			 WHERE id = $4 AND account_id = $5
			 RETURNING id, account_id, user_id, key_id, source_id, skill_version_id, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`,
			v.ID, revisionIn.TreeHash, revisionIn.PackageHash, rev.ID, owner.Account(),
		).Scan(scanRevision(&rev)...)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM skill_version_files WHERE source_revision_id = $1 AND account_id = $2`, rev.ID, owner.Account()); err != nil {
		return nil, nil, nil, err
	}
	files := make([]SkillVersionFile, 0, len(fileInputs))
	for _, input := range fileInputs {
		var f SkillVersionFile
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_version_files
			 (account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 RETURNING id, account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at`,
			owner.Account(), owner.UserID, owner.KeyID, rev.ID, v.ID, input.Path, input.Kind, input.SHA256, input.SizeBytes, input.MimeType, input.Indexable, input.ContentSnapshot,
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

func (r *Repo) GetSkillStats(ctx context.Context, accountID, skillName string) (*SkillStats, error) {
	var stats SkillStats
	stats.SkillName = skillName
	err := r.pool.QueryRow(ctx,
		`SELECT
			COUNT(*),
			COALESCE(AVG(CASE WHEN outcome = 'success' THEN 1.0 ELSE 0.0 END), 0),
			COALESCE(AVG(CASE WHEN outcome = 'failure' THEN 1.0 ELSE 0.0 END), 0),
			COALESCE(AVG(CASE WHEN outcome = 'user_corrected' THEN 1.0 ELSE 0.0 END), 0)
		 FROM skill_logs
		 WHERE account_id = $1 AND skill_name = $2`,
		accountID, skillName,
	).Scan(&stats.TotalRuns, &stats.SuccessRate, &stats.FailureRate, &stats.CorrectionRate)
	if err != nil {
		return nil, err
	}
	return &stats, nil
}

// SkillSignals returns recent failure/correction logs for a skill (learning signal for SkillEvolver)
func (r *Repo) SkillSignals(ctx context.Context, accountID, skillName string, limit int) ([]SkillLog, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, skill_name, skill_version, agent_id, session_id, trigger_text, was_triggered, outcome, failure_reason, user_correction, tool_calls, duration_ms, created_at
		 FROM skill_logs
		 WHERE account_id = $1 AND skill_name = $2 AND outcome IN ('failure', 'user_corrected', 'partial')
		 ORDER BY created_at DESC LIMIT $3`,
		accountID, skillName, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]SkillLog, 0)
	for rows.Next() {
		var l SkillLog
		if err := rows.Scan(&l.ID, &l.AccountID, &l.UserID, &l.KeyID, &l.SkillName, &l.SkillVersion, &l.AgentID, &l.SessionID, &l.TriggerText,
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
		&s.AccountID,
		&s.UserID,
		&s.KeyID,
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
		&rev.AccountID,
		&rev.UserID,
		&rev.KeyID,
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
		&f.AccountID,
		&f.UserID,
		&f.KeyID,
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

func nullableString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
