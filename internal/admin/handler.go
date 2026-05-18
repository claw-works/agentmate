package admin

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

func (h *Handler) Stats(c *gin.Context) {
	ctx := c.Request.Context()
	var users, apikeys, todos, notesCount int
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&users)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM api_keys").Scan(&apikeys)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM todos").Scan(&todos)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM notes").Scan(&notesCount)
	c.JSON(http.StatusOK, gin.H{
		"users":    users,
		"api_keys": apikeys,
		"todos":    todos,
		"notes":    notesCount,
	})
}

type userRow struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) Users(c *gin.Context) {
	rows, err := h.pool.Query(c.Request.Context(),
		"SELECT id, email, role, created_at FROM users ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []userRow
	for rows.Next() {
		var u userRow
		if err := rows.Scan(&u.ID, &u.Email, &u.Role, &u.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, u)
	}
	c.JSON(http.StatusOK, list)
}

type apiKeyRow struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UserEmail string    `json:"user_email"`
	KeyPrefix string    `json:"key_prefix"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) APIKeys(c *gin.Context) {
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT ak.id, ak.name, u.email, ak.prefix, ak.created_at
		 FROM api_keys ak JOIN users u ON ak.user_id = u.id
		 ORDER BY ak.created_at DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []apiKeyRow
	for rows.Next() {
		var k apiKeyRow
		if err := rows.Scan(&k.ID, &k.Name, &k.UserEmail, &k.KeyPrefix, &k.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, k)
	}
	c.JSON(http.StatusOK, list)
}
