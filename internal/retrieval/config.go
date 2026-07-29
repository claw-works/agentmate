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
	EmbeddingFormat    string

	// CheapLLM* is read but not yet consumed by anything.
	//
	// It reserves the credential for the cheap-LLM query planner and reranker in
	// memory-design-v0.3 (§“未来 cheap LLM query planner/reranker”), which has not
	// been implemented. Setting these variables therefore has no observable effect
	// today, and that is worth stating here: a populated config field reads like a
	// wired capability, and the next person to set CHEAP_LLM_MODEL will otherwise
	// spend time wondering why nothing changed.
	//
	// The DeepSeek credential itself does have a live consumer — REVIEWER_API_KEY in
	// internal/llm, where a second vendor is what makes reviewer_independence
	// cross_provider instead of same_provider.
	CheapLLMBaseURL            string
	CheapLLMChatCompletionsURL string
	CheapLLMAPIKey             string
	CheapLLMModel              string

	Timeout time.Duration
}

func ConfigFromEnv() Config {
	cheapLLMBaseURL := strings.TrimRight(env("CHEAP_LLM_BASE_URL", "https://api.deepseek.com"), "/")
	return Config{
		QdrantURL:        env("QDRANT_URL", "http://localhost:6333"),
		QdrantAPIKey:     os.Getenv("QDRANT_API_KEY"),
		QdrantCollection: env("QDRANT_COLLECTION", DefaultCollection),
		QdrantVectorName: env("QDRANT_VECTOR_NAME", DefaultVectorName),
		QdrantDistance:   env("QDRANT_DISTANCE", DefaultDistance),

		EmbeddingBaseURL:   strings.TrimRight(env("EMBEDDING_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"), "/"),
		EmbeddingAPIKey:    os.Getenv("EMBEDDING_API_KEY"),
		EmbeddingModel:     env("EMBEDDING_MODEL", "text-embedding-v4"),
		EmbeddingDimension: envInt("EMBEDDING_DIMENSION", 1024),
		EmbeddingFormat:    env("EMBEDDING_ENCODING_FORMAT", "float"),

		CheapLLMBaseURL:            cheapLLMBaseURL,
		CheapLLMChatCompletionsURL: env("CHEAP_LLM_CHAT_COMPLETIONS_URL", cheapLLMBaseURL+"/chat/completions"),
		CheapLLMAPIKey:             os.Getenv("CHEAP_LLM_API_KEY"),
		CheapLLMModel:              env("CHEAP_LLM_MODEL", "deepseek-v4-flash"),

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
