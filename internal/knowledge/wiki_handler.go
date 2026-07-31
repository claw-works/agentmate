package knowledge

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/claw-works/agentmate/internal/auth"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// ─── K3: wiki compilation ───

// Compile enqueues a build and returns a receipt.
//
// 202, not 200: nothing has been compiled when this returns. The synchronous
// version took 200-400 seconds against a reasoning model, past any sane client
// default timeout, and a caller that gave up lost the work entirely. Callers poll
// the build.
func (h *Handler) Compile(c *gin.Context) {
	var req CompileRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.EnqueueCompile(c.Request.Context(), owner, req)
	if err != nil {
		// An unconfigured compiler is an operator problem, not a bad request, and
		// not a server fault either: 501 says "this deployment cannot do that".
		if errors.Is(err, ErrCompilerUnavailable) {
			c.JSON(http.StatusNotImplemented, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if response.Reused {
		// Nothing was queued, so the request is already fully satisfied.
		c.JSON(http.StatusOK, response)
		return
	}
	c.JSON(http.StatusAccepted, response)
}

// QueueStats answers "why is my compile not done yet". Without it, waiting in a
// queue is indistinguishable from a stuck worker.
func (h *Handler) QueueStats(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	stats, err := h.svc.QueueStats(c.Request.Context(), owner.Account())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, stats)
}

func (h *Handler) ListBuilds(c *gin.Context) {
	limit, offset, err := strictPagination(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, listErr := h.svc.ListBuilds(c.Request.Context(), owner.Account(), c.Query("source_id"), limit, offset)
	if listErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": listErr.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetBuild(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	build, err := h.svc.GetBuild(c.Request.Context(), owner.Account(), c.Param("build_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, build)
}

func (h *Handler) ListBuildPages(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.ListPages(c.Request.Context(), owner.Account(), c.Param("build_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetBuildPage(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	// Wildcard params arrive with a leading slash; page paths are stored without
	// one, so the route shape must not leak into the stored identity.
	path := strings.TrimPrefix(c.Param("path"), "/")
	page, err := h.svc.GetPage(c.Request.Context(), owner.Account(), c.Param("build_id"), path)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	// Page bodies are tenant content.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, page)
}

func (h *Handler) DiffBuilds(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	diff, err := h.svc.DiffBuilds(c.Request.Context(), owner.Account(), c.Query("from"), c.Param("build_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, diff)
}

// ActivateBuild is also the rollback endpoint: activating an older build reverts
// the wiki, so there is no separate rollback route to keep in sync.
func (h *Handler) ActivateBuild(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.ActivateBuild(c.Request.Context(), owner, c.Param("build_id"))
	if err != nil {
		if errors.Is(err, ErrBuildNotActivatable) {
			// 409: the build exists and the request is well-formed, but its state
			// forbids the transition.
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) ListBuildEvents(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	events, err := h.svc.ListBuildEvents(c.Request.Context(), owner.Account(), c.Param("build_id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"build_id": c.Param("build_id"), "items": events, "total": len(events)})
}

// ─── K3.6: wiki retrieval ───

func (h *Handler) IndexActiveWikiBuilds(c *gin.Context) {
	var req IndexWikiRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.IndexActiveWikiBuilds(c.Request.Context(), owner, req.SourceID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}

func (h *Handler) SearchWiki(c *gin.Context) {
	var req SearchWikiRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.SearchWiki(c.Request.Context(), owner, req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Snippets, page bodies and citation excerpts are all tenant content.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

func (h *Handler) WikiIndexStatus(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	statuses, err := h.svc.WikiIndexStatuses(c.Request.Context(), owner.Account(), c.Query("source_id"))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	stale := 0
	for _, status := range statuses {
		if status.Stale {
			stale++
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": statuses, "total": len(statuses), "stale": stale})
}

// ─── K3.7: lint ───

// LintWiki runs every lint rule against a source's active build.
//
// Write scope, and 200 rather than 202: it records a run and its findings, but it changes
// no wiki content and blocks nothing. Findings are observations about a wiki that is
// already serving — a rule that could stop a wiki from serving belongs in check.
func (h *Handler) LintWiki(c *gin.Context) {
	var req struct {
		SourceID string `json:"source_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.SourceID) == "" {
		req.SourceID = c.Query("source_id")
	}
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.LintActiveWiki(c.Request.Context(), owner, req.SourceID)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Findings carry page paths, document paths and the compiler's notes: tenant content.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

func (h *Handler) ListLintRuns(c *gin.Context) {
	limit, offset, err := strictPagination(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, listErr := h.svc.ListLintRuns(c.Request.Context(), owner.Account(), c.Query("source_id"), limit, offset)
	if listErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": listErr.Error()})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetLintRun(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.GetLintRun(c.Request.Context(), owner.Account(), c.Param("run_id"))
	if err != nil {
		// Only an absent row is a 404. Reporting a database failure as "not found" would
		// tell the caller the run never existed, which is a different problem with a
		// different response — and it would retry against a wall.
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "lint run lookup failed"})
		return
	}
	// Page paths, document paths and the compiler's contradiction notes are tenant content.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

// ─── K3.8: review ───

// ReviewBuild runs, or re-runs, faithfulness review on a committed build.
//
// Synchronous and possibly slow: one reviewer call per page, bounded by the page cap. It is
// not queued because, unlike compilation, nothing depends on it finishing — a caller that
// gives up loses only the verdict, and the wiki keeps serving either way.
func (h *Handler) ReviewBuild(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.ReviewBuild(c.Request.Context(), owner, c.Param("build_id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	// Findings quote page text and source content: tenant content.
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

func (h *Handler) GetBuildReview(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.GetBuildReview(c.Request.Context(), owner.Account(), c.Param("build_id"))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "review lookup failed"})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

// ─── K3.9: validation signals ───

// RecordSignal stores one reported validation signal.
//
// Write scope: it records evidence. It changes no wiki and gates nothing — §7.3 is explicit
// that validation measures long-term quality and can never be a build gate, because it is
// biased, lagging and sparse by construction.
func (h *Handler) RecordSignal(c *gin.Context) {
	var req RecordSignalRequest
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	signal, err := h.svc.RecordSignal(c.Request.Context(), owner, req)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusNotFound, gin.H{"error": "source not found"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusCreated, signal)
}

func (h *Handler) ListSignals(c *gin.Context) {
	limit, offset, err := strictPagination(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, listErr := h.svc.ListSignals(c.Request.Context(), owner.Account(), SignalFilter{
		SourceID:  c.Query("source_id"),
		PagePath:  c.Query("page_path"),
		Direction: c.Query("direction"),
		Cause:     c.Query("cause"),
	}, limit, offset)
	if listErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": listErr.Error()})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

func (h *Handler) SignalSummary(c *gin.Context) {
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.SignalSummary(c.Request.Context(), owner.Account(), c.Query("source_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "private, no-store")
	c.JSON(http.StatusOK, response)
}

// SweepNeverRetrieved records the one signal that carries no reporting bias.
func (h *Handler) SweepNeverRetrieved(c *gin.Context) {
	var req struct {
		IdleDays int `json:"idle_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	owner := auth.OwnerFromContext(c)
	response, err := h.svc.SweepNeverRetrieved(c.Request.Context(), owner, req.IdleDays)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, response)
}
