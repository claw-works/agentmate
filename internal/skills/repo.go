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

const skillVersionColumns = `id, account_id, user_id, key_id, source_id, source_revision_id, skill_name, version, content, content_hash, package_hash, agent_id, change_summary, eval_pass_rate, is_active, published_at`
const skillRevisionColumns = `id, account_id, user_id, key_id, source_id, skill_version_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`

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

func (r *Repo) UpdateSourceSyncState(ctx context.Context, accountID, sourceID, status string, state GitSourceSyncState) (*SkillSource, error) {
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	var source SkillSource
	err = r.pool.QueryRow(ctx,
		`UPDATE skill_sources
		 SET status = $3,
		     metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('git_sync', $4::jsonb),
		     updated_at = NOW()
		 WHERE id = $1 AND account_id = $2
		 RETURNING id, account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at`,
		sourceID, accountID, status, string(stateJSON),
	).Scan(scanSource(&source)...)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func (r *Repo) ListSourceRevisions(ctx context.Context, accountID, sourceID string, limit, offset int) ([]SkillSourceRevision, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, source_id, skill_version_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at
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

	if err := lockSkillTx(ctx, tx, owner.Account(), req.SkillName); err != nil {
		return nil, err
	}

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
		`INSERT INTO skill_versions
		 (account_id, user_id, key_id, source_id, source_revision_id, skill_name, version, content, content_hash, package_hash, agent_id, change_summary, eval_pass_rate, is_active)
		 VALUES ($1, $2, $3, NULL, NULL, $4, $5, $6, $7, $7, $8, $9, $10, $11)
		 RETURNING `+skillVersionColumns,
		nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, req.SkillName, req.Version, req.Content, contentHash, req.AgentID, req.ChangeSummary, req.EvalPassRate, req.Activate,
	).Scan(scanVersion(&v)...)
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
		`SELECT `+skillVersionColumns+`
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
		if err := rows.Scan(scanVersion(&v)...); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (r *Repo) GetActiveVersion(ctx context.Context, accountID, skillName string) (*SkillVersion, error) {
	var v SkillVersion
	err := r.pool.QueryRow(ctx,
		`SELECT `+skillVersionColumns+`
		 FROM skill_versions
		 WHERE account_id = $1 AND skill_name = $2 AND is_active = true`,
		accountID, skillName,
	).Scan(scanVersion(&v)...)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *Repo) ListActiveVersions(ctx context.Context, accountID, skillName string) ([]SkillVersion, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+skillVersionColumns+`
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
		if err := rows.Scan(scanVersion(&v)...); err != nil {
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

	var skillName string
	err = tx.QueryRow(ctx, `SELECT skill_name FROM skill_versions WHERE id = $1 AND account_id = $2`, id, accountID).Scan(&skillName)
	if err != nil {
		return nil, err
	}
	if err := lockSkillTx(ctx, tx, accountID, skillName); err != nil {
		return nil, err
	}

	_, err = tx.Exec(ctx, `UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND account_id = $2 AND is_active = true`, skillName, accountID)
	if err != nil {
		return nil, err
	}

	var v SkillVersion
	err = tx.QueryRow(ctx,
		`UPDATE skill_versions SET is_active = true WHERE id = $1 AND account_id = $2
		 RETURNING `+skillVersionColumns,
		id, accountID,
	).Scan(scanVersion(&v)...)
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
		`SELECT file.id, file.account_id, file.user_id, file.key_id, file.source_revision_id, file.version_id, file.path, file.kind, file.sha256, file.size_bytes, file.mime_type, file.indexable, file.content_snapshot, file.created_at
		 FROM skill_version_files AS file
		 JOIN skill_versions AS version ON version.source_revision_id = file.source_revision_id
		 WHERE version.account_id = $1 AND file.account_id = $1 AND version.id = $2
		 ORDER BY file.path`,
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

func (r *Repo) IngestSourceRevision(ctx context.Context, owner ownership.Owner, source *SkillSource, versionReq CreateVersionRequest, revisionIn SkillSourceRevision, fileInputs []SkillVersionFile, syncState *GitSourceSyncState) (*SkillSourceRevision, *SkillVersion, []SkillVersionFile, error) {
	if revisionIn.RevisionKey == "" {
		return nil, nil, nil, fmt.Errorf("revision_key required")
	}

	hash := sha256.Sum256([]byte(versionReq.Content))
	contentHash := hex.EncodeToString(hash[:])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, nil, nil, err
	}
	defer tx.Rollback(ctx)

	if err := lockSkillTx(ctx, tx, owner.Account(), versionReq.SkillName); err != nil {
		return nil, nil, nil, err
	}
	if err := lockSourceTx(ctx, tx, source.ID); err != nil {
		return nil, nil, nil, err
	}

	updatedSource, err := updateSourceActiveTx(ctx, tx, owner.Account(), source.ID, syncState)
	if err != nil {
		return nil, nil, nil, err
	}

	rev, err := findRevisionByIdentityTx(ctx, tx, owner.Account(), source.ID, revisionIn.RevisionKey, revisionIn.LocalSnapshotID, revisionIn.CommitSHA)
	if err == nil {
		if rev.PackageHash != revisionIn.PackageHash || rev.TreeHash != revisionIn.TreeHash {
			return nil, nil, nil, fmt.Errorf("snapshot identity already exists with different package content")
		}
		if rev.SkillVersionID == nil {
			return nil, nil, nil, fmt.Errorf("source revision %s is missing its skill version", rev.ID)
		}

		var version SkillVersion
		err = tx.QueryRow(ctx,
			`SELECT `+skillVersionColumns+` FROM skill_versions WHERE id = $1 AND account_id = $2`,
			*rev.SkillVersionID, owner.Account(),
		).Scan(scanVersion(&version)...)
		if err != nil {
			return nil, nil, nil, err
		}
		if version.SourceID == nil || *version.SourceID != source.ID ||
			version.SourceRevisionID == nil || *version.SourceRevisionID != rev.ID ||
			version.PackageHash != rev.PackageHash {
			return nil, nil, nil, fmt.Errorf("source revision %s has inconsistent package identity", rev.ID)
		}
		if version.SkillName != versionReq.SkillName {
			return nil, nil, nil, fmt.Errorf("source revision already belongs to skill %s", version.SkillName)
		}
		if versionReq.Activate && !version.IsActive {
			if _, err := tx.Exec(ctx,
				`UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND account_id = $2 AND is_active = true`,
				version.SkillName, owner.Account(),
			); err != nil {
				return nil, nil, nil, err
			}
			err = tx.QueryRow(ctx,
				`UPDATE skill_versions SET is_active = true WHERE id = $1 AND account_id = $2 RETURNING `+skillVersionColumns,
				version.ID, owner.Account(),
			).Scan(scanVersion(&version)...)
			if err != nil {
				return nil, nil, nil, err
			}
		}

		files, err := listRevisionFilesTx(ctx, tx, owner.Account(), rev.ID)
		if err != nil {
			return nil, nil, nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, nil, err
		}
		*source = *updatedSource
		return rev, &version, files, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil, err
	}

	rev = &SkillSourceRevision{}
	err = tx.QueryRow(ctx,
		`INSERT INTO skill_source_revisions
		 (account_id, user_id, key_id, source_id, skill_version_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error)
		 VALUES ($1, $2, $3, $4, NULL, $5, $6, $7, $8, $9, 'ingested', '')
		 RETURNING id, account_id, user_id, key_id, source_id, skill_version_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`,
		owner.Account(), owner.UserID, owner.KeyID, source.ID, revisionIn.RevisionKey, revisionIn.CommitSHA, revisionIn.LocalSnapshotID, revisionIn.TreeHash, revisionIn.PackageHash,
	).Scan(scanRevision(rev)...)
	if err != nil {
		return nil, nil, nil, err
	}
	if revisionIn.LocalSnapshotID != "" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO skill_source_revision_aliases
			 (account_id, user_id, key_id, source_id, revision_id, local_snapshot_id)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			owner.Account(), owner.UserID, owner.KeyID, source.ID, rev.ID, revisionIn.LocalSnapshotID,
		); err != nil {
			return nil, nil, nil, err
		}
	}

	if versionReq.Activate {
		if _, err := tx.Exec(ctx,
			`UPDATE skill_versions SET is_active = false WHERE skill_name = $1 AND account_id = $2 AND is_active = true`,
			versionReq.SkillName, owner.Account(),
		); err != nil {
			return nil, nil, nil, err
		}
	}

	var version SkillVersion
	err = tx.QueryRow(ctx,
		`INSERT INTO skill_versions
		 (account_id, user_id, key_id, source_id, source_revision_id, skill_name, version, content, content_hash, package_hash, agent_id, change_summary, eval_pass_rate, is_active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		 RETURNING `+skillVersionColumns,
		nullableString(owner.Account()), nullableString(owner.UserID), owner.KeyID, source.ID, rev.ID, versionReq.SkillName, versionReq.Version, versionReq.Content, contentHash, revisionIn.PackageHash, versionReq.AgentID, versionReq.ChangeSummary, versionReq.EvalPassRate, versionReq.Activate,
	).Scan(scanVersion(&version)...)
	if err != nil {
		return nil, nil, nil, err
	}

	err = tx.QueryRow(ctx,
		`UPDATE skill_source_revisions
		 SET skill_version_id = $1
		 WHERE id = $2 AND account_id = $3
		 RETURNING id, account_id, user_id, key_id, source_id, skill_version_id, revision_key, commit_sha, local_snapshot_id, tree_hash, package_hash, status, error, created_at`,
		version.ID, rev.ID, owner.Account(),
	).Scan(scanRevision(rev)...)
	if err != nil {
		return nil, nil, nil, err
	}

	files := make([]SkillVersionFile, 0, len(fileInputs))
	for _, input := range fileInputs {
		var file SkillVersionFile
		err = tx.QueryRow(ctx,
			`INSERT INTO skill_version_files
			 (account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			 RETURNING id, account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at`,
			owner.Account(), owner.UserID, owner.KeyID, rev.ID, version.ID, input.Path, input.Kind, input.SHA256, input.SizeBytes, input.MimeType, input.Indexable, input.ContentSnapshot,
		).Scan(scanVersionFile(&file)...)
		if err != nil {
			return nil, nil, nil, err
		}
		files = append(files, file)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, nil, err
	}
	*source = *updatedSource
	return rev, &version, files, nil
}

func updateSourceActiveTx(ctx context.Context, tx pgx.Tx, accountID, sourceID string, syncState *GitSourceSyncState) (*SkillSource, error) {
	var source SkillSource
	if syncState == nil {
		err := tx.QueryRow(ctx,
			`UPDATE skill_sources
			 SET status = 'active', updated_at = NOW()
			 WHERE id = $1 AND account_id = $2
			 RETURNING id, account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at`,
			sourceID, accountID,
		).Scan(scanSource(&source)...)
		if err != nil {
			return nil, err
		}
		return &source, nil
	}

	stateJSON, err := json.Marshal(syncState)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRow(ctx,
		`UPDATE skill_sources
		 SET status = 'active',
		     metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object('git_sync', $3::jsonb),
		     updated_at = NOW()
		 WHERE id = $1 AND account_id = $2
		 RETURNING id, account_id, user_id, key_id, name, type, repository_url, package_path, default_ref, sync_mode, visibility, status, metadata, created_at, updated_at`,
		sourceID, accountID, string(stateJSON),
	).Scan(scanSource(&source)...)
	if err != nil {
		return nil, err
	}
	return &source, nil
}

func findRevisionByIdentityTx(ctx context.Context, tx pgx.Tx, accountID, sourceID, revisionKey, localSnapshotID, commitSHA string) (*SkillSourceRevision, error) {
	byKey, err := getRevisionByIdentityTx(ctx, tx, accountID, sourceID, "revision_key", revisionKey)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}

	var bySnapshot *SkillSourceRevision
	if localSnapshotID != "" {
		bySnapshot, err = getRevisionBySnapshotAliasTx(ctx, tx, accountID, sourceID, localSnapshotID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	var byCommit *SkillSourceRevision
	if commitSHA != "" {
		byCommit, err = getRevisionByIdentityTx(ctx, tx, accountID, sourceID, "commit_sha", commitSHA)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}

	if byKey != nil && bySnapshot != nil && byKey.ID != bySnapshot.ID {
		return nil, fmt.Errorf("revision key and local snapshot ID refer to different source revisions")
	}
	if byKey != nil && byCommit != nil && byKey.ID != byCommit.ID {
		return nil, fmt.Errorf("revision key and commit SHA refer to different source revisions")
	}
	if byKey != nil {
		if localSnapshotID != "" && bySnapshot == nil && byKey.LocalSnapshotID != localSnapshotID {
			return nil, fmt.Errorf("revision key is already bound to a different local snapshot ID")
		}
		return byKey, nil
	}
	if bySnapshot != nil {
		if bySnapshot.RevisionKey != revisionKey {
			return nil, fmt.Errorf("local snapshot ID is already bound to a different revision key")
		}
		return bySnapshot, nil
	}
	if byCommit != nil {
		return nil, fmt.Errorf("commit SHA is already bound to a different revision key")
	}
	return nil, pgx.ErrNoRows
}

func getRevisionBySnapshotAliasTx(ctx context.Context, tx pgx.Tx, accountID, sourceID, localSnapshotID string) (*SkillSourceRevision, error) {
	var revision SkillSourceRevision
	err := tx.QueryRow(ctx,
		`SELECT `+skillRevisionColumns+`
		 FROM skill_source_revisions
		 WHERE account_id = $1
		   AND source_id = $2
		   AND id = (
		     SELECT revision_id
		     FROM skill_source_revision_aliases
		     WHERE account_id = $1 AND source_id = $2 AND local_snapshot_id = $3
		   )`,
		accountID, sourceID, localSnapshotID,
	).Scan(scanRevision(&revision)...)
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func getRevisionByIdentityTx(ctx context.Context, tx pgx.Tx, accountID, sourceID, column, value string) (*SkillSourceRevision, error) {
	var revision SkillSourceRevision
	err := tx.QueryRow(ctx,
		`SELECT `+skillRevisionColumns+` FROM skill_source_revisions WHERE account_id = $1 AND source_id = $2 AND `+column+` = $3`,
		accountID, sourceID, value,
	).Scan(scanRevision(&revision)...)
	if err != nil {
		return nil, err
	}
	return &revision, nil
}

func lockSkillTx(ctx context.Context, tx pgx.Tx, accountID, skillName string) error {
	_, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		fmt.Sprintf("skill-version:%d:%s:%s", len(accountID), accountID, skillName),
	)
	return err
}

func lockSourceTx(ctx context.Context, tx pgx.Tx, sourceID string) error {
	_, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"skill-source:"+sourceID,
	)
	return err
}

func listRevisionFilesTx(ctx context.Context, tx pgx.Tx, accountID, revisionID string) ([]SkillVersionFile, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, account_id, user_id, key_id, source_revision_id, version_id, path, kind, sha256, size_bytes, mime_type, indexable, content_snapshot, created_at
		 FROM skill_version_files
		 WHERE account_id = $1 AND source_revision_id = $2
		 ORDER BY path`,
		accountID, revisionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	files := make([]SkillVersionFile, 0)
	for rows.Next() {
		var file SkillVersionFile
		if err := rows.Scan(scanVersionFile(&file)...); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, rows.Err()
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
		&rev.RevisionKey,
		&rev.CommitSHA,
		&rev.LocalSnapshotID,
		&rev.TreeHash,
		&rev.PackageHash,
		&rev.Status,
		&rev.Error,
		&rev.CreatedAt,
	}
}

func scanVersion(version *SkillVersion) []any {
	return []any{
		&version.ID,
		&version.AccountID,
		&version.UserID,
		&version.KeyID,
		&version.SourceID,
		&version.SourceRevisionID,
		&version.SkillName,
		&version.Version,
		&version.Content,
		&version.ContentHash,
		&version.PackageHash,
		&version.AgentID,
		&version.ChangeSummary,
		&version.EvalPassRate,
		&version.IsActive,
		&version.PublishedAt,
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
