package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ErrNotConfigured means the role has no usable endpoint, key or model. Callers
// surface this as "compilation unavailable" rather than as a failed build: a
// missing credential is an operator problem, not a defect in the sources.
var ErrNotConfigured = errors.New("llm role is not configured")

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Usage is reported per call so a build can account for what it spent. Cost
// control is a design requirement of the compiler, and it cannot be enforced
// without measuring.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Completion struct {
	Content string
	Model   string
	Usage   Usage
}

// Client is the interface consumers depend on, so tests can drive compilation
// with a scripted model instead of a network call.
type Client interface {
	Complete(ctx context.Context, messages []Message, opts ...Option) (*Completion, error)
	Model() string
	Configured() bool
}

type Option func(*requestOptions)

type requestOptions struct {
	temperature *float64
	maxTokens   int
	// jsonObject asks the provider to emit syntactically valid JSON. It is a
	// hint, not a guarantee, so callers still parse defensively.
	jsonObject bool
}

func WithTemperature(value float64) Option {
	return func(o *requestOptions) { o.temperature = &value }
}

func WithMaxTokens(value int) Option {
	return func(o *requestOptions) { o.maxTokens = value }
}

func WithJSONObject() Option {
	return func(o *requestOptions) { o.jsonObject = true }
}

type HTTPClient struct {
	cfg    RoleConfig
	role   string
	client *http.Client
}

func NewHTTPClient(role string, cfg RoleConfig) *HTTPClient {
	return &HTTPClient{
		cfg:    cfg,
		role:   role,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *HTTPClient) Model() string    { return c.cfg.Model }
func (c *HTTPClient) Configured() bool { return c.cfg.Configured() }
func (c *HTTPClient) Role() string     { return c.role }
func (c *HTTPClient) Endpoint() string { return c.cfg.BaseURL }

func (c *HTTPClient) Complete(ctx context.Context, messages []Message, opts ...Option) (*Completion, error) {
	if !c.cfg.Configured() {
		return nil, fmt.Errorf("%w: %s", ErrNotConfigured, c.role)
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("messages required")
	}

	options := requestOptions{maxTokens: c.cfg.MaxTokens}
	for _, apply := range opts {
		apply(&options)
	}
	temperature := c.cfg.Temperature
	if options.temperature != nil {
		temperature = *options.temperature
	}

	payload := map[string]any{
		"model":       c.cfg.Model,
		"messages":    messages,
		"temperature": temperature,
	}
	if options.maxTokens > 0 {
		payload["max_tokens"] = options.maxTokens
	}
	if options.jsonObject {
		payload["response_format"] = map[string]any{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a bounded prefix: provider errors are small, and an unbounded read
		// on a misbehaving endpoint is a denial-of-service on ourselves. The key
		// is never echoed, only the provider's message.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("%s completion failed with status %d: %s",
			c.role, resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message      Message `json:"message"`
			FinishReason string  `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s completion returned no choices", c.role)
	}
	choice := out.Choices[0]
	if choice.FinishReason == "length" {
		// Truncated output would be parsed as if complete, silently losing pages
		// or citations. Fail loudly instead.
		return nil, fmt.Errorf("%s completion hit the token limit; raise max_tokens or split the input", c.role)
	}
	model := out.Model
	if model == "" {
		model = c.cfg.Model
	}
	return &Completion{Content: choice.Message.Content, Model: model, Usage: out.Usage}, nil
}
