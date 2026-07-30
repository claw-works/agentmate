package notes

import (
	"context"
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

func (r *Repo) Create(ctx context.Context, owner ownership.Owner, req CreateRequest) (*Note, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var n Note
	err := r.pool.QueryRow(ctx,
		`INSERT INTO notes (account_id, user_id, key_id, title, content, tags)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, account_id, user_id, key_id, title, content, tags, created_at, updated_at`,
		owner.Account(), owner.UserID, owner.KeyID, req.Title, req.Content, tags,
	).Scan(&n.ID, &n.AccountID, &n.UserID, &n.KeyID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	return &n, err
}

func (r *Repo) Get(ctx context.Context, accountID, id string) (*Note, error) {
	var n Note
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, title, content, tags, created_at, updated_at
		 FROM notes WHERE id = $1 AND account_id = $2`, id, accountID,
	).Scan(&n.ID, &n.AccountID, &n.UserID, &n.KeyID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

type ListNotesParams struct {
	Tags   []string
	Limit  int
	Offset int
}

func (r *Repo) Count(ctx context.Context, accountID string, params ListNotesParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM notes WHERE account_id = $1 AND ($2::text[] = '{}' OR tags @> $2::text[])`,
		accountID, params.Tags,
	).Scan(&count)
	return count, err
}

func (r *Repo) List(ctx context.Context, accountID string, params ListNotesParams) ([]Note, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, title, content, tags, created_at, updated_at
		 FROM notes WHERE account_id = $1 AND ($2::text[] = '{}' OR tags @> $2::text[])
		 ORDER BY created_at DESC LIMIT $3 OFFSET $4`, accountID, params.Tags, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]Note, 0)
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.AccountID, &n.UserID, &n.KeyID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}

func (r *Repo) Update(ctx context.Context, accountID, id string, req UpdateRequest) (*Note, error) {
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
		 WHERE id=$1 AND account_id=$2
		 RETURNING id, account_id, user_id, key_id, title, content, tags, created_at, updated_at`,
		id, accountID, existing.Title, existing.Content, tags,
	).Scan(&n.ID, &n.AccountID, &n.UserID, &n.KeyID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	return &n, err
}

func (r *Repo) Append(ctx context.Context, id, accountID, content string) (*Note, error) {
	var n Note
	err := r.pool.QueryRow(ctx,
		`UPDATE notes
		 SET content = content || $3, updated_at = now()
		 WHERE id = $1 AND account_id = $2
		 RETURNING id, account_id, user_id, key_id, title, content, tags, created_at, updated_at`,
		id, accountID, content,
	).Scan(&n.ID, &n.AccountID, &n.UserID, &n.KeyID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *Repo) Delete(ctx context.Context, accountID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM notes WHERE id = $1 AND account_id = $2", id, accountID)
	return err
}

func (r *Repo) Search(ctx context.Context, accountID, query string) ([]Note, error) {
	pattern := "%" + query + "%"
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, title, content, tags, created_at, updated_at
		 FROM notes WHERE account_id = $1 AND (title ILIKE $2 OR content ILIKE $2)`, accountID, pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notes := make([]Note, 0)
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.AccountID, &n.UserID, &n.KeyID, &n.Title, &n.Content, &n.Tags, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		notes = append(notes, n)
	}
	return notes, nil
}
