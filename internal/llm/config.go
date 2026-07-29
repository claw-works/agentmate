// Package llm provides a minimal chat-completions client for the two LLM roles
// the platform needs: the compiler that writes wiki pages, and the reviewer that
// checks whether those pages stay faithful to their citations.
//
// The two roles are configured independently — endpoint, key and model each —
// because the reviewer is supposed to be heterogeneous. A reviewer sharing the
// generator's priors misses the generator's mistakes, so review has to be able to
// run on a different provider entirely. Whether it actually does is recorded per
// build rather than assumed: see Independence.
package llm

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Role names the two configured LLM identities.
const (
	RoleCompiler = "compiler"
	RoleReviewer = "reviewer"
)

// Independence describes how separated the reviewer is from the compiler. It is
// stored alongside each build so the collusion risk of a given result is visible
// in the data instead of depending on someone remembering the configuration at
// the time.
const (
	// IndependenceCrossProvider: different endpoints, so different vendors.
	IndependenceCrossProvider = "cross_provider"
	// IndependenceSameProvider: same endpoint, different model. Reduces
	// correlation but does not remove it — models from one vendor share training
	// data and failure modes.
	IndependenceSameProvider = "same_provider"
	// IndependenceSameModel: identical model. Self-review; treat its verdicts as
	// nearly uninformative.
	IndependenceSameModel = "same_model"
	// IndependenceUnavailable: the reviewer is not configured.
	IndependenceUnavailable = "unavailable"
)

type RoleConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	Temperature float64
	MaxTokens   int
	Timeout     time.Duration
	// Pricing is per-role because the compiler and reviewer are different models
	// and often different vendors.
	Pricing Pricing
}

// Pricing converts token counts into recorded cost.
//
// Both rates default to zero, and a zero rate yields zero cost rather than a
// guess. Cost control is a design requirement, and an invented default would put
// an authoritative-looking wrong number in the accounting — worse than a visible
// zero that says "nobody told us the price".
type Pricing struct {
	InputMicrosPer1KTokens  int64
	OutputMicrosPer1KTokens int64
}

func (p Pricing) Configured() bool {
	return p.InputMicrosPer1KTokens > 0 || p.OutputMicrosPer1KTokens > 0
}

// Cost prices one completion in micros. Integer arithmetic throughout: money in
// floats accumulates error that later has to be explained to someone.
func (p Pricing) Cost(usage Usage) int64 {
	return (int64(usage.PromptTokens)*p.InputMicrosPer1KTokens +
		int64(usage.CompletionTokens)*p.OutputMicrosPer1KTokens) / 1000
}

func (c RoleConfig) Configured() bool {
	return c.APIKey != "" && c.Model != "" && c.BaseURL != ""
}

type Config struct {
	Compiler RoleConfig
	Reviewer RoleConfig
}

// Independence classifies the reviewer relative to the compiler.
func (c Config) Independence() string {
	if !c.Reviewer.Configured() {
		return IndependenceUnavailable
	}
	switch {
	case !strings.EqualFold(c.Reviewer.BaseURL, c.Compiler.BaseURL):
		return IndependenceCrossProvider
	case strings.EqualFold(c.Reviewer.Model, c.Compiler.Model):
		return IndependenceSameModel
	default:
		return IndependenceSameProvider
	}
}

// ConfigFromEnv reads both roles.
//
// Each role falls back to the embedding endpoint and key, because in practice a
// deployment starts with one provider credential and only later splits the roles
// apart. Falling back keeps that first deployment working while Independence
// reports honestly that the reviewer is not actually independent yet.
func ConfigFromEnv() Config {
	sharedBaseURL := env("EMBEDDING_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1")
	sharedKey := os.Getenv("EMBEDDING_API_KEY")

	compiler := RoleConfig{
		BaseURL: strings.TrimRight(env("COMPILER_BASE_URL", sharedBaseURL), "/"),
		APIKey:  env("COMPILER_API_KEY", sharedKey),
		Model:   env("COMPILER_MODEL", "qwen3.7-plus"),
		// Compilation must be as reproducible as an LLM allows: the same sources
		// should not produce wildly different wikis run to run. It is still not
		// deterministic, which is why builds are immutable and versioned.
		Temperature: envFloat("COMPILER_TEMPERATURE", 0.2),
		// A whole wiki is emitted in one reply, so the output budget has to hold
		// every page at once. 4096 was the first value tried and it truncated a
		// three-document knowledge base immediately; 16384 then truncated a 6 KB
		// corpus while a larger one had fit in 13k, because a reasoning compiler
		// spends its output budget on its own thinking before it writes anything.
		// The budget therefore has to be a large multiple of the expected wiki
		// size, not a snug fit. The client turns a truncated reply into a failed
		// build rather than a partial wiki, which is what made this visible
		// instead of silently dropping pages.
		MaxTokens: envInt("COMPILER_MAX_TOKENS", 32768),
		// Long by deliberate choice: one call emits a whole wiki, and a reasoning
		// model producing 16k tokens routinely runs past three minutes — 180s
		// timed out on a three-document knowledge base. A generous ceiling here is
		// not a substitute for the asynchronous job K3.4 adds; it only makes the
		// synchronous path usable for small corpora.
		Timeout: time.Duration(envInt("COMPILER_TIMEOUT_SECONDS", 900)) * time.Second,
		Pricing: Pricing{
			InputMicrosPer1KTokens:  int64(envInt("COMPILER_INPUT_MICROS_PER_1K_TOKENS", 0)),
			OutputMicrosPer1KTokens: int64(envInt("COMPILER_OUTPUT_MICROS_PER_1K_TOKENS", 0)),
		},
	}
	reviewer := RoleConfig{
		BaseURL: strings.TrimRight(env("REVIEWER_BASE_URL", sharedBaseURL), "/"),
		APIKey:  env("REVIEWER_API_KEY", sharedKey),
		// A different model by default so review is at least not literal
		// self-review. Point REVIEWER_BASE_URL at another vendor for real
		// independence.
		Model: env("REVIEWER_MODEL", "qwen-max"),
		// Judgement should be near-deterministic: the same claim and the same
		// source ought to get the same verdict.
		Temperature: envFloat("REVIEWER_TEMPERATURE", 0),
		MaxTokens:   envInt("REVIEWER_MAX_TOKENS", 2048),
		Timeout:     time.Duration(envInt("REVIEWER_TIMEOUT_SECONDS", 120)) * time.Second,
		Pricing: Pricing{
			InputMicrosPer1KTokens:  int64(envInt("REVIEWER_INPUT_MICROS_PER_1K_TOKENS", 0)),
			OutputMicrosPer1KTokens: int64(envInt("REVIEWER_OUTPUT_MICROS_PER_1K_TOKENS", 0)),
		},
	}
	return Config{Compiler: compiler, Reviewer: reviewer}
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil && value > 0 {
		return value
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}
