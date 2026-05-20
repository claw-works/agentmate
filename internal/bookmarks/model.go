package bookmarks

import "time"

type Bookmark struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	URL       string     `json:"url"`
	Title     string     `json:"title"`
	Summary   string     `json:"summary"`
	Content   string     `json:"content,omitempty"`
	Tags      []string   `json:"tags"`
	Source    string     `json:"source"`
	IsRead    bool       `json:"is_read"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

type CreateRequest struct {
	URL     string   `json:"url" binding:"required"`
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Content string   `json:"content"`
	Tags    []string `json:"tags"`
	Source  string   `json:"source"`
}

type UpdateRequest struct {
	Title   *string  `json:"title"`
	Summary *string  `json:"summary"`
	Content *string  `json:"content"`
	Tags    []string `json:"tags"`
	Source  *string  `json:"source"`
	IsRead  *bool    `json:"is_read"`
}
