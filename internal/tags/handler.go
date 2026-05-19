package tags

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wellxie/agentmate/internal/auth"
)

type TagCount struct {
	Tag   string `json:"tag"`
	Count int    `json:"count"`
}

type Handler struct {
	pool *pgxpool.Pool
}

func NewHandler(pool *pgxpool.Pool) *Handler {
	return &Handler{pool: pool}
}

func (h *Handler) List(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	module := c.DefaultQuery("module", "all")

	result := make(map[string][]TagCount)

	if module == "all" || module == "todos" {
		tags, _ := h.queryTags(c, "todos", userID)
		result["todos"] = tags
	}
	if module == "all" || module == "notes" {
		tags, _ := h.queryTags(c, "notes", userID)
		result["notes"] = tags
	}
	if module == "all" || module == "reports" {
		tags, _ := h.queryTags(c, "reports", userID)
		result["reports"] = tags
	}

	c.JSON(http.StatusOK, result)
}

func (h *Handler) queryTags(c *gin.Context, table, userID string) ([]TagCount, error) {
	rows, err := h.pool.Query(c.Request.Context(),
		`SELECT tag, count(*) FROM `+table+`, unnest(tags) AS tag WHERE user_id=$1 GROUP BY tag ORDER BY count DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := make([]TagCount, 0)
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		tags = append(tags, tc)
	}
	return tags, nil
}
