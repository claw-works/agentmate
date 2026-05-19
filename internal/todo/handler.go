package todo

import (
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

func (h *Handler) Create(c *gin.Context) {
	var req CreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	t, err := h.svc.Create(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (h *Handler) Get(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	t, err := h.svc.Get(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *Handler) List(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	tag := c.Query("tag")
	todos, err := h.svc.List(c.Request.Context(), userID, ListTodosParams{Tag: tag})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, todos)
}

func (h *Handler) Update(c *gin.Context) {
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	t, err := h.svc.Update(c.Request.Context(), userID, c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *Handler) Delete(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	if err := h.svc.Delete(c.Request.Context(), userID, c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) Search(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	q := c.Query("q")
	todos, err := h.svc.Search(c.Request.Context(), userID, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, todos)
}
