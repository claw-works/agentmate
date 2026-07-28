package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/wellxie/agentmate/internal/admin"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/bookmarks"
	"github.com/wellxie/agentmate/internal/db"
	"github.com/wellxie/agentmate/internal/expenses"
	"github.com/wellxie/agentmate/internal/knowledge"
	"github.com/wellxie/agentmate/internal/memory"
	"github.com/wellxie/agentmate/internal/middleware"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/reports"
	"github.com/wellxie/agentmate/internal/retrieval"
	"github.com/wellxie/agentmate/internal/skills"
	"github.com/wellxie/agentmate/internal/tags"
	"github.com/wellxie/agentmate/internal/todo"
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
	knowledgeHandler := knowledge.NewHandler(knowledgeSvc)

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
	api.POST("/auth/register", authHandler.Register)
	api.POST("/auth/login", authHandler.Login)
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
	protected.GET("/memory/entries/:id", auth.RequireScope("memory:r"), memoryHandler.GetEntry)
	protected.POST("/memory/search", auth.RequireScope("memory:r"), memoryHandler.SearchEntries)
	protected.GET("/memory/timeline", auth.RequireScope("memory:r"), memoryHandler.SessionTimeline)
	protected.GET("/memory/entries/:id/attribution", auth.RequireScope("memory:r"), memoryHandler.EntryAttribution)
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
	// Knowledge - write
	protected.POST("/knowledge/sources", auth.RequireScope("knowledge:rw"), knowledgeHandler.CreateSource)
	protected.POST("/knowledge/sources/:id/snapshots", auth.RequireScope("knowledge:rw"), knowledgeHandler.SubmitSnapshot)
	protected.POST("/knowledge/sources/:id/sync", auth.RequireScope("knowledge:rw"), knowledgeHandler.SyncGitSource)
	protected.POST("/knowledge/index", auth.RequireScope("knowledge:rw"), knowledgeHandler.IndexActiveRevisions)

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

	registerFrontend(r)

	printBanner(port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
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
	for _, route := range []string{"reports", "bookmarks"} {
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
