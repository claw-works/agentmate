package skills

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/wellxie/agentmate/internal/auth"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// ─── Skill Logs ───

func (h *Handler) CreateLog(c *gin.Context) {
	var req CreateLogRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	l, err := h.svc.CreateLog(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, l)
}

func (h *Handler) ListLogs(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	params := LogListParams{
		SkillName: c.Query("skill_name"),
		AgentID:   c.Query("agent_id"),
		Outcome:   c.Query("outcome"),
		Limit:     limit,
		Offset:    offset,
	}
	total, _ := h.svc.CountLogs(c.Request.Context(), userID, params)
	list, err := h.svc.ListLogs(c.Request.Context(), userID, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "total": total, "limit": limit, "offset": offset})
}

// ─── Skill Sources ───

func (h *Handler) CreateSource(c *gin.Context) {
	var req CreateSkillSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	source, err := h.svc.CreateSource(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, source)
}

func (h *Handler) ListSources(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	params := SkillSourceListParams{
		Type:   c.Query("type"),
		Status: c.Query("status"),
		Limit:  limit,
		Offset: offset,
	}
	sources, err := h.svc.ListSources(c.Request.Context(), userID, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": sources, "limit": limit, "offset": offset})
}

func (h *Handler) GetSource(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	source, err := h.svc.GetSource(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, source)
}

func (h *Handler) ListSourceRevisions(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	revisions, err := h.svc.ListSourceRevisions(c.Request.Context(), userID, c.Param("id"), limit, offset)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": revisions, "limit": limit, "offset": offset})
}

func (h *Handler) SubmitLocalSnapshot(c *gin.Context) {
	var req SubmitLocalSnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	resp, err := h.svc.SubmitLocalSnapshot(c.Request.Context(), userID, c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, resp)
}

// ─── Skill Versions ───

func (h *Handler) CreateVersion(c *gin.Context) {
	var req CreateVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	v, err := h.svc.CreateVersion(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, v)
}

func (h *Handler) ListVersions(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	params := VersionListParams{
		SkillName: c.Query("skill_name"),
		Limit:     limit,
		Offset:    offset,
	}
	list, err := h.svc.ListVersions(c.Request.Context(), userID, params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list})
}

func (h *Handler) GetActiveVersion(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	skillName := c.Query("skill_name")
	if skillName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skill_name required"})
		return
	}
	v, err := h.svc.GetActiveVersion(c.Request.Context(), userID, skillName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *Handler) ActivateVersion(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	v, err := h.svc.ActivateVersion(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, v)
}

func (h *Handler) ListVersionFiles(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	files, err := h.svc.ListVersionFiles(c.Request.Context(), userID, c.Param("id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": files})
}

func (h *Handler) IndexActiveVersions(c *gin.Context) {
	var req IndexSkillsRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	result, err := h.svc.IndexActiveVersions(c.Request.Context(), userID, req.SkillName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) Search(c *gin.Context) {
	var req SearchSkillsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	userID := c.GetString(auth.ContextUserID)
	result, err := h.svc.Search(c.Request.Context(), userID, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) GetStats(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	skillName := c.Query("skill_name")
	if skillName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skill_name required"})
		return
	}
	stats, err := h.svc.GetSkillStats(c.Request.Context(), userID, skillName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *Handler) GetSignals(c *gin.Context) {
	userID := c.GetString(auth.ContextUserID)
	skillName := c.Query("skill_name")
	if skillName == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "skill_name required"})
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "10"))
	signals, err := h.svc.SkillSignals(c.Request.Context(), userID, skillName, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": signals})
}
