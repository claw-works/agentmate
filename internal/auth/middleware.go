package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const ContextUserID = "user_id"

func Middleware(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try x-api-key header first
		if key := c.GetHeader("x-api-key"); key != "" {
			userID, err := svc.ValidateAPIKey(c.Request.Context(), key)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
				return
			}
			c.Set(ContextUserID, userID)
			c.Next()
			return
		}

		// Try Authorization header (Bearer token — JWT or API Key)
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing credentials"})
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")

		// If starts with "ak_", treat as API Key
		if strings.HasPrefix(token, "ak_") {
			userID, err := svc.ValidateAPIKey(c.Request.Context(), token)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
				return
			}
			c.Set(ContextUserID, userID)
			c.Next()
			return
		}

		// Otherwise treat as JWT
		userID, err := svc.ValidateJWT(token)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		c.Set(ContextUserID, userID)
		c.Next()
	}
}
