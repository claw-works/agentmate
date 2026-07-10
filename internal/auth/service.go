package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
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
		`WITH ids AS (
		   SELECT gen_random_uuid() AS id
		 ), created_account AS (
		   INSERT INTO accounts (id, name)
		   SELECT id, $1 FROM ids
		   RETURNING id
		 )
		 INSERT INTO users (id, account_id, email, password_hash, role)
		 SELECT id, id, $1, $2, 'user' FROM created_account
		 RETURNING id, account_id, email, password_hash, role, created_at`,
		email, string(hash),
	).Scan(&u.ID, &u.AccountID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (string, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		"SELECT id, account_id, email, password_hash, role, created_at FROM users WHERE email = $1", email,
	).Scan(&u.ID, &u.AccountID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return "", errors.New("invalid credentials")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return "", errors.New("invalid credentials")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub":        u.ID,
		"account_id": u.AccountID,
		"exp":        time.Now().Add(72 * time.Hour).Unix(),
	})
	return token.SignedString(s.jwtSecret)
}

func (s *Service) GetUser(ctx context.Context, userID string) (*User, error) {
	var u User
	err := s.pool.QueryRow(ctx,
		"SELECT id, account_id, email, password_hash, role, created_at FROM users WHERE id = $1", userID,
	).Scan(&u.ID, &u.AccountID, &u.Email, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (s *Service) CreateAPIKey(ctx context.Context, accountID, userID, name string, scopes []string) (string, *APIKey, error) {
	if scopes == nil {
		scopes = []string{}
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	key := "ak_" + hex.EncodeToString(raw)
	hash := sha256Hash(key)
	prefix := key[:10]

	var ak APIKey
	err := s.pool.QueryRow(ctx,
		`INSERT INTO api_keys (account_id, user_id, name, key_hash, prefix, scopes)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, account_id, user_id, name, prefix, scopes, created_at`,
		accountID, userID, name, hash, prefix, scopes,
	).Scan(&ak.ID, &ak.AccountID, &ak.UserID, &ak.Name, &ak.Prefix, &ak.Scopes, &ak.CreatedAt)
	if err != nil {
		return "", nil, err
	}
	return key, &ak, nil
}

func (s *Service) ListAPIKeys(ctx context.Context, accountID string) ([]APIKey, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, account_id, user_id, name, prefix, scopes, created_at
		 FROM api_keys
		 WHERE account_id = $1
		 ORDER BY created_at DESC`,
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]APIKey, 0)
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.AccountID, &k.UserID, &k.Name, &k.Prefix, &k.Scopes, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (s *Service) DeleteAPIKey(ctx context.Context, accountID, keyID string) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM api_keys WHERE id = $1 AND account_id = $2", keyID, accountID)
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

func (s *Service) ValidateAPIKey(ctx context.Context, key string) (*APIKey, error) {
	hash := sha256Hash(key)
	var ak APIKey
	err := s.pool.QueryRow(ctx,
		`SELECT id, account_id, user_id, name, prefix, scopes, created_at
		 FROM api_keys WHERE key_hash = $1`,
		hash,
	).Scan(&ak.ID, &ak.AccountID, &ak.UserID, &ak.Name, &ak.Prefix, &ak.Scopes, &ak.CreatedAt)
	if err != nil {
		return nil, errors.New("invalid api key")
	}
	return &ak, nil
}

// HasScope checks if the key has the required scope.
// Empty scopes means full access. "resource:rw" implies "resource:r".
func HasScope(key *APIKey, scope string) bool {
	if len(key.Scopes) == 0 {
		return true
	}
	for _, s := range key.Scopes {
		if s == scope {
			return true
		}
		// resource:rw implies resource:r
		if strings.HasSuffix(scope, ":r") && s == strings.TrimSuffix(scope, ":r")+":rw" {
			return true
		}
	}
	return false
}

// ScopesSubset checks that all requested scopes are covered by the parent scopes.
func ScopesSubset(parent, requested []string) bool {
	if len(parent) == 0 {
		return true // parent has full access
	}
	parentKey := &APIKey{Scopes: parent}
	for _, s := range requested {
		if !HasScope(parentKey, s) {
			return false
		}
	}
	return true
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func (s *Service) IsAdmin(ctx context.Context, userID string) bool {
	var role string
	err := s.pool.QueryRow(ctx, "SELECT role FROM users WHERE id = $1", userID).Scan(&role)
	return err == nil && role == "admin"
}

func (s *Service) Pool() *pgxpool.Pool {
	return s.pool
}
