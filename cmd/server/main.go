package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/wellxie/agentmate/internal/admin"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/db"
	"github.com/wellxie/agentmate/internal/notes"
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

	// Router
	r := gin.Default()

	// Public routes
	r.POST("/auth/register", authHandler.Register)
	r.POST("/auth/login", authHandler.Login)

	// Protected routes
	protected := r.Group("/", auth.Middleware(authSvc))
	protected.GET("/auth/me", authHandler.Me)
	protected.POST("/auth/apikeys", authHandler.CreateAPIKey)
	protected.GET("/auth/apikeys", authHandler.ListAPIKeys)
	protected.DELETE("/auth/apikeys/:id", authHandler.DeleteAPIKey)

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

	// Admin
	adminHandler := admin.NewHandler(pool)
	r.StaticFile("/admin", "./web/admin.html")
	adminAPI := r.Group("/admin/api", admin.Middleware(authSvc))
	adminAPI.GET("/stats", adminHandler.Stats)
	adminAPI.GET("/users", adminHandler.Users)
	adminAPI.GET("/apikeys", adminHandler.APIKeys)

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
