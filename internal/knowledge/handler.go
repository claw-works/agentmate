package knowledge

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

// ─── Sources ───

func (h *Handler) CreateSource(c *gin.Context) {
	var req CreateKnowledgeSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	source, err := h.svc.CreateSource(c.Request.Context(), owner, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, source)
}

func (h *Handler) ListSources(c *gin.Context) {
	limit, offset, err := strictPagination(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	sources, listErr := h.svc.ListSources(c.Request.Context(), owner.Account(), KnowledgeSourceListParams{
		Type:   c.Query("type"),
		Status: c.Query("status"),
		Limit:  limit,
		Offset: offset,
	})
	if listErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": listErr.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": sources, "limit": limit, "offset": offset})
}

func (h *Handler) ListSourceRevisions(c *gin.Context) {
	limit, offset, err := strictPagination(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	revisions, listErr := h.svc.ListSourceRevisions(c.Request.Context(), owner.Account(), c.Param("id"), limit, offset)
	if listErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": revisions, "limit": limit, "offset": offset})
}

func (h *Handler) SubmitSnapshot(c *gin.Context) {
	var req SubmitSnapshotRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	resp, err := h.svc.SubmitSnapshot(c.Request.Context(), owner, c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, resp)
}

func (h *Handler) SyncGitSource(c *gin.Context) {
	var req SyncGitSourceRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	resp, err := h.svc.SyncGitSource(c.Request.Context(), owner, c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, resp)
}

// ─── Documents ───

func (h *Handler) ListRevisionDocuments(c *gin.Context) {
	limit, offset, err := strictPagination(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	resp, listErr := h.svc.ListRevisionDocuments(c.Request.Context(), owner.Account(), c.Param("id"), DocumentListParams{Limit: limit, Offset: offset})
	if listErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) GetDocument(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	document, err := h.svc.GetDocument(c.Request.Context(), owner.Account(), c.Param("id"), c.Param("doc_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, document)
}

// strictPagination mirrors the skills handler contract: explicit limit must
// be an integer in [1, 100] and offset a non-negative integer.
func strictPagination(c *gin.Context) (int, int, error) {
	limit := 20
	if rawLimit, present := c.GetQuery("limit"); present {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, 0, errors.New("limit must be an integer between 1 and 100")
		}
		limit = parsed
	}
	offset := 0
	if rawOffset, present := c.GetQuery("offset"); present {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 {
			return 0, 0, errors.New("offset must be a non-negative integer")
		}
		offset = parsed
	}
	return limit, offset, nil
}
