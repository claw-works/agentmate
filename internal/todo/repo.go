package todo

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

func (r *Repo) Create(ctx context.Context, userID string, req CreateRequest) (*Todo, error) {
	var t Todo
	var dueDate *time.Time
	if req.DueDate != "" {
		d, _ := time.Parse(time.RFC3339, req.DueDate)
		dueDate = &d
	}
	priority := req.Priority
	if priority == "" {
		priority = "medium"
	}
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO todos (user_id, title, description, priority, due_date, tags)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, user_id, title, description, status, priority, due_date, tags, created_at, updated_at`,
		userID, req.Title, req.Description, priority, dueDate, tags,
	).Scan(&t.ID, &t.UserID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.DueDate, &t.Tags, &t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

func (r *Repo) Get(ctx context.Context, userID, id string) (*Todo, error) {
	var t Todo
	err := r.pool.QueryRow(ctx,
		`SELECT id, user_id, title, description, status, priority, due_date, tags, created_at, updated_at
		 FROM todos WHERE id = $1 AND user_id = $2`, id, userID,
	).Scan(&t.ID, &t.UserID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.DueDate, &t.Tags, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

type ListTodosParams struct {
	Tags   []string
	Status string
	Limit  int
	Offset int
}

func (r *Repo) Count(ctx context.Context, userID string, params ListTodosParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM todos WHERE user_id = $1 AND ($2::text[] = '{}' OR tags && $2::text[]) AND ($3 = '' OR status = $3)`,
		userID, params.Tags, params.Status,
	).Scan(&count)
	return count, err
}

func (r *Repo) List(ctx context.Context, userID string, params ListTodosParams) ([]Todo, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, title, description, status, priority, due_date, tags, created_at, updated_at
		 FROM todos WHERE user_id = $1 AND ($2::text[] = '{}' OR tags && $2::text[]) AND ($3 = '' OR status = $3)
		 ORDER BY created_at DESC LIMIT $4 OFFSET $5`, userID, params.Tags, params.Status, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	todos := make([]Todo, 0)
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.UserID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.DueDate, &t.Tags, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	return todos, nil
}

func (r *Repo) Update(ctx context.Context, userID, id string, req UpdateRequest) (*Todo, error) {
	existing, err := r.Get(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	if req.Title != nil {
		existing.Title = *req.Title
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Status != nil {
		existing.Status = *req.Status
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	var dueDate *time.Time
	if req.DueDate != nil {
		d, _ := time.Parse(time.RFC3339, *req.DueDate)
		dueDate = &d
	} else {
		dueDate = existing.DueDate
	}
	tags := req.Tags
	if tags == nil {
		tags = existing.Tags
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var t Todo
	err = r.pool.QueryRow(ctx,
		`UPDATE todos SET title=$3, description=$4, status=$5, priority=$6, due_date=$7, tags=$8, updated_at=now()
		 WHERE id=$1 AND user_id=$2
		 RETURNING id, user_id, title, description, status, priority, due_date, tags, created_at, updated_at`,
		id, userID, existing.Title, existing.Description, existing.Status, existing.Priority, dueDate, tags,
	).Scan(&t.ID, &t.UserID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.DueDate, &t.Tags, &t.CreatedAt, &t.UpdatedAt)
	return &t, err
}

func (r *Repo) Delete(ctx context.Context, userID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM todos WHERE id = $1 AND user_id = $2", id, userID)
	return err
}

func (r *Repo) Search(ctx context.Context, userID, query string) ([]Todo, error) {
	pattern := "%" + query + "%"
	rows, err := r.pool.Query(ctx,
		`SELECT id, user_id, title, description, status, priority, due_date, tags, created_at, updated_at
		 FROM todos WHERE user_id = $1 AND (title ILIKE $2 OR description ILIKE $2)`, userID, pattern,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	todos := make([]Todo, 0)
	for rows.Next() {
		var t Todo
		if err := rows.Scan(&t.ID, &t.UserID, &t.Title, &t.Description, &t.Status, &t.Priority, &t.DueDate, &t.Tags, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		todos = append(todos, t)
	}
	return todos, nil
}
