package todo

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
	owner := auth.OwnerFromContext(c)
	t, err := h.svc.Create(c.Request.Context(), owner, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (h *Handler) Get(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	t, err := h.svc.Get(c.Request.Context(), owner.Account(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *Handler) List(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	tags := parseTags(c)
	status := c.Query("status")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	params := ListTodosParams{Tags: tags, Status: status, Limit: limit, Offset: offset}
	total, _ := h.svc.Count(c.Request.Context(), owner.Account(), params)
	todos, err := h.svc.List(c.Request.Context(), owner.Account(), params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": todos, "total": total, "limit": limit, "offset": offset})
}

func (h *Handler) Update(c *gin.Context) {
	var req UpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	t, err := h.svc.Update(c.Request.Context(), owner.Account(), c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, t)
}

func (h *Handler) Delete(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	if err := h.svc.Delete(c.Request.Context(), owner.Account(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) Search(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	q := c.Query("q")
	todos, err := h.svc.Search(c.Request.Context(), owner.Account(), q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, todos)
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
