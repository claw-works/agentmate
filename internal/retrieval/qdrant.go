package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

type VectorStore interface {
	EnsureCollection(ctx context.Context, vectorSize int) error
	Upsert(ctx context.Context, points []VectorPoint) error
	Search(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error)
	Collection() string
	VectorName() string
}

type VectorPoint struct {
	ID      string
	Vector  []float32
	Payload map[string]any
}

type VectorSearchRequest struct {
	Vector         []float32
	Limit          int
	Filter         map[string]any
	WithPayload    bool
	ScoreThreshold *float64
}

type VectorSearchResult struct {
	ID      string
	Score   float64
	Payload map[string]any
}

type QdrantClient struct {
	url        string
	apiKey     string
	collection string
	vectorName string
	distance   string
	client     *http.Client
}

func NewQdrantClient(cfg Config) *QdrantClient {
	collection := cfg.QdrantCollection
	if collection == "" {
		collection = DefaultCollection
	}
	vectorName := cfg.QdrantVectorName
	if vectorName == "" {
		vectorName = DefaultVectorName
	}
	distance := cfg.QdrantDistance
	if distance == "" {
		distance = DefaultDistance
	}
	return &QdrantClient{
		url:        strings.TrimRight(cfg.QdrantURL, "/"),
		apiKey:     cfg.QdrantAPIKey,
		collection: collection,
		vectorName: vectorName,
		distance:   distance,
		client:     &http.Client{Timeout: cfg.Timeout},
	}
}

func (c *QdrantClient) Collection() string {
	return c.collection
}

func (c *QdrantClient) VectorName() string {
	return c.vectorName
}

func (c *QdrantClient) EnsureCollection(ctx context.Context, vectorSize int) error {
	if vectorSize <= 0 {
		return fmt.Errorf("vector size must be positive")
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/collections/"+c.collection, nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode != http.StatusNotFound {
		return fmt.Errorf("check qdrant collection failed: status %d", resp.StatusCode)
	}

	body, err := json.Marshal(map[string]any{
		"vectors": map[string]any{
			c.vectorName: map[string]any{
				"size":     vectorSize,
				"distance": c.distance,
			},
		},
	})
	if err != nil {
		return err
	}
	req, err = c.newRequest(ctx, http.MethodPut, "/collections/"+c.collection, body)
	if err != nil {
		return err
	}
	resp, err = c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeHTTPError(resp, "create qdrant collection")
	}
	return nil
}

func (c *QdrantClient) Upsert(ctx context.Context, points []VectorPoint) error {
	if len(points) == 0 {
		return nil
	}
	qPoints := make([]map[string]any, 0, len(points))
	for _, p := range points {
		qPoints = append(qPoints, map[string]any{
			"id":      p.ID,
			"vector":  map[string]any{c.vectorName: p.Vector},
			"payload": p.Payload,
		})
	}
	body, err := json.Marshal(map[string]any{"points": qPoints})
	if err != nil {
		return err
	}
	req, err := c.newRequest(ctx, http.MethodPut, "/collections/"+c.collection+"/points?wait=true", body)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeHTTPError(resp, "upsert qdrant points")
	}
	return nil
}

func (c *QdrantClient) Search(ctx context.Context, req VectorSearchRequest) ([]VectorSearchResult, error) {
	if req.Limit <= 0 {
		req.Limit = DefaultTopK
	}
	bodyMap := map[string]any{
		"vector": map[string]any{
			"name":   c.vectorName,
			"vector": req.Vector,
		},
		"limit":        req.Limit,
		"with_payload": req.WithPayload,
	}
	if req.Filter != nil {
		bodyMap["filter"] = req.Filter
	}
	if req.ScoreThreshold != nil {
		bodyMap["score_threshold"] = *req.ScoreThreshold
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	httpReq, err := c.newRequest(ctx, http.MethodPost, "/collections/"+c.collection+"/points/search", body)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, decodeHTTPError(resp, "search qdrant points")
	}

	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	var out struct {
		Result []struct {
			ID      any            `json:"id"`
			Score   float64        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := decoder.Decode(&out); err != nil {
		return nil, err
	}
	results := make([]VectorSearchResult, 0, len(out.Result))
	for _, item := range out.Result {
		results = append(results, VectorSearchResult{
			ID:      pointIDString(item.ID),
			Score:   item.Score,
			Payload: item.Payload,
		})
	}
	return results, nil
}

func (c *QdrantClient) newRequest(ctx context.Context, method, path string, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	return req, nil
}

func PayloadMatchFilter(values map[string]any) map[string]any {
	must := make([]map[string]any, 0, len(values))
	for key, value := range values {
		must = append(must, map[string]any{
			"key": key,
			"match": map[string]any{
				"value": value,
			},
		})
	}
	return map[string]any{"must": must}
}

func pointIDString(id any) string {
	switch v := id.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return fmt.Sprintf("%v", v)
	}
}
