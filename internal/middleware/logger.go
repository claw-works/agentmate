package middleware

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
)

func APILogger(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		latency := int(time.Since(start).Milliseconds())
		userID, _ := c.Get("user_id")
		keyID, _ := c.Get("key_id")
		method := c.Request.Method
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		statusCode := c.Writer.Status()

		go func() {
			pool.Exec(context.Background(),
				`INSERT INTO api_logs (user_id, key_id, method, path, status_code, latency_ms)
				 VALUES ($1, $2, $3, $4, $5, $6)`,
				userID, keyID, method, path, statusCode, latency,
			)
		}()
	}
}
