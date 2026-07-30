package admin

import (
	"net/http"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/gin-gonic/gin"
)

func Middleware(svc *auth.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Reuse auth middleware logic inline
		auth.Middleware(svc)(c)
		if c.IsAborted() {
			return
		}
		userID := c.GetString(auth.ContextUserID)
		if !svc.IsAdmin(c.Request.Context(), userID) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}
		c.Next()
	}
}
