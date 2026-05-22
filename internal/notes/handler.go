package notes

import (
	"net/http"
	"strconv"
	"strings"

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
	n, err := h.svc.Create(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, n)
}

func (h *Handler) Get(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	n, err := h.svc.Get(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, n)
}

func (h *Handler) List(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	tags := parseTags(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	params := ListNotesParams{Tags: tags, Limit: limit, Offset: offset}
	total, _ := h.svc.Count(c.Request.Context(), userID, params)
	list, err := h.svc.List(c.Request.Context(), userID, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) Update(c *gin.Context) {
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	n, err := h.svc.Update(c.Request.Context(), userID, c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, n)
}

func (h *Handler) Append(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	id := c.Param("id")
	var req struct {
		Content string `json:"content" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 40000, "message": err.Error()})
		return
	}
	n, err := h.svc.Append(c.Request.Context(), id, userID, req.Content)
	if err != nil {
		c.JSON(500, gin.H{"code": 50000, "message": err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": 0, "data": n, "message": "ok"})
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
	list, err := h.svc.Search(c.Request.Context(), userID, q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, list)
}

func parseTags(c *gin.Context) []string {
	tags := c.QueryArray("tags")
	if tag := c.Query("tag"); tag != "" {
		tags = append(tags, tag)
	}
	var result []string
	for _, t := range tags {
		for _, s := range strings.Split(t, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				result = append(result, s)
			}
		}
	}
	if result == nil {
		result = []string{}
	}
	return result
}