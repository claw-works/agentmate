package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testRole(server *httptest.Server) RoleConfig {
	return RoleConfig{
		BaseURL:     server.URL,
		APIKey:      "test-key",
		Model:       "test-model",
		Temperature: 0.2,
		MaxTokens:   256,
		Timeout:     5 * time.Second,
	}
}

func TestCompleteSendsConfiguredRequestAndReportsUsage(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization = %q", got)
		}
		_ = json.NewDecoder(r.Body).Decode(&received)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model": "test-model-2026",
			"choices": []map[string]any{{
				"message":       map[string]string{"role": "assistant", "content": `{"pages":[]}`},
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 11, "completion_tokens": 7, "total_tokens": 18},
		})
	}))
	defer server.Close()

	client := NewHTTPClient(RoleCompiler, testRole(server))
	completion, err := client.Complete(context.Background(),
		[]Message{{Role: "user", Content: "hi"}}, WithJSONObject(), WithTemperature(0))
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if completion.Content != `{"pages":[]}` {
		t.Fatalf("content = %q", completion.Content)
	}
	// The provider's reported model is kept, not the configured alias: provenance
	// must record what actually answered.
	if completion.Model != "test-model-2026" {
		t.Fatalf("model = %q", completion.Model)
	}
	if completion.Usage.TotalTokens != 18 {
		t.Fatalf("usage = %+v", completion.Usage)
	}
	if received["temperature"] != float64(0) {
		t.Fatalf("temperature override lost: %v", received["temperature"])
	}
	if _, ok := received["response_format"]; !ok {
		t.Fatal("json object request not sent")
	}
}

// A truncated completion would be parsed as if complete, silently dropping pages
// or citations. It has to fail loudly.
func TestCompleteRejectsTruncatedOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message":       map[string]string{"role": "assistant", "content": `{"pages":[{"pa`},
				"finish_reason": "length",
			}},
		})
	}))
	defer server.Close()

	client := NewHTTPClient(RoleCompiler, testRole(server))
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected a truncated completion to fail")
	}
	if !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("error = %v", err)
	}
}

func TestCompleteSurfacesProviderErrorWithoutLeakingKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	}))
	defer server.Close()

	client := NewHTTPClient(RoleReviewer, testRole(server))
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "429") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("error should carry the provider detail: %v", err)
	}
	if strings.Contains(err.Error(), "test-key") {
		t.Fatalf("error leaked the api key: %v", err)
	}
}

// A missing credential is an operator problem, and callers distinguish it from a
// failed build, so it needs its own sentinel.
func TestCompleteReportsNotConfigured(t *testing.T) {
	client := NewHTTPClient(RoleCompiler, RoleConfig{BaseURL: "https://x.example", Model: "m"})
	_, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("error = %v, want ErrNotConfigured", err)
	}
	if client.Configured() {
		t.Fatal("client should report itself unconfigured")
	}
}

func TestCompleteRejectsEmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	defer server.Close()

	client := NewHTTPClient(RoleCompiler, testRole(server))
	if _, err := client.Complete(context.Background(), []Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatal("expected an error when the provider returns no choices")
	}
}

// TestRetryableClassification pins which failures are worth another multi-minute
// model call. Getting this wrong is expensive in both directions: retrying a
// truncated reply pays twice for the same outcome, while not retrying a dropped
// connection throws away a build over a network blip.
func TestRetryableClassification(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "transport failure", err: &Error{Message: "unexpected EOF", Retryable: true}, want: true},
		{name: "rate limited", err: &Error{Status: 429, Retryable: retryableStatus(429)}, want: true},
		{name: "provider fault", err: &Error{Status: 503, Retryable: retryableStatus(503)}, want: true},
		{name: "bad request", err: &Error{Status: 400, Retryable: retryableStatus(400)}, want: false},
		{name: "unauthorized", err: &Error{Status: 401, Retryable: retryableStatus(401)}, want: false},
		{name: "truncated reply", err: &Error{Status: 200, Message: "hit the token limit", Retryable: false}, want: false},
		// An unknown error is not retryable: a compile is expensive, so the default
		// has to be the cheap answer.
		{name: "unknown error", err: errors.New("something else"), want: false},
		{name: "not configured", err: ErrNotConfigured, want: false},
	} {
		if got := Retryable(testCase.err); got != testCase.want {
			t.Errorf("%s: Retryable = %v, want %v", testCase.name, got, testCase.want)
		}
	}
}

// TestPricingIsZeroWhenUnconfigured keeps an invented default out of the
// accounting. A made-up rate produces an authoritative-looking wrong number; zero
// is visibly unknown.
func TestPricingIsZeroWhenUnconfigured(t *testing.T) {
	usage := Usage{PromptTokens: 1000, CompletionTokens: 2000, TotalTokens: 3000}
	var unset Pricing
	if unset.Configured() {
		t.Fatal("an unset price must not report itself as configured")
	}
	if cost := unset.Cost(usage); cost != 0 {
		t.Fatalf("expected zero cost when no price is configured, got %d", cost)
	}
	priced := Pricing{InputMicrosPer1KTokens: 2000, OutputMicrosPer1KTokens: 6000}
	if cost := priced.Cost(usage); cost != 14000 {
		t.Fatalf("expected 14000 micros, got %d", cost)
	}
}
