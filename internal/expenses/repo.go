package expenses

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/ownership"
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Create(ctx context.Context, owner ownership.Owner, req CreateRequest) (*Expense, error) {
	tags := req.Tags
	if tags == nil {
		tags = []string{}
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	currency := req.Currency
	if currency == "" {
		currency = "CNY"
	}
	var happenedAt *time.Time
	if req.HappenedAt != "" {
		t, err := time.Parse(time.RFC3339, req.HappenedAt)
		if err != nil {
			return nil, err
		}
		happenedAt = &t
	}
	var e Expense
	err := r.pool.QueryRow(ctx,
		`INSERT INTO expenses (account_id, user_id, key_id, amount, currency, description, tags, source, happened_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()))
		 RETURNING id, account_id, user_id, key_id, amount, currency, description, tags, source, happened_at, created_at, updated_at`,
		owner.Account(), owner.UserID, owner.KeyID, req.Amount, currency, req.Description, tags, req.Source, happenedAt,
	).Scan(&e.ID, &e.AccountID, &e.UserID, &e.KeyID, &e.Amount, &e.Currency, &e.Description, &e.Tags, &e.Source, &e.HappenedAt, &e.CreatedAt, &e.UpdatedAt)
	return &e, err
}

func (r *Repo) Get(ctx context.Context, accountID, id string) (*Expense, error) {
	var e Expense
	err := r.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, key_id, amount, currency, description, tags, source, happened_at, created_at, updated_at
		 FROM expenses WHERE id = $1 AND account_id = $2`, id, accountID,
	).Scan(&e.ID, &e.AccountID, &e.UserID, &e.KeyID, &e.Amount, &e.Currency, &e.Description, &e.Tags, &e.Source, &e.HappenedAt, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &e, nil
}

func (r *Repo) Count(ctx context.Context, accountID string, params ListParams) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT count(*) FROM expenses
		 WHERE account_id = $1
		   AND ($2::text[] = '{}' OR tags @> $2::text[])
		   AND ($3 = '' OR description ILIKE '%' || $3 || '%')
		   AND ($4 = '' OR happened_at >= $4::timestamptz)
		   AND ($5 = '' OR happened_at <= $5::timestamptz)`,
		accountID, params.Tags, params.Search, params.Start, params.End,
	).Scan(&count)
	return count, err
}

func (r *Repo) List(ctx context.Context, accountID string, params ListParams) ([]Expense, error) {
	limit := params.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, account_id, user_id, key_id, amount, currency, description, tags, source, happened_at, created_at, updated_at
		 FROM expenses
		 WHERE account_id = $1
		   AND ($2::text[] = '{}' OR tags @> $2::text[])
		   AND ($3 = '' OR description ILIKE '%' || $3 || '%')
		   AND ($4 = '' OR happened_at >= $4::timestamptz)
		   AND ($5 = '' OR happened_at <= $5::timestamptz)
		 ORDER BY happened_at DESC LIMIT $6 OFFSET $7`,
		accountID, params.Tags, params.Search, params.Start, params.End, limit, params.Offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Expense, 0)
	for rows.Next() {
		var e Expense
		if err := rows.Scan(&e.ID, &e.AccountID, &e.UserID, &e.KeyID, &e.Amount, &e.Currency, &e.Description, &e.Tags, &e.Source, &e.HappenedAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, e)
	}
	return items, nil
}

type Summary struct {
	Total    float64            `json:"total"`
	Count    int                `json:"count"`
	Currency string             `json:"currency"`
	ByTag    map[string]float64 `json:"by_tag"`
}

func (r *Repo) Summary(ctx context.Context, accountID string, params ListParams) (*Summary, error) {
	var total float64
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(amount),0), COUNT(*) FROM expenses
		 WHERE account_id = $1
		   AND ($2::text[] = '{}' OR tags @> $2::text[])
		   AND ($3 = '' OR happened_at >= $3::timestamptz)
		   AND ($4 = '' OR happened_at <= $4::timestamptz)`,
		accountID, params.Tags, params.Start, params.End,
	).Scan(&total, &count)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx,
		`SELECT tag, COALESCE(SUM(amount),0) FROM expenses, unnest(tags) AS tag
		 WHERE account_id = $1
		   AND ($2::text[] = '{}' OR tags @> $2::text[])
		   AND ($3 = '' OR happened_at >= $3::timestamptz)
		   AND ($4 = '' OR happened_at <= $4::timestamptz)
		 GROUP BY tag ORDER BY SUM(amount) DESC`,
		accountID, params.Tags, params.Start, params.End,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byTag := make(map[string]float64)
	for rows.Next() {
		var tag string
		var amt float64
		if err := rows.Scan(&tag, &amt); err != nil {
			return nil, err
		}
		byTag[tag] = amt
	}
	return &Summary{Total: total, Count: count, Currency: "CNY", ByTag: byTag}, nil
}

func (r *Repo) Update(ctx context.Context, accountID, id string, req UpdateRequest) (*Expense, error) {
	existing, err := r.Get(ctx, accountID, id)
	if err != nil {
		return nil, err
	}
	if req.Amount != nil {
		existing.Amount = *req.Amount
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.HappenedAt != nil {
		t, err := time.Parse(time.RFC3339, *req.HappenedAt)
		if err != nil {
			return nil, err
		}
		existing.HappenedAt = t
	}
	tags := req.Tags
	if tags == nil {
		tags = existing.Tags
	}
	for i, tag := range tags {
		tags[i] = strings.ToLower(strings.TrimSpace(tag))
	}
	var e Expense
	err = r.pool.QueryRow(ctx,
		`UPDATE expenses SET amount=$3, description=$4, tags=$5, happened_at=$6, updated_at=now()
		 WHERE id=$1 AND account_id=$2
		 RETURNING id, account_id, user_id, key_id, amount, currency, description, tags, source, happened_at, created_at, updated_at`,
		id, accountID, existing.Amount, existing.Description, tags, existing.HappenedAt,
	).Scan(&e.ID, &e.AccountID, &e.UserID, &e.KeyID, &e.Amount, &e.Currency, &e.Description, &e.Tags, &e.Source, &e.HappenedAt, &e.CreatedAt, &e.UpdatedAt)
	return &e, err
}

func (r *Repo) Delete(ctx context.Context, accountID, id string) error {
	_, err := r.pool.Exec(ctx, "DELETE FROM expenses WHERE id = $1 AND account_id = $2", id, accountID)
	return err
}
