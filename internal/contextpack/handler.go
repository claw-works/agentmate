package contextpack

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/wellxie/agentmate/internal/auth"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Pack(c *gin.Context) {
	var req PackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Scopes are enforced per layer rather than by one endpoint-level check, so
	// a caller holding only some read scopes gets those layers and an explicit
	// note on the rest. A JWT session is unrestricted, matching RequireScope.
	var scopes []string
	if c.GetString(auth.ContextAuthMethod) != "jwt" {
		raw, _ := c.Get(auth.ContextScopes)
		scopes, _ = raw.([]string)
	}
	response, err := h.svc.Pack(c.Request.Context(), auth.OwnerFromContext(c), scopes, req)
	if err != nil {
		if errors.Is(err, ErrInvalidInput) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	// A pack embeds skill instructions, knowledge passages and remembered
	// experience, so it must not be cached by intermediaries.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}
