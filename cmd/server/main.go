package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/wellxie/agentmate/internal/admin"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/db"
	"github.com/wellxie/agentmate/internal/middleware"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/reports"
	"github.com/wellxie/agentmate/internal/tags"
	"github.com/wellxie/agentmate/internal/todo"
	"github.com/wellxie/agentmate/mcp"
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

	// Public routes
	r.POST("/auth/register", authHandler.Register)
	r.POST("/auth/login", authHandler.Login)

	// Protected routes
	protected := r.Group("/", auth.Middleware(authSvc), middleware.APILogger(pool))
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
	protected.DELETE("/notes/:id", auth.RequireScope("notes:rw"), notesHandler.Delete)

	// Reports - read
	protected.GET("/reports", auth.RequireScope("reports:r"), reportsHandler.List)
	protected.GET("/reports/sources", auth.RequireScope("reports:r"), reportsHandler.Sources)
	protected.GET("/reports/:id", auth.RequireScope("reports:r"), reportsHandler.Get)
	// Reports - write
	protected.POST("/reports", auth.RequireScope("reports:rw"), reportsHandler.Create)
	protected.PATCH("/reports/:id", auth.RequireScope("reports:rw"), reportsHandler.Update)
	protected.DELETE("/reports/:id", auth.RequireScope("reports:rw"), reportsHandler.Delete)

	// Admin
	adminHandler := admin.NewHandler(pool)
	r.StaticFile("/admin", "./web/admin.html")
	adminAPI := r.Group("/admin/api", admin.Middleware(authSvc))
	adminAPI.GET("/stats", adminHandler.Stats)
	adminAPI.GET("/users", adminHandler.Users)
	adminAPI.GET("/apikeys", adminHandler.APIKeys)
	adminAPI.GET("/reports", adminHandler.Reports)
	adminAPI.GET("/usage", adminHandler.Usage)

	// MCP Server
	if mcpPort := env("MCP_PORT", ""); mcpPort != "" {
		mcpSrv := mcp.NewServer(todoSvc, notesSvc, reportsSvc)

		// session -> userID mapping, populated during SSE GET, read during message POST
		var sessionUsers sync.Map

		sseSrv := mcpserver.NewSSEServer(mcpSrv,
			mcpserver.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
				// Look up userID stored by the middleware during the SSE GET
				sessionID := r.URL.Query().Get("sessionId")
				if userID, ok := sessionUsers.Load(sessionID); ok {
					return context.WithValue(ctx, mcp.UserIDKey, userID.(string))
				}
				return ctx
			}),
		)

		mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Auth on SSE connection (GET /sse)
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/sse") {
				apiKey := r.URL.Query().Get("api_key")
				if apiKey == "" {
					apiKey = r.Header.Get("X-Api-Key")
				}
				if apiKey == "" {
					if bearer := r.Header.Get("Authorization"); strings.HasPrefix(bearer, "Bearer ") {
						apiKey = strings.TrimPrefix(bearer, "Bearer ")
					}
				}
				if apiKey == "" {
					http.Error(w, "missing api_key", http.StatusUnauthorized)
					return
				}
				ak, err := authSvc.ValidateAPIKey(r.Context(), apiKey)
				if err != nil {
					http.Error(w, "invalid api_key", http.StatusUnauthorized)
					return
				}
				// Capture the sessionId from the response to map it to the user.
				// mcp-go writes the session endpoint as the first SSE event, so we
				// wrap the ResponseWriter to sniff the sessionId.
				sw := &sessionCapture{ResponseWriter: w, userID: ak.UserID, store: &sessionUsers}
				sseSrv.ServeHTTP(sw, r)
				return
			}
			sseSrv.ServeHTTP(w, r)
		})

		go func() {
			log.Printf("starting MCP SSE server on :%s", mcpPort)
			if err := http.ListenAndServe(":"+mcpPort, mcpHandler); err != nil {
				log.Printf("mcp server error: %v", err)
			}
		}()
	}

	log.Printf("starting server on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// sessionCapture wraps http.ResponseWriter to extract the sessionId from the
// first SSE event written by mcp-go, then stores the userID mapping.
type sessionCapture struct {
	http.ResponseWriter
	userID  string
	store   *sync.Map
	captured bool
}

func (sc *sessionCapture) Write(b []byte) (int, error) {
	if !sc.captured {
		sc.captured = true
		// First write is: "event: endpoint\ndata: ...?sessionId=<uuid>\r\n\r\n"
		if s := string(b); strings.Contains(s, "sessionId=") {
			parts := strings.SplitAfter(s, "sessionId=")
			if len(parts) > 1 {
				sessionID := strings.TrimRight(parts[1], "\r\n")
				sc.store.Store(sessionID, sc.userID)
			}
		}
	}
	return sc.ResponseWriter.Write(b)
}

func (sc *sessionCapture) Flush() {
	if f, ok := sc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
