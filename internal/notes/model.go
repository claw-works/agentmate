package notes

import "time"

type Note struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	UserID    string    `json:"user_id"`
	KeyID     *string   `json:"key_id,omitempty"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Tags      []string  `json:"tags"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateRequest struct {
	Title   string   `json:"title" binding:"required"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
}

type UpdateRequest struct {
	Title   *string  `json:"title"`
	Content *string  `json:"content"`
	Tags    []string `json:"tags"`
}
