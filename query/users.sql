-- name: CreateUser :one
WITH ids AS (
  SELECT gen_random_uuid() AS id
), created_account AS (
  INSERT INTO accounts (id, name)
  SELECT id, $1 FROM ids
  RETURNING id
)
INSERT INTO users (id, account_id, email, password_hash)
SELECT id, id, $1, $2 FROM created_account
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;
