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
