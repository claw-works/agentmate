package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	user, err := h.svc.Register(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "email already exists"})
		return
	}
	c.JSON(http.StatusCreated, user)
}

func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	token, err := h.svc.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": token})
}

func (h *Handler) Me(c *gin.Context) {
	userID := c.GetString(ContextUserID)
	user, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}
	c.JSON(http.StatusOK, user)
}

func (h *Handler) CreateAPIKey(c *gin.Context) {
	var req CreateAPIKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If authenticated via API key, require manage_keys scope
	if c.GetString(ContextAuthMethod) == "apikey" {
		scopes, _ := c.Get(ContextScopes)
		parentScopes, _ := scopes.([]string)
		ak := &APIKey{Scopes: parentScopes}
		if !HasScope(ak, "manage_keys") {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient scope: manage_keys"})
			return
		}
		// New key's scopes cannot exceed parent's scopes
		if !ScopesSubset(parentScopes, req.Scopes) {
			c.JSON(http.StatusForbidden, gin.H{"error": "requested scopes exceed current key's scopes"})
			return
		}
	}

	owner := OwnerFromContext(c)
	key, ak, err := h.svc.CreateAPIKey(c.Request.Context(), owner.Account(), owner.UserID, req.Name, req.Scopes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"key": key, "api_key": ak})
}

func (h *Handler) ListAPIKeys(c *gin.Context) {
	owner := OwnerFromContext(c)
	keys, err := h.svc.ListAPIKeys(c.Request.Context(), owner.Account())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, keys)
}

func (h *Handler) DeleteAPIKey(c *gin.Context) {
	// If authenticated via API key, require manage_keys scope
	if c.GetString(ContextAuthMethod) == "apikey" {
		scopes, _ := c.Get(ContextScopes)
		s, _ := scopes.([]string)
		ak := &APIKey{Scopes: s}
		if !HasScope(ak, "manage_keys") {
			c.JSON(http.StatusForbidden, gin.H{"error": "insufficient scope: manage_keys"})
			return
		}
	}

	keyID := c.Param("id")
	owner := OwnerFromContext(c)
	if err := h.svc.DeleteAPIKey(c.Request.Context(), owner.Account(), keyID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
