package todo

import "time"

type Todo struct {
	ID          string     `json:"id"`
	AccountID   string     `json:"account_id"`
	UserID      string     `json:"user_id"`
	KeyID       *string    `json:"key_id,omitempty"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"`
	Priority    string     `json:"priority"`
	DueDate     *time.Time `json:"due_date,omitempty"`
	Tags        []string   `json:"tags"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type CreateRequest struct {
	Title       string   `json:"title" binding:"required"`
	Description string   `json:"description"`
	Priority    string   `json:"priority"`
	DueDate     string   `json:"due_date"`
	Tags        []string `json:"tags"`
}

type UpdateRequest struct {
	Title       *string  `json:"title"`
	Description *string  `json:"description"`
	Status      *string  `json:"status"`
	Priority    *string  `json:"priority"`
	DueDate     *string  `json:"due_date"`
	Tags        []string `json:"tags"`
}
