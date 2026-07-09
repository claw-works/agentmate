package retrieval

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	QdrantURL        string
	QdrantAPIKey     string
	QdrantCollection string
	QdrantVectorName string
	QdrantDistance   string

	EmbeddingBaseURL   string
	EmbeddingAPIKey    string
	EmbeddingModel     string
	EmbeddingDimension int

	Timeout time.Duration
}

func ConfigFromEnv() Config {
	return Config{
		QdrantURL:        env("QDRANT_URL", "http://localhost:6333"),
		QdrantAPIKey:     os.Getenv("QDRANT_API_KEY"),
		QdrantCollection: env("QDRANT_COLLECTION", DefaultCollection),
		QdrantVectorName: env("QDRANT_VECTOR_NAME", DefaultVectorName),
		QdrantDistance:   env("QDRANT_DISTANCE", DefaultDistance),

		EmbeddingBaseURL:   strings.TrimRight(env("EMBEDDING_BASE_URL", "https://api.openai.com/v1"), "/"),
		EmbeddingAPIKey:    os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:     env("EMBEDDING_MODEL", "text-embedding-3-small"),
		EmbeddingDimension: envInt("EMBEDDING_DIMENSION", 1536),

		Timeout: time.Duration(envInt("RETRIEVAL_TIMEOUT_SECONDS", 15)) * time.Second,
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		i, err := strconv.Atoi(v)
		if err == nil {
			return i
		}
	}
	return fallback
}
