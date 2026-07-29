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

// Error carries whether a failure is worth retrying, because the caller cannot
// tell from the message and guessing is expensive in both directions: retrying a
// truncated reply burns another multi-minute call for the same outcome, while not
// retrying a dropped connection throws away a build over a network blip.
//
// Retryable covers transport failures, rate limits and provider-side errors.
// Everything that would deterministically recur — a bad request, an oversized
// reply — is not retryable.
type Error struct {
	Role string
	// Status is 0 for failures that never reached a response.
	Status    int
	Message   string
	Retryable bool
}

func (e *Error) Error() string {
	if e.Status == 0 {
		return fmt.Sprintf("%s completion failed: %s", e.Role, e.Message)
	}
	return fmt.Sprintf("%s completion failed with status %d: %s", e.Role, e.Status, e.Message)
}

// Retryable reports whether err is worth another attempt. An error of an unknown
// kind is treated as not retryable: a compile is expensive, so the default has to
// be the cheap answer.
func Retryable(err error) bool {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Retryable
	}
	return false
}

// retryableStatus classifies HTTP status codes. 408 and 429 are explicit
// invitations to try again; 5xx is the provider saying the failure is on its side.
func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}

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
		// A request that never got a response is retryable unless the caller's own
		// context was cancelled — in that case nobody is waiting for a retry.
		return nil, &Error{Role: c.role, Message: err.Error(), Retryable: ctx.Err() == nil}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read a bounded prefix: provider errors are small, and an unbounded read
		// on a misbehaving endpoint is a denial-of-service on ourselves. The key
		// is never echoed, only the provider's message.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, &Error{
			Role: c.role, Status: resp.StatusCode,
			Message:   strings.TrimSpace(string(detail)),
			Retryable: retryableStatus(resp.StatusCode),
		}
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
		// A body that started arriving and then stopped is a transport failure, not
		// a malformed contract: worth retrying.
		return nil, &Error{Role: c.role, Status: resp.StatusCode,
			Message: "decode response: " + err.Error(), Retryable: true}
	}
	if len(out.Choices) == 0 {
		return nil, &Error{Role: c.role, Status: resp.StatusCode,
			Message: "response contained no choices", Retryable: true}
	}
	choice := out.Choices[0]
	if choice.FinishReason == "length" {
		// Truncated output would be parsed as if complete, silently losing pages
		// or citations. Fail loudly instead — and do not retry: the same budget
		// will truncate the same way, so a retry only doubles the bill.
		return nil, &Error{Role: c.role, Status: resp.StatusCode,
			Message:   "hit the token limit; raise max_tokens or split the input",
			Retryable: false}
	}
	model := out.Model
	if model == "" {
		model = c.cfg.Model
	}
	return &Completion{Content: choice.Message.Content, Model: model, Usage: out.Usage}, nil
}
