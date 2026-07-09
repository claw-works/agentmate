package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
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
