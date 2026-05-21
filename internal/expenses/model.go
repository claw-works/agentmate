package expenses

import "time"

type Expense struct {
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	Amount      float64   `json:"amount"`
	Currency    string    `json:"currency"`
	Description string    `json:"description"`
	Tags        []string  `json:"tags"`
	Source      string    `json:"source"`
	HappenedAt  time.Time `json:"happened_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type CreateRequest struct {
	Amount      float64  `json:"amount" binding:"required,gt=0"`
	Currency    string   `json:"currency"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	Source      string   `json:"source"`
	HappenedAt  string   `json:"happened_at"`
}

type UpdateRequest struct {
	Amount      *float64 `json:"amount"`
	Description *string  `json:"description"`
	Tags        []string `json:"tags"`
	HappenedAt  *string  `json:"happened_at"`
}

type ListParams struct {
	Tags   []string
	Search string
	Start  string
	End    string
	Limit  int
	Offset int
}
