package admin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/wellxie/agentmate/internal/auth"
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
