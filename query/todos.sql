-- name: CreateTodo :one
INSERT INTO todos (user_id, title, description, priority, due_date, tags)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetTodo :one
SELECT * FROM todos WHERE id = $1 AND user_id = $2;

-- name: ListTodos :many
SELECT * FROM todos WHERE user_id = $1 ORDER BY created_at DESC;

-- name: UpdateTodo :one
UPDATE todos SET title = $3, description = $4, status = $5, priority = $6, due_date = $7, tags = $8, updated_at = now()
WHERE id = $1 AND user_id = $2 RETURNING *;

-- name: DeleteTodo :exec
DELETE FROM todos WHERE id = $1 AND user_id = $2;

-- name: SearchTodos :many
SELECT * FROM todos WHERE user_id = $1 AND (title ILIKE $2 OR description ILIKE $2);
