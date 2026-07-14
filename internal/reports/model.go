package reports

import "time"

type Report struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	UserID      string    `json:"user_id"`
	KeyID       *string   `json:"key_id,omitempty"`
	Title       string    `json:"title"`
	Content     string    `json:"content,omitempty"`
	Format      string    `json:"format"`
	Tags        []string  `json:"tags"`
	Source      string    `json:"source"`
	SourceKeyID *string   `json:"source_key_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type PublicReport struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Content   string    `json:"content,omitempty"`
	Format    string    `json:"format"`
	Tags      []string  `json:"tags"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type CreateReportRequest struct {
	Title   string   `json:"title" binding:"required"`
	Content string   `json:"content"`
	Format  string   `json:"format"`
	Tags    []string `json:"tags"`
	Source  string   `json:"source"`
}

type UpdateReportRequest struct {
	Title   *string  `json:"title"`
	Content *string  `json:"content"`
	Tags    []string `json:"tags"`
	Source  *string  `json:"source"`
}

type ListReportsParams struct {
	Tag    string `form:"tag"`
	Source string `form:"source"`
	Search string `form:"q"`
	Limit  int    `form:"limit"`
	Offset int    `form:"offset"`
}
