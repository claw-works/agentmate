package reports

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
	var req CreateReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	rpt, err := h.svc.Create(c.Request.Context(), owner, req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, rpt)
}

func (h *Handler) Get(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	rpt, err := h.svc.Get(c.Request.Context(), owner.Account(), c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, rpt)
}

func (h *Handler) List(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	var params ListReportsParams
	if err := c.ShouldBindQuery(&params); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if params.Limit <= 0 || params.Limit > 100 {
		params.Limit = 20
	}
	total, _ := h.svc.Count(c.Request.Context(), owner.Account(), params)
	list, err := h.svc.List(c.Request.Context(), owner.Account(), params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": list, "total": total, "limit": params.Limit, "offset": params.Offset})
}

func (h *Handler) Update(c *gin.Context) {
	var req UpdateReportRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	rpt, err := h.svc.Update(c.Request.Context(), owner.Account(), c.Param("id"), req)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, rpt)
}

func (h *Handler) Delete(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	if err := h.svc.Delete(c.Request.Context(), owner.Account(), c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) Sources(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	sources, err := h.svc.ListSources(c.Request.Context(), owner.Account())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, sources)
}
