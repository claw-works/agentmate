package auth

import "time"

type User struct {
	ID           string    `json:"id"`
	Email        string    `json:"email"`
	PasswordHash string    `json:"-"`
	Role         string    `json:"role" db:"role"`
	CreatedAt    time.Time `json:"created_at"`
}

type APIKey struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Name      string    `json:"name"`
	Prefix    string    `json:"prefix"`
	Scopes    []string  `json:"scopes" db:"scopes"`
	CreatedAt time.Time `json:"created_at"`
}

type RegisterRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=8"`
}

type LoginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type CreateAPIKeyRequest struct {
	Name   string   `json:"name" binding:"required"`
	Scopes []string `json:"scopes"` // 不传则默认空数组（全权限）
}
