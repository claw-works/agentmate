package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	ContextUserID    = "user_id"
	ContextScopes    = "scopes"
	ContextAuthMethod = "auth_method"
)

func Middleware(svc *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try x-api-key header first
		if key := c.GetHeader("x-api-key"); key != "" {
			ak, err := svc.ValidateAPIKey(c.Request.Context(), key)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
				return
			}
			c.Set(ContextUserID, ak.UserID)
			c.Set(ContextScopes, ak.Scopes)
			c.Set(ContextAuthMethod, "apikey")
			c.Next()
			return
		}

		// Try Authorization header (Bearer token — JWT or API Key)
		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing credentials"})
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")

		// If starts with "ak_", treat as API Key
		if strings.HasPrefix(token, "ak_") {
			ak, err := svc.ValidateAPIKey(c.Request.Context(), token)
			if err != nil {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid api key"})
				return
			}
			c.Set(ContextUserID, ak.UserID)
			c.Set(ContextScopes, ak.Scopes)
			c.Set(ContextAuthMethod, "apikey")
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
		c.Set(ContextAuthMethod, "jwt")
		c.Next()
	}
}

// RequireScope returns a middleware that checks the API key has the given scope.
// JWT-authenticated users bypass scope checks (full access).
func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString(ContextAuthMethod) == "jwt" {
			c.Next()
			return
		}
		scopes, _ := c.Get(ContextScopes)
		s, _ := scopes.([]string)
		ak := &APIKey{Scopes: s}
		if !HasScope(ak, scope) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "insufficient scope: " + scope})
			return
		}
		c.Next()
	}
}
