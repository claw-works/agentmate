package middleware

import (
	"context"
	"time"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func APILogger(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		latency := int(time.Since(start).Milliseconds())
		accountID, _ := c.Get(auth.ContextAccountID)
		userID, _ := c.Get(auth.ContextUserID)
		keyID, _ := c.Get(auth.ContextKeyID)
		method := c.Request.Method
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		statusCode := c.Writer.Status()

		go func() {
			pool.Exec(context.Background(),
				`INSERT INTO api_logs (account_id, user_id, key_id, method, path, status_code, latency_ms)
				 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				accountID, userID, keyID, method, path, statusCode, latency,
			)
		}()
	}
}
