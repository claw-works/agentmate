-- name: CreateAPIKey :one
INSERT INTO api_keys (account_id, user_id, name, key_hash, prefix) VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: ListAPIKeysByUser :many
SELECT id, account_id, user_id, name, prefix, created_at FROM api_keys WHERE account_id = $1 ORDER BY created_at DESC;

-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys WHERE key_hash = $1;

-- name: DeleteAPIKey :exec
DELETE FROM api_keys WHERE id = $1 AND account_id = $2;
