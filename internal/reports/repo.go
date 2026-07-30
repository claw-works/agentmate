package reports

import (
	"context"
	"fmt"
	"strings"

	"github.com/claw-works/agentmate/internal/ownership"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Create(ctx context.Context, owner ownership.Owner, req CreateReportRequest) (*Report, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	format := req.Format
	if format == "" {
		format = "md"
	}
	var rpt Report
	err := r.pool.QueryRow(ctx,
		`INSERT INTO reports (account_id, user_id, key_id, title, content, format, tags, source, source_key_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $3)
		 RETURNING id, account_id, user_id, key_id, title, content, format, tags, source, source_key_id, created_at, updated_at`,
		owner.Account(), owner.UserID, owner.KeyID, req.Title, req.Content, format, tags, req.Source,
	).Scan(&rpt.ID, &rpt.AccountID, &rpt.UserID, &rpt.KeyID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt)
	return &rpt, err
}

func (r *Repo) Get(ctx context.Context, accountID, id string) (*Report, error) {
	var rpt Report
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, title, content, format, tags, source, source_key_id, created_at, updated_at
		 FROM reports WHERE id = $1 AND account_id = $2`, id, accountID,
	).Scan(&rpt.ID, &rpt.AccountID, &rpt.UserID, &rpt.KeyID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &rpt, nil
}

func (r *Repo) PublicGet(ctx context.Context, id string) (*PublicReport, error) {
	var rpt PublicReport
	err := r.pool.QueryRow(ctx,
		`SELECT id, title, content, format, tags, source, created_at, updated_at
		 FROM reports WHERE id = $1`, id,
	).Scan(&rpt.ID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.CreatedAt, &rpt.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &rpt, nil
}

func (r *Repo) Count(ctx context.Context, accountID string, params ListReportsParams) (int, error) {
	query := `SELECT count(*) FROM reports WHERE account_id = $1`
	args := []any{accountID}
	argIdx := 2
	if params.Tag != "" {
		query += fmt.Sprintf(" AND $%d = ANY(tags)", argIdx)
		args = append(args, params.Tag)
		argIdx++
	}
	if params.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, params.Source)
		argIdx++
	}
	if params.Search != "" {
		query += fmt.Sprintf(" AND to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', $%d)", argIdx)
		args = append(args, params.Search)
		argIdx++
	}
	var count int
	err := r.pool.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

func (r *Repo) PublicCount(ctx context.Context, params ListReportsParams) (int, error) {
	query := `SELECT count(*) FROM reports WHERE true`
	args := []any{}
	argIdx := 1
	if params.Tag != "" {
		query += fmt.Sprintf(" AND $%d = ANY(tags)", argIdx)
		args = append(args, params.Tag)
		argIdx++
	}
	if params.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, params.Source)
		argIdx++
	}
	if params.Search != "" {
		query += fmt.Sprintf(" AND to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', $%d)", argIdx)
		args = append(args, params.Search)
	}
	var count int
	err := r.pool.QueryRow(ctx, query, args...).Scan(&count)
	return count, err
}

func (r *Repo) List(ctx context.Context, accountID string, params ListReportsParams) ([]Report, error) {
	query := `SELECT id, account_id, user_id, key_id, title, format, tags, source, source_key_id, created_at, updated_at FROM reports WHERE account_id = $1`
	args := []any{accountID}
	argIdx := 2

	if params.Tag != "" {
		query += fmt.Sprintf(" AND $%d = ANY(tags)", argIdx)
		args = append(args, params.Tag)
		argIdx++
	}
	if params.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, params.Source)
		argIdx++
	}
	if params.Search != "" {
		query += fmt.Sprintf(" AND to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', $%d)", argIdx)
		args = append(args, params.Search)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)
	argIdx++

	if params.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, params.Offset)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := make([]Report, 0)
	for rows.Next() {
		var rpt Report
		if err := rows.Scan(&rpt.ID, &rpt.AccountID, &rpt.UserID, &rpt.KeyID, &rpt.Title, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt); err != nil {
			return nil, err
		}
		reports = append(reports, rpt)
	}
	return reports, nil
}

func (r *Repo) PublicList(ctx context.Context, params ListReportsParams) ([]PublicReport, error) {
	query := `SELECT id, title, content, format, tags, source, created_at, updated_at FROM reports WHERE true`
	args := []any{}
	argIdx := 1

	if params.Tag != "" {
		query += fmt.Sprintf(" AND $%d = ANY(tags)", argIdx)
		args = append(args, params.Tag)
		argIdx++
	}
	if params.Source != "" {
		query += fmt.Sprintf(" AND source = $%d", argIdx)
		args = append(args, params.Source)
		argIdx++
	}
	if params.Search != "" {
		query += fmt.Sprintf(" AND to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, '')) @@ plainto_tsquery('simple', $%d)", argIdx)
		args = append(args, params.Search)
		argIdx++
	}

	query += " ORDER BY created_at DESC"

	limit := params.Limit
	if limit <= 0 {
		limit = 5
	} else if limit > 20 {
		limit = 20
	}
	query += fmt.Sprintf(" LIMIT $%d", argIdx)
	args = append(args, limit)
	argIdx++

	if params.Offset > 0 {
		query += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, params.Offset)
	}

	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reports := make([]PublicReport, 0)
	for rows.Next() {
		var rpt PublicReport
		if err := rows.Scan(&rpt.ID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.CreatedAt, &rpt.UpdatedAt); err != nil {
			return nil, err
		}
		reports = append(reports, rpt)
	}
	return reports, nil
}

func (r *Repo) Update(ctx context.Context, accountID, id string, req UpdateReportRequest) (*Report, error) {
	existing, err := r.Get(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Content != nil {
		existing.Content = *req.Content
	}
	if req.Source != nil {
		existing.Source = *req.Source
	}
	tags := req.Tags
	if tags == nil {
		tags = existing.Tags
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var rpt Report
	err = r.pool.QueryRow(ctx,
		`UPDATE reports SET title=$3, content=$4, tags=$5, source=$6, updated_at=now()
		 WHERE id=$1 AND account_id=$2
		 RETURNING id, account_id, user_id, key_id, title, content, format, tags, source, source_key_id, created_at, updated_at`,
		id, accountID, existing.Title, existing.Content, tags, existing.Source,
	).Scan(&rpt.ID, &rpt.AccountID, &rpt.UserID, &rpt.KeyID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt)
	return &rpt, err
}

func (r *Repo) Delete(ctx context.Context, accountID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM reports WHERE id = $1 AND account_id = $2", id, accountID)
	return err
}

type SourceStat struct {
	Source string `json:"source"`
	Count  int    `json:"count"`
}

func (r *Repo) ListSources(ctx context.Context, accountID string) ([]SourceStat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT source, count(*) as count FROM reports
		 WHERE account_id = $1 AND source != ''
		 GROUP BY source ORDER BY count DESC, source`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SourceStat, 0)
	for rows.Next() {
		var s SourceStat
		if err := rows.Scan(&s.Source, &s.Count); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}

func (r *Repo) PublicListSources(ctx context.Context) ([]SourceStat, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT source, count(*) as count FROM reports
		 WHERE source != ''
		 GROUP BY source ORDER BY count DESC, source`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]SourceStat, 0)
	for rows.Next() {
		var s SourceStat
		if err := rows.Scan(&s.Source, &s.Count); err != nil {
			return nil, err
		}
		result = append(result, s)
	}
	return result, nil
}
