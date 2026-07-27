package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wellxie/agentmate/internal/retrieval"
)

type Handler struct {
	pool          *pgxpool.Pool
	retrievalRepo *retrieval.Repo
}

func NewHandler(pool *pgxpool.Pool, retrievalRepo *retrieval.Repo) *Handler {
	return &Handler{pool: pool, retrievalRepo: retrievalRepo}
}

// RebuildLexical recomputes the CJK-capable lexical projection of retrieval
// documents. It is an operational repair path for rows written before the
// projection existed (or before its rule changed): the projection is derived
// from stored title and content, so this neither re-embeds anything nor calls
// Qdrant. Scope it with optional account_id and namespace query parameters.
func (h *Handler) RebuildLexical(c *gin.Context) {
	if h.retrievalRepo == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "retrieval repository is not configured"})
		return
	}
	batchSize := 0
	if raw, present := c.GetQuery("batch_size"); present {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "batch_size must be a positive integer"})
			return
		}
		batchSize = parsed
	}
	updated, err := h.retrievalRepo.RebuildLexicalProjections(
		c.Request.Context(), c.Query("account_id"), c.Query("namespace"), batchSize,
	)
	if err != nil {
		// Report progress alongside the failure: the rebuild is incremental, so
		// a partial result tells the operator how far it got.
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error(), "updated": updated})
		return
	}
	c.JSON(http.StatusOK, gin.H{"updated": updated})
}

func (h *Handler) Stats(c *gin.Context) {
	ctx := c.Request.Context()
	var users, apikeys, todos, notesCount, reportCount int
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM users").Scan(&users)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM api_keys").Scan(&apikeys)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM todos").Scan(&todos)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM notes").Scan(&notesCount)
	h.pool.QueryRow(ctx, "SELECT COUNT(*) FROM reports").Scan(&reportCount)
	c.JSON(http.StatusOK, gin.H{
		"users":    users,
		"api_keys": apikeys,
		"todos":    todos,
		"notes":    notesCount,
		"reports":  reportCount,
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

type reportRow struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Format    string    `json:"format"`
	Tags      []string  `json:"tags"`
	Source    string    `json:"source"`
	UserEmail string    `json:"user_email"`
	CreatedAt time.Time `json:"created_at"`
}

func (h *Handler) Reports(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT r.id, r.title, r.format, r.tags, r.source, u.email, r.created_at
		 FROM reports r JOIN users u ON r.user_id = u.id
		 ORDER BY r.created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []reportRow
	for rows.Next() {
		var r reportRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Format, &r.Tags, &r.Source, &r.UserEmail, &r.CreatedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, r)
	}
	if list == nil {
		list = []reportRow{}
	}
	c.JSON(http.StatusOK, list)
}

type usageRow struct {
	KeyID      string     `json:"key_id"`
	KeyName    string     `json:"key_name"`
	KeyPrefix  string     `json:"key_prefix"`
	UserEmail  string     `json:"user_email"`
	TotalCalls int        `json:"total_calls"`
	TodayCalls int        `json:"today_calls"`
	LastUsedAt *time.Time `json:"last_used_at"`
}

func (h *Handler) Usage(c *gin.Context) {
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT
			ak.id, ak.name, ak.prefix,
			u.email,
			count(l.id) as total_calls,
			count(l.id) FILTER (WHERE l.created_at > now() - interval '24 hours') as today_calls,
			max(l.created_at) as last_used_at
		FROM api_keys ak
		JOIN users u ON ak.user_id = u.id
		LEFT JOIN api_logs l ON l.key_id = ak.id
		GROUP BY ak.id, ak.name, ak.prefix, u.email
		ORDER BY total_calls DESC`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()
	var list []usageRow
	for rows.Next() {
		var r usageRow
		if err := rows.Scan(&r.KeyID, &r.KeyName, &r.KeyPrefix, &r.UserEmail, &r.TotalCalls, &r.TodayCalls, &r.LastUsedAt); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		list = append(list, r)
	}
	if list == nil {
		list = []usageRow{}
	}
	c.JSON(http.StatusOK, list)
}
