package memory

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
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

// ListScopes 报告本账号已在用的 scope 组合，按用量降序：用得最多的那个就是这个
// 账号事实上的约定。调用方据此跟随，而不是各编一个把同一个项目散成两半。
func (h *Handler) ListScopes(c *gin.Context) {
	items, err := h.svc.ListScopes(c.Request.Context(), auth.OwnerFromContext(c).Account())
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items, "total": len(items),
		"note": "scope_key 是自由文本，服务端不规定它该是仓库名还是路径。" +
			"新写入请沿用这里已有的组合；列表为空说明本账号还没有任何记忆。",
	})
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
		// 结构化字段错误随 400 一起返回：调用方是 agent，让它从一段自然语言里
		// 反解合法值不如直接给 machine-readable 的 field/allowed。文本仍然保留，
		// 老调用方不受影响。
		var inputErr *InputError
		if errors.As(err, &inputErr) && len(inputErr.Fields) > 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "fields": inputErr.Fields})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": ErrNotFound.Error()})
	case errors.Is(err, ErrIdempotencyConflict), errors.Is(err, ErrSupersedeConflict):
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

// ─── M3: supersede, feedback, checkpoint ───

func (h *Handler) SupersedeEntry(c *gin.Context) {
	var req SupersedeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// The path identifies the replacement, so the body only needs to name what is
	// being replaced. Accepting both and disagreeing would be ambiguous.
	req.SupersedingID = c.Param("id")
	response, err := h.svc.SupersedeEntry(c.Request.Context(), auth.OwnerFromContext(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) RecordFeedback(c *gin.Context) {
	var req FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.MemoryID = c.Param("id")
	response, err := h.svc.RecordFeedback(c.Request.Context(), auth.OwnerFromContext(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusCreated
	if !response.Created {
		status = http.StatusOK
	}
	c.JSON(status, response)
}

func (h *Handler) ListFeedback(c *gin.Context) {
	limit := 0
	if raw, present := c.GetQuery("limit"); present {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be an integer"})
			return
		}
		limit = parsed
	}
	items, err := h.svc.ListFeedback(c.Request.Context(), auth.OwnerFromContext(c), c.Param("id"), limit)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "total": len(items)})
}

func (h *Handler) SaveCheckpoint(c *gin.Context) {
	var req SaveCheckpointRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	response, err := h.svc.SaveCheckpoint(c.Request.Context(), auth.OwnerFromContext(c), req)
	if err != nil {
		writeError(c, err)
		return
	}
	status := http.StatusCreated
	if !response.Created {
		status = http.StatusOK
	}
	c.JSON(status, response)
}

func (h *Handler) Resume(c *gin.Context) {
	response, err := h.svc.Resume(c.Request.Context(), auth.OwnerFromContext(c), c.Query("session_id"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, response)
}
