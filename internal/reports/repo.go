package reports

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Create(ctx context.Context, userID string, req CreateReportRequest, sourceKeyID *string) (*Report, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	format := req.Format
	if format == "" {
		format = "md"
	}
	var rpt Report
	err := r.pool.QueryRow(ctx,
		`INSERT INTO reports (user_id, title, content, format, tags, source, source_key_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, user_id, title, content, format, tags, source, source_key_id, created_at, updated_at`,
		userID, req.Title, req.Content, format, tags, req.Source, sourceKeyID,
	).Scan(&rpt.ID, &rpt.UserID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt)
	return &rpt, err
}

func (r *Repo) Get(ctx context.Context, userID, id string) (*Report, error) {
	var rpt Report
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, title, content, format, tags, source, source_key_id, created_at, updated_at
		 FROM reports WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&rpt.ID, &rpt.UserID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &rpt, nil
}

func (r *Repo) List(ctx context.Context, userID string, params ListReportsParams) ([]Report, error) {
	query := `SELECT id, user_id, title, format, tags, source, source_key_id, created_at, updated_at FROM reports WHERE user_id = $1`
	args := []any{userID}
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
		if err := rows.Scan(&rpt.ID, &rpt.UserID, &rpt.Title, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt); err != nil {
			return nil, err
		}
		reports = append(reports, rpt)
	}
	return reports, nil
}

func (r *Repo) Update(ctx context.Context, userID, id string, req UpdateReportRequest) (*Report, error) {
	existing, err := r.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Source != nil {
		existing.Source = *req.Source
	}
	tags := req.Tags
	if tags == nil {
		tags = existing.Tags
	}
	var rpt Report
	err = r.pool.QueryRow(ctx,
		`UPDATE reports SET title=$3, tags=$4, source=$5, updated_at=now()
		 WHERE id=$1 AND user_id=$2
		 RETURNING id, user_id, title, content, format, tags, source, source_key_id, created_at, updated_at`,
		id, userID, existing.Title, tags, existing.Source,
	).Scan(&rpt.ID, &rpt.UserID, &rpt.Title, &rpt.Content, &rpt.Format, &rpt.Tags, &rpt.Source, &rpt.SourceKeyID, &rpt.CreatedAt, &rpt.UpdatedAt)
	return &rpt, err
}

func (r *Repo) Delete(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM reports WHERE id = $1 AND user_id = $2", id, userID)
	return err
}
