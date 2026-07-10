-- name: CreateNote :one
INSERT INTO notes (account_id, user_id, key_id, title, content, tags) VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetNote :one
SELECT * FROM notes WHERE id = $1 AND account_id = $2;

-- name: ListNotes :many
SELECT * FROM notes WHERE account_id = $1 ORDER BY created_at DESC;

-- name: UpdateNote :one
UPDATE notes SET title = $3, content = $4, tags = $5, updated_at = now()
WHERE id = $1 AND account_id = $2 RETURNING *;

-- name: DeleteNote :exec
DELETE FROM notes WHERE id = $1 AND account_id = $2;

-- name: SearchNotes :many
SELECT * FROM notes WHERE account_id = $1 AND (title ILIKE $2 OR content ILIKE $2);
