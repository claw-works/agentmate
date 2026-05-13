package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

type Service struct {
	pool      *pgxpool.Pool
	jwtSecret []byte
}

func NewService(pool *pgxpool.Pool, jwtSecret string) *Service {
	return &Service{pool: pool, jwtSecret: []byte(jwtSecret)}
}

func (s *Service) Register(ctx context.Context, email, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	var u User
	err = s.pool.QueryRow(ctx,
		"INSERT INTO users (email, password_hash) VALUES ($1, $2) RETURNING id, email, password_hash, created_at",
		email, string(hash),
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		"SELECT id, email, password_hash, created_at FROM users WHERE email = $1", email,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return "", errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", errors.New("invalid credentials")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.ID,
		"exp": time.Now().Add(72 * time.Hour).Unix(),
	})
	return token.SignedString(s.jwtSecret)
}

func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		"SELECT id, email, password_hash, created_at FROM users WHERE id = $1", userID,
	).Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, userID, name string) (string, *APIKey, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	key := "ak_" + hex.EncodeToString(raw)
	hash := sha256Hash(key)
	prefix := key[:10]

	var ak APIKey
	err := s.pool.QueryRow(ctx,
		"INSERT INTO api_keys (user_id, name, key_hash, prefix) VALUES ($1, $2, $3, $4) RETURNING id, user_id, name, prefix, created_at",
		userID, name, hash, prefix,
	).Scan(&ak.ID, &ak.UserID, &ak.Name, &ak.Prefix, &ak.CreatedAt)
	if err != nil {
		return "", nil, err
	}
	return key, &ak, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, userID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT id, user_id, name, prefix, created_at FROM api_keys WHERE user_id = $1 ORDER BY created_at DESC", userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.Prefix, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, userID, keyID string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM api_keys WHERE id = $1 AND user_id = $2", keyID, userID)
	return err
}

func (s *Service) ValidateJWT(tokenStr string) (string, error) {
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return s.jwtSecret, nil
	})
	if err != nil || !token.Valid {
		return "", errors.New("invalid token")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", errors.New("invalid claims")
	}
	sub, _ := claims.GetSubject()
	return sub, nil
}

func (s *Service) ValidateAPIKey(ctx context.Context, key string) (string, error) {
	hash := sha256Hash(key)
	var userID string
	err := s.pool.QueryRow(ctx, "SELECT user_id FROM api_keys WHERE key_hash = $1", hash).Scan(&userID)
	if err != nil {
		return "", errors.New("invalid api key")
	}
	return userID, nil
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}
