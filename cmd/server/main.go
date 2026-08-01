package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/claw-works/agentmate/internal/admin"
	"github.com/claw-works/agentmate/internal/auth"
	"github.com/claw-works/agentmate/internal/bookmarks"
	"github.com/claw-works/agentmate/internal/contextpack"
	"github.com/claw-works/agentmate/internal/db"
	"github.com/claw-works/agentmate/internal/expenses"
	"github.com/claw-works/agentmate/internal/knowledge"
	"github.com/claw-works/agentmate/internal/llm"
	"github.com/claw-works/agentmate/internal/memory"
	"github.com/claw-works/agentmate/internal/middleware"
	"github.com/claw-works/agentmate/internal/notes"
	"github.com/claw-works/agentmate/internal/reports"
	"github.com/claw-works/agentmate/internal/retrieval"
	"github.com/claw-works/agentmate/internal/skills"
	"github.com/claw-works/agentmate/internal/tags"
	"github.com/claw-works/agentmate/internal/todo"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	ctx := context.Background()

	dbURL := env("DATABASE_URL", "postgres://agentmate:secret@localhost:5432/agentmate?sslmode=disable")
	jwtSecret := env("JWT_SECRET", "change-me")
	port := env("SERVER_PORT", "26001")

	pool, err := db.NewPool(ctx, dbURL)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer pool.Close()

	// Services
	authSvc := auth.NewService(pool, jwtSecret)
	authHandler := auth.NewHandler(authSvc)

	todoRepo := todo.NewRepo(pool)
	todoSvc := todo.NewService(todoRepo)
	todoHandler := todo.NewHandler(todoSvc)

	notesRepo := notes.NewRepo(pool)
	notesSvc := notes.NewService(notesRepo)
	notesHandler := notes.NewHandler(notesSvc)

	reportsRepo := reports.NewRepo(pool)
	reportsSvc := reports.NewService(reportsRepo)
	reportsHandler := reports.NewHandler(reportsSvc)

	bookmarksRepo := bookmarks.NewRepo(pool)
	bookmarksSvc := bookmarks.NewService(bookmarksRepo)
	bookmarksHandler := bookmarks.NewHandler(bookmarksSvc)

	expensesRepo := expenses.NewRepo(pool)
	expensesSvc := expenses.NewService(expensesRepo)
	expensesHandler := expenses.NewHandler(expensesSvc)

	retrievalCfg := retrieval.ConfigFromEnv()
	retrievalRepo := retrieval.NewRepo(pool)
	retrievalStore := retrieval.NewQdrantClient(retrievalCfg)
	retrievalEmbedder := retrieval.NewOpenAIEmbeddingClient(retrievalCfg)
	retrievalSvc := retrieval.NewService(retrievalRepo, retrievalStore, retrievalEmbedder)

	memoryRepo := memory.NewRepo(pool)
	memorySvc := memory.NewService(memoryRepo, retrievalSvc)
	memoryHandler := memory.NewHandler(memorySvc)

	skillsRepo := skills.NewRepo(pool)
	skillsSvc := skills.NewService(skillsRepo, retrievalSvc)
	skillsHandler := skills.NewHandler(skillsSvc)

	knowledgeRepo := knowledge.NewRepo(pool)
	knowledgeSvc := knowledge.NewService(knowledgeRepo, retrievalSvc)
	// K4 discovery resolves Skill knowledge contracts against the K0 catalog; the
	// contract is read through the skills domain so it stays the compiled one.
	knowledgeSvc.WithSkillContracts(skillsSvc)
	// The wiki compiler and its reviewer are configured independently so review
	// can run on a different vendor. Whether it actually does is recorded on every
	// build via Independence rather than assumed here.
	llmCfg := llm.ConfigFromEnv()
	knowledgeSvc.WithLLM(knowledge.LLMSetup{
		Compiler:        llm.NewHTTPClient(llm.RoleCompiler, llmCfg.Compiler),
		Reviewer:        llm.NewHTTPClient(llm.RoleReviewer, llmCfg.Reviewer),
		Independence:    llmCfg.Independence(),
		CompilerPricing: llmCfg.Compiler.Pricing,
		ReviewerPricing: llmCfg.Reviewer.Pricing,
	})
	log.Printf("wiki compiler model=%s reviewer=%s independence=%s",
		llmCfg.Compiler.Model, llmCfg.Reviewer.Model, llmCfg.Independence())
	knowledgeHandler := knowledge.NewHandler(knowledgeSvc)

	// Context Pack aggregates the three planes plus live app facts. It owns no
	// storage: every layer is read through the domain that owns it, so per-layer
	// ownership and scope rules keep applying.
	contextSvc := contextpack.NewService(contextpack.Providers{
		Skills:    skillsSvc,
		Knowledge: knowledgeSvc,
		Memory:    memorySvc,
		Todos:     todoSvc,
		Notes:     notesSvc,
	})
	contextHandler := contextpack.NewHandler(contextSvc)

	// Router
	r := gin.Default()
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Api-Key"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
		MaxAge:           12 * time.Hour,
	}))

	// 所有 REST API 挂在 /api 前缀下，与前端页面路径（/todos /reports/:id 等）区分，
	// 避免同源托管时路由冲突。/admin（静态页）、/mcp*（agent 集成）不受影响。
	api := r.Group("/api")

	// Public routes
	//
	// /api/health 是接入方在拿到凭证之前唯一能验证的东西：base URL 对不对。
	// 它必须无鉴权且返回 JSON——如果只能靠受保护端点探活，一个配错的 base URL
	// 会先表现为 401（看起来像"凭证不对"）而不是"地址不对"，把排查引向错误方向。
	api.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":  "ok",
			"service": "agentmate",
			// 声明 MCP 挂载点，省掉接入方猜路径。
			"mcp_endpoints": []string{
				"/mcp/todos", "/mcp/notes", "/mcp/reports", "/mcp/bookmarks",
				"/mcp/expenses", "/mcp/memory", "/mcp/skills", "/mcp/knowledge", "/mcp/context",
			},
		})
	})
	api.POST("/auth/register", authHandler.Register)
	api.POST("/auth/login", authHandler.Login)

	// /api/schema 让写入 schema 可以从 REST 侧发现。
	//
	// 真实接入里，唯一完整的 schema 真相源曾经只有 MCP 的 inputSchema：agent 必须
	// 先 initialize、再 tools/list、再从 memory_store 的 inputSchema 里翻出
	// memory_type 的合法值，在此之前一整轮 6 条写入全部 400。只走 REST 的调用方
	// 没有等价入口，而枚举值恰恰是猜不出来的那部分。
	//
	// 这里只声明枚举与必填约束，不是完整的 OpenAPI：端点列表已经在 README 与
	// llms.txt 里，而手写维护一份 OpenAPI 只会漂移成另一份过期文档。枚举值直接
	// 从各领域的校验表导出，所以它不可能和实际校验分叉。
	api.GET("/schema", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"memory": memory.InputSchema(),
			"note": "枚举值由服务端校验表导出，与实际校验同源。" +
				"400 响应体同时返回 fields[].field / fields[].allowed，可直接据此修正请求。",
		})
	})
	api.GET("/public/reports", reportsHandler.PublicList)
	api.GET("/public/reports/sources", reportsHandler.PublicSources)
	api.GET("/public/reports/:id", reportsHandler.PublicGet)

	// Protected routes
	protected := api.Group("/", auth.Middleware(authSvc), middleware.APILogger(pool))
	protected.GET("/auth/me", authHandler.Me)
	protected.POST("/auth/apikeys", authHandler.CreateAPIKey)
	protected.GET("/auth/apikeys", authHandler.ListAPIKeys)
	protected.DELETE("/auth/apikeys/:id", authHandler.DeleteAPIKey)

	// Tags aggregation
	tagsHandler := tags.NewHandler(pool)
	protected.GET("/tags", tagsHandler.List)

	// Todos - read
	protected.GET("/todos", auth.RequireScope("todos:r"), todoHandler.List)
	protected.GET("/todos/search", auth.RequireScope("todos:r"), todoHandler.Search)
	protected.GET("/todos/:id", auth.RequireScope("todos:r"), todoHandler.Get)
	// Todos - write
	protected.POST("/todos", auth.RequireScope("todos:rw"), todoHandler.Create)
	protected.PATCH("/todos/:id", auth.RequireScope("todos:rw"), todoHandler.Update)
	protected.DELETE("/todos/:id", auth.RequireScope("todos:rw"), todoHandler.Delete)

	// Notes - read
	protected.GET("/notes", auth.RequireScope("notes:r"), notesHandler.List)
	protected.GET("/notes/search", auth.RequireScope("notes:r"), notesHandler.Search)
	protected.GET("/notes/:id", auth.RequireScope("notes:r"), notesHandler.Get)
	// Notes - write
	protected.POST("/notes", auth.RequireScope("notes:rw"), notesHandler.Create)
	protected.PATCH("/notes/:id", auth.RequireScope("notes:rw"), notesHandler.Update)
	protected.PATCH("/notes/:id/append", auth.RequireScope("notes:rw"), notesHandler.Append)
	protected.DELETE("/notes/:id", auth.RequireScope("notes:rw"), notesHandler.Delete)

	// Reports - read
	protected.GET("/reports", auth.RequireScope("reports:r"), reportsHandler.List)
	protected.GET("/reports/sources", auth.RequireScope("reports:r"), reportsHandler.Sources)
	protected.GET("/reports/:id", auth.RequireScope("reports:r"), reportsHandler.Get)
	// Reports - write
	protected.POST("/reports", auth.RequireScope("reports:rw"), reportsHandler.Create)
	protected.PATCH("/reports/:id", auth.RequireScope("reports:rw"), reportsHandler.Update)
	protected.DELETE("/reports/:id", auth.RequireScope("reports:rw"), reportsHandler.Delete)

	// Bookmarks - read
	protected.GET("/bookmarks", auth.RequireScope("bookmarks:r"), bookmarksHandler.List)
	protected.GET("/bookmarks/:id", auth.RequireScope("bookmarks:r"), bookmarksHandler.Get)
	// Bookmarks - write
	protected.POST("/bookmarks", auth.RequireScope("bookmarks:rw"), bookmarksHandler.Create)
	protected.PATCH("/bookmarks/:id", auth.RequireScope("bookmarks:rw"), bookmarksHandler.Update)
	protected.DELETE("/bookmarks/:id", auth.RequireScope("bookmarks:rw"), bookmarksHandler.Delete)

	// Expenses - read
	protected.GET("/expenses", auth.RequireScope("expenses:r"), expensesHandler.List)
	protected.GET("/expenses/summary", auth.RequireScope("expenses:r"), expensesHandler.Summary)
	protected.GET("/expenses/:id", auth.RequireScope("expenses:r"), expensesHandler.Get)
	// Expenses - write
	protected.POST("/expenses", auth.RequireScope("expenses:rw"), expensesHandler.Create)
	protected.PATCH("/expenses/:id", auth.RequireScope("expenses:rw"), expensesHandler.Update)
	protected.DELETE("/expenses/:id", auth.RequireScope("expenses:rw"), expensesHandler.Delete)

	// Memory - read
	protected.GET("/memory/entries", auth.RequireScope("memory:r"), memoryHandler.ListEntries)
	protected.GET("/memory/scopes", auth.RequireScope("memory:r"), memoryHandler.ListScopes)
	protected.GET("/memory/entries/:id", auth.RequireScope("memory:r"), memoryHandler.GetEntry)
	protected.POST("/memory/search", auth.RequireScope("memory:r"), memoryHandler.SearchEntries)
	protected.GET("/memory/timeline", auth.RequireScope("memory:r"), memoryHandler.SessionTimeline)
	protected.GET("/memory/entries/:id/attribution", auth.RequireScope("memory:r"), memoryHandler.EntryAttribution)
	protected.GET("/memory/entries/:id/feedback", auth.RequireScope("memory:r"), memoryHandler.ListFeedback)
	protected.GET("/memory/resume", auth.RequireScope("memory:r"), memoryHandler.Resume)
	protected.POST("/memory/entries/:id/supersede", auth.RequireScope("memory:rw"), memoryHandler.SupersedeEntry)
	protected.POST("/memory/entries/:id/feedback", auth.RequireScope("memory:rw"), memoryHandler.RecordFeedback)
	protected.POST("/memory/checkpoints", auth.RequireScope("memory:rw"), memoryHandler.SaveCheckpoint)

	// Layer-level scopes are enforced inside the service; the route requires
	// only memory:r so a partially scoped key still gets the layers it may read.
	protected.POST("/context/pack", auth.RequireScope("memory:r"), contextHandler.Pack)
	// Memory - write
	protected.POST("/memory/events", auth.RequireScope("memory:rw"), memoryHandler.RecordEvent)
	protected.POST("/memory/entries", auth.RequireScope("memory:rw"), memoryHandler.CreateEntry)

	// Skills - read
	protected.GET("/skills/logs", auth.RequireScope("skills:r"), skillsHandler.ListLogs)
	protected.GET("/skills/sources", auth.RequireScope("skills:r"), skillsHandler.ListSources)
	protected.GET("/skills/sources/:id", auth.RequireScope("skills:r"), skillsHandler.GetSource)
	protected.GET("/skills/sources/:id/revisions", auth.RequireScope("skills:r"), skillsHandler.ListSourceRevisions)
	protected.GET("/skills/versions", auth.RequireScope("skills:r"), skillsHandler.ListVersions)
	protected.GET("/skills/versions/active", auth.RequireScope("skills:r"), skillsHandler.GetActiveVersion)
	protected.GET("/skills/catalog", auth.RequireScope("skills:r"), skillsHandler.ListCatalog)
	protected.GET("/skills/versions/:id/instructions", auth.RequireScope("skills:r"), skillsHandler.GetInstructions)
	protected.GET("/skills/versions/:id/resources", auth.RequireScope("skills:r"), skillsHandler.GetResources)
	protected.GET("/skills/versions/:id/resources/:file_id", auth.RequireScope("skills:r"), skillsHandler.GetResource)
	protected.GET("/skills/versions/:id/files", auth.RequireScope("skills:r"), skillsHandler.ListVersionFiles)
	protected.GET("/skills/versions/:id/quality-runs", auth.RequireScope("skills:r"), skillsHandler.ListQualityRuns)
	protected.GET("/skills/quality-runs/:run_id", auth.RequireScope("skills:r"), skillsHandler.GetQualityRun)
	protected.GET("/skills/stats", auth.RequireScope("skills:r"), skillsHandler.GetStats)
	protected.GET("/skills/signals", auth.RequireScope("skills:r"), skillsHandler.GetSignals)
	protected.POST("/skills/search", auth.RequireScope("skills:r"), skillsHandler.Search)
	// Skills - write
	protected.POST("/skills/logs", auth.RequireScope("skills:rw"), skillsHandler.CreateLog)
	protected.POST("/skills/sources", auth.RequireScope("skills:rw"), skillsHandler.CreateSource)
	protected.POST("/skills/sources/:id/snapshots", auth.RequireScope("skills:rw"), skillsHandler.SubmitLocalSnapshot)
	protected.POST("/skills/sources/:id/sync", auth.RequireScope("skills:rw"), skillsHandler.SyncGitSource)
	protected.POST("/skills/versions", auth.RequireScope("skills:rw"), skillsHandler.CreateVersion)
	protected.POST("/skills/versions/:id/activate", auth.RequireScope("skills:rw"), skillsHandler.ActivateVersion)
	protected.POST("/skills/versions/:id/quality-runs", auth.RequireScope("skills:rw"), skillsHandler.RunQuality)
	protected.POST("/skills/compile", auth.RequireScope("skills:rw"), skillsHandler.Compile)
	protected.POST("/skills/index", auth.RequireScope("skills:rw"), skillsHandler.IndexActiveVersions)

	// Knowledge - read
	protected.GET("/knowledge/sources", auth.RequireScope("knowledge:r"), knowledgeHandler.ListSources)
	protected.GET("/knowledge/sources/:id/revisions", auth.RequireScope("knowledge:r"), knowledgeHandler.ListSourceRevisions)
	protected.GET("/knowledge/revisions/:id/documents", auth.RequireScope("knowledge:r"), knowledgeHandler.ListRevisionDocuments)
	protected.GET("/knowledge/revisions/:id/documents/:doc_id", auth.RequireScope("knowledge:r"), knowledgeHandler.GetDocument)
	protected.GET("/knowledge/catalog", auth.RequireScope("knowledge:r"), knowledgeHandler.ListCatalog)
	protected.GET("/knowledge/documents/:doc_id/links", auth.RequireScope("knowledge:r"), knowledgeHandler.ListDocumentLinks)
	protected.POST("/knowledge/search", auth.RequireScope("knowledge:r"), knowledgeHandler.Search)
	// K4 discovery reads the compiled contract (skills domain) and the K0 catalog
	// (knowledge domain); both scopes are required, matching the context pack's
	// per-domain authorisation stance.
	protected.POST("/knowledge/discover", auth.RequireScope("knowledge:r"), auth.RequireScope("skills:r"), knowledgeHandler.Discover)
	// K4 resolution runs: recording validates the requirement against the compiled
	// contract (skills data), so the write route needs skills:r on top of knowledge:rw.
	protected.POST("/knowledge/resolutions", auth.RequireScope("knowledge:rw"), auth.RequireScope("skills:r"), knowledgeHandler.RecordResolution)
	protected.GET("/knowledge/resolutions", auth.RequireScope("knowledge:r"), knowledgeHandler.ListResolutions)
	protected.GET("/knowledge/resolutions/:run_id", auth.RequireScope("knowledge:r"), knowledgeHandler.GetResolution)

	// K3 wiki reads. Builds are immutable, so these are all safe to cache-bust
	// on id alone.
	protected.GET("/knowledge/builds", auth.RequireScope("knowledge:r"), knowledgeHandler.ListBuilds)
	protected.GET("/knowledge/builds/:build_id", auth.RequireScope("knowledge:r"), knowledgeHandler.GetBuild)
	protected.GET("/knowledge/builds/:build_id/pages", auth.RequireScope("knowledge:r"), knowledgeHandler.ListBuildPages)
	protected.GET("/knowledge/builds/:build_id/pages/*path", auth.RequireScope("knowledge:r"), knowledgeHandler.GetBuildPage)
	protected.GET("/knowledge/builds/:build_id/diff", auth.RequireScope("knowledge:r"), knowledgeHandler.DiffBuilds)
	protected.GET("/knowledge/builds/:build_id/events", auth.RequireScope("knowledge:r"), knowledgeHandler.ListBuildEvents)
	// Knowledge - write
	protected.POST("/knowledge/sources", auth.RequireScope("knowledge:rw"), knowledgeHandler.CreateSource)
	protected.POST("/knowledge/sources/:id/snapshots", auth.RequireScope("knowledge:rw"), knowledgeHandler.SubmitSnapshot)
	protected.POST("/knowledge/sources/:id/sync", auth.RequireScope("knowledge:rw"), knowledgeHandler.SyncGitSource)
	protected.POST("/knowledge/index", auth.RequireScope("knowledge:rw"), knowledgeHandler.IndexActiveRevisions)
	// Compilation writes a new build and moves the active pointer, so it needs
	// write scope even though it only reads the raw layer.
	protected.POST("/knowledge/compile", auth.RequireScope("knowledge:rw"), knowledgeHandler.Compile)
	protected.GET("/knowledge/queue", auth.RequireScope("knowledge:r"), knowledgeHandler.QueueStats)
	// K3.6 wiki retrieval. Search is a read; indexing writes retrieval rows.
	protected.POST("/knowledge/wiki/search", auth.RequireScope("knowledge:r"), knowledgeHandler.SearchWiki)
	protected.GET("/knowledge/wiki/index", auth.RequireScope("knowledge:r"), knowledgeHandler.WikiIndexStatus)
	protected.POST("/knowledge/wiki/index", auth.RequireScope("knowledge:rw"), knowledgeHandler.IndexActiveWikiBuilds)
	// K3.7 lint. Write scope because it records a run, but it changes no wiki content
	// and blocks nothing: findings describe a wiki that is already serving.
	protected.POST("/knowledge/wiki/lint", auth.RequireScope("knowledge:rw"), knowledgeHandler.LintWiki)
	protected.GET("/knowledge/wiki/lint/runs", auth.RequireScope("knowledge:r"), knowledgeHandler.ListLintRuns)
	protected.GET("/knowledge/wiki/lint/runs/:run_id", auth.RequireScope("knowledge:r"), knowledgeHandler.GetLintRun)
	// K3.8 review. Write scope because it spends money on a model and records a verdict;
	// it still cannot change a page or block a build.
	protected.POST("/knowledge/builds/:build_id/review", auth.RequireScope("knowledge:rw"), knowledgeHandler.ReviewBuild)
	protected.GET("/knowledge/builds/:build_id/review", auth.RequireScope("knowledge:r"), knowledgeHandler.GetBuildReview)
	// K3.9 validation signals. Recording evidence is a write; it gates nothing.
	protected.POST("/knowledge/validation/signals", auth.RequireScope("knowledge:rw"), knowledgeHandler.RecordSignal)
	protected.GET("/knowledge/validation/signals", auth.RequireScope("knowledge:r"), knowledgeHandler.ListSignals)
	protected.GET("/knowledge/validation/summary", auth.RequireScope("knowledge:r"), knowledgeHandler.SignalSummary)
	protected.POST("/knowledge/validation/sweep", auth.RequireScope("knowledge:rw"), knowledgeHandler.SweepNeverRetrieved)
	protected.GET("/knowledge/validation/skill-patterns", auth.RequireScope("knowledge:r"), knowledgeHandler.SkillPatterns)
	protected.POST("/knowledge/builds/:build_id/activate", auth.RequireScope("knowledge:rw"), knowledgeHandler.ActivateBuild)

	// Admin
	adminHandler := admin.NewHandler(pool, retrievalRepo)
	r.StaticFile("/admin", "./web/admin.html")
	adminAPI := r.Group("/api/admin", admin.Middleware(authSvc))
	adminAPI.GET("/stats", adminHandler.Stats)
	adminAPI.GET("/users", adminHandler.Users)
	adminAPI.GET("/apikeys", adminHandler.APIKeys)
	adminAPI.GET("/reports", adminHandler.Reports)
	adminAPI.GET("/usage", adminHandler.Usage)
	adminAPI.POST("/retrieval/lexical/rebuild", adminHandler.RebuildLexical)

	// MCP Servers — 每个业务模块独立挂载一个 Streamable HTTP MCP 端点，
	// 而不是聚合成单个 /mcp（与 skills 保持一致的模式）。
	r.Any("/mcp/todos", gin.WrapH(todo.NewMCPServer(todoSvc, authSvc)))
	r.Any("/mcp/notes", gin.WrapH(notes.NewMCPServer(notesSvc, authSvc)))
	r.Any("/mcp/reports", gin.WrapH(reports.NewMCPServer(reportsSvc, authSvc)))
	r.Any("/mcp/bookmarks", gin.WrapH(bookmarks.NewMCPServer(bookmarksSvc, authSvc)))
	r.Any("/mcp/expenses", gin.WrapH(expenses.NewMCPServer(expensesSvc, authSvc)))
	r.Any("/mcp/memory", gin.WrapH(memory.NewMCPServer(memorySvc, authSvc)))
	r.Any("/mcp/skills", gin.WrapH(skills.NewMCPServer(skillsSvc, authSvc)))
	r.Any("/mcp/knowledge", gin.WrapH(knowledge.NewMCPServer(knowledgeSvc, authSvc)))
	r.Any("/mcp/context", gin.WrapH(contextpack.NewMCPServer(contextSvc, authSvc)))

	registerFrontend(r)

	// The compile worker runs in-process. Compilation is a multi-minute model call
	// and cannot live inside a request, but it does not need a separate deployment
	// either: the queue is a table, so several replicas of this same binary form
	// the worker pool, and leases keep them from compiling the same build twice.
	workerCtx, stopWorker := context.WithCancel(context.Background())
	wikiWorker := knowledge.NewWorker(knowledgeSvc, knowledgeRepo, knowledge.WorkerConfigFromEnv(llmCfg.Compiler.Timeout))
	wikiWorker.Start(workerCtx)

	server := &http.Server{Addr: ":" + port, Handler: r}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	printBanner(port)

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	log.Printf("shutting down")

	// Stop accepting requests first, then let the worker hand its builds back.
	// Doing it the other way round would let a request enqueue work into a queue
	// nobody is serving any more.
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelShutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("http shutdown: %v", err)
	}
	stopWorker()
	wikiWorker.Stop(25 * time.Second)
}

const banner = `
 _   _ _____ _     _     ___
| | | | ____| |   | |   / _ \
| |_| |  _| | |   | |  | | | |
|  _  | |___| |___| |__| |_| |
|_| |_|_____|_____|_____\___/

      agentmate is running
`

func printBanner(port string) {
	log.Print(banner)
	log.Printf("listening on http://0.0.0.0:%s (accessible on all network interfaces)", port)
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// registerFrontend 托管 agentmate-web 的静态导出产物（next build, output: 'export'）。
// 产物目录结构是 <route>.html + <route>/ 混合布局，不是单一 index.html 的 SPA，
// 因此 NoRoute 兜底时按「请求路径 + .html」优先查找，找不到再退回根 index.html
// （交给前端的客户端路由处理未知路径，如报告/书签详情页的动态 id）。
func registerFrontend(r *gin.Engine) {
	const dir = "./web/dist"
	if _, err := os.Stat(dir); err != nil {
		log.Printf("frontend: %s not found, skip serving (run web build first)", dir)
		return
	}

	r.Static("/_next", dir+"/_next")
	r.NoRoute(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}

		reqPath := c.Request.URL.Path

		// API 与 MCP 命名空间永不回退到前端。落到 SPA 会让拼错的端点、尚未实现的
		// 端点都返回 200 + HTML，调用方把 HTML 当 JSON 解析，报错指向解析失败而不是
		// 指向真正的原因（路径不存在）。这里必须给出机器可读的 404。
		if strings.HasPrefix(reqPath, "/api/") || reqPath == "/api" ||
			strings.HasPrefix(reqPath, "/mcp/") || reqPath == "/mcp" {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such endpoint: " + reqPath})
			return
		}

		if dynamicCandidate := dynamicExportCandidate(dir, reqPath); dynamicCandidate != "" && fileExists(dynamicCandidate) {
			c.File(dynamicCandidate)
			return
		}

		htmlCandidate := filepath.Join(dir, reqPath+".html")
		if reqPath == "/" {
			htmlCandidate = filepath.Join(dir, "index.html")
		}
		if fileExists(htmlCandidate) {
			c.File(htmlCandidate)
			return
		}

		staticCandidate := filepath.Join(dir, reqPath)
		if fileExists(staticCandidate) {
			c.File(staticCandidate)
			return
		}

		c.File(filepath.Join(dir, "index.html"))
	})
}

func dynamicExportCandidate(dir, reqPath string) string {
	// 更长的前缀在前：/reports/manage/<id> 是管理详情的动态路由，必须先于
	// /reports/<id>（公开文章页）匹配，否则 manage 会被当成一篇报告的 id。
	for _, route := range []string{"reports/manage", "reports", "bookmarks"} {
		prefix := "/" + route + "/"
		if !strings.HasPrefix(reqPath, prefix) {
			continue
		}

		rest := strings.TrimPrefix(reqPath, prefix)
		if rest == "" || strings.Contains(rest, "..") {
			continue
		}

		parts := strings.SplitN(rest, "/", 2)
		id := parts[0]
		if id == "" {
			continue
		}

		if len(parts) == 1 {
			switch {
			case strings.HasSuffix(id, ".txt"):
				return filepath.Join(dir, route, "placeholder.txt")
			case strings.HasSuffix(id, ".html"):
				return filepath.Join(dir, route, "placeholder.html")
			case !strings.Contains(id, "."):
				return filepath.Join(dir, route, "placeholder.html")
			}
			continue
		}

		return filepath.Join(dir, route, "placeholder", parts[1])
	}
	return ""
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
