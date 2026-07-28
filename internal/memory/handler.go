package memory

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wellxie/agentmate/internal/auth"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RecordEvent(c *gin.Context) {
	var req RecordEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	event, created, err := h.svc.RecordEvent(c.Request.Context(), auth.OwnerFromContext(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	if !created {
		c.Header("X-Idempotent-Replay", "true")
		c.JSON(http.StatusOK, event)
		return
	}
	c.JSON(http.StatusCreated, event)
}

func (h *Handler) CreateEntry(c *gin.Context) {
	var req CreateEntryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	entry, err := h.svc.CreateEntry(c.Request.Context(), auth.OwnerFromContext(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, entry)
}

func (h *Handler) GetEntry(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	entry, err := h.svc.GetEntry(c.Request.Context(), owner.Account(), c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, entry)
}

func (h *Handler) ListEntries(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(DefaultListLimit)))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	params := ListEntriesParams{
		ScopeType:  c.Query("scope_type"),
		ScopeKey:   c.Query("scope_key"),
		MemoryType: c.Query("memory_type"),
		Status:     c.Query("status"),
		Limit:      limit,
		Offset:     offset,
	}
	items, total, err := h.svc.ListEntries(c.Request.Context(), owner.Account(), params)
	if err != nil {
		writeError(c, err)
		return
	}
	if limit <= 0 || limit > MaxListLimit {
		limit = DefaultListLimit
	}
	if offset < 0 {
		offset = 0
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "total": total, "limit": limit, "offset": offset,
	})
}

func (h *Handler) SearchEntries(c *gin.Context) {
	var req SearchEntriesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	result, err := h.svc.SearchEntries(c.Request.Context(), auth.OwnerFromContext(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound.Error()})
	case errors.Is(err, ErrIdempotencyConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "23503", "23514":
				c.JSON(http.StatusBadRequest, gin.H{"error": "memory references invalid data"})
				return
			case "23505":
				c.JSON(http.StatusConflict, gin.H{"error": "memory record conflicts with existing data"})
				return
			}
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

// ─── M1: attribution ───

func (h *Handler) SessionTimeline(c *gin.Context) {
	limit := 0
	if raw, present := c.GetQuery("limit"); present {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be an integer"})
			return
		}
		limit = parsed
	}
	response, err := h.svc.SessionTimeline(c.Request.Context(), auth.OwnerFromContext(c), SessionTimelineParams{
		SessionID:      c.Query("session_id"),
		SkillVersionID: c.Query("skill_version_id"),
		Limit:          limit,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) EntryAttribution(c *gin.Context) {
	response, err := h.svc.EntryAttribution(c.Request.Context(), auth.OwnerFromContext(c), c.Param("id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}
