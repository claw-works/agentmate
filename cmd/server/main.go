package main

import (
	"context"
	"log"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/wellxie/agentmate/internal/auth"
	"github.com/wellxie/agentmate/internal/db"
	"github.com/wellxie/agentmate/internal/notes"
	"github.com/wellxie/agentmate/internal/todo"
)

func main() {
	ctx := context.Background()

	dbURL := env("DATABASE_URL", "postgres://agentmate:secret@localhost:5432/agentmate?sslmode=disable")
	jwtSecret := env("JWT_SECRET", "change-me")
	port := env("SERVER_PORT", "8080")

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

	protected.POST("/todos", todoHandler.Create)
	protected.GET("/todos", todoHandler.List)
	protected.GET("/todos/search", todoHandler.Search)
	protected.GET("/todos/:id", todoHandler.Get)
	protected.PATCH("/todos/:id", todoHandler.Update)
	protected.DELETE("/todos/:id", todoHandler.Delete)

	protected.POST("/notes", notesHandler.Create)
	protected.GET("/notes", notesHandler.List)
	protected.GET("/notes/search", notesHandler.Search)
	protected.GET("/notes/:id", notesHandler.Get)
	protected.PATCH("/notes/:id", notesHandler.Update)
	protected.DELETE("/notes/:id", notesHandler.Delete)

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
