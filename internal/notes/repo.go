package notes

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Create(ctx context.Context, userID string, req CreateRequest) (*Note, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var n Note
	err := r.pool.QueryRow(ctx,
		`INSERT INTO notes (user_id, title, content, tags)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, user_id, title, content, tags, created_at, updated_at`,
		userID, req.Title, req.Content, tags,
	).Scan(&n.ID, &n.UserID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	return &n, err
}

func (r *Repo) Get(ctx context.Context, userID, id string) (*Note, error) {
	var n Note
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, title, content, tags, created_at, updated_at
		 FROM notes WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&n.ID, &n.UserID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

type ListNotesParams struct {
	Tag string
}

func (r *Repo) List(ctx context.Context, userID string, params ListNotesParams) ([]Note, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, title, content, tags, created_at, updated_at
		 FROM notes WHERE user_id = $1 AND ($2 = '' OR $2 = ANY(tags)) ORDER BY created_at DESC`, userID, params.Tag,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]Note, 0)
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func (r *Repo) Update(ctx context.Context, userID, id string, req UpdateRequest) (*Note, error) {
	existing, err := r.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Content != nil {
		existing.Content = *req.Content
	}
	tags := req.Tags
	if tags == nil {
		tags = existing.Tags
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var n Note
	err = r.pool.QueryRow(ctx,
		`UPDATE notes SET title=$3, content=$4, tags=$5, updated_at=now()
		 WHERE id=$1 AND user_id=$2
		 RETURNING id, user_id, title, content, tags, created_at, updated_at`,
		id, userID, existing.Title, existing.Content, tags,
	).Scan(&n.ID, &n.UserID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	return &n, err
}

func (r *Repo) Delete(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM notes WHERE id = $1 AND user_id = $2", id, userID)
	return err
}

func (r *Repo) Search(ctx context.Context, userID, query string) ([]Note, error) {
	pattern := "%" + query + "%"
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, title, content, tags, created_at, updated_at
		 FROM notes WHERE user_id = $1 AND (title ILIKE $2 OR content ILIKE $2)`, userID, pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]Note, 0)
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.UserID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}
