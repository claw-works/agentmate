package bookmarks

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Create(ctx context.Context, userID string, req CreateRequest) (*Bookmark, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var b Bookmark
	err := r.pool.QueryRow(ctx,
		`INSERT INTO bookmarks (user_id, url, title, summary, content, tags, source)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id, user_id, url, title, summary, content, tags, source, is_read, read_at, created_at, updated_at`,
		userID, req.URL, req.Title, req.Summary, req.Content, tags, req.Source,
	).Scan(&b.ID, &b.UserID, &b.URL, &b.Title, &b.Summary, &b.Content, &b.Tags, &b.Source, &b.IsRead, &b.ReadAt, &b.CreatedAt, &b.UpdatedAt)
	return &b, err
}

func (r *Repo) Get(ctx context.Context, userID, id string) (*Bookmark, error) {
	var b Bookmark
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, url, title, summary, content, tags, source, is_read, read_at, created_at, updated_at
		 FROM bookmarks WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&b.ID, &b.UserID, &b.URL, &b.Title, &b.Summary, &b.Content, &b.Tags, &b.Source, &b.IsRead, &b.ReadAt, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

type ListParams struct {
	Tag    string
	IsRead *bool
	Search string
	Limit  int
	Offset int
}

func (r *Repo) Count(ctx context.Context, userID string, params ListParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM bookmarks
		 WHERE user_id = $1
		   AND ($2 = '' OR $2 = ANY(tags))
		   AND ($3::boolean IS NULL OR is_read = $3)
		   AND ($4 = '' OR to_tsvector('simple', coalesce(title,'') || ' ' || coalesce(summary,'') || ' ' || url) @@ plainto_tsquery('simple', $4))`,
		userID, params.Tag, params.IsRead, params.Search,
	).Scan(&count)
	return count, err
}

func (r *Repo) List(ctx context.Context, userID string, params ListParams) ([]Bookmark, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, url, title, summary, tags, source, is_read, read_at, created_at, updated_at
		 FROM bookmarks
		 WHERE user_id = $1
		   AND ($2 = '' OR $2 = ANY(tags))
		   AND ($3::boolean IS NULL OR is_read = $3)
		   AND ($4 = '' OR to_tsvector('simple', coalesce(title,'') || ' ' || coalesce(summary,'') || ' ' || url) @@ plainto_tsquery('simple', $4))
		 ORDER BY created_at DESC LIMIT $5 OFFSET $6`,
		userID, params.Tag, params.IsRead, params.Search, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Bookmark, 0)
	for rows.Next() {
		var b Bookmark
		if err := rows.Scan(&b.ID, &b.UserID, &b.URL, &b.Title, &b.Summary, &b.Tags, &b.Source, &b.IsRead, &b.ReadAt, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, b)
	}
	return items, nil
}

func (r *Repo) Update(ctx context.Context, userID, id string, req UpdateRequest) (*Bookmark, error) {
	existing, err := r.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Summary != nil {
		existing.Summary = *req.Summary
	}
	if req.Content != nil {
		existing.Content = *req.Content
	}
	if req.Source != nil {
		existing.Source = *req.Source
	}
	if req.IsRead != nil {
		existing.IsRead = *req.IsRead
		if *req.IsRead {
			now := time.Now()
			existing.ReadAt = &now
		} else {
			existing.ReadAt = nil
		}
	}
	tags := req.Tags
	if tags == nil {
		tags = existing.Tags
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var b Bookmark
	err = r.pool.QueryRow(ctx,
		`UPDATE bookmarks SET title=$3, summary=$4, content=$5, tags=$6, source=$7, is_read=$8, read_at=$9, updated_at=now()
		 WHERE id=$1 AND user_id=$2
		 RETURNING id, user_id, url, title, summary, content, tags, source, is_read, read_at, created_at, updated_at`,
		id, userID, existing.Title, existing.Summary, existing.Content, tags, existing.Source, existing.IsRead, existing.ReadAt,
	).Scan(&b.ID, &b.UserID, &b.URL, &b.Title, &b.Summary, &b.Content, &b.Tags, &b.Source, &b.IsRead, &b.ReadAt, &b.CreatedAt, &b.UpdatedAt)
	return &b, err
}

func (r *Repo) Delete(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM bookmarks WHERE id = $1 AND user_id = $2", id, userID)
	return err
}
